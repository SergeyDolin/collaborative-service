package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"collaborative/internal/model"
	"collaborative/internal/services"
	"collaborative/internal/storage"

	"github.com/go-chi/chi"
	"go.uber.org/zap"
)

// CollaborativeHandler обрабатывает запросы коллаборативного позиционирования
type CollaborativeHandler struct {
	db     *storage.DBStorage
	logger *zap.SugaredLogger
}

func NewCollaborativeHandler(db *storage.DBStorage, logger *zap.SugaredLogger) *CollaborativeHandler {
	return &CollaborativeHandler{db: db, logger: logger}
}

// createSessionRequest — тело запроса на создание сессии
type createSessionRequest struct {
	DeviceID        int64                `json:"deviceId"`
	ConnectionType  model.ConnectionType `json:"connectionType"`
	TCPHost         string               `json:"tcpHost"`
	TCPPort         int                  `json:"tcpPort"`
	NTRIPHost       string               `json:"ntripHost"`
	NTRIPPort       int                  `json:"ntripPort"`
	NTRIPMountpoint string               `json:"ntripMountpoint"`
	NTRIPUser       string               `json:"ntripUser"`
	NTRIPPass       string               `json:"ntripPass"`
}

// CreateSession POST /api/collaborative/sessions
func (h *CollaborativeHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	login, ok := GetUserFromContext(r)
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		SendJSONError(w, "Неверный формат запроса", http.StatusBadRequest, h.logger)
		return
	}

	if req.DeviceID == 0 {
		SendJSONError(w, "Укажите устройство", http.StatusBadRequest, h.logger)
		return
	}
	if req.ConnectionType != model.ConnectionTypeTCP && req.ConnectionType != model.ConnectionTypeNTRIP {
		SendJSONError(w, "Тип подключения: tcp или ntrip", http.StatusBadRequest, h.logger)
		return
	}
	if req.ConnectionType == model.ConnectionTypeTCP && (req.TCPHost == "" || req.TCPPort == 0) {
		SendJSONError(w, "Укажите хост и порт TCP устройства", http.StatusBadRequest, h.logger)
		return
	}
	if req.ConnectionType == model.ConnectionTypeNTRIP &&
		(req.NTRIPHost == "" || req.NTRIPPort == 0 || req.NTRIPMountpoint == "") {
		SendJSONError(w, "Укажите хост, порт и маунтпоинт NTRIP", http.StatusBadRequest, h.logger)
		return
	}

	// Проверяем, что устройство принадлежит пользователю
	devices, err := h.db.GetUserDevices(login)
	if err != nil {
		SendJSONError(w, "Ошибка получения устройств", http.StatusInternalServerError, h.logger)
		return
	}
	var deviceName string
	for _, d := range devices {
		if d.ID == req.DeviceID {
			deviceName = d.Name
			break
		}
	}
	if deviceName == "" {
		SendJSONError(w, "Устройство не найдено", http.StatusNotFound, h.logger)
		return
	}

	// Выделяем порт
	port, err := h.db.AllocatePort()
	if err != nil {
		SendJSONError(w, "Нет свободных портов: "+err.Error(), http.StatusServiceUnavailable, h.logger)
		return
	}

	sess := &model.CollaborativeSession{
		UserLogin:       login,
		DeviceID:        req.DeviceID,
		DeviceName:      deviceName,
		ConnectionType:  req.ConnectionType,
		TCPHost:         req.TCPHost,
		TCPPort:         req.TCPPort,
		NTRIPHost:       req.NTRIPHost,
		NTRIPPort:       req.NTRIPPort,
		NTRIPMountpoint: req.NTRIPMountpoint,
		NTRIPUser:       req.NTRIPUser,
		NTRIPPass:       req.NTRIPPass,
		AssignedPort:    port,
	}
	sess.Str2strCmd = services.BuildStr2strCmd(sess)

	if err := h.db.CreateCollaborativeSession(sess); err != nil {
		SendJSONError(w, "Ошибка создания сессии: "+err.Error(), http.StatusInternalServerError, h.logger)
		return
	}

	h.logger.Infof("CollaborativeSession created: id=%d user=%s device=%s port=%d", sess.ID, login, deviceName, port)
	SendJSONResponse(w, http.StatusCreated, sess, h.logger)
}

// ListSessions GET /api/collaborative/sessions
func (h *CollaborativeHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	login, ok := GetUserFromContext(r)
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	sessions, err := h.db.GetCollaborativeSessions(login)
	if err != nil {
		SendJSONError(w, "Ошибка загрузки сессий", http.StatusInternalServerError, h.logger)
		return
	}
	if sessions == nil {
		sessions = []model.CollaborativeSession{}
	}
	SendJSONResponse(w, http.StatusOK, sessions, h.logger)
}

// DeleteSession DELETE /api/collaborative/sessions/{id}
func (h *CollaborativeHandler) DeleteSession(w http.ResponseWriter, r *http.Request) {
	login, ok := GetUserFromContext(r)
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		SendJSONError(w, "Неверный ID", http.StatusBadRequest, h.logger)
		return
	}

	if err := h.db.DeleteCollaborativeSession(id, login); err != nil {
		SendJSONError(w, err.Error(), http.StatusNotFound, h.logger)
		return
	}

	SendJSONResponse(w, http.StatusOK, map[string]string{"message": "Сессия удалена"}, h.logger)
}

// SetPositioning POST /api/collaborative/sessions/{id}/positioning
// Body: {"enabled": true|false}
func (h *CollaborativeHandler) SetPositioning(w http.ResponseWriter, r *http.Request) {
	login, ok := GetUserFromContext(r)
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		SendJSONError(w, "Неверный ID", http.StatusBadRequest, h.logger)
		return
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		SendJSONError(w, "Неверный формат запроса", http.StatusBadRequest, h.logger)
		return
	}

	if err := h.db.UpdateSessionPositioning(id, login, body.Enabled); err != nil {
		SendJSONError(w, err.Error(), http.StatusNotFound, h.logger)
		return
	}

	msg := "Позиционирование включено"
	if !body.Enabled {
		msg = "Позиционирование отключено"
	}
	SendJSONResponse(w, http.StatusOK, map[string]string{"message": msg}, h.logger)
}
