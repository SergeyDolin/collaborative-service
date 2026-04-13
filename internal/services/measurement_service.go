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
	s.logger.Infof("Processing measurement: task=%s, method=%s, mode=%s, size=%.2f MB",
		taskID, config.Method, config.Mode, float64(len(fileData))/(1024*1024))

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

	// Сохраняем файл (потоковое копирование для больших файлов)
	obsPath := filepath.Join(workDir, filename)

	// Для больших файлов используем буферизованную запись
	f, err := os.Create(obsPath)
	if err != nil {
		s.handleError(taskID, login, fmt.Sprintf("Failed to create file: %v", err))
		return err
	}
	defer f.Close()

	// Пишем с буфером 1 MB для оптимизации
	buffer := make([]byte, 1024*1024) // 1 MB buffer
	offset := 0
	for offset < len(fileData) {
		n := copy(buffer, fileData[offset:])
		if _, err := f.Write(buffer[:n]); err != nil {
			s.handleError(taskID, login, fmt.Sprintf("Failed to write file: %v", err))
			return err
		}
		offset += n
	}

	s.logger.Infof("File saved: %s (size: %.2f MB)", obsPath, float64(len(fileData))/(1024*1024))

	// Конвертируем файл если нужно
	convertedPath, err := s.converter.ConvertFile(obsPath, workDir)
	if err != nil {
		s.handleError(taskID, login, fmt.Sprintf("File conversion failed: %v", err))
		return err
	}

	rinexPath := convertedPath
	s.logger.Infof("Converted to: %s", rinexPath)

	// После парсинга даты из RINEX (после строки с ParseObservationDate)
	// После строки:
	date, err := s.rinexParser.ParseObservationDate(rinexPath)
	if err != nil {
		s.logger.Warnf("Failed to parse RINEX date: %v, using current time", err)
		date = time.Now()
	}

	// Добавьте сохранение даты в БД:
	if err := s.taskStorage.UpdateTaskObservationDate(taskID, date); err != nil {
		s.logger.Warnf("Failed to save observation date: %v", err)
	} else {
		s.logger.Infof("Saved observation date for task %s: %s", taskID, date.Format("2006-01-02"))
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
		files.BIAFile, _ = s.downloader.DownloadBIA(date, taskID)

		configPath, cfgErr := s.configGen.GenerateConfig(*config, taskID, date, files, rinexPath)
		if cfgErr != nil {
			s.handleError(taskID, login, fmt.Sprintf("Config generation failed: %v", cfgErr))
			return cfgErr
		}

		outputPath, procErr = s.rtk.ProcessPPP(
			rinexPath, files.NavigationFile,
			files.EphemerisFile, files.ClockFile,
			configPath, taskID)

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

	// Удаляем временную директорию задачи — файл хранится в БД
	if err := os.RemoveAll(workDir); err != nil {
		s.logger.Warnf("Failed to remove work directory %s: %v", workDir, err)
	}

	completedAt := time.Now()
	task = &model.ProcessingTask{
		ID:            taskID,
		Status:        model.StatusCompleted,
		CompletedAt:   &completedAt,
		ProcessingSec: completedAt.Sub(now).Seconds(),
	}
	s.taskStorage.UpdateTask(task)

	s.logger.Infof("Task completed: %s in %.2fs", taskID, task.ProcessingSec)
	return nil
}

// parseResult парсит результаты обработки и находит лучшее решение
// parseResult парсит результаты обработки и находит лучшее решение
func (s *MeasurementService) parseResult(
	outputData []byte,
	taskID string,
	login string,
	config *model.UserProcessingConfig,
) *model.ProcessingResult {
	result := &model.ProcessingResult{
		TaskID:         taskID,
		UserLogin:      login,
		RawOutput:      string(outputData),
		FullResultFile: outputData,
		CreatedAt:      time.Now(),
	}

	lines := strings.Split(string(outputData), "\n")

	type Solution struct {
		Line   string
		Lat    float64
		Lon    float64
		Height float64
		Q      int
		NSat   int
		SDX    float32
		SDY    float32
		SDZ    float32
	}

	var solutions []Solution
	var lastSolution *Solution

	// Счетчики для статистики внутри файла
	var totalEpochs int // Всего эпох с Q=1 или Q=6
	var fixEpochs int   // Эпохи с Q=1

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}

		var sol Solution
		sol.Line = line

		if len(fields) >= 5 {
			fmt.Sscanf(fields[2], "%f", &sol.Lat)
			fmt.Sscanf(fields[3], "%f", &sol.Lon)
			fmt.Sscanf(fields[4], "%f", &sol.Height)
		}

		if len(fields) >= 6 {
			fmt.Sscanf(fields[5], "%d", &sol.Q)
		}

		if len(fields) >= 7 {
			fmt.Sscanf(fields[6], "%d", &sol.NSat)
		}

		if len(fields) >= 8 {
			var sdx float64
			fmt.Sscanf(fields[7], "%f", &sdx)
			sol.SDX = float32(sdx)
		}
		if len(fields) >= 9 {
			var sdy float64
			fmt.Sscanf(fields[8], "%f", &sdy)
			sol.SDY = float32(sdy)
		}
		if len(fields) >= 10 {
			var sdz float64
			fmt.Sscanf(fields[9], "%f", &sdz)
			sol.SDZ = float32(sdz)
		}

		solutions = append(solutions, sol)
		lastSolution = &sol

		// Считаем статистику внутри файла
		if sol.Q == 1 || sol.Q == 6 {
			totalEpochs++
			if sol.Q == 1 {
				fixEpochs++
			}
		}
	}

	// Вычисляем процент FIX внутри файла
	if totalEpochs > 0 {
		result.FixRate = float32(float64(fixEpochs) / float64(totalEpochs) * 100)
		s.logger.Infof("File %s: FIX rate = %.1f%% (%d/%d epochs)",
			taskID, result.FixRate, fixEpochs, totalEpochs)
	}

	// Ищем лучшее решение
	var bestSolution *Solution

	// Сначала ищем Q=1 с конца
	for i := len(solutions) - 1; i >= 0; i-- {
		if solutions[i].Q == 1 {
			bestSolution = &solutions[i]
			break
		}
	}

	// Если Q=1 не найдено, ищем Q=6
	if bestSolution == nil {
		for i := len(solutions) - 1; i >= 0; i-- {
			if solutions[i].Q == 6 {
				bestSolution = &solutions[i]
				break
			}
		}
	}

	// Если и Q=6 нет, берем последнее решение
	if bestSolution == nil && lastSolution != nil {
		bestSolution = lastSolution
	}

	// Заполняем результат
	if bestSolution != nil {
		result.Latitude = bestSolution.Lat
		result.Longitude = bestSolution.Lon
		result.Height = bestSolution.Height
		result.Q = bestSolution.Q
		result.NSat = bestSolution.NSat
		result.SDX = bestSolution.SDX
		result.SDY = bestSolution.SDY
		result.SDZ = bestSolution.SDZ
		result.LastSolutionLine = bestSolution.Line
	}

	if config.Mode == model.ModeStatic {
		result.FileType = "static"
	} else {
		result.FileType = "kinematic"
	}

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
