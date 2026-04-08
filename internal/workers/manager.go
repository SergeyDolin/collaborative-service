package workers

import (
	"context"
	"time"

	"collaborative/internal/storage"

	"go.uber.org/zap"
)

const (
	cleanupInterval    = 1 * time.Hour
	recoveryInterval   = 5 * time.Minute
	stalledTaskTimeout = 30 * time.Minute
	oldFilesAge        = 48 * time.Hour
)

// Manager manages background workers: cleanup and stalled-task recovery.
type Manager struct {
	logger      *zap.SugaredLogger
	taskStorage *storage.TaskStorage
	workDir     string
}

// NewManager creates a new background worker manager.
func NewManager(logger *zap.SugaredLogger, taskStorage *storage.TaskStorage, workDir string) *Manager {
	return &Manager{
		logger:      logger,
		taskStorage: taskStorage,
		workDir:     workDir,
	}
}

// Start launches background goroutines. They run until ctx is cancelled.
func (m *Manager) Start(ctx context.Context) {
	go m.runCleanup(ctx)
	go m.runRecovery(ctx)
}

// runCleanup periodically deletes expired results from the database.
func (m *Manager) runCleanup(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Cleanup worker stopped")
			return
		case <-ticker.C:
			m.cleanExpiredResults()
		}
	}
}

// runRecovery periodically marks stalled processing tasks as failed.
func (m *Manager) runRecovery(ctx context.Context) {
	ticker := time.NewTicker(recoveryInterval)
	defer ticker.Stop()

	// Run once immediately on start to recover from crash
	m.recoverStalledTasks()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Recovery worker stopped")
			return
		case <-ticker.C:
			m.recoverStalledTasks()
		}
	}
}

func (m *Manager) cleanExpiredResults() {
	if m.taskStorage == nil {
		return
	}
	if err := m.taskStorage.CleanExpiredResults(); err != nil {
		m.logger.Warnf("Failed to clean expired results: %v", err)
	} else {
		m.logger.Debug("Expired results cleaned")
	}
}

func (m *Manager) recoverStalledTasks() {
	if m.taskStorage == nil {
		return
	}
	if err := m.taskStorage.FailStalledTasks(stalledTaskTimeout); err != nil {
		m.logger.Warnf("Failed to recover stalled tasks: %v", err)
	}
}
