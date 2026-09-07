package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"collaborative/internal/middlewares"
	"collaborative/internal/model"
	"collaborative/internal/services"
	"collaborative/internal/storage"

	"github.com/go-chi/chi"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type CalibrationHandler struct {
	taskStorage *storage.TaskStorage
	calibSvc    *services.CalibrationService
	measSvc     *services.MeasurementService
	logger      *zap.SugaredLogger
}

func NewCalibrationHandler(
	taskStorage *storage.TaskStorage,
	logger *zap.SugaredLogger,
	measHandler *MeasurementHandler,
) *CalibrationHandler {
	measSvc := measHandler.NewMeasurementService()
	calibSvc := services.NewCalibrationService(taskStorage, measSvc, logger)
	return &CalibrationHandler{
		taskStorage: taskStorage,
		calibSvc:    calibSvc,
		measSvc:     measSvc,
		logger:      logger,
	}
}

// POST /api/calibration/start
//
// Body JSON:
//
//	{
//	  "deviceId": 42,
//	  "deviceModel": "iPhone 15 Pro",
//	  "mode": "full|horizontal_only|quick",
//	  "refType": "geodetic|receiver|none",
//	  "refLat": 55.1234, "refLon": 37.5678, "refH": 123.45,   // если geodetic
//	  "reduceH": 0.12, "reduceE": 0.0, "reduceN": 0.0
//	}
//
// Ответ: {"taskId": "..."}
func (h *CalibrationHandler) StartCalibration(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	var req struct {
		DeviceID    int     `json:"deviceId"`
		DeviceModel string  `json:"deviceModel"`
		Mode        string  `json:"mode"`
		RefType     string  `json:"refType"`
		RefLat      float64 `json:"refLat"`
		RefLon      float64 `json:"refLon"`
		RefH        float64 `json:"refH"`
		ReduceH     float64 `json:"reduceH"`
		ReduceE     float64 `json:"reduceE"`
		ReduceN     float64 `json:"reduceN"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		SendJSONError(w, "Неверный формат запроса", http.StatusBadRequest, h.logger)
		return
	}

	validModes := map[string]bool{
		model.CalibModeFullCalib:      true,
		model.CalibModeHorizontalOnly: true,
		model.CalibModeQuick:          true,
	}
	if !validModes[req.Mode] {
		SendJSONError(w, "Неверный режим калибровки", http.StatusBadRequest, h.logger)
		return
	}
	if req.RefType == "" {
		req.RefType = model.CalibRefNone
	}

	task := &model.CalibrationTask{
		ID:          uuid.NewString(),
		UserLogin:   login,
		DeviceID:    req.DeviceID,
		DeviceModel: req.DeviceModel,
		Mode:        req.Mode,
		Status:      "pending",
		RefType:     req.RefType,
		RefLat:      req.RefLat,
		RefLon:      req.RefLon,
		RefH:        req.RefH,
		ReduceH:     req.ReduceH,
		ReduceE:     req.ReduceE,
		ReduceN:     req.ReduceN,
		CreatedAt:   time.Now(),
	}

	if err := h.taskStorage.CreateCalibrationTask(task); err != nil {
		h.logger.Errorf("create calibration task: %v", err)
		SendJSONError(w, "Ошибка создания задачи", http.StatusInternalServerError, h.logger)
		return
	}

	SendJSONResponse(w, http.StatusCreated, map[string]string{"taskId": task.ID}, h.logger)
}

// POST /api/calibration/{taskId}/receiver
//
// Загружает RINEX-файл опорного приёмника. Запускает PPP асинхронно.
// Ответ: {"sessionPppTaskId": "..."}
func (h *CalibrationHandler) UploadReceiverFile(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}
	taskID := chi.URLParam(r, "taskId")

	task, err := h.taskStorage.GetCalibrationTask(taskID)
	if err != nil || task.UserLogin != login {
		SendJSONError(w, "Задача не найдена", http.StatusNotFound, h.logger)
		return
	}
	if task.RefType != model.CalibRefReceiver {
		SendJSONError(w, "Задача не требует файла приёмника", http.StatusBadRequest, h.logger)
		return
	}

	fileData, filename, err := readMultipartFile(r, "file")
	if err != nil {
		SendJSONError(w, "Ошибка загрузки файла: "+err.Error(), http.StatusBadRequest, h.logger)
		return
	}

	pppTaskID, err := h.measSvc.SubmitForPPP(r.Context(), login, fileData, filename)
	if err != nil {
		h.logger.Errorf("[calib:%s] receiver PPP submit: %v", taskID, err)
		SendJSONError(w, "Ошибка запуска PPP для приёмника", http.StatusInternalServerError, h.logger)
		return
	}

	// Сохранить ppp_task_id приёмника в задаче
	if err := h.taskStorage.SetCalibrationReceiverTask(taskID, pppTaskID); err != nil {
		h.logger.Errorf("[calib:%s] set receiver task: %v", taskID, err)
	}

	SendJSONResponse(w, http.StatusAccepted, map[string]string{"receiverPppTaskId": pppTaskID}, h.logger)
}

// POST /api/calibration/{taskId}/session
//
// Загружает один RINEX-файл сеанса смартфона.
// Form fields: file (binary), position (vertical|horizontal), orientation (north|south|east|west).
// Запускает PPP асинхронно.
// Ответ: {"sessionId": "...", "pppTaskId": "..."}
func (h *CalibrationHandler) UploadSession(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}
	taskID := chi.URLParam(r, "taskId")

	task, err := h.taskStorage.GetCalibrationTask(taskID)
	if err != nil || task.UserLogin != login {
		SendJSONError(w, "Задача не найдена", http.StatusNotFound, h.logger)
		return
	}
	if task.Status != "pending" {
		SendJSONError(w, "Задача уже запущена или завершена", http.StatusConflict, h.logger)
		return
	}

	if err := r.ParseMultipartForm(1 << 30); err != nil {
		SendJSONError(w, "Ошибка разбора формы", http.StatusBadRequest, h.logger)
		return
	}
	position := r.FormValue("position")
	orientation := r.FormValue("orientation")

	if position != model.CalibPosVertical && position != model.CalibPosHorizontal {
		SendJSONError(w, "Неверное положение: vertical или horizontal", http.StatusBadRequest, h.logger)
		return
	}
	validOrient := map[string]bool{
		model.CalibOrientNorth: true,
		model.CalibOrientSouth: true,
		model.CalibOrientEast:  true,
		model.CalibOrientWest:  true,
	}
	if !validOrient[orientation] {
		SendJSONError(w, "Неверная ориентация: north|south|east|west", http.StatusBadRequest, h.logger)
		return
	}

	fileData, filename, err := readMultipartFile(r, "file")
	if err != nil {
		SendJSONError(w, "Ошибка загрузки файла: "+err.Error(), http.StatusBadRequest, h.logger)
		return
	}

	pppTaskID, err := h.measSvc.SubmitForPPP(r.Context(), login, fileData, filename)
	if err != nil {
		h.logger.Errorf("[calib:%s] session PPP submit: %v", taskID, err)
		SendJSONError(w, "Ошибка запуска PPP для сеанса", http.StatusInternalServerError, h.logger)
		return
	}

	sess := &model.CalibrationSession{
		ID:          uuid.NewString(),
		TaskID:      taskID,
		Filename:    filename,
		Position:    position,
		Orientation: orientation,
		PPPTaskID:   pppTaskID,
		Status:      "pending",
	}
	if err := h.taskStorage.AddCalibrationSession(sess); err != nil {
		h.logger.Errorf("[calib:%s] add session: %v", taskID, err)
		SendJSONError(w, "Ошибка сохранения сеанса", http.StatusInternalServerError, h.logger)
		return
	}

	SendJSONResponse(w, http.StatusAccepted, map[string]string{
		"sessionId": sess.ID,
		"pppTaskId": pppTaskID,
	}, h.logger)
}

// POST /api/calibration/{taskId}/submit
//
// Запускает вычисление фазового центра по уже загруженным сеансам.
func (h *CalibrationHandler) Submit(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}
	taskID := chi.URLParam(r, "taskId")

	task, err := h.taskStorage.GetCalibrationTask(taskID)
	if err != nil || task.UserLogin != login {
		SendJSONError(w, "Задача не найдена", http.StatusNotFound, h.logger)
		return
	}
	if task.Status != "pending" {
		SendJSONError(w, "Задача уже запущена или завершена", http.StatusConflict, h.logger)
		return
	}
	if len(task.Sessions) == 0 {
		SendJSONError(w, "Нет загруженных сеансов", http.StatusBadRequest, h.logger)
		return
	}

	go h.calibSvc.RunCalibration(r.Context(), taskID)

	SendJSONResponse(w, http.StatusAccepted, map[string]string{"status": "processing"}, h.logger)
}

// GET /api/calibration/{taskId}/status
func (h *CalibrationHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}
	taskID := chi.URLParam(r, "taskId")

	task, err := h.taskStorage.GetCalibrationTask(taskID)
	if err != nil || task.UserLogin != login {
		SendJSONError(w, "Задача не найдена", http.StatusNotFound, h.logger)
		return
	}

	SendJSONResponse(w, http.StatusOK, task, h.logger)
}

// GET /api/calibration/list
func (h *CalibrationHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	tasks, err := h.taskStorage.ListCalibrationTasks(login)
	if err != nil {
		SendJSONError(w, "Ошибка получения задач", http.StatusInternalServerError, h.logger)
		return
	}
	if tasks == nil {
		tasks = []*model.CalibrationTask{}
	}
	SendJSONResponse(w, http.StatusOK, tasks, h.logger)
}

// readMultipartFile читает файл из multipart-запроса.
func readMultipartFile(r *http.Request, field string) ([]byte, string, error) {
	if r.MultipartForm == nil {
		if err := r.ParseMultipartForm(1 << 30); err != nil {
			return nil, "", err
		}
	}
	f, fh, err := r.FormFile(field)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	return data, fh.Filename, err
}
