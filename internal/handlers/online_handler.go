package handlers

import (
	"collaborative/internal/middlewares"
	"collaborative/internal/model"
	"collaborative/internal/storage"
	"encoding/json"
	"net"
	"net/http"
	"os"

	"go.uber.org/zap"
)

const (
	EPHStreamPort = 9200
	SSRStreamPort = 9201
)

// OnlineHandler обрабатывает запросы онлайн-позиционирования
type OnlineHandler struct {
	db     *storage.DBStorage
	logger *zap.SugaredLogger
}

func NewOnlineHandler(db *storage.DBStorage, logger *zap.SugaredLogger) *OnlineHandler {
	return &OnlineHandler{db: db, logger: logger}
}

// Register регистрирует онлайн-сессию и возвращает адреса EPH/SSR потоков
func (h *OnlineHandler) Register(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		middlewares.SendJSONError(w, "unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	var req model.RegisterOnlineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middlewares.SendJSONError(w, "invalid request body", http.StatusBadRequest, h.logger)
		return
	}

	sess := &model.OnlineSession{
		UserLogin:         login,
		PublicIP:          req.PublicIP,
		OutputPort:        req.OutputPort,
		SolutionPort:      req.SolutionPort,
		RoughenedLat:      req.RoughenedLat,
		RoughenedLon:      req.RoughenedLon,
		RoughenedHeight:   req.RoughenedHeight,
		ConvergenceStatus: 0,
	}

	if err := h.db.UpsertOnlineSession(sess); err != nil {
		h.logger.Errorf("Register online session for %s: %v", login, err)
		middlewares.SendJSONError(w, "database error", http.StatusInternalServerError, h.logger)
		return
	}

	serverHost := os.Getenv("SERVER_HOST")
	if serverHost == "" {
		// r.Host может содержать порт (например "localhost:8000") — берём только хост
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			serverHost = h
		} else {
			serverHost = r.Host
		}
		if serverHost == "" {
			serverHost = "localhost"
		}
	}

	resp := model.RegisterOnlineResponse{
		SessionID: int(sess.ID),
		EPHHost:   serverHost,
		EPHPort:   EPHStreamPort,
		SSRHost:   serverHost,
		SSRPort:   SSRStreamPort,
	}
	middlewares.SendJSONResponse(w, http.StatusOK, resp, h.logger)
}

// UpdateStatus обновляет статус сходимости/движения и возвращает ближайшего кандидата
func (h *OnlineHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		middlewares.SendJSONError(w, "unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	var req model.UpdateOnlineStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middlewares.SendJSONError(w, "invalid request body", http.StatusBadRequest, h.logger)
		return
	}

	if err := h.db.UpdateOnlineStatus(login, req.ConvergenceStatus, req.IsMoving, req.RoughenedLat, req.RoughenedLon); err != nil {
		h.logger.Warnf("UpdateOnlineStatus for %s: %v", login, err)
		middlewares.SendJSONError(w, "session not found, call /register first", http.StatusNotFound, h.logger)
		return
	}

	var resp model.UpdateOnlineStatusResponse
	// Кандидата ищем только для тех, у кого решение ещё не сошлось
	if req.ConvergenceStatus == 0 {
		candidate, err := h.db.FindNearestCandidate(req.RoughenedLat, req.RoughenedLon, login)
		if err == nil {
			resp.Candidate = &model.CandidateInfo{
				IP:           candidate.PublicIP,
				Port:         candidate.OutputPort,
				SolutionPort: candidate.SolutionPort,
				IsMoving:     candidate.IsMoving,
			}
		}
	}

	middlewares.SendJSONResponse(w, http.StatusOK, resp, h.logger)
}

// Delete завершает онлайн-сессию
func (h *OnlineHandler) Delete(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		middlewares.SendJSONError(w, "unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	if err := h.db.DeleteOnlineSession(login); err != nil {
		h.logger.Errorf("Delete online session for %s: %v", login, err)
		middlewares.SendJSONError(w, "database error", http.StatusInternalServerError, h.logger)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
