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

	// Получаем дату наблюдения и проверяем владельца без использования GetTaskByID
	storedObs, createdAt, found, err := h.taskStorage.GetObservationDateForUser(taskID, login)
	if err != nil {
		h.logger.Errorf("Failed to get obs date for task %s: %v", taskID, err)
		SendJSONError(w, "Task not found", http.StatusNotFound, h.logger)
		return
	}
	if !found {
		SendJSONError(w, "Task not found", http.StatusNotFound, h.logger)
		return
	}

	var obsDate string
	var source string

	// Сначала проверяем сохраненную дату в БД
	if storedObs != nil {
		obsDate = storedObs.Format("2006-01-02")
		source = "database"
		h.logger.Debugf("Using cached observation date for task %s: %s", taskID, obsDate)
	} else {
		// Пробуем найти файл в рабочей директории
		workDir := filepath.Join("./tmp", taskID)
		rinexPath := h.findRINEXFile(workDir)

		// Парсим дату из файла
		if rinexPath != "" {
			parsedDate, parseErr := h.parser.ParseObservationDate(rinexPath)
			if parseErr == nil {
				obsDate = parsedDate.Format("2006-01-02")
				source = "rinex_header"
				h.logger.Infof("Parsed observation date from RINEX: %s", obsDate)
				go h.saveObservationDate(taskID, parsedDate)
			} else {
				h.logger.Warnf("Failed to parse observation date from %s: %v", rinexPath, parseErr)
				obsDate = createdAt.Format("2006-01-02")
				source = "task_created"
			}
		} else {
			obsDate = createdAt.Format("2006-01-02")
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
