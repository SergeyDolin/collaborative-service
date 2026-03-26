package handlers

import (
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

func sendJSONResponse(res http.ResponseWriter, status int, data interface{}, logger *zap.SugaredLogger) {
	res.Header().Set("Content-Type", "application/json")

	res.WriteHeader(status)

	if err := json.NewEncoder(res).Encode(data); err != nil {
		logger.Errorf("Failed to encode response: %v", err)
	}
}

func sendJSONError(res http.ResponseWriter, msg string, status int, logger *zap.SugaredLogger) {
	sendJSONResponse(res, status, ErrorResponse{Error: msg}, logger)
}

func IndexHandler(res http.ResponseWriter, req *http.Request) {
	logger, err := zap.NewDevelopment()
	if err != nil {
		logger.Fatal("cannot initialize zap")
	}
	defer logger.Sync()

	sugar := logger.Sugar()

	if req.Method != http.MethodGet {
		sendJSONError(res, "Only GET request allowed!", http.StatusMethodNotAllowed, sugar)
		return
	}

	http.ServeFile(res, req, filepath.Join("static", "index.html"))
}

func RegisterHandler(dbStor *storage.DBStorage, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			sendJSONError(res, "Only POST request allowed!", http.StatusMethodNotAllowed, logger)
			return
		}

		var auth model.AuthRequest
		if err := json.NewDecoder(req.Body).Decode(&auth); err != nil {
			sendJSONError(res, "Invalid request", http.StatusBadRequest, logger)
		}

		exists, err := dbStor.UserExists(auth.Login)
		if err != nil {
			logger.Errorf("Failed to check user: %v", err)
			sendJSONError(res, "Internal error", http.StatusInternalServerError, logger)
			return
		}
		if exists {
			sendJSONError(res, "User already exists", http.StatusConflict, logger)
			return
		}

		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(auth.Password), bcrypt.DefaultCost)
		if err != nil {
			logger.Errorf("Failed to hash password")
			sendJSONError(res, "Internal error", http.StatusInternalServerError, logger)
			return
		}

		if err := dbStor.CreateUser(auth.Login, string(hashedPassword)); err != nil {
			logger.Errorf("Failed to create user: %v", err)
			sendJSONError(res, "Failed to create user", http.StatusInternalServerError, logger)
			return
		}

		sendJSONResponse(res, http.StatusCreated, model.AuthResponse{
			Message: "User registered successfully",
			Login:   auth.Login,
		}, logger)
	}
}
