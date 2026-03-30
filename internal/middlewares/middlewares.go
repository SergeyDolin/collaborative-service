package middlewares

import (
	"collaborative/internal/auth"
	"context"
	"encoding/json"
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

type contextKey string

const UserContextKey contextKey = "user"

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
				logger.Errorf("Invalid token: %v", err)
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
