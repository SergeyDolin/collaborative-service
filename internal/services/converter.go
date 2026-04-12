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

// ConvertCRX2RNX конвертирует Hatanaka сжатый файл (.crx) в RINEX (.rnx)
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
	}

	// Добавляем PATH
	if pathEnv := os.Getenv("PATH"); pathEnv != "" {
		for _, dir := range strings.Split(pathEnv, ":") {
			pathsToTry = append(pathsToTry, filepath.Join(dir, "crx2rnx"))
		}
	}

	var crx2rnxPath string

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
			return fmt.Errorf("crx2rnx not found. Please install RTKLIB tools")
		}
	}

	// Делаем файл исполняемым
	if err := os.Chmod(crx2rnxPath, 0755); err != nil {
		c.logger.Warnf("Failed to chmod: %v", err)
	}

	// Запускаем конвертацию
	cmd := exec.Command(crx2rnxPath, inputPath, outputPath)
	output, err := cmd.CombinedOutput()

	if err != nil {
		c.logger.Errorf("crx2rnx failed with error: %v", err)
		c.logger.Errorf("Command output: %s", string(output))

		// Пробуем другой формат команды: crx2rnx < input.crx > output.obs
		cmd2 := exec.Command(crx2rnxPath)

		inFile, err := os.Open(inputPath)
		if err == nil {
			defer inFile.Close()
			cmd2.Stdin = inFile
		}

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
			c.logger.Warnf("convbin not found, cannot convert RINEX 3 to 2")
			return fmt.Errorf("convbin not found")
		}
	}

	// Делаем файл исполняемым
	if err := os.Chmod(convbinPath, 0755); err != nil {
		c.logger.Warnf("Failed to chmod convbin: %v", err)
	}

	cmd := exec.Command(convbinPath, inputPath, "-v", "2.11", "-o", outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		c.logger.Errorf("convbin failed with error: %v", err)
		c.logger.Errorf("Command output: %s", string(output))
		return fmt.Errorf("convbin conversion failed: %w", err)
	}

	c.logger.Infof("Successfully converted RINEX 3 to 2: %s", outputPath)
	return nil
}

// IsRINEX3 проверяет, является ли файл RINEX версии 3
func (c *ConverterService) IsRINEX3(filePath string) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	// Читаем первую строку
	buffer := make([]byte, 100)
	n, _ := f.Read(buffer)
	if n < 20 {
		return false
	}

	firstLine := string(buffer[:n])

	// RINEX 3 имеет метку "RINEX VERSION 3" в первой строке
	if strings.Contains(firstLine, "RINEX VERSION 3") {
		return true
	}

	// Или формат: "     3.03           OBSERVATION DATA    M                   RINEX VERSION / TYPE"
	if strings.Contains(firstLine, "3.") && strings.Contains(firstLine, "RINEX VERSION") {
		return true
	}

	return false
}

// ConvertFile автоматически определяет тип файла и конвертирует если нужно
func (c *ConverterService) ConvertFile(filePath, workDir string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	c.logger.Infof("Converting file: %s (extension: %s)", filePath, ext)

	// RINEX 2 (.obs или .o) - не требует конвертации, просто возвращаем путь
	if ext == ".obs" || ext == ".o" {
		// Проверяем, не является ли это на самом деле RINEX 3
		if c.IsRINEX3(filePath) {
			c.logger.Infof("File has .obs extension but appears to be RINEX 3, converting...")
			outputPath := filepath.Join(workDir, "converted.obs")
			if err := c.ConvertRINEX3to2(filePath, outputPath); err != nil {
				c.logger.Warnf("RINEX 3 to 2 conversion failed: %v, using original file", err)
				return filePath, nil
			}
			return outputPath, nil
		}

		c.logger.Infof("File is RINEX 2 (.obs/.o), no conversion needed")
		return filePath, nil
	}

	// RINEX 3 (.rnx) - нужна конвертация в RINEX 2
	if ext == ".rnx" {
		c.logger.Infof("File is RINEX 3.x, converting to RINEX 2...")
		outputPath := filepath.Join(workDir, "converted.obs")
		if err := c.ConvertRINEX3to2(filePath, outputPath); err != nil {
			c.logger.Warnf("RINEX 3 to 2 conversion failed: %v, using RINEX 3", err)
			return filePath, nil
		}
		return outputPath, nil
	}

	// Hatanaka CRX
	if ext == ".crx" {
		c.logger.Infof("File is Hatanaka compressed, decompressing...")
		outputPath := filepath.Join(workDir, "decompressed.rnx")
		if err := c.ConvertCRX2RNX(filePath, outputPath); err != nil {
			return "", err
		}
		// После декомпрессии проверяем версию и конвертируем если нужно
		return c.ConvertFile(outputPath, workDir)
	}

	// GZ архив
	if ext == ".gz" {
		c.logger.Infof("File is GZ compressed, unpacking...")
		unpackedPath := filePath[:len(filePath)-3]
		if err := c.unpackGzip(filePath, unpackedPath); err != nil {
			return "", err
		}

		// Рекурсивно обрабатываем распакованный файл
		return c.ConvertFile(unpackedPath, workDir)
	}

	return "", fmt.Errorf("unknown file format: %s", filePath)
}

// unpackGzip распаковывает gzip файл
func (c *ConverterService) unpackGzip(src, dst string) error {
	// Простой способ через gunzip
	cmd := exec.Command("gunzip", "-c", src)

	outFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer outFile.Close()

	cmd.Stdout = outFile
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gunzip failed: %w", err)
	}

	c.logger.Infof("Unpacked: %s -> %s", src, dst)
	os.Remove(src)

	return nil
}
