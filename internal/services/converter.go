package services

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

type ConverterService struct {
	rtklibPath string
	logger     *zap.SugaredLogger
}

func NewConverterService(rtklibPath string, logger *zap.SugaredLogger) *ConverterService {
	return &ConverterService{
		rtklibPath: rtklibPath,
		logger:     logger,
	}
}

// ConvertCRX2RNX конвертирует Hatanaka сжатый файл (.crx) в RINEX (.obs)
func (c *ConverterService) ConvertCRX2RNX(inputPath, outputPath string) error {
	c.logger.Infof("Converting CRX to RNX: %s -> %s", inputPath, outputPath)

	// Проверяем входной файл
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return fmt.Errorf("input file not found: %s", inputPath)
	}

	// Получаем размер входного файла
	info, _ := os.Stat(inputPath)
	c.logger.Infof("Input file size: %d bytes", info.Size())

	// Ищем crx2rnx в разных местах
	pathsToTry := []string{
		filepath.Join(c.rtklibPath, "crx2rnx"),
		"./cmd/solver/app/crx2rnx",
		"/usr/local/bin/crx2rnx",
		"/opt/homebrew/bin/crx2rnx",
	}

	// Добавляем PATH
	if pathEnv := os.Getenv("PATH"); pathEnv != "" {
		for _, dir := range strings.Split(pathEnv, ":") {
			pathsToTry = append(pathsToTry, filepath.Join(dir, "crx2rnx"))
		}
	}

	var crx2rnxPath string
	var err error

	for _, path := range pathsToTry {
		if _, err := os.Stat(path); err == nil {
			crx2rnxPath = path
			c.logger.Infof("Found crx2rnx at: %s", crx2rnxPath)
			break
		}
	}

	if crx2rnxPath == "" {
		// Пробуем выполнить команду
		if path, err := exec.LookPath("crx2rnx"); err == nil {
			crx2rnxPath = path
			c.logger.Infof("Found crx2rnx in PATH: %s", crx2rnxPath)
		} else {
			c.logger.Errorf("crx2rnx not found in any location")
			c.logger.Errorf("Searched paths: %v", pathsToTry)
			return fmt.Errorf("crx2rnx not found. Please install RTKLIB tools")
		}
	}

	// Делаем файл исполняемым
	if err := os.Chmod(crx2rnxPath, 0755); err != nil {
		c.logger.Warnf("Failed to chmod: %v", err)
	}

	// Запускаем конвертацию
	cmd := exec.Command(crx2rnxPath, inputPath, outputPath)

	// Получаем вывод для отладки
	output, err := cmd.CombinedOutput()

	if err != nil {
		c.logger.Errorf("crx2rnx failed with error: %v", err)
		c.logger.Errorf("Command output: %s", string(output))

		// Пробуем другой формат команды: crx2rnx < input.crx > output.obs
		cmd2 := exec.Command(crx2rnxPath)

		// Открываем входной файл
		inFile, err := os.Open(inputPath)
		if err == nil {
			defer inFile.Close()
			cmd2.Stdin = inFile
		}

		// Создаем выходной файл
		outFile, err := os.Create(outputPath)
		if err == nil {
			defer outFile.Close()
			cmd2.Stdout = outFile
		}

		cmd2.Stderr = os.Stderr

		if err := cmd2.Run(); err != nil {
			c.logger.Errorf("Alternative crx2rnx also failed: %v", err)
			return fmt.Errorf("crx2rnx conversion failed: %w", err)
		}
	}

	// Проверяем выходной файл
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return fmt.Errorf("output file not created: %s", outputPath)
	}

	outInfo, _ := os.Stat(outputPath)
	c.logger.Infof("Successfully converted: %s (%d bytes) -> %s (%d bytes)",
		inputPath, info.Size(), outputPath, outInfo.Size())

	return nil
}

// ConvertFile автоматически определяет тип файла и конвертирует если нужно
func (c *ConverterService) ConvertFile(filePath, workDir string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	// Уже RINEX
	if ext == ".obs" || ext == ".rnx" || ext == ".o" {
		c.logger.Infof("File already in RINEX format: %s", filePath)
		return filePath, nil
	}

	// Hatanaka CRX
	if ext == ".crx" {
		outputPath := filepath.Join(workDir, "converted.obs")
		if err := c.ConvertCRX2RNX(filePath, outputPath); err != nil {
			return "", err
		}
		return outputPath, nil
	}

	// GZ архив
	if ext == ".gz" {
		// Распаковываем
		unpackedPath := filePath[:len(filePath)-3]
		if err := c.unpackGzip(filePath, unpackedPath); err != nil {
			return "", err
		}

		// Проверяем распакованный файл
		newExt := strings.ToLower(filepath.Ext(unpackedPath))
		if newExt == ".crx" {
			// Конвертируем CRX в RINEX
			outputPath := filepath.Join(workDir, "converted.obs")
			if err := c.ConvertCRX2RNX(unpackedPath, outputPath); err != nil {
				return "", err
			}
			os.Remove(unpackedPath)
			return outputPath, nil
		} else if newExt == ".obs" || newExt == ".rnx" || newExt == ".o" {
			return unpackedPath, nil
		}
	}

	return "", fmt.Errorf("unknown file format: %s", filePath)
}

func (c *ConverterService) unpackGzip(src, dst string) error {
	gzipReader, err := os.Open(src)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	// Простой способ через gunzip
	cmd := exec.Command("gunzip", "-c", src)

	outFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer outFile.Close()

	cmd.Stdout = outFile

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gunzip failed: %w", err)
	}

	c.logger.Infof("Unpacked: %s -> %s", src, dst)
	os.Remove(src)

	return nil
}
