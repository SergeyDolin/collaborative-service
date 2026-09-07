package workers

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"collaborative/internal/model"
	"collaborative/internal/services"
	"collaborative/internal/storage"

	"go.uber.org/zap"
)

const (
	retryWorkerInterval = 2 * time.Minute
	maxTaskRetries      = 3
	// Задача со своим внутренним дедлайном (taskProcessingTimeout в
	// measurement_service.go) должна была уже сама себя завершить с ошибкой.
	// Если "processing" длится дольше этого времени — горутина погибла вместе
	// с процессом (например, рестарт сервера), и задачу безопасно перезапустить.
	orphanProcessingTimeout = 25 * time.Minute
)

// TaskRetryWorker периодически находит зависшие ("осиротевшие" после рестарта
// сервера) и временно неудавшиеся (сетевые ошибки скачивания) задачи обработки
// измерений и перезапускает их по сохранённой копии исходного файла — так
// пользователь получает готовый результат, а не просто статус "ошибка".
type TaskRetryWorker struct {
	logger         *zap.SugaredLogger
	taskStorage    *storage.TaskStorage
	measurementSvc *services.MeasurementService
	workDir        string
}

// NewTaskRetryWorker создаёт воркер повторных попыток.
func NewTaskRetryWorker(
	logger *zap.SugaredLogger,
	taskStorage *storage.TaskStorage,
	measurementSvc *services.MeasurementService,
	workDir string,
) *TaskRetryWorker {
	return &TaskRetryWorker{
		logger:         logger,
		taskStorage:    taskStorage,
		measurementSvc: measurementSvc,
		workDir:        workDir,
	}
}

// Start запускает воркер; завершается при отмене ctx.
func (w *TaskRetryWorker) Start(ctx context.Context) {
	go w.run(ctx)
}

func (w *TaskRetryWorker) run(ctx context.Context) {
	ticker := time.NewTicker(retryWorkerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Task retry worker stopped")
			return
		case <-ticker.C:
			w.tick()
		}
	}
}

func (w *TaskRetryWorker) tick() {
	if w.taskStorage == nil || w.measurementSvc == nil {
		return
	}

	w.cleanupExhausted()

	tasks, err := w.taskStorage.GetRecoverableTasks(maxTaskRetries, orphanProcessingTimeout)
	if err != nil {
		w.logger.Warnf("TaskRetryWorker: failed to query recoverable tasks: %v", err)
		return
	}

	for _, task := range tasks {
		w.retryTask(task)
	}
}

func (w *TaskRetryWorker) retryTask(task model.ProcessingTask) {
	uploadPath := filepath.Join(w.workDir, "uploads", task.ID, task.Filename)
	fileData, err := os.ReadFile(uploadPath)
	if err != nil {
		w.logger.Warnf("TaskRetryWorker: no persisted upload for task %s (%s), leaving as-is: %v",
			task.ID, uploadPath, err)
		return
	}

	if err := w.taskStorage.RequeueTaskForRetry(task.ID); err != nil {
		w.logger.Warnf("TaskRetryWorker: failed to requeue task %s: %v", task.ID, err)
		return
	}

	w.logger.Infof("TaskRetryWorker: retrying task %s (attempt %d)", task.ID, task.RetryCount+1)

	cfg := task.Config
	go func() {
		if err := w.measurementSvc.ProcessMeasurement(
			context.Background(), task.ID, task.UserLogin, &cfg, fileData, task.Filename,
		); err != nil {
			w.logger.Errorf("TaskRetryWorker: retry failed for task %s: %v", task.ID, err)
		}
	}()
}

// cleanupExhausted удаляет сохранённые копии файлов задач, исчерпавших лимит
// попыток — дальнейших перезапусков для них не будет.
func (w *TaskRetryWorker) cleanupExhausted() {
	ids, err := w.taskStorage.GetExhaustedTaskIDs(maxTaskRetries)
	if err != nil {
		w.logger.Warnf("TaskRetryWorker: failed to query exhausted tasks: %v", err)
		return
	}
	for _, id := range ids {
		dir := filepath.Join(w.workDir, "uploads", id)
		if err := os.RemoveAll(dir); err != nil {
			w.logger.Warnf("TaskRetryWorker: failed to clean upload dir for %s: %v", id, err)
		}
	}
}
