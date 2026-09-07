package handlers

import (
	"collaborative/internal/middlewares"
	"net/http"
)

// DeleteTaskHandler удаляет одну задачу по ID
func (h *MeasurementHandler) DeleteTaskHandler(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		SendJSONError(w, "Task ID required", http.StatusBadRequest, h.logger)
		return
	}

	if err := h.taskStorage.DeleteTaskByID(taskID, login); err != nil {
		h.logger.Errorf("Failed to delete task %s: %v", taskID, err)
		SendJSONError(w, "Failed to delete task", http.StatusInternalServerError, h.logger)
		return
	}

	h.historyCache.Invalidate(login)
	h.logger.Infof("Task %s deleted by user %s", taskID, login)

	SendJSONResponse(w, http.StatusOK, map[string]string{
		"message": "Task deleted successfully",
	}, h.logger)
}

// DeleteAllTasksHandler удаляет всю историю пользователя
func (h *MeasurementHandler) DeleteAllTasksHandler(w http.ResponseWriter, r *http.Request) {
	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	count, err := h.taskStorage.DeleteAllUserTasks(login)
	if err != nil {
		h.logger.Errorf("Failed to delete all tasks for %s: %v", login, err)
		SendJSONError(w, "Failed to delete history", http.StatusInternalServerError, h.logger)
		return
	}

	h.historyCache.Invalidate(login)
	h.logger.Infof("Deleted %d tasks for user %s", count, login)

	SendJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "History cleared",
		"deleted": count,
	}, h.logger)
}
