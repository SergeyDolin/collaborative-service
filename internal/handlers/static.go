package handlers

import (
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// StaticFileServer создает обработчик статических файлов с кэшированием
func StaticFileServer(logger *zap.SugaredLogger) http.Handler {
	fs := http.FileServer(http.Dir("./static"))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// no-cache: браузер всегда проверяет свежесть файла у сервера
		w.Header().Set("Cache-Control", "no-cache")

		// Логируем только важные ошибки
		if strings.HasSuffix(r.URL.Path, ".css") || strings.HasSuffix(r.URL.Path, ".js") {
			logger.Debugf("Serving static file: %s", r.URL.Path)
		}

		fs.ServeHTTP(w, r)
	})
}
