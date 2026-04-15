package handlers

import (
	"encoding/json"
	"net/http"

	"collaborative/internal/cache"
	"collaborative/internal/storage"

	"go.uber.org/zap"
)

// StartCollaborativeHandler запускает сессию коллаборативного позиционирования
func StartCollaborativeHandler(dbStor *storage.DBStorage, cache *cache.Cache, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		login, ok := GetUserFromContext(r)
		if !ok {
			SendJSONError(w, "Unauthorized", http.StatusUnauthorized, logger)
			return
		}

		var req struct {
			DeviceID int64 `json:"deviceId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			SendJSONError(w, "Invalid request body", http.StatusBadRequest, logger)
			return
		}

		if req.DeviceID == 0 {
			SendJSONError(w, "Device ID is required", http.StatusBadRequest, logger)
			return
		}

		// Проверяем, что устройство принадлежит пользователю
		devices, err := dbStor.GetUserDevices(login)
		if err != nil {
			logger.Errorf("GetUserDevices %s: %v", login, err)
			SendJSONError(w, "Failed to get devices", http.StatusInternalServerError, logger)
			return
		}

		deviceFound := false
		for _, d := range devices {
			if d.ID == req.DeviceID {
				deviceFound = true
				break
			}
		}
		if !deviceFound {
			SendJSONError(w, "Device not found or does not belong to user", http.StatusNotFound, logger)
			return
		}

		// Генерируем порты для пользователя
		// В реальном приложении здесь должна быть логика выделения портов из пула
		// Для демонстрации используем простой подход
		basePort := 50000
		userHash := hashString(login)
		txPort := basePort + (userHash%1000)*2
		rxPort := txPort + 1

		// Сохраняем сессию в кэш
		sessionKey := "collab:" + login
		sessionData := map[string]interface{}{
			"deviceId": req.DeviceID,
			"txPort":   txPort,
			"rxPort":   rxPort,
			"active":   true,
		}
		if err := cache.Set(sessionKey, sessionData, 3600); err != nil {
			logger.Errorf("Cache set %s: %v", sessionKey, err)
		}

		response := map[string]interface{}{
			"message":    "Collaborative positioning session started",
			"serverHost": "localhost", // В продакшене здесь должен быть реальный хост сервера
			"txPort":     txPort,
			"rxPort":     rxPort,
			"deviceId":   req.DeviceID,
		}

		SendJSONResponse(w, http.StatusOK, response, logger)
	}
}

// hashString простая хеш-функция для строки
func hashString(s string) int {
	hash := 0
	for i := 0; i < len(s); i++ {
		hash = hash*31 + int(s[i])
	}
	if hash < 0 {
		hash = -hash
	}
	return hash
}

// StopCollaborativeHandler останавливает сессию коллаборативного позиционирования
func StopCollaborativeHandler(cache *cache.Cache, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		login, ok := GetUserFromContext(r)
		if !ok {
			SendJSONError(w, "Unauthorized", http.StatusUnauthorized, logger)
			return
		}

		sessionKey := "collab:" + login
		if err := cache.Delete(sessionKey); err != nil {
			logger.Errorf("Cache delete %s: %v", sessionKey, err)
		}

		SendJSONResponse(w, http.StatusOK, map[string]string{"message": "Session stopped"}, logger)
	}
}

// GetCollaborativeStatusHandler возвращает статус сессии коллаборативного позиционирования
func GetCollaborativeStatusHandler(cache *cache.Cache, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		login, ok := GetUserFromContext(r)
		if !ok {
			SendJSONError(w, "Unauthorized", http.StatusUnauthorized, logger)
			return
		}

		sessionKey := "collab:" + login
		data, err := cache.Get(sessionKey)
		if err != nil || data == nil {
			SendJSONResponse(w, http.StatusOK, map[string]interface{}{
				"active": false,
			}, logger)
			return
		}

		sessionData, ok := data.(map[string]interface{})
		if !ok {
			SendJSONResponse(w, http.StatusOK, map[string]interface{}{
				"active": false,
			}, logger)
			return
		}

		response := map[string]interface{}{
			"active":   sessionData["active"],
			"deviceId": sessionData["deviceId"],
			"txPort":   sessionData["txPort"],
			"rxPort":   sessionData["rxPort"],
		}

		SendJSONResponse(w, http.StatusOK, response, logger)
	}
}
