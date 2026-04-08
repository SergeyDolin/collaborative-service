package handlers

import (
	"collaborative/internal/auth"
	"collaborative/internal/model"
	"collaborative/internal/storage"
	"collaborative/internal/validators"
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// RegisterHandler обрабатывает регистрацию пользователя
func RegisterHandler(
	dbStor *storage.DBStorage,
	logger *zap.SugaredLogger,
) http.HandlerFunc {
	validator := validators.NewAuthValidator()

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			SendJSONError(w, "Only POST request allowed", http.StatusMethodNotAllowed, logger)
			return
		}

		var authReq model.AuthRequest
		if err := json.NewDecoder(r.Body).Decode(&authReq); err != nil {
			SendJSONError(w, "Invalid request body", http.StatusBadRequest, logger)
			return
		}

		// Валидируем логин и пароль
		if err := validator.ValidateLogin(authReq.Login); err != nil {
			SendJSONError(w, err.Error(), http.StatusBadRequest, logger)
			return
		}

		if err := validator.ValidatePassword(authReq.Password); err != nil {
			SendJSONError(w, err.Error(), http.StatusBadRequest, logger)
			return
		}

		// Проверяем наличие пользователя
		exists, err := dbStor.UserExists(authReq.Login)
		if err != nil {
			logger.Errorf("Failed to check user: %v", err)
			SendJSONError(w, "Internal error", http.StatusInternalServerError, logger)
			return
		}

		if exists {
			SendJSONError(w, "User already exists", http.StatusConflict, logger)
			return
		}

		// Хешируем пароль
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(authReq.Password), bcrypt.DefaultCost)
		if err != nil {
			logger.Errorf("Failed to hash password: %v", err)
			SendJSONError(w, "Internal error", http.StatusInternalServerError, logger)
			return
		}

		// Создаем пользователя
		if err := dbStor.CreateUser(authReq.Login, string(hashedPassword)); err != nil {
			logger.Errorf("Failed to create user: %v", err)
			SendJSONError(w, "Failed to create user", http.StatusInternalServerError, logger)
			return
		}

		logger.Infof("User registered: %s", authReq.Login)

		SendJSONResponse(w, http.StatusCreated, model.AuthResponse{
			Message: "User registered successfully",
			Login:   authReq.Login,
		}, logger)
	}
}

// LoginHandler обрабатывает вход пользователя
func LoginHandler(
	dbStor *storage.DBStorage,
	jwtService *auth.JWTService,
	logger *zap.SugaredLogger,
) http.HandlerFunc {
	validator := validators.NewAuthValidator()

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			SendJSONError(w, "Only POST request allowed", http.StatusMethodNotAllowed, logger)
			return
		}

		var authReq model.AuthRequest
		if err := json.NewDecoder(r.Body).Decode(&authReq); err != nil {
			SendJSONError(w, "Invalid request body", http.StatusBadRequest, logger)
			return
		}

		// Валидируем входные данные
		if err := validator.ValidateLogin(authReq.Login); err != nil {
			SendJSONError(w, err.Error(), http.StatusBadRequest, logger)
			return
		}

		// Получаем пользователя
		user, err := dbStor.GetUser(authReq.Login)
		if err != nil {
			logger.Warnf("Login attempt for non-existent user: %s", authReq.Login)
			SendJSONError(w, "Invalid credentials", http.StatusUnauthorized, logger)
			return
		}

		// Проверяем пароль
		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(authReq.Password)); err != nil {
			logger.Warnf("Failed login attempt for user: %s", authReq.Login)
			SendJSONError(w, "Invalid credentials", http.StatusUnauthorized, logger)
			return
		}

		// Генерируем токен
		token, err := jwtService.GenerateToken(authReq.Login)
		if err != nil {
			logger.Errorf("Failed to generate token: %v", err)
			SendJSONError(w, "Internal server error", http.StatusInternalServerError, logger)
			return
		}

		logger.Infof("User logged in: %s", authReq.Login)

		SendJSONResponse(w, http.StatusOK, model.AuthResponse{
			Message: "Login successful",
			Login:   authReq.Login,
			Token:   token,
		}, logger)
	}
}
