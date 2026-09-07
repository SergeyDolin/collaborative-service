package services

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

// FileService обрабатывает файлы
type FileService struct {
	workDir string
	logger  *zap.SugaredLogger
}

// NewFileService создает новый сервис работы с файлами
func NewFileService(workDir string, logger *zap.SugaredLogger) *FileService {
	return &FileService{
		workDir: workDir,
		logger:  logger,
	}
}

// CopyFile копирует файл потоком (не загружает в память)
func (fs *FileService) CopyFile(src, dst string) (int64, error) {
	source, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open source: %w", err)
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return 0, fmt.Errorf("create destination: %w", err)
	}
	defer destination.Close()

	// Копируем с буфером 32KB
	const bufferSize = 32 * 1024
	return io.CopyBuffer(destination, source, make([]byte, bufferSize))
}

// ValidateFile проверяет файл
func (fs *FileService) ValidateFile(filepath string) error {
	info, err := os.Stat(filepath)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return fmt.Errorf("path is directory")
	}

	if info.Size() == 0 {
		return fmt.Errorf("file is empty")
	}

	return nil
}

// CleanupOldFiles удаляет старые файлы
func (fs *FileService) CleanupOldFiles(dir string, age time.Duration) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	cutoff := time.Now().Add(-age)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			path := filepath.Join(dir, entry.Name())
			if err := os.RemoveAll(path); err != nil {
				fs.logger.Warnf("Failed to remove old file: %v", err)
			}
		}
	}

	return nil
}
