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

// ConvertRINEX3to2 конвертирует RINEX версии 3 в версию 2 используя convbin
func (c *ConverterService) ConvertRINEX3to2(inputPath, outputPath string) error {
	c.logger.Infof("Converting RINEX 3 to RINEX 2: %s -> %s", inputPath, outputPath)

	// Проверяем входной файл
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return fmt.Errorf("input file not found: %s", inputPath)
	}

	// Ищем convbin
	pathsToTry := []string{
		filepath.Join(c.rtklibPath, "convbin"),
		"./cmd/solver/app/convbin",
		"/usr/local/bin/convbin",
		"/opt/homebrew/bin/convbin",
	}

	// Добавляем PATH
	if pathEnv := os.Getenv("PATH"); pathEnv != "" {
		for _, dir := range strings.Split(pathEnv, ":") {
			pathsToTry = append(pathsToTry, filepath.Join(dir, "convbin"))
		}
	}

	var convbinPath string

	for _, path := range pathsToTry {
		if _, err := os.Stat(path); err == nil {
			convbinPath = path
			c.logger.Infof("Found convbin at: %s", convbinPath)
			break
		}
	}

	if convbinPath == "" {
		// Пробуем выполнить команду
		if path, err := exec.LookPath("convbin"); err == nil {
			convbinPath = path
			c.logger.Infof("Found convbin in PATH: %s", convbinPath)
		} else {
			c.logger.Warnf("convbin not found, skipping RINEX 3 to 2 conversion")
			// Не падаем с ошибкой, так как это опциональная конвертация
			return nil
		}
	}

	// Делаем файл исполняемым
	if err := os.Chmod(convbinPath, 0755); err != nil {
		c.logger.Warnf("Failed to chmod convbin: %v", err)
	}

	// Запускаем конвертацию
	// convbin -r rinex3 -o rinex2 input.rnx
	cmd := exec.Command(convbinPath, "-r", "rinex3", "-o", "rinex2", inputPath)

	// Получаем вывод для отладки
	output, err := cmd.CombinedOutput()

	if err != nil {
		c.logger.Errorf("convbin failed with error: %v", err)
		c.logger.Errorf("Command output: %s", string(output))
		return fmt.Errorf("convbin conversion failed: %w", err)
	}

	c.logger.Infof("Successfully converted RINEX 3 to 2: %s", inputPath)

	// convbin переименовывает файл добавляя расширение .rnx
	// Нам нужно переместить результат в нужное место
	expectedOutput := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + ".rnx"

	if _, err := os.Stat(expectedOutput); err == nil {
		// Переименовываем в нужный путь
		if err := os.Rename(expectedOutput, outputPath); err != nil {
			c.logger.Errorf("Failed to rename converted file: %v", err)
			return fmt.Errorf("failed to move converted file: %w", err)
		}
		c.logger.Infof("Moved converted file to: %s", outputPath)
	} else {
		c.logger.Warnf("Expected output file not found at: %s", expectedOutput)
		// Если convbin вывел в inputPath, то просто копируем
		if err := os.Rename(inputPath, outputPath); err != nil {
			c.logger.Errorf("Failed to rename file: %v", err)
			return fmt.Errorf("failed to move file: %w", err)
		}
	}

	return nil
}

// ConvertFile автоматически определяет тип файла и конвертирует если нужно
func (c *ConverterService) ConvertFile(filePath, workDir string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	// Уже RINEX 2.x
	if ext == ".obs" || ext == ".o" {
		c.logger.Infof("File already in RINEX 2 format: %s", filePath)
		return filePath, nil
	}

	// RINEX 3.x - нужна конвертация
	if ext == ".rnx" {
		c.logger.Infof("File is RINEX 3.x, converting to RINEX 2...")
		outputPath := filepath.Join(workDir, "converted.obs")
		if err := c.ConvertRINEX3to2(filePath, outputPath); err != nil {
			// Не падаем если convbin недоступен, используем как есть
			c.logger.Warnf("RINEX 3 to 2 conversion failed: %v, using RINEX 3", err)
			return filePath, nil
		}
		return outputPath, nil
	}

	// Hatanaka CRX
	if ext == ".crx" {
		outputPath := filepath.Join(workDir, "converted.obs")
		if err := c.ConvertCRX2RNX(filePath, outputPath); err != nil {
			return "", err
		}
		// После конвертации CRX->RNX, проверяем версию и конвертируем если нужно
		return c.ConvertFile(outputPath, workDir)
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
			// Конвертируем CRX в RNX
			outputPath := filepath.Join(workDir, "converted.obs")
			if err := c.ConvertCRX2RNX(unpackedPath, outputPath); err != nil {
				return "", err
			}
			os.Remove(unpackedPath)
			// После конвертации CRX->RNX, проверяем версию
			return c.ConvertFile(outputPath, workDir)
		} else if newExt == ".rnx" {
			// Это RINEX 3, конвертируем в RINEX 2
			outputPath := filepath.Join(workDir, "converted.obs")
			if err := c.ConvertRINEX3to2(unpackedPath, outputPath); err != nil {
				c.logger.Warnf("RINEX 3 to 2 conversion failed, using RINEX 3")
				return unpackedPath, nil
			}
			os.Remove(unpackedPath)
			return outputPath, nil
		} else if newExt == ".obs" || newExt == ".o" {
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
