package handlers

import (
	"collaborative/internal/auth"
	"collaborative/internal/middlewares"
	"collaborative/internal/model"
	"collaborative/internal/storage"
	"encoding/json"
	"net/http"
	"path/filepath"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

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

// ServeStaticFile serves static HTML files
func ServeStaticFile(filename string, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			SendJSONError(res, "Only GET request allowed!", http.StatusMethodNotAllowed, logger)
			return
		}
		http.ServeFile(res, req, filepath.Join("static", filename))
	}
}

// HTML page handlers
func IndexHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return ServeStaticFile("index.html", logger)
}

func LoginPageHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return ServeStaticFile("login.html", logger)
}

func RegisterPageHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return ServeStaticFile("register.html", logger)
}

func ProfilePageHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return ServeStaticFile("profile.html", logger)
}

func RegisterHandler(dbStor *storage.DBStorage, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			SendJSONError(res, "Only POST request allowed!", http.StatusMethodNotAllowed, logger)
			return
		}

		var auth model.AuthRequest
		if err := json.NewDecoder(req.Body).Decode(&auth); err != nil {
			SendJSONError(res, "Invalid request", http.StatusBadRequest, logger)
			return
		}

		if auth.Login == "" || auth.Password == "" {
			SendJSONError(res, "Login and password are required", http.StatusBadRequest, logger)
			return
		}

		if len(auth.Password) < 8 {
			SendJSONError(res, "Password must be at least 8 characters", http.StatusBadRequest, logger)
			return
		}

		exists, err := dbStor.UserExists(auth.Login)
		if err != nil {
			logger.Errorf("Failed to check user: %v", err)
			SendJSONError(res, "Internal error", http.StatusInternalServerError, logger)
			return
		}
		if exists {
			SendJSONError(res, "User already exists", http.StatusConflict, logger)
			return
		}

		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(auth.Password), bcrypt.DefaultCost)
		if err != nil {
			logger.Errorf("Failed to hash password")
			SendJSONError(res, "Internal error", http.StatusInternalServerError, logger)
			return
		}

		if err := dbStor.CreateUser(auth.Login, string(hashedPassword)); err != nil {
			logger.Errorf("Failed to create user: %v", err)
			SendJSONError(res, "Failed to create user", http.StatusInternalServerError, logger)
			return
		}

		SendJSONResponse(res, http.StatusCreated, model.AuthResponse{
			Message: "User registered successfully",
			Login:   auth.Login,
		}, logger)
	}
}

func LoginHandler(dbStor *storage.DBStorage, jwtService *auth.JWTService, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			SendJSONError(res, "Only POST request allowed!", http.StatusMethodNotAllowed, logger)
			return
		}

		var authReq model.AuthRequest
		if err := json.NewDecoder(req.Body).Decode(&authReq); err != nil {
			SendJSONError(res, "Invalid request body", http.StatusBadRequest, logger)
			return
		}

		// Validation
		if authReq.Login == "" || authReq.Password == "" {
			SendJSONError(res, "Login and password are required", http.StatusBadRequest, logger)
			return
		}

		// Get user from DB
		user, err := dbStor.GetUser(authReq.Login)
		if err != nil {
			logger.Errorf("Failed to get user: %v", err)
			SendJSONError(res, "Invalid credentials", http.StatusUnauthorized, logger)
			return
		}

		// Check password
		if err := bcrypt.CompareHashAndPassword([]byte(user.Auth.Password), []byte(authReq.Password)); err != nil {
			logger.Warnf("Failed login attempt for user: %s", authReq.Login)
			SendJSONError(res, "Invalid credentials", http.StatusUnauthorized, logger)
			return
		}

		// Generate JWT token
		token, err := jwtService.GenerateToken(authReq.Login)
		if err != nil {
			logger.Errorf("Failed to generate token: %v", err)
			SendJSONError(res, "Internal server error", http.StatusInternalServerError, logger)
			return
		}

		// Sending response with token
		SendJSONResponse(res, http.StatusOK, model.AuthResponse{
			Message: "Login successful",
			Login:   authReq.Login,
			Token:   token,
		}, logger)
	}
}

func ProfileHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
		// Get user from context
		login, ok := middlewares.GetUserFromContext(req.Context())
		if !ok {
			SendJSONError(res, "Unauthorized", http.StatusUnauthorized, logger)
			return
		}

		SendJSONResponse(res, http.StatusOK, map[string]interface{}{
			"login":   login,
			"message": "Welcome to your profile!",
		}, logger)
	}
}
