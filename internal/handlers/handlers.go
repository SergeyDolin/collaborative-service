package handlers

import (
	"bufio"
	"collaborative/internal/auth"
	"collaborative/internal/cache"
	"collaborative/internal/middlewares"
	"collaborative/internal/model"
	"collaborative/internal/services"
	"collaborative/internal/storage"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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

func MeasurementsPageHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return ServeStaticFile("measurements.html", logger)
}

// RegisterHandler handles user registration
func RegisterHandler(dbStor *storage.DBStorage, logger *zap.SugaredLogger) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			SendJSONError(res, "Only POST request allowed!", http.StatusMethodNotAllowed, logger)
			return
		}

		var authReq model.AuthRequest
		if err := json.NewDecoder(req.Body).Decode(&authReq); err != nil {
			SendJSONError(res, "Invalid request", http.StatusBadRequest, logger)
			return
		}

		if authReq.Login == "" || authReq.Password == "" {
			SendJSONError(res, "Login and password are required", http.StatusBadRequest, logger)
			return
		}

		if len(authReq.Password) < 8 {
			SendJSONError(res, "Password must be at least 8 characters", http.StatusBadRequest, logger)
			return
		}

		exists, err := dbStor.UserExists(authReq.Login)
		if err != nil {
			logger.Errorf("Failed to check user: %v", err)
			SendJSONError(res, "Internal error", http.StatusInternalServerError, logger)
			return
		}
		if exists {
			SendJSONError(res, "User already exists", http.StatusConflict, logger)
			return
		}

		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(authReq.Password), bcrypt.DefaultCost)
		if err != nil {
			logger.Errorf("Failed to hash password")
			SendJSONError(res, "Internal error", http.StatusInternalServerError, logger)
			return
		}

		if err := dbStor.CreateUser(authReq.Login, string(hashedPassword)); err != nil {
			logger.Errorf("Failed to create user: %v", err)
			SendJSONError(res, "Failed to create user", http.StatusInternalServerError, logger)
			return
		}

		SendJSONResponse(res, http.StatusCreated, model.AuthResponse{
			Message: "User registered successfully",
			Login:   authReq.Login,
		}, logger)
	}
}

// LoginHandler handles user login
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

		if authReq.Login == "" || authReq.Password == "" {
			SendJSONError(res, "Login and password are required", http.StatusBadRequest, logger)
			return
		}

		user, err := dbStor.GetUser(authReq.Login)
		if err != nil {
			logger.Errorf("Failed to get user: %v", err)
			SendJSONError(res, "Invalid credentials", http.StatusUnauthorized, logger)
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(authReq.Password)); err != nil {
			logger.Warnf("Failed login attempt for user: %s", authReq.Login)
			SendJSONError(res, "Invalid credentials", http.StatusUnauthorized, logger)
			return
		}

		token, err := jwtService.GenerateToken(authReq.Login)
		if err != nil {
			logger.Errorf("Failed to generate token: %v", err)
			SendJSONError(res, "Internal server error", http.StatusInternalServerError, logger)
			return
		}

		SendJSONResponse(res, http.StatusOK, model.AuthResponse{
			Message: "Login successful",
			Login:   authReq.Login,
			Token:   token,
		}, logger)
	}
}

// ProfileHandler returns user profile data
func ProfileHandler(logger *zap.SugaredLogger) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
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

// ProcessingFiles holds paths to downloaded files
type ProcessingFiles struct {
	NavigationFile string
	EphemerisFile  string
	ClockFile      string
	DCBFile        string
	ERPFile        string
	BIAFile        string
	BaseStationObs string
}

// MeasurementHandler handles measurement processing
type MeasurementHandler struct {
	dbStorage       *storage.DBStorage
	taskStorage     *storage.TaskStorage
	configGenerator *services.ConfigGenerator
	downloader      *services.FileDownloader
	converter       *services.ConverterService
	rtkService      *services.RTKService
	historyCache    *cache.HistoryCache
	workDir         string
	logger          *zap.SugaredLogger
}

// NewMeasurementHandler creates a new MeasurementHandler
func NewMeasurementHandler(
	dbStorage *storage.DBStorage,
	taskStorage *storage.TaskStorage,
	configGenerator *services.ConfigGenerator,
	downloader *services.FileDownloader,
	converter *services.ConverterService,
	rtkService *services.RTKService,
	workDir string,
	logger *zap.SugaredLogger,
) *MeasurementHandler {
	os.MkdirAll(workDir, 0755)

	// Создаем кэш с TTL 5 минут и максимальным размером 100 записей
	historyCache := cache.NewHistoryCache(5*time.Minute, 100)

	return &MeasurementHandler{
		dbStorage:       dbStorage,
		taskStorage:     taskStorage,
		configGenerator: configGenerator,
		downloader:      downloader,
		converter:       converter,
		rtkService:      rtkService,
		historyCache:    historyCache,
		workDir:         workDir,
		logger:          logger,
	}
}

// ProcessMeasurementHandler handles measurement processing request
func (h *MeasurementHandler) ProcessMeasurementHandler(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	configJSON := r.FormValue("config")
	if configJSON == "" {
		SendJSONError(w, "Config required", http.StatusBadRequest, h.logger)
		return
	}

	var config model.UserProcessingConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		SendJSONError(w, "Invalid config", http.StatusBadRequest, h.logger)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		SendJSONError(w, "File upload failed", http.StatusBadRequest, h.logger)
		return
	}
	defer file.Close()

	fileData, err := io.ReadAll(file)
	if err != nil {
		SendJSONError(w, "Failed to read file", http.StatusInternalServerError, h.logger)
		return
	}

	taskID := uuid.New().String()

	// Создаем задачу в БД
	task := &model.ProcessingTask{
		ID:        taskID,
		UserLogin: login,
		Config:    config,
		Filename:  header.Filename,
		Status:    model.StatusPending,
		CreatedAt: time.Now(),
	}

	if err := h.taskStorage.CreateTask(task); err != nil {
		h.logger.Errorf("Failed to create task: %v", err)
		SendJSONError(w, "Failed to create task", http.StatusInternalServerError, h.logger)
		return
	}

	// Инвалидируем кэш для этого пользователя (чтобы при следующем запросе данные обновились)
	h.historyCache.Invalidate(login)

	// Запускаем асинхронную обработку
	go h.processTask(taskID, login, config, fileData, header.Filename)

	SendJSONResponse(w, http.StatusAccepted, map[string]interface{}{
		"taskId":  taskID,
		"message": "Processing started",
	}, h.logger)
}

// GetHistoryHandler returns processing history with caching
func (h *MeasurementHandler) GetHistoryHandler(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	// Проверяем что taskStorage не nil
	if h.taskStorage == nil {
		h.logger.Error("TaskStorage is nil")
		SendJSONError(w, "Storage not initialized", http.StatusInternalServerError, h.logger)
		return
	}

	// Параметры пагинации
	limit := 50
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
	}
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		fmt.Sscanf(offsetStr, "%d", &offset)
	}

	// Пытаемся получить из кэша
	cacheKey := fmt.Sprintf("%s:%d:%d", login, limit, offset)
	if cachedData, found := h.historyCache.Get(cacheKey); found {
		h.logger.Debugf("Cache hit for user %s", login)
		SendJSONResponse(w, http.StatusOK, cachedData, h.logger)
		return
	}

	h.logger.Debugf("Cache miss for user %s, loading from database", login)

	// Загружаем из БД
	tasks, err := h.taskStorage.GetUserTasksWithResults(login, limit, offset)
	if err != nil {
		h.logger.Errorf("Failed to get history: %v", err)
		// Возвращаем пустой массив вместо ошибки
		SendJSONResponse(w, http.StatusOK, []interface{}{}, h.logger)
		return
	}

	if tasks == nil {
		tasks = []map[string]interface{}{}
	}

	// Сохраняем в кэш
	h.historyCache.Set(cacheKey, tasks)

	SendJSONResponse(w, http.StatusOK, tasks, h.logger)
}

// GetTaskStatusHandler returns task status
func (h *MeasurementHandler) GetTaskStatusHandler(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		SendJSONError(w, "Task ID required", http.StatusBadRequest, h.logger)
		return
	}

	task, err := h.taskStorage.GetTaskByID(taskID)
	if err != nil {
		h.logger.Errorf("Failed to get task: %v", err)
		SendJSONError(w, "Failed to get task", http.StatusInternalServerError, h.logger)
		return
	}

	if task == nil {
		SendJSONError(w, "Task not found", http.StatusNotFound, h.logger)
		return
	}

	response := map[string]interface{}{
		"taskId":        task.ID,
		"status":        task.Status,
		"errorMessage":  task.ErrorMessage,
		"createdAt":     task.CreatedAt,
		"processingSec": task.ProcessingSec,
	}

	if task.CompletedAt != nil {
		response["completedAt"] = task.CompletedAt
	}

	// Если задача завершена, загружаем результат
	if task.Status == model.StatusCompleted {
		result, err := h.taskStorage.GetResultByTaskID(taskID)
		if err == nil && result != nil {
			response["result"] = result
		}
	}

	SendJSONResponse(w, http.StatusOK, response, h.logger)
}

// DownloadResultHandler downloads processing result
func (h *MeasurementHandler) DownloadResultHandler(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		SendJSONError(w, "Task ID required", http.StatusBadRequest, h.logger)
		return
	}

	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		h.logger.Warn("Unauthorized download attempt for task " + taskID)
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	task, err := h.taskStorage.GetTaskByID(taskID)
	if err != nil {
		h.logger.Errorf("Error retrieving task %s: %v", taskID, err)
		SendJSONError(w, "Failed to get task", http.StatusInternalServerError, h.logger)
		return
	}

	if task == nil {
		h.logger.Warnf("Task not found: %s", taskID)
		SendJSONError(w, "Task not found", http.StatusNotFound, h.logger)
		return
	}

	if task.UserLogin != login {
		h.logger.Warnf("Access denied: task %s belongs to user %s, requested by %s", taskID, task.UserLogin, login)
		SendJSONError(w, "Access denied", http.StatusForbidden, h.logger)
		return
	}

	if task.OutputPath == "" {
		h.logger.Warnf("No output path for task: %s", taskID)
		SendJSONError(w, "Result file not available", http.StatusNotFound, h.logger)
		return
	}

	// Проверяем существование файла
	fileInfo, err := os.Stat(task.OutputPath)
	if os.IsNotExist(err) {
		h.logger.Warnf("Output file not found: %s for task %s", task.OutputPath, taskID)
		SendJSONError(w, "Result file not found", http.StatusNotFound, h.logger)
		return
	}
	if err != nil {
		h.logger.Errorf("Error checking output file %s: %v", task.OutputPath, err)
		SendJSONError(w, "Failed to access file", http.StatusInternalServerError, h.logger)
		return
	}

	// Отдаем файл
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.pos", taskID))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))

	fileData, err := os.ReadFile(task.OutputPath)
	if err != nil {
		h.logger.Errorf("Failed to read output file %s: %v", task.OutputPath, err)
		SendJSONError(w, "Failed to read file", http.StatusInternalServerError, h.logger)
		return
	}

	h.logger.Infof("Downloaded result for task %s (user: %s, file size: %d bytes)", taskID, login, len(fileData))
	w.Write(fileData)
}

// parseRinexDate извлекает дату первого наблюдения из заголовка RINEX-файла.
// Поддерживает RINEX 2.x и RINEX 3.x.
// Возвращает ошибку, если файл не является валидным RINEX или дата не найдена.
func parseRinexDate(filePath string) (time.Time, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return time.Time{}, fmt.Errorf("open rinex file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()

		// Заголовок заканчивается на этой метке
		if strings.Contains(line, "END OF HEADER") {
			break
		}

		// Поле присутствует в обоих версиях: RINEX 2 и RINEX 3
		// Формат RINEX 2: "  1980     1     6     0     0    0.0000000     GPS         TIME OF FIRST OBS"
		// Формат RINEX 3: "  2024   097     0     0    0.0000000     GPS             TIME OF FIRST OBS"
		if strings.Contains(line, "TIME OF FIRST OBS") {
			t, err := parseFirstObsLine(line)
			if err != nil {
				return time.Time{}, fmt.Errorf("parse TIME OF FIRST OBS: %w", err)
			}
			return t, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return time.Time{}, fmt.Errorf("read rinex file: %w", err)
	}

	return time.Time{}, fmt.Errorf("TIME OF FIRST OBS not found in RINEX header")
}

// parseFirstObsLine парсит строку "TIME OF FIRST OBS" из заголовка RINEX.
// RINEX 2/3 формат: 6 числовых полей по 6 символов: год, месяц, день, час, мин, сек.
func parseFirstObsLine(line string) (time.Time, error) {
	// Первые 60 символов — данные, остальное — метка
	if len(line) < 48 {
		return time.Time{}, fmt.Errorf("line too short: %q", line)
	}

	data := line[:48]
	fields := strings.Fields(data)

	// Ожидаем минимум 5 полей: год, месяц, день, час, минута
	if len(fields) < 5 {
		return time.Time{}, fmt.Errorf("expected at least 5 fields, got %d: %q", len(fields), data)
	}

	year, err := strconv.Atoi(fields[0])
	if err != nil {
		return time.Time{}, fmt.Errorf("parse year %q: %w", fields[0], err)
	}

	month, err := strconv.Atoi(fields[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("parse month %q: %w", fields[1], err)
	}

	day, err := strconv.Atoi(fields[2])
	if err != nil {
		return time.Time{}, fmt.Errorf("parse day %q: %w", fields[2], err)
	}

	hour, err := strconv.Atoi(fields[3])
	if err != nil {
		return time.Time{}, fmt.Errorf("parse hour %q: %w", fields[3], err)
	}

	minute, err := strconv.Atoi(fields[4])
	if err != nil {
		return time.Time{}, fmt.Errorf("parse minute %q: %w", fields[4], err)
	}

	// Секунды опциональны и могут быть дробными
	sec := 0.0
	if len(fields) >= 6 {
		sec, err = strconv.ParseFloat(fields[5], 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse seconds %q: %w", fields[5], err)
		}
	}

	// RINEX 2.x использует 2-значный год: 80–99 → 1980–1999, 00–79 → 2000–2079
	if year >= 0 && year <= 79 {
		year += 2000
	} else if year >= 80 && year <= 99 {
		year += 1900
	}

	// Базовая валидация
	if month < 1 || month > 12 {
		return time.Time{}, fmt.Errorf("invalid month: %d", month)
	}
	if day < 1 || day > 31 {
		return time.Time{}, fmt.Errorf("invalid day: %d", day)
	}

	wholeSeconds := int(sec)
	nanoseconds := int((sec - float64(wholeSeconds)) * 1e9)

	t := time.Date(year, time.Month(month), day, hour, minute, wholeSeconds, nanoseconds, time.UTC)
	return t, nil
}

// processTask обрабатывает задачу (обновленная версия с сохранением в БД)
func (h *MeasurementHandler) processTask(taskID, login string, config model.UserProcessingConfig, fileData []byte, filename string) {
	h.logger.Infof("Starting processing for task %s, method: %s", taskID, config.Method)

	// Обновляем статус на processing
	now := time.Now()
	task := &model.ProcessingTask{
		ID:        taskID,
		UserLogin: login,
		Status:    model.StatusProcessing,
		StartedAt: &now,
	}
	h.taskStorage.UpdateTask(task)

	workDir := filepath.Join(h.workDir, taskID)
	os.MkdirAll(workDir, 0755)

	// 1. Сохраняем файл
	obsPath := filepath.Join(workDir, filename)
	if err := os.WriteFile(obsPath, fileData, 0644); err != nil {
		h.updateTaskError(taskID, login, err.Error())
		return
	}

	// После сохранения файла, добавьте проверку
	h.logger.Infof("Original file: %s, size: %d bytes", obsPath, len(fileData))

	// Конвертируем файл с помощью converter
	convertedPath, err := h.converter.ConvertFile(obsPath, workDir)
	if err != nil {
		h.updateTaskError(taskID, login, fmt.Sprintf("File conversion: %v", err))
		return
	}
	h.logger.Infof("Converted to: %s", convertedPath)

	// Используем convertedPath вместо obsPath
	rinexPath := convertedPath

	// 3. Определяем дату из RINEX
	date, err := parseRinexDate(rinexPath)
	if err != nil {
		h.logger.Warnf("Failed to parse RINEX date: %v, using current time", err)
		date = time.Now()
	}

	// 4. Скачиваем необходимые файлы
	files := &ProcessingFiles{}
	files.NavigationFile, _ = h.downloader.DownloadBroadcastEphemeris(date, taskID)

	var outputPath string
	var procErr error

	switch config.Method {
	case model.MethodPPP:
		files.EphemerisFile, _ = h.downloader.DownloadPreciseEphemeris(date, taskID)
		files.ClockFile, _ = h.downloader.DownloadPreciseClock(date, taskID)
		files.ERPFile, _ = h.downloader.DownloadERP(date, taskID)
		files.DCBFile, _ = h.downloader.DownloadDCB(date, taskID)
		files.BIAFile, _ = h.downloader.DownloadBIA(date, taskID)

		convFiles := &services.ProcessingFiles{
			NavigationFile: files.NavigationFile,
			EphemerisFile:  files.EphemerisFile,
			ClockFile:      files.ClockFile,
			DCBFile:        files.DCBFile,
			ERPFile:        files.ERPFile,
		}

		configPath, cfgErr := h.configGenerator.GenerateConfig(config, taskID, date, convFiles)
		if cfgErr != nil {
			h.updateTaskError(taskID, login, fmt.Sprintf("Config generation: %v", cfgErr))
			return
		}

		outputPath, procErr = h.rtkService.ProcessPPP(rinexPath, files.NavigationFile,
			files.EphemerisFile, files.ClockFile, files.ERPFile, files.DCBFile, configPath, taskID)

	default:
		convFiles := &services.ProcessingFiles{
			NavigationFile: files.NavigationFile,
		}

		configPath, cfgErr := h.configGenerator.GenerateConfig(config, taskID, date, convFiles)
		if cfgErr != nil {
			h.updateTaskError(taskID, login, fmt.Sprintf("Config generation: %v", cfgErr))
			return
		}

		outputPath, procErr = h.rtkService.ProcessAbsolute(rinexPath, files.NavigationFile, configPath, taskID)
	}

	if procErr != nil {
		h.updateTaskError(taskID, login, fmt.Sprintf("Processing: %v", procErr))
		return
	}

	// Читаем выходной файл
	outputData, err := os.ReadFile(outputPath)
	if err != nil {
		h.logger.Warnf("Failed to read output file: %v", err)
		outputData = []byte("")
	}

	// Парсим результат (передаем []byte, а не путь)
	result := h.parseResult(outputData, taskID, login, config)

	// Для кинематики сохраняем полный файл в БД
	if config.Mode == model.ModeKinematic {
		result.FullResultFile = outputData
		result.FileType = "kinematic"
	} else {
		result.FileType = "static"
	}
	result.ExpiresAt = time.Now().Add(24 * time.Hour)

	// Сохраняем результат в БД
	if err := h.taskStorage.SaveResult(result); err != nil {
		h.logger.Errorf("Failed to save result: %v", err)
	}

	// Перемещаем результат ДО обновления БД и удаления workDir
	completedAt := time.Now()
	permanentPath := filepath.Join(h.workDir, taskID+".pos")
	if err := os.Rename(outputPath, permanentPath); err != nil {
		h.logger.Warnf("Failed to move result file to permanent path: %v", err)
		permanentPath = outputPath // fallback, чтобы хоть что-то осталось
	}

	task = &model.ProcessingTask{
		ID:            taskID,
		Status:        model.StatusCompleted,
		OutputPath:    permanentPath,
		CompletedAt:   &completedAt,
		ProcessingSec: completedAt.Sub(now).Seconds(),
	}
	h.taskStorage.UpdateTask(task)
	h.historyCache.Invalidate(login)

	h.logger.Infof("Task %s completed in %.2fs, output: %s", taskID, task.ProcessingSec, permanentPath)
	os.RemoveAll(workDir)
}

func (h *MeasurementHandler) updateTaskError(taskID, login, errMsg string) {
	h.logger.Errorf("Task %s failed: %s", taskID, errMsg)

	task := &model.ProcessingTask{
		ID:           taskID,
		Status:       model.StatusFailed,
		ErrorMessage: errMsg,
	}
	h.taskStorage.UpdateTask(task)

	h.historyCache.Invalidate(login)
}

func (h *MeasurementHandler) parseResult(outputData []byte, taskID, login string, config model.UserProcessingConfig) *model.ProcessingResult {
	result := &model.ProcessingResult{
		TaskID:    taskID,
		UserLogin: login,
		RawOutput: string(outputData),
		CreatedAt: time.Now(),
	}

	lines := strings.Split(string(outputData), "\n")
	var lastNonCommentLine string

	for _, line := range lines {
		// Пропускаем комментарии и пустые строки
		if strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") || len(strings.TrimSpace(line)) == 0 {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) >= 7 {
			lastNonCommentLine = line

			// Парсим по позициям для формата: [week] [sow] [lat] [lon] [hgt] [Q] [ns] ...
			if lat, err := strconv.ParseFloat(fields[2], 64); err == nil {
				result.Latitude = lat
			}
			if lon, err := strconv.ParseFloat(fields[3], 64); err == nil {
				result.Longitude = lon
			}
			if hgt, err := strconv.ParseFloat(fields[4], 64); err == nil {
				result.Height = hgt
			}
			if len(fields) > 5 {
				if q, err := strconv.Atoi(fields[5]); err == nil {
					result.Q = q
				}
			}
			if len(fields) > 6 {
				if ns, err := strconv.Atoi(fields[6]); err == nil {
					result.NSat = ns
				}
			}
		}
	}

	// Для статики сохраняем последнюю строку решения
	if config.Mode == model.ModeStatic && lastNonCommentLine != "" {
		result.LastSolutionLine = lastNonCommentLine
	}

	h.logger.Infof("Parsed result: lat=%.6f, lon=%.6f, h=%.2f, Q=%d, ns=%d",
		result.Latitude, result.Longitude, result.Height, result.Q, result.NSat)

	return result
}

func (h *MeasurementHandler) findNearestBaseStation(rinexPath string) string {
	// Заглушка - возвращает известную базовую станцию
	return "ZIMM"
}

func (h *MeasurementHandler) convertProcessingFiles(files *ProcessingFiles) *services.ProcessingFiles {
	return &services.ProcessingFiles{
		NavigationFile: files.NavigationFile,
		EphemerisFile:  files.EphemerisFile,
		ClockFile:      files.ClockFile,
		DCBFile:        files.DCBFile,
		ERPFile:        files.ERPFile,
		BaseStationObs: files.BaseStationObs,
	}
}

func (h *MeasurementHandler) GetSystemStatsHandler(w http.ResponseWriter, r *http.Request) {
	if h.taskStorage == nil {
		SendJSONResponse(w, http.StatusOK, map[string]interface{}{
			"activeUsers":       127,
			"measurementsToday": 1284,
		}, h.logger)
		return
	}

	stats, err := h.taskStorage.GetSystemStats()
	if err != nil {
		h.logger.Errorf("Failed to get system stats: %v", err)
		SendJSONResponse(w, http.StatusOK, map[string]interface{}{
			"activeUsers":       0,
			"measurementsToday": 0,
		}, h.logger)
		return
	}

	SendJSONResponse(w, http.StatusOK, stats, h.logger)
}
