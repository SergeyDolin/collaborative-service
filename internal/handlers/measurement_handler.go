package handlers

import (
	"collaborative/internal/cache"
	"collaborative/internal/middlewares"
	"collaborative/internal/model"
	"collaborative/internal/services"
	"collaborative/internal/storage"
	"collaborative/internal/validators"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// MeasurementHandler обрабатывает запросы по измерениям
type MeasurementHandler struct {
	dbStorage      *storage.DBStorage
	taskStorage    *storage.TaskStorage
	measurementSvc *services.MeasurementService
	fileSvc        *services.FileService
	cacheSvc       *services.CacheService
	historyCache   *cache.HistoryCache
	logger         *zap.SugaredLogger
}

// NewMeasurementHandler создает новый обработчик измерений
func NewMeasurementHandler(
	dbStorage *storage.DBStorage,
	taskStorage *storage.TaskStorage,
	logger *zap.SugaredLogger,
) *MeasurementHandler {
	return &MeasurementHandler{
		dbStorage:    dbStorage,
		taskStorage:  taskStorage,
		historyCache: cache.NewHistoryCache(5*time.Minute, 100),
		logger:       logger,
	}
}

// ProcessMeasurementHandler обрабатывает загрузку и обработку измерений
func (h *MeasurementHandler) ProcessMeasurementHandler(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	// Проверяем размер запроса перед парсингом multipart формы
	if r.ContentLength > 1<<30 { // 1 GB
		SendJSONError(w, "File too large. Maximum size: 1 GB", http.StatusRequestEntityTooLarge, h.logger)
		return
	}

	// Устанавливаем максимальный размер для multipart формы
	if err := r.ParseMultipartForm(1 << 30); err != nil { // 1 GB
		SendJSONError(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest, h.logger)
		return
	}

	// Парсим конфигурацию
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

	// Валидируем конфигурацию
	configValidator := validators.NewConfigValidator()
	if err := configValidator.ValidateProcessingConfig(&config); err != nil {
		SendJSONError(w, err.Error(), http.StatusBadRequest, h.logger)
		return
	}

	// Получаем загруженный файл
	file, header, err := r.FormFile("file")
	if err != nil {
		SendJSONError(w, "File upload failed", http.StatusBadRequest, h.logger)
		return
	}
	defer file.Close()

	// Валидируем файл
	fileValidator := validators.NewFileValidator()
	if err := fileValidator.ValidateFilename(header.Filename); err != nil {
		SendJSONError(w, err.Error(), http.StatusBadRequest, h.logger)
		return
	}

	if err := fileValidator.ValidateFileSize(header.Size); err != nil {
		SendJSONError(w, err.Error(), http.StatusBadRequest, h.logger)
		return
	}

	// Читаем данные файла
	fileData, err := io.ReadAll(file)
	if err != nil {
		SendJSONError(w, "Failed to read file", http.StatusInternalServerError, h.logger)
		return
	}

	// Создаем задачу
	taskID := uuid.New().String()
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

	// Инвалидируем кэш
	h.historyCache.Invalidate(login)

	// Запускаем обработку асинхронно
	go h.processTaskAsync(taskID, login, config, fileData, header.Filename)

	h.logger.Infof("Task created: %s for user: %s", taskID, login)

	SendJSONResponse(w, http.StatusAccepted, map[string]interface{}{
		"taskId":  taskID,
		"message": "Processing started",
	}, h.logger)
}

// GetHistoryHandler возвращает историю обработок с кэшированием
func (h *MeasurementHandler) GetHistoryHandler(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	if h.taskStorage == nil {
		SendJSONError(w, "Storage not initialized", http.StatusInternalServerError, h.logger)
		return
	}

	// Парсим параметры пагинации
	limit := 50
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil {
			limit = val
		}
	}
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if val, err := strconv.Atoi(offsetStr); err == nil {
			offset = val
		}
	}

	// Валидируем параметры
	paginationValidator := validators.NewPaginationValidator(100, 1000000)
	limit, offset, _ = paginationValidator.ValidateLimitOffset(limit, offset)

	// Пытаемся получить из кэша
	cacheKey := fmt.Sprintf("%s:%d:%d", login, limit, offset)
	if cachedData, found := h.historyCache.Get(cacheKey); found {
		h.logger.Debugf("Cache hit for user: %s", login)
		SendJSONResponse(w, http.StatusOK, cachedData, h.logger)
		return
	}

	h.logger.Debugf("Cache miss for user: %s, loading from database", login)

	// Загружаем из БД
	tasks, err := h.taskStorage.GetUserTasksWithResults(login, limit, offset)
	if err != nil {
		h.logger.Errorf("Failed to get history: %v", err)
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

// GetTaskStatusHandler возвращает статус задачи
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

// DownloadResultHandler скачивает результат обработки
func (h *MeasurementHandler) DownloadResultHandler(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		SendJSONError(w, "Task ID required", http.StatusBadRequest, h.logger)
		return
	}

	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	task, err := h.taskStorage.GetTaskByID(taskID)
	if err != nil {
		SendJSONError(w, "Failed to get task", http.StatusInternalServerError, h.logger)
		return
	}
	if task == nil {
		SendJSONError(w, "Task not found", http.StatusNotFound, h.logger)
		return
	}
	if task.UserLogin != login {
		h.logger.Warnf("Access denied: task %s belongs to %s, requested by %s", taskID, task.UserLogin, login)
		SendJSONError(w, "Access denied", http.StatusForbidden, h.logger)
		return
	}

	result, err := h.taskStorage.GetResultByTaskID(taskID)
	if err != nil || result == nil {
		SendJSONError(w, "Result not found", http.StatusNotFound, h.logger)
		return
	}
	if len(result.FullResultFile) == 0 {
		SendJSONError(w, "Result file not available", http.StatusNotFound, h.logger)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.pos", taskID))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(result.FullResultFile)))
	h.logger.Infof("Downloaded result for task %s (user: %s, size: %d bytes)", taskID, login, len(result.FullResultFile))
	w.Write(result.FullResultFile)
}

// GetSystemStatsHandler возвращает системную статистику
func (h *MeasurementHandler) GetSystemStatsHandler(w http.ResponseWriter, r *http.Request) {
	if h.taskStorage == nil {
		SendJSONResponse(w, http.StatusOK, map[string]interface{}{
			"activeUsers":       0,
			"measurementsToday": 0,
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

// processTaskAsync processes a task in the background using MeasurementService.
// All heavy-lifting (file I/O, conversion, download, RTK) is delegated to the service.
func (h *MeasurementHandler) processTaskAsync(taskID, login string, config model.UserProcessingConfig, fileData []byte, filename string) {
	h.logger.Infof("Starting async processing for task: %s (file: %s, size: %.2f MB)",
		taskID, filename, float64(len(fileData))/(1024*1024))

	const (
		defaultWorkDir   = "./tmp"
		defaultConfigDir = "./cmd/solver/configs"
		defaultSolverDir = "./cmd/solver/app"
		defaultBLQScript = "./cmd/solver/src/generate_blq.py"
		defaultBLQConfig = "./cmd/solver/src/fes_ocean_loading.yml"
	)

	// Отложенная очистка: удаляем всю папку с временными файлами в конце
	// defer func() {
	// 	h.logger.Debugf("Cleaning up temporary directory: %s", defaultWorkDir)
	// 	if err := os.RemoveAll(defaultWorkDir); err != nil {
	// 		h.logger.Warnf("Failed to remove work directory %s: %v", defaultWorkDir, err)
	// 	} else {
	// 		h.logger.Infof("Successfully cleaned up work directory: %s", defaultWorkDir)
	// 	}
	// }()

	configGen := services.NewConfigGenerator(defaultConfigDir, defaultWorkDir, h.logger)
	downloader := services.NewFileDownloader(defaultWorkDir, h.logger)
	converter := services.NewConverterService(defaultSolverDir, h.logger)
	rtk := services.NewRTKService(defaultSolverDir, defaultWorkDir, h.logger)
	fileSvc := services.NewFileService(defaultWorkDir, h.logger)
	blqSvc := services.NewBLQService(defaultBLQScript, defaultBLQConfig, defaultWorkDir, h.logger)

	measurementSvc := services.NewMeasurementService(
		h.taskStorage,
		configGen,
		downloader,
		converter,
		rtk,
		fileSvc,
		blqSvc,
		defaultWorkDir,
		h.logger,
	)

	ctx := context.Background()
	if err := measurementSvc.ProcessMeasurement(ctx, taskID, login, &config, fileData, filename); err != nil {
		h.logger.Errorf("Task %s failed: %v", taskID, err)
	}

	// Invalidate cache so next history request reflects updated status
	h.historyCache.Invalidate(login)
}
