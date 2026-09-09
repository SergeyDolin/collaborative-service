package handlers

import (
	"collaborative/internal/middlewares"
	"collaborative/internal/model"
	"collaborative/internal/services"
	"collaborative/internal/storage"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type CalibrationHandler struct {
	taskStorage *storage.TaskStorage
	calibSvc    *services.CalibrationService
	logger      *zap.SugaredLogger
}

func NewCalibrationHandler(st *storage.TaskStorage, l *zap.SugaredLogger, m *MeasurementHandler) *CalibrationHandler {
	return &CalibrationHandler{st, services.NewCalibrationService(st, m.NewMeasurementService(), l), l}
}
func (h *CalibrationHandler) owned(w http.ResponseWriter, r *http.Request) (*model.CalibrationTask, string) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", 401, h.logger)
		return nil, ""
	}
	t, e := h.taskStorage.GetCalibrationTask(chi.URLParam(r, "taskId"))
	if e != nil || t.UserLogin != login {
		SendJSONError(w, "Задача недоступна или срок хранения истёк", 404, h.logger)
		return nil, ""
	}
	return t, login
}
func (h *CalibrationHandler) StartCalibration(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", 401, h.logger)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	var t model.CalibrationTask
	var raw json.RawMessage
	if e := json.NewDecoder(r.Body).Decode(&raw); e != nil || json.Unmarshal(raw, &t) != nil {
		SendJSONError(w, "Неверный JSON", 400, h.logger)
		return
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	var options map[string]json.RawMessage
	_ = json.Unmarshal(fields["options"], &options)
	present := func(m map[string]json.RawMessage, keys ...string) bool {
		for _, key := range keys {
			if len(m[key]) == 0 || string(m[key]) == "null" {
				return false
			}
		}
		return true
	}
	if !present(options, "baseLat", "baseLon", "baseH") || t.RefType == "geodetic" && !present(fields, "refLat", "refLon", "refH") {
		SendJSONError(w, "Введите все координаты B, L, H явно, включая нулевые значения", 400, h.logger)
		return
	}
	if t.Mode != "full" && t.Mode != "horizontal_only" && t.Mode != "quick" {
		SendJSONError(w, "Неверный режим", 400, h.logger)
		return
	}
	if t.RefType != "geodetic" && t.RefType != "none" || t.RefType == "none" && t.Mode != "horizontal_only" {
		SendJSONError(w, "Для этого режима нужны координаты марки", 400, h.logger)
		return
	}
	valid := func(lat, lon, h float64) bool {
		return !math.IsNaN(lat) && !math.IsNaN(lon) && !math.IsNaN(h) && math.Abs(lat) <= 90 && math.Abs(lon) <= 180 && math.Abs(h) < 100000
	}
	if !valid(t.Options.BaseLat, t.Options.BaseLon, t.Options.BaseH) || t.RefType == "geodetic" && !valid(t.RefLat, t.RefLon, t.RefH) {
		SendJSONError(w, "Неверные координаты", 400, h.logger)
		return
	}
	if strings.TrimSpace(t.Options.ReferenceFrame) == "" {
		SendJSONError(w, "Укажите систему отсчёта и эпоху координат", 400, h.logger)
		return
	}
	if t.Options.Frequency != "l1" {
		SendJSONError(w, "Эта версия поддерживает GPS L1 / Galileo E1", 400, h.logger)
		return
	}
	t.ID = uuid.NewString()
	t.UserLogin = login
	t.Status = "pending"
	t.CreatedAt = time.Now()
	t.CompletedAt = nil
	t.Result = nil
	t.Sessions = nil
	t.ReceiverTaskID = ""
	// Selection of a profile is optional; no write to device profiles is performed.
	t.DeviceID = 0
	if e := h.taskStorage.CreateCalibrationTask(&t); e != nil {
		SendJSONError(w, "Не удалось создать задачу", 500, h.logger)
		return
	}
	SendJSONResponse(w, 201, map[string]string{"taskId": t.ID}, h.logger)
}
func (h *CalibrationHandler) UploadReceiverFile(w http.ResponseWriter, r *http.Request) {
	h.upload(w, r, true)
}
func (h *CalibrationHandler) UploadSession(w http.ResponseWriter, r *http.Request) {
	h.upload(w, r, false)
}
func (h *CalibrationHandler) upload(w http.ResponseWriter, r *http.Request, base bool) {
	task, login := h.owned(w, r)
	if task == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<20)
	if e := r.ParseMultipartForm(8 << 20); e != nil {
		SendJSONError(w, "Не удалось прочитать файл; лимит 128 МБ", 400, h.logger)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, info, e := r.FormFile("file")
	if e != nil {
		SendJSONError(w, "Выберите файл", 400, h.logger)
		return
	}
	defer file.Close()
	data, e := io.ReadAll(file)
	if e != nil || len(data) == 0 {
		SendJSONError(w, "Не удалось прочитать файл", 400, h.logger)
		return
	}
	filename := filepath.Base(info.Filename)
	// Calibration accepts plain RINEX 3 only. This avoids changing the reference
	// antenna header and makes observation epochs and signals auditable.
	first := strings.SplitN(string(data[:min(len(data), 200)]), "\n", 2)[0]
	version := 0.0
	if len(first) >= 9 {
		version, _ = strconv.ParseFloat(strings.TrimSpace(first[:9]), 64)
	}
	if len(first) < 61 || !strings.Contains(first, "RINEX VERSION / TYPE") || !(version >= 3 && version < 4) || first[20] != 'O' {
		SendJSONError(w, "Загрузите несжатый файл наблюдений RINEX 3 (.obs или .rnx)", 400, h.logger)
		return
	}
	filename = strings.TrimSuffix(filename, filepath.Ext(filename)) + ".rnx"
	id := "base"
	var sess *model.CalibrationSession
	if !base {
		id = uuid.NewString()
		sess = &model.CalibrationSession{ID: id, TaskID: task.ID, Filename: filename, Position: r.FormValue("position"), Orientation: r.FormValue("orientation"), Status: "pending"}
		if e = json.Unmarshal([]byte(r.FormValue("geometry")), &sess.Geometry); e != nil {
			SendJSONError(w, "Укажите геометрию установки", 400, h.logger)
			return
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal([]byte(r.FormValue("geometry")), &fields)
		for _, key := range []string{"reduceE", "reduceN", "reduceH"} {
			var value *float64
			if json.Unmarshal(fields[key], &value) != nil || value == nil {
				SendJSONError(w, "Введите все три компоненты редуцирования, включая нулевые", 400, h.logger)
				return
			}
		}
		if e = services.ValidateCalibrationSession(task, *sess); e != nil {
			SendJSONError(w, e.Error(), 400, h.logger)
			return
		}
	}
	if e = h.taskStorage.SaveCalibrationUpload(task.ID, login, id, filename, data, sess); e != nil {
		SendJSONError(w, e.Error(), 409, h.logger)
		return
	}
	SendJSONResponse(w, 201, map[string]string{"sessionId": id}, h.logger)
}
func (h *CalibrationHandler) Submit(w http.ResponseWriter, r *http.Request) {
	task, login := h.owned(w, r)
	if task == nil {
		return
	}
	if e := services.ValidateCalibration(task); e != nil {
		SendJSONError(w, e.Error(), 400, h.logger)
		return
	}
	claimed, e := h.taskStorage.ClaimCalibration(task.ID, login)
	if e != nil || !claimed {
		SendJSONError(w, "Задача уже запущена либо недоступна", 409, h.logger)
		return
	}
	go h.calibSvc.RunCalibration(context.Background(), task.ID)
	SendJSONResponse(w, 202, map[string]string{"status": "processing"}, h.logger)
}
func (h *CalibrationHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	task, _ := h.owned(w, r)
	if task == nil {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	SendJSONResponse(w, 200, task, h.logger)
}
func (h *CalibrationHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", 401, h.logger)
		return
	}
	ts, e := h.taskStorage.ListCalibrationTasks(login)
	if e != nil {
		SendJSONError(w, "Не удалось получить задачи", 500, h.logger)
		return
	}
	if ts == nil {
		ts = []*model.CalibrationTask{}
	}
	w.Header().Set("Cache-Control", "no-store")
	SendJSONResponse(w, 200, ts, h.logger)
}
