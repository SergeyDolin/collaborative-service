package handlers

import (
	"net/http"
	"path/filepath"

	"go.uber.org/zap"
)

// ServeStaticFile обслуживает статический HTML файл
func ServeStaticFile(filename string, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			SendJSONError(w, "Only GET request allowed", http.StatusMethodNotAllowed, logger)
			return
		}

		filePath := filepath.Join("static", filename)
		http.ServeFile(w, r, filePath)
	}
}

// IndexHandler обслуживает главную страницу
func IndexHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return ServeStaticFile("index.html", logger)
}

// LoginPageHandler обслуживает страницу входа
func LoginPageHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return ServeStaticFile("login.html", logger)
}

// RegisterPageHandler обслуживает страницу регистрации
func RegisterPageHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return ServeStaticFile("register.html", logger)
}

// ProfilePageHandler обслуживает страницу профиля
func ProfilePageHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return ServeStaticFile("profile.html", logger)
}

// MeasurementsPageHandler обслуживает страницу измерений
func MeasurementsPageHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return ServeStaticFile("measurements.html", logger)
}

// CollaborativePageHandler обслуживает страницу коллаборативного позиционирования
func CollaborativePageHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return ServeStaticFile("collaborative.html", logger)
}
