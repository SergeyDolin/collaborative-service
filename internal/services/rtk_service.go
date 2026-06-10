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

// ProcessPPP запускает PPP обработку с использованием точных файлов
func (r *RTKService) ProcessPPP(roverObs, navFile, sp3File, clkFile, configPath, taskID string) (string, error) {
	taskDir := filepath.Join(r.workDir, taskID)
	os.MkdirAll(taskDir, 0755)
	outputFile := absPath(filepath.Join(taskDir, "output.pos"))

	// Все пути — абсолютные, чтобы rnx2rtkp работал из любой CWD
	args := []string{
		"-k", absPath(configPath),
		"-o", outputFile,
		absPath(roverObs),
	}

	if navFile != "" {
		args = append(args, absPath(navFile), absPath(sp3File), absPath(clkFile))
	}

	r.logger.Infof("Running PPP with command: %s", strings.Join(args, " "))

	binPath := absPath(filepath.Join(r.rtklibPath, "rnx2rtkp"))
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
		r.logger.Errorf("rnx2rtkp failed: %v, stderr: %s", err, stderr.String())
		return "", fmt.Errorf("PPP processing failed: %w", err)
	}

	r.logger.Infof("PPP completed in %.2f seconds, output: %s", duration, outputFile)
	r.logger.Debugf("stdout: %s", stdout.String())

	return outputFile, nil
}

// ProcessRelative запускает относительную обработку (DGPS/RTK)
func (r *RTKService) ProcessRelative(roverObs, baseObs, navFile, configPath, taskID string) (string, error) {
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

	r.logger.Infof("Running Relative positioning with command: %s", strings.Join(args, " "))

	binPath := absPath(filepath.Join(r.rtklibPath, "rnx2rtkp"))
	cmd := exec.Command(binPath, args...)
	cmd.Dir = filepath.Dir(binPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime).Seconds()

	if err != nil {
		r.logger.Errorf("rnx2rtkp failed: %v, stderr: %s", err, stderr.String())
		return "", fmt.Errorf("Relative processing failed: %w", err)
	}

	r.logger.Infof("Relative positioning completed in %.2f seconds, output: %s", duration, outputFile)

	return outputFile, nil
}

// ProcessAbsolute запускает абсолютное позиционирование (SPP)
func (r *RTKService) ProcessAbsolute(roverObs, navFile, configPath, taskID string) (string, error) {
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

	r.logger.Infof("Running Absolute positioning with command: %s", strings.Join(args, " "))

	binPath := absPath(filepath.Join(r.rtklibPath, "rnx2rtkp"))
	cmd := exec.Command(binPath, args...)
	cmd.Dir = filepath.Dir(binPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime).Seconds()

	if err != nil {
		r.logger.Errorf("rnx2rtkp failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		return "", fmt.Errorf("Absolute processing failed: %w", err)
	}

	if out := stdout.String() + stderr.String(); out != "" {
		r.logger.Debugf("rnx2rtkp output: %s", out)
	}

	if _, statErr := os.Stat(outputFile); os.IsNotExist(statErr) {
		r.logger.Warnf("rnx2rtkp exited 0 but produced no output file (no solutions). stdout: %s stderr: %s",
			stdout.String(), stderr.String())
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