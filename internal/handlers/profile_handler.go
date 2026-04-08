package handlers

import (
	"net/http"

	"go.uber.org/zap"
)

// ProfileHandler возвращает профиль пользователя
func ProfileHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Получаем логин из контекста (установлен в AuthMiddleware)
		login, ok := GetUserFromContext(r)
		if !ok {
			SendJSONError(w, "Unauthorized", http.StatusUnauthorized, logger)
			return
		}

		profile := map[string]interface{}{
			"login":   login,
			"message": "Welcome to your profile!",
		}

		SendJSONResponse(w, http.StatusOK, profile, logger)
	}
}
