package middlewareservice

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

type (
	responseData struct {
		status int
		size   int
	}

	loggingResponseWriter struct {
		http.ResponseWriter
		responseData *responseData
	}
)

func (r *loggingResponseWriter) Write(b []byte) (int, error) {
	size, err := r.ResponseWriter.Write(b)
	r.responseData.size += size
	return size, err
}

func (r *loggingResponseWriter) WriteHeader(statusCode int) {
	r.ResponseWriter.WriteHeader(statusCode)
	r.responseData.status = statusCode
}

func LogMiddleware(logger *zap.SugaredLogger) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			responseData := responseData{
				status: 0,
				size:   0,
			}

			lw := loggingResponseWriter{
				ResponseWriter: w,
				responseData:   &responseData,
			}

			defer func() {
				if err := recover(); err != nil {
					logger.Errorf("PANIC recovered: %v", err)
					http.Error(&lw, "Internal Server Error", http.StatusInternalServerError)
				}
			}()
			h.ServeHTTP(&lw, r)
			duration := time.Since(start)
			logger.Infof("%s %s %d %v %d", r.RequestURI, r.Method, responseData.status, duration, responseData.size)
		})
	}
}
