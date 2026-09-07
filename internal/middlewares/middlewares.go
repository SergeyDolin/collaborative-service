package middlewares

import (
	"collaborative/internal/auth"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/middleware"
	"go.uber.org/zap"
)

type contextKey string

const UserContextKey contextKey = "user"

// LogMiddleware keeps routine requests at debug and failures visible at info level.
func LogMiddleware(logger *zap.SugaredLogger) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			lw := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				if err := recover(); err != nil {
					logger.Errorw("HTTP panic", "request_id", middleware.GetReqID(r.Context()), "panic", err)
					http.Error(lw, "Internal Server Error", http.StatusInternalServerError)
				}
				status := lw.Status()
				if status == 0 {
					status = http.StatusOK
				}
				fields := []interface{}{
					"method", r.Method, "path", r.URL.Path, "status", status,
					"duration", time.Since(start), "bytes", lw.BytesWritten(),
					"request_id", middleware.GetReqID(r.Context()),
				}
				switch {
				case status >= 500:
					logger.Errorw("HTTP request", fields...)
				case status >= 400:
					logger.Warnw("HTTP request", fields...)
				default:
					logger.Debugw("HTTP request", fields...)
				}
			}()
			h.ServeHTTP(lw, r)
		})
	}
}

func AuthMiddleware(jwtService *auth.JWTService, logger *zap.SugaredLogger) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")

			if authHeader == "" {
				SendJSONError(w, "Authorization header required", http.StatusUnauthorized, logger)
				return
			}

			tokenString, err := auth.ExtractTokenFromHeader(authHeader)
			if err != nil {
				SendJSONError(w, err.Error(), http.StatusUnauthorized, logger)
				return
			}

			claims, err := jwtService.ValidateToken(tokenString)
			if err != nil {
				logger.Debugf("Token validation failed: %v", err)
				SendJSONError(w, "Invalid or expired token", http.StatusUnauthorized, logger)
				return
			}

			ctx := context.WithValue(r.Context(), UserContextKey, claims.Login)

			h.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetUserFromContext give login of user from context
func GetUserFromContext(ctx context.Context) (string, bool) {
	user, ok := ctx.Value(UserContextKey).(string)
	return user, ok
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func SendJSONResponse(res http.ResponseWriter, status int, data interface{}, logger *zap.SugaredLogger) {
	res.Header().Set("Content-Type", "application/json")

	res.WriteHeader(status)

	if err := json.NewEncoder(res).Encode(data); err != nil {
		logger.Errorf("Failed to encode response: %v", err)
	}
}

func SendJSONError(res http.ResponseWriter, msg string, status int, logger *zap.SugaredLogger) {
	SendJSONResponse(res, status, ErrorResponse{Error: msg}, logger)
}

const MaxUploadSize = 1 << 30 // 1 GB

// MaxUploadSizeMiddleware ограничивает размер тела запроса
func MaxUploadSizeMiddleware(logger *zap.SugaredLogger) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Проверяем Content-Length если он есть
			if r.ContentLength > MaxUploadSize {
				logger.Warnf("Request too large: %d bytes (max: %d)", r.ContentLength, MaxUploadSize)
				SendJSONError(w, fmt.Sprintf("File too large. Maximum size: %d GB", MaxUploadSize/(1024*1024*1024)),
					http.StatusRequestEntityTooLarge, logger)
				return
			}

			// Ограничиваем тело запроса
			r.Body = http.MaxBytesReader(w, r.Body, MaxUploadSize)
			h.ServeHTTP(w, r)
		})
	}
}
