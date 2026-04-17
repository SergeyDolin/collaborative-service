package handlers

import (
	"collaborative/internal/auth"
	"collaborative/internal/model"
	"collaborative/internal/storage"
	"collaborative/internal/validators"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// applyDeviceDefaults валидирует устройство перед сохранением.
// Для ГНСС-приёмника требует имя антенны.
// Для мобильных устройств принимает методы "none" (смещения = 0) и "manual" (смещения обязательны).
func applyDeviceDefaults(d *model.UserDevice) error {
	if d.DeviceType == model.DeviceTypeGNSS {
		if d.AntennaName == "" {
			return fmt.Errorf("для ГНСС-приёмника необходимо указать название антенны в формате RINEX")
		}
	} else if d.DeviceType != "" {
		if d.PhaseCenterMethod != "none" && d.PhaseCenterMethod != "manual" {
			return fmt.Errorf("метод фазового центра должен быть 'none' или 'manual'")
		}
		if d.PhaseCenterMethod == "manual" && d.AntennaE == 0 && d.AntennaN == 0 && d.AntennaU == 0 {
			return fmt.Errorf("при ручном вводе необходимо указать хотя бы одно ненулевое смещение ENU")
		}
	}
	return nil
}

const maxAvatarSize = 5 * 1024 * 1024 // 5 MB

// RegisterHandler обрабатывает регистрацию пользователя.
// Поддерживает как JSON (старый формат), так и multipart/form-data
// с полями: login, password, fullName, avatar (file), device (JSON).
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

		var login, password, fullName string
		var avatarData []byte
		var devicePtr *model.UserDevice

		ct := r.Header.Get("Content-Type")
		isMultipart := len(ct) > 19 && ct[:19] == "multipart/form-data"

		if isMultipart {
			if err := r.ParseMultipartForm(maxAvatarSize + 1*1024*1024); err != nil {
				SendJSONError(w, "Failed to parse form", http.StatusBadRequest, logger)
				return
			}
			login = r.FormValue("login")
			password = r.FormValue("password")
			fullName = r.FormValue("fullName")

			// Аватар — опционально
			file, _, err := r.FormFile("avatar")
			if err == nil {
				defer file.Close()
				raw, err := io.ReadAll(io.LimitReader(file, maxAvatarSize))
				if err == nil && len(raw) > 0 {
					avatarData = raw
				}
			}

			// Устройство — опционально
			if devJSON := r.FormValue("device"); devJSON != "" {
				var d model.UserDevice
				if err := json.Unmarshal([]byte(devJSON), &d); err == nil {
					devicePtr = &d
				}
			}
		}

		// Валидация
		if err := validator.ValidateLogin(login); err != nil {
			SendJSONError(w, err.Error(), http.StatusBadRequest, logger)
			return
		}
		if err := validator.ValidatePassword(password); err != nil {
			SendJSONError(w, err.Error(), http.StatusBadRequest, logger)
			return
		}

		exists, err := dbStor.UserExists(login)
		if err != nil {
			logger.Errorf("Failed to check user: %v", err)
			SendJSONError(w, "Internal error", http.StatusInternalServerError, logger)
			return
		}
		if exists {
			SendJSONError(w, "User already exists", http.StatusConflict, logger)
			return
		}

		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			logger.Errorf("Failed to hash password: %v", err)
			SendJSONError(w, "Internal error", http.StatusInternalServerError, logger)
			return
		}

		if err := dbStor.CreateUser(login, string(hashedPassword)); err != nil {
			logger.Errorf("Failed to create user: %v", err)
			SendJSONError(w, "Failed to create user", http.StatusInternalServerError, logger)
			return
		}

		// Сохраняем профиль (ФИО + аватар)
		if fullName != "" || avatarData != nil {
			if err := dbStor.UpdateUserProfile(login, fullName, avatarData); err != nil {
				logger.Warnf("Failed to save profile for %s: %v", login, err)
			}
		}

		// Сохраняем устройство
		if devicePtr != nil && devicePtr.Name != "" {
			devicePtr.UserLogin = login
			if err := applyDeviceDefaults(devicePtr); err != nil {
				logger.Warnf("Device validation failed for %s: %v", login, err)
			} else if err := dbStor.CreateDevice(devicePtr); err != nil {
				logger.Warnf("Failed to save device for %s: %v", login, err)
			}
		}

		logger.Infof("User registered: %s (fullName=%q, hasAvatar=%v, hasDevice=%v)",
			login, fullName, avatarData != nil, devicePtr != nil)

		SendJSONResponse(w, http.StatusCreated, model.AuthResponse{
			Message: "User registered successfully",
			Login:   login,
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

		if err := validator.ValidateLogin(authReq.Login); err != nil {
			SendJSONError(w, err.Error(), http.StatusBadRequest, logger)
			return
		}

		user, err := dbStor.GetUser(authReq.Login)
		if err != nil {
			logger.Warnf("Login attempt for non-existent user: %s", authReq.Login)
			SendJSONError(w, "Invalid credentials", http.StatusUnauthorized, logger)
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(authReq.Password)); err != nil {
			logger.Warnf("Failed login attempt for user: %s", authReq.Login)
			SendJSONError(w, "Invalid credentials", http.StatusUnauthorized, logger)
			return
		}

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

// ProfileDataHandler возвращает полные данные профиля (ФИО, аватар, устройства)
func ProfileDataHandler(dbStor *storage.DBStorage, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		login, ok := GetUserFromContext(r)
		if !ok {
			SendJSONError(w, "Unauthorized", http.StatusUnauthorized, logger)
			return
		}

		fullName, avatar, createdAt, err := dbStor.GetUserProfile(login)
		if err != nil {
			logger.Errorf("GetUserProfile %s: %v", login, err)
			SendJSONError(w, "Internal error", http.StatusInternalServerError, logger)
			return
		}

		devices, err := dbStor.GetUserDevices(login)
		if err != nil {
			logger.Warnf("GetUserDevices %s: %v", login, err)
			devices = []model.UserDevice{}
		}
		if devices == nil {
			devices = []model.UserDevice{}
		}

		var avatarB64 string
		if len(avatar) > 0 {
			avatarB64 = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(avatar)
		}

		SendJSONResponse(w, http.StatusOK, map[string]interface{}{
			"login":     login,
			"fullName":  fullName,
			"avatar":    avatarB64,
			"createdAt": createdAt,
			"devices":   devices,
		}, logger)
	}
}

// UpdateProfileHandler обновляет ФИО и аватар
func UpdateProfileHandler(dbStor *storage.DBStorage, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		login, ok := GetUserFromContext(r)
		if !ok {
			SendJSONError(w, "Unauthorized", http.StatusUnauthorized, logger)
			return
		}

		if err := r.ParseMultipartForm(maxAvatarSize + 512*1024); err != nil {
			SendJSONError(w, "Failed to parse form", http.StatusBadRequest, logger)
			return
		}

		fullName := r.FormValue("fullName")

		var avatarData []byte
		file, _, err := r.FormFile("avatar")
		if err == nil {
			defer file.Close()
			raw, err := io.ReadAll(io.LimitReader(file, maxAvatarSize))
			if err == nil && len(raw) > 0 {
				avatarData = raw
			}
		}

		if err := dbStor.UpdateUserProfile(login, fullName, avatarData); err != nil {
			logger.Errorf("UpdateUserProfile %s: %v", login, err)
			SendJSONError(w, "Failed to update profile", http.StatusInternalServerError, logger)
			return
		}

		SendJSONResponse(w, http.StatusOK, map[string]string{"message": "Profile updated"}, logger)
	}
}

// AddDeviceHandler добавляет новое устройство
func AddDeviceHandler(dbStor *storage.DBStorage, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		login, ok := GetUserFromContext(r)
		if !ok {
			SendJSONError(w, "Unauthorized", http.StatusUnauthorized, logger)
			return
		}

		var d model.UserDevice
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			SendJSONError(w, "Invalid request body", http.StatusBadRequest, logger)
			return
		}
		if d.Name == "" {
			SendJSONError(w, "Device name is required", http.StatusBadRequest, logger)
			return
		}

		d.UserLogin = login
		if err := applyDeviceDefaults(&d); err != nil {
			SendJSONError(w, err.Error(), http.StatusBadRequest, logger)
			return
		}
		if err := dbStor.CreateDevice(&d); err != nil {
			logger.Errorf("CreateDevice %s: %v", login, err)
			SendJSONError(w, "Failed to add device", http.StatusInternalServerError, logger)
			return
		}

		SendJSONResponse(w, http.StatusCreated, d, logger)
	}
}

// DeleteDeviceHandler удаляет устройство
func DeleteDeviceHandler(dbStor *storage.DBStorage, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		login, ok := GetUserFromContext(r)
		if !ok {
			SendJSONError(w, "Unauthorized", http.StatusUnauthorized, logger)
			return
		}

		idStr := r.URL.Query().Get("id")
		if idStr == "" {
			SendJSONError(w, "Device ID required", http.StatusBadRequest, logger)
			return
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			SendJSONError(w, "Invalid device ID", http.StatusBadRequest, logger)
			return
		}

		if err := dbStor.DeleteDevice(id, login); err != nil {
			logger.Warnf("DeleteDevice %d user %s: %v", id, login, err)
			SendJSONError(w, fmt.Sprintf("Failed to delete device: %v", err), http.StatusInternalServerError, logger)
			return
		}

		SendJSONResponse(w, http.StatusOK, map[string]string{"message": "Device deleted"}, logger)
	}
}
