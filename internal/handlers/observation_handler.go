package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"time"

	"collaborative/internal/middlewares"
	"collaborative/internal/parsers"
	"collaborative/internal/storage"

	"go.uber.org/zap"
)

type ObservationHandler struct {
	taskStorage *storage.TaskStorage
	parser      *parsers.RINEXParser
	logger      *zap.SugaredLogger
}

func NewObservationHandler(taskStorage *storage.TaskStorage, logger *zap.SugaredLogger) *ObservationHandler {
	return &ObservationHandler{
		taskStorage: taskStorage,
		parser:      parsers.NewRINEXParser(),
		logger:      logger,
	}
}

// GetObservationDate возвращает дату первого наблюдения из RINEX-файла
func (h *ObservationHandler) GetObservationDate(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		SendJSONError(w, "task_id is required", http.StatusBadRequest, h.logger)
		return
	}

	// Получаем задачу
	task, err := h.taskStorage.GetTaskByID(taskID)
	if err != nil {
		h.logger.Errorf("Failed to get task %s: %v", taskID, err)
		SendJSONError(w, "Task not found", http.StatusNotFound, h.logger)
		return
	}
	if task == nil {
		SendJSONError(w, "Task not found", http.StatusNotFound, h.logger)
		return
	}

	// Проверяем права доступа
	if task.UserLogin != login {
		h.logger.Warnf("Access denied: task %s belongs to %s, requested by %s", taskID, task.UserLogin, login)
		SendJSONError(w, "Access denied", http.StatusForbidden, h.logger)
		return
	}

	var obsDate string
	var source string

	// Сначала проверяем сохраненную дату
	if task.ObservationDate != nil {
		obsDate = task.ObservationDate.Format("2006-01-02")
		source = "database"
		h.logger.Debugf("Using cached observation date for task %s: %s", taskID, obsDate)
	} else {
		// Пытаемся получить результат для определения пути к файлу
		result, _ := h.taskStorage.GetResultByTaskID(taskID)

		// Определяем путь к RINEX-файлу
		var rinexPath string
		if task.RinexPath != "" {
			rinexPath = task.RinexPath
		} else if result != nil && result.FileType != "" {
			// Для статики файл может быть уже удален, используем дату создания
			obsDate = task.CreatedAt.Format("2006-01-02")
			source = "task_created"
			h.logger.Debugf("Using task creation date for static file: %s", obsDate)
		} else {
			// Пробуем найти файл в рабочей директории
			workDir := filepath.Join("./tmp", taskID)
			rinexPath = h.findRINEXFile(workDir)
		}

		// Парсим дату из файла
		if rinexPath != "" {
			parsedDate, err := h.parser.ParseObservationDate(rinexPath)
			if err == nil {
				obsDate = parsedDate.Format("2006-01-02")
				source = "rinex_header"
				h.logger.Infof("Parsed observation date from RINEX: %s", obsDate)

				// Сохраняем дату в БД для будущих запросов
				go h.saveObservationDate(taskID, parsedDate)
			} else {
				h.logger.Warnf("Failed to parse observation date from %s: %v", rinexPath, err)
				obsDate = task.CreatedAt.Format("2006-01-02")
				source = "task_created"
			}
		} else {
			obsDate = task.CreatedAt.Format("2006-01-02")
			source = "task_created"
			h.logger.Debugf("No RINEX file found, using task creation date: %s", obsDate)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"date":    obsDate,
		"source":  source,
	})
}

// findRINEXFile ищет RINEX файл в директории
func (h *ObservationHandler) findRINEXFile(dir string) string {
	entries, err := filepath.Glob(filepath.Join(dir, "*.obs"))
	if err == nil && len(entries) > 0 {
		return entries[0]
	}

	entries, _ = filepath.Glob(filepath.Join(dir, "*.rnx"))
	if len(entries) > 0 {
		return entries[0]
	}

	entries, _ = filepath.Glob(filepath.Join(dir, "*.o"))
	if len(entries) > 0 {
		return entries[0]
	}

	entries, _ = filepath.Glob(filepath.Join(dir, "converted.obs"))
	if len(entries) > 0 {
		return entries[0]
	}

	return ""
}

// saveObservationDate сохраняет дату наблюдения в БД
func (h *ObservationHandler) saveObservationDate(taskID string, date time.Time) {
	query := `UPDATE processing_tasks SET observation_date = $1 WHERE id = $2`
	ctx := context.Background()
	_, err := h.taskStorage.Pool().Exec(ctx, query, date, taskID)
	if err != nil {
		h.logger.Warnf("Failed to save observation date for task %s: %v", taskID, err)
	} else {
		h.logger.Debugf("Saved observation date for task %s: %s", taskID, date.Format("2006-01-02"))
	}
}
