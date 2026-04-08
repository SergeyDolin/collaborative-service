package services

import (
	"collaborative/internal/model"
	"collaborative/internal/parsers"
	"collaborative/internal/storage"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

// MeasurementService содержит бизнес-логику обработки измерений
type MeasurementService struct {
	taskStorage *storage.TaskStorage
	configGen   *ConfigGenerator
	downloader  *FileDownloader
	converter   *ConverterService
	rtk         *RTKService
	fileService *FileService
	rinexParser *parsers.RINEXParser
	workDir     string
	logger      *zap.SugaredLogger
}

// NewMeasurementService создает новый сервис
func NewMeasurementService(
	taskStorage *storage.TaskStorage,
	configGen *ConfigGenerator,
	downloader *FileDownloader,
	converter *ConverterService,
	rtk *RTKService,
	fileService *FileService,
	workDir string,
	logger *zap.SugaredLogger,
) *MeasurementService {
	return &MeasurementService{
		taskStorage: taskStorage,
		configGen:   configGen,
		downloader:  downloader,
		converter:   converter,
		rtk:         rtk,
		fileService: fileService,
		rinexParser: parsers.NewRINEXParser(),
		workDir:     workDir,
		logger:      logger,
	}
}

// ProcessMeasurement обрабатывает измерения
func (s *MeasurementService) ProcessMeasurement(
	ctx context.Context,
	taskID string,
	login string,
	config *model.UserProcessingConfig,
	fileData []byte,
	filename string,
) error {
	s.logger.Infof("Processing measurement: task=%s, method=%s, mode=%s", taskID, config.Method, config.Mode)

	// Обновляем статус на processing
	now := time.Now()
	task := &model.ProcessingTask{
		ID:        taskID,
		UserLogin: login,
		Status:    model.StatusProcessing,
		StartedAt: &now,
	}
	if err := s.taskStorage.UpdateTask(task); err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}

	// Создаем рабочую директорию
	workDir := filepath.Join(s.workDir, taskID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		s.handleError(taskID, login, fmt.Sprintf("Failed to create work directory: %v", err))
		return err
	}

	defer func() {
		// Очищаем временные файлы
		s.logger.Debugf("Cleaning up work directory: %s", workDir)
		if err := os.RemoveAll(workDir); err != nil {
			s.logger.Warnf("Failed to remove work directory: %v", err)
		}
	}()

	// Сохраняем файл (потоковое копирование)
	obsPath := filepath.Join(workDir, filename)
	if err := os.WriteFile(obsPath, fileData, 0644); err != nil {
		s.handleError(taskID, login, fmt.Sprintf("Failed to save file: %v", err))
		return err
	}

	s.logger.Infof("File saved: %s (size: %d bytes)", obsPath, len(fileData))

	// Конвертируем файл если нужно
	convertedPath, err := s.converter.ConvertFile(obsPath, workDir)
	if err != nil {
		s.handleError(taskID, login, fmt.Sprintf("File conversion failed: %v", err))
		return err
	}

	rinexPath := convertedPath
	s.logger.Infof("Converted to: %s", rinexPath)

	// Определяем дату из RINEX
	date, err := s.rinexParser.ParseObservationDate(rinexPath)
	if err != nil {
		s.logger.Warnf("Failed to parse RINEX date: %v, using current time", err)
		date = time.Now()
	}

	// Скачиваем необходимые файлы
	files := &ProcessingFiles{}
	files.NavigationFile, _ = s.downloader.DownloadBroadcastEphemeris(date, taskID)

	var outputPath string
	var procErr error

	// Выбираем метод обработки
	switch config.Method {
	case model.MethodPPP:
		s.logger.Infof("Using PPP method for task: %s", taskID)

		files.EphemerisFile, _ = s.downloader.DownloadPreciseEphemeris(date, taskID)
		files.ClockFile, _ = s.downloader.DownloadPreciseClock(date, taskID)
		files.ERPFile, _ = s.downloader.DownloadERP(date, taskID)
		files.DCBFile, _ = s.downloader.DownloadDCB(date, taskID)

		configPath, cfgErr := s.configGen.GenerateConfig(*config, taskID, date, files, rinexPath)
		if cfgErr != nil {
			s.handleError(taskID, login, fmt.Sprintf("Config generation failed: %v", cfgErr))
			return cfgErr
		}

		outputPath, procErr = s.rtk.ProcessPPP(
			rinexPath, files.NavigationFile,
			files.EphemerisFile, files.ClockFile,
			files.ERPFile, files.DCBFile, configPath, taskID,
		)

	case model.MethodRelative:
		s.logger.Infof("Using Relative method for task: %s", taskID)

		configPath, cfgErr := s.configGen.GenerateConfig(*config, taskID, date, files, rinexPath)
		if cfgErr != nil {
			s.handleError(taskID, login, fmt.Sprintf("Config generation failed: %v", cfgErr))
			return cfgErr
		}

		outputPath, procErr = s.rtk.ProcessRelative(
			rinexPath, "", files.NavigationFile, configPath, taskID,
		)

	default: // MethodSingle
		s.logger.Infof("Using Single Point Positioning for task: %s", taskID)

		configPath, cfgErr := s.configGen.GenerateConfig(*config, taskID, date, files, rinexPath)
		if cfgErr != nil {
			s.handleError(taskID, login, fmt.Sprintf("Config generation failed: %v", cfgErr))
			return cfgErr
		}

		outputPath, procErr = s.rtk.ProcessAbsolute(
			rinexPath, files.NavigationFile, configPath, taskID,
		)
	}

	if procErr != nil {
		s.handleError(taskID, login, fmt.Sprintf("Processing failed: %v", procErr))
		return procErr
	}

	// Читаем результат
	outputData, err := os.ReadFile(outputPath)
	if err != nil {
		s.logger.Warnf("Failed to read output file: %v", err)
		outputData = []byte("")
	}

	// Парсим результат
	result := s.parseResult(outputData, taskID, login, config)
	result.ExpiresAt = time.Now().Add(24 * time.Hour)

	// Сохраняем результат в БД
	if err := s.taskStorage.SaveResult(result); err != nil {
		s.logger.Errorf("Failed to save result: %v", err)
		return err
	}

	// Перемещаем результат в постоянное хранилище
	completedAt := time.Now()
	permanentPath := filepath.Join(s.workDir, taskID+".pos")
	if err := os.Rename(outputPath, permanentPath); err != nil {
		s.logger.Warnf("Failed to move result file: %v", err)
		permanentPath = outputPath
	}

	// Обновляем задачу как завершенную
	task = &model.ProcessingTask{
		ID:            taskID,
		Status:        model.StatusCompleted,
		OutputPath:    permanentPath,
		CompletedAt:   &completedAt,
		ProcessingSec: completedAt.Sub(now).Seconds(),
	}
	s.taskStorage.UpdateTask(task)

	s.logger.Infof("Task completed: %s in %.2fs", taskID, task.ProcessingSec)
	return nil
}

// parseResult парсит результаты обработки
func (s *MeasurementService) parseResult(
	outputData []byte,
	taskID string,
	login string,
	config *model.UserProcessingConfig,
) *model.ProcessingResult {
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

			// Парсим координаты и качество
			parseFloatIfPossible := func(s string) float64 {
				var f float64
				fmt.Sscanf(s, "%f", &f)
				return f
			}

			parseIntIfPossible := func(s string) int {
				var i int
				fmt.Sscanf(s, "%d", &i)
				return i
			}

			result.Latitude = parseFloatIfPossible(fields[2])
			result.Longitude = parseFloatIfPossible(fields[3])
			result.Height = parseFloatIfPossible(fields[4])
			result.Q = parseIntIfPossible(fields[5])
			result.NSat = parseIntIfPossible(fields[6])
		}
	}

	// Для статики сохраняем последнюю строку
	if config.Mode == model.ModeStatic && lastNonCommentLine != "" {
		result.LastSolutionLine = lastNonCommentLine
		result.FileType = "static"
	} else {
		result.FileType = "kinematic"
		result.FullResultFile = outputData
	}

	s.logger.Infof("Parsed result: lat=%.6f, lon=%.6f, h=%.2f, Q=%d, ns=%d",
		result.Latitude, result.Longitude, result.Height, result.Q, result.NSat)

	return result
}

// handleError обрабатывает ошибку
func (s *MeasurementService) handleError(taskID, login, errMsg string) {
	s.logger.Errorf("Task failed: %s - %s", taskID, errMsg)

	task := &model.ProcessingTask{
		ID:           taskID,
		Status:       model.StatusFailed,
		ErrorMessage: errMsg,
	}
	s.taskStorage.UpdateTask(task)
}
