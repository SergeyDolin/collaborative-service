package services

import (
	"collaborative/internal/model"

	"go.uber.org/zap"
)

// NotificationService отправляет уведомления
type NotificationService struct {
	logger *zap.SugaredLogger
}

// NewNotificationService создает новый сервис уведомлений
func NewNotificationService(logger *zap.SugaredLogger) *NotificationService {
	return &NotificationService{
		logger: logger,
	}
}

// NotifyTaskComplete отправляет уведомление о завершении задачи
func (ns *NotificationService) NotifyTaskComplete(taskID, userLogin string, result *model.ProcessingResult) error {
	ns.logger.Infof("Task completed notification: task=%s, user=%s, lat=%.6f, lon=%.6f",
		taskID, userLogin, result.Latitude, result.Longitude)

	// TODO: Реализовать отправку уведомления
	// - Email
	// - Webhook
	// - WebSocket
	// - Database notification

	return nil
}

// NotifyTaskFailed отправляет уведомление об ошибке
func (ns *NotificationService) NotifyTaskFailed(taskID, userLogin, errorMsg string) error {
	ns.logger.Warnf("Task failed notification: task=%s, user=%s, error=%s", taskID, userLogin, errorMsg)

	// TODO: Реализовать отправку уведомления об ошибке
	return nil
}
