package handlers

import (
	"collaborative/internal/middlewares"
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

// ErrorResponse структура ошибки
type ErrorResponse struct {
	Error string `json:"error"`
}

// SuccessResponse структура успеха
type SuccessResponse struct {
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// SendJSONResponse отправляет JSON ответ
func SendJSONResponse(w http.ResponseWriter, status int, data interface{}, logger *zap.SugaredLogger) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Errorf("Failed to encode response: %v", err)
	}
}

// SendJSONError отправляет JSON ошибку
func SendJSONError(w http.ResponseWriter, msg string, status int, logger *zap.SugaredLogger) {
	SendJSONResponse(w, status, ErrorResponse{Error: msg}, logger)
}

// GetUserFromContext получает пользователя из контекста
func GetUserFromContext(ctx *http.Request) (string, bool) {
	return middlewares.GetUserFromContext(ctx.Context())
}
