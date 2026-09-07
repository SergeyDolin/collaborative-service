package services

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

type RTKService struct {
	rtklibPath string
	workDir    string
	logger     *zap.SugaredLogger
}

func NewRTKService(rtklibPath, workDir string, logger *zap.SugaredLogger) *RTKService {
	return &RTKService{
		rtklibPath: rtklibPath,
		workDir:    workDir,
		logger:     logger,
	}
}

// absPath возвращает абсолютный путь. Если путь уже абсолютный — возвращает как есть.
func absPath(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

const (
	binRnx2rtkp     = "rnx2rtkp"
	binRnx2rtkPhone = "rnx2rtkpPhone"
)

// solverBinary выбирает бинарь обработки по типу устройства:
// "mobile" (смартфон) → rnx2rtkPhone, всё остальное → rnx2rtkp.
// Если rnx2rtkPhone на сервере отсутствует — откатываемся на rnx2rtkp,
// чтобы обработка не падала целиком.
func (r *RTKService) solverBinary(deviceType string) string {
	name := binRnx2rtkp
	if strings.EqualFold(deviceType, "mobile") {
		phone := absPath(filepath.Join(r.rtklibPath, binRnx2rtkPhone))
		if _, err := os.Stat(phone); err == nil {
			return phone
		}
		r.logger.Warnf("%s не найден в %s — используем %s", binRnx2rtkPhone, r.rtklibPath, binRnx2rtkp)
	}
	return absPath(filepath.Join(r.rtklibPath, name))
}

// ProcessPPP запускает PPP обработку с использованием точных файлов.
// deviceType выбирает бинарь: "mobile" → rnx2rtkPhone, иначе rnx2rtkp.
func (r *RTKService) ProcessPPP(roverObs, navFile, sp3File, clkFile, configPath, taskID, deviceType string) (string, error) {
	taskDir := filepath.Join(r.workDir, taskID)
	os.MkdirAll(taskDir, 0755)
	outputFile := absPath(filepath.Join(taskDir, "output.pos"))

	// Все пути — абсолютные, чтобы решатель работал из любой CWD
	args := []string{
		"-k", absPath(configPath),
		"-o", outputFile,
		absPath(roverObs),
	}

	if navFile != "" {
		args = append(args, absPath(navFile), absPath(sp3File), absPath(clkFile))
	}

	binPath := r.solverBinary(deviceType)
	r.logger.Infof("Running PPP: %s %s", binPath, strings.Join(args, " "))

	cmd := exec.Command(binPath, args...)
	// Запускаем из директории бинарника — rnx2rtkp может искать ресурсы рядом с собой
	cmd.Dir = filepath.Dir(binPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime).Seconds()

	if err != nil {
		r.logger.Errorf("%s failed: %v, stderr: %s", filepath.Base(binPath), err, stderr.String())
		return "", fmt.Errorf("PPP processing failed: %w", err)
	}

	r.logger.Infof("PPP completed in %.2f seconds, output: %s", duration, outputFile)
	r.logger.Debugf("stdout: %s", stdout.String())

	return outputFile, nil
}

// ProcessRelative запускает относительную обработку (DGPS/RTK).
// deviceType выбирает бинарь: "mobile" → rnx2rtkPhone, иначе rnx2rtkp.
func (r *RTKService) ProcessRelative(roverObs, baseObs, navFile, configPath, taskID, deviceType string) (string, error) {
	taskDir := filepath.Join(r.workDir, taskID)
	os.MkdirAll(taskDir, 0755)
	outputFile := absPath(filepath.Join(taskDir, "output.pos"))

	args := []string{
		"-k", absPath(configPath),
		"-o", outputFile,
		absPath(roverObs),
	}

	if baseObs != "" {
		args = append(args, absPath(baseObs))
	}

	if navFile != "" {
		args = append(args, absPath(navFile))
	}

	binPath := r.solverBinary(deviceType)
	r.logger.Infof("Running Relative positioning: %s %s", binPath, strings.Join(args, " "))

	cmd := exec.Command(binPath, args...)
	cmd.Dir = filepath.Dir(binPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime).Seconds()

	if err != nil {
		r.logger.Errorf("%s failed: %v, stderr: %s", filepath.Base(binPath), err, stderr.String())
		return "", fmt.Errorf("Relative processing failed: %w", err)
	}

	r.logger.Infof("Relative positioning completed in %.2f seconds, output: %s", duration, outputFile)

	return outputFile, nil
}

// ProcessAbsolute запускает абсолютное позиционирование (SPP).
// deviceType выбирает бинарь: "mobile" → rnx2rtkPhone, иначе rnx2rtkp.
func (r *RTKService) ProcessAbsolute(roverObs, navFile, configPath, taskID, deviceType string) (string, error) {
	taskDir := filepath.Join(r.workDir, taskID)
	os.MkdirAll(taskDir, 0755)
	outputFile := absPath(filepath.Join(taskDir, "output.pos"))

	args := []string{
		"-k", absPath(configPath),
		"-o", outputFile,
		absPath(roverObs),
	}

	if navFile != "" {
		args = append(args, absPath(navFile))
	}

	binPath := r.solverBinary(deviceType)
	r.logger.Infof("Running Absolute positioning: %s %s", binPath, strings.Join(args, " "))

	cmd := exec.Command(binPath, args...)
	cmd.Dir = filepath.Dir(binPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime).Seconds()

	solverName := filepath.Base(binPath)
	if err != nil {
		r.logger.Errorf("%s failed: %v\nstdout: %s\nstderr: %s", solverName, err, stdout.String(), stderr.String())
		return "", fmt.Errorf("Absolute processing failed: %w", err)
	}

	if out := stdout.String() + stderr.String(); out != "" {
		r.logger.Debugf("%s output: %s", solverName, out)
	}

	if _, statErr := os.Stat(outputFile); os.IsNotExist(statErr) {
		r.logger.Warnf("%s exited 0 but produced no output file (no solutions). stdout: %s stderr: %s",
			solverName, stdout.String(), stderr.String())
		return "", nil
	}

	r.logger.Infof("Absolute positioning completed in %.2f seconds, output: %s", duration, outputFile)

	return outputFile, nil
}

// ProcessWithConfig общий метод для запуска с любым конфигом
func (r *RTKService) ProcessWithConfig(configPath, rinexPath, taskID string) (string, error) {
	taskDir := filepath.Join(r.workDir, taskID)
	os.MkdirAll(taskDir, 0755)
	outputFile := absPath(filepath.Join(taskDir, "output.pos"))

	args := []string{
		"-k", absPath(configPath),
		"-o", outputFile,
		absPath(rinexPath),
	}

	r.logger.Infof("Running rnx2rtkp with config: %s", configPath)

	binPath := absPath(filepath.Join(r.rtklibPath, "rnx2rtkp"))
	cmd := exec.Command(binPath, args...)
	cmd.Dir = filepath.Dir(binPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		r.logger.Errorf("rnx2rtkp failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		return "", fmt.Errorf("processing failed: %w", err)
	}

	if out := stdout.String() + stderr.String(); out != "" {
		r.logger.Debugf("rnx2rtkp output: %s", out)
	}

	if _, statErr := os.Stat(outputFile); os.IsNotExist(statErr) {
		r.logger.Warnf("rnx2rtkp exited 0 but produced no output file (no solutions). stdout: %s stderr: %s",
			stdout.String(), stderr.String())
		return "", nil
	}

	return outputFile, nil
}
