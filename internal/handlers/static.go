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
		// Добавляем заголовки кэширования для статических файлов
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

		// Логируем только важные ошибки
		if strings.HasSuffix(r.URL.Path, ".css") || strings.HasSuffix(r.URL.Path, ".js") {
			logger.Debugf("Serving static file: %s", r.URL.Path)
		}

		fs.ServeHTTP(w, r)
	})
}
