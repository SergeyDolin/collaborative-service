package services

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// reHatanakaExt соответствует расширению .YYd / .YYD (Hatanaka compact RINEX 2).
var reHatanakaExt = regexp.MustCompile(`\.\d{2}[dD]$`)

// isHatanakaExt возвращает true если расширение файла — Hatanaka (.YYd / .YYD).
func isHatanakaExt(lower string) bool {
	return reHatanakaExt.MatchString(lower)
}

// ConvertCRX2RNX конвертирует Hatanaka сжатый файл (.crx или .YYd) в RINEX (.obs)
func (c *ConverterService) ConvertCRX2RNX(inputPath, outputPath string) error {
	c.logger.Infof("ConvertCRX2RNX: %s -> %s", inputPath, outputPath)

	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return fmt.Errorf("input file not found: %s", inputPath)
	}

	info, _ := os.Stat(inputPath)
	c.logger.Infof("Input file size: %d bytes", info.Size())

	// Ищем crx2rnx
	pathsToTry := []string{
		filepath.Join(c.rtklibPath, "crx2rnx"),
		"./cmd/solver/app/crx2rnx",
	}
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
		if path, err := exec.LookPath("crx2rnx"); err == nil {
			crx2rnxPath = path
			c.logger.Infof("Found crx2rnx in PATH: %s", crx2rnxPath)
		} else {
			return fmt.Errorf("crx2rnx not found. Please install RTKLIB tools")
		}
	}

	if err := os.Chmod(crx2rnxPath, 0755); err != nil {
		c.logger.Warnf("Failed to chmod crx2rnx: %v", err)
	}

	// Попытка 1: crx2rnx <input> <output>
	cmd := exec.Command(crx2rnxPath, inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		c.logger.Warnf("crx2rnx with args failed: %v\noutput: %s", err, string(output))

		// Попытка 2: crx2rnx < input > output
		cmd2 := exec.Command(crx2rnxPath)
		if inFile, e := os.Open(inputPath); e == nil {
			defer inFile.Close()
			cmd2.Stdin = inFile
		}
		if outFile, e := os.Create(outputPath); e == nil {
			defer outFile.Close()
			cmd2.Stdout = outFile
		}
		cmd2.Stderr = os.Stderr

		if err2 := cmd2.Run(); err2 != nil {
			c.logger.Errorf("crx2rnx stdin/stdout also failed: %v", err2)
			return fmt.Errorf("crx2rnx conversion failed: %w", err2)
		}
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return fmt.Errorf("output file not created: %s", outputPath)
	}

	outInfo, _ := os.Stat(outputPath)
	c.logger.Infof("Hatanaka decompressed: %s (%d b) -> %s (%d b)",
		inputPath, info.Size(), outputPath, outInfo.Size())
	return nil
}

// ConvertRINEX3to2 конвертирует RINEX 3 в RINEX 2 через convbin
func (c *ConverterService) ConvertRINEX3to2(inputPath, outputPath string) error {
	c.logger.Infof("ConvertRINEX3to2: %s -> %s", inputPath, outputPath)

	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return fmt.Errorf("input file not found: %s", inputPath)
	}

	pathsToTry := []string{
		filepath.Join(c.rtklibPath, "convbin"),
		"./cmd/solver/app/convbin",
	}
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
		if path, err := exec.LookPath("convbin"); err == nil {
			convbinPath = path
		} else {
			return fmt.Errorf("convbin not found")
		}
	}

	if err := os.Chmod(convbinPath, 0755); err != nil {
		c.logger.Warnf("Failed to chmod convbin: %v", err)
	}

	cmd := exec.Command(convbinPath, inputPath, "-v", "2.11", "-o", outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		c.logger.Errorf("convbin failed: %v\n%s", err, string(output))
		return fmt.Errorf("convbin conversion failed: %w", err)
	}

	c.logger.Infof("RINEX 3→2 done: %s", outputPath)
	return nil
}

// IsRINEX3 проверяет, является ли файл RINEX версии 3
func (c *ConverterService) IsRINEX3(filePath string) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 100)
	n, _ := f.Read(buf)
	if n < 20 {
		return false
	}
	first := string(buf[:n])
	return strings.Contains(first, "RINEX VERSION 3") ||
		(strings.Contains(first, "3.") && strings.Contains(first, "RINEX VERSION"))
}

// ConvertFile определяет тип файла и конвертирует при необходимости.
// Возвращает путь к готовому RINEX 2 файлу наблюдений.
//
// Поддерживаемые форматы на входе:
//
//	.obs / .o     — RINEX 2 наблюдений (без конвертации, если не RINEX 3 внутри)
//	.rnx          — RINEX 3 → convbin → RINEX 2
//	.crx          — Hatanaka CRX → crx2rnx → RINEX (затем проверка версии)
//	.YYd / .YYD   — Hatanaka compact RINEX 2 (напр. .24d) → crx2rnx → RINEX 2
//	.gz           — gzip → распаковка → рекурсивная обработка
func (c *ConverterService) ConvertFile(filePath, workDir string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	c.logger.Infof("ConvertFile: %s (ext=%s)", filePath, ext)

	// ── RINEX 2 (.obs / .o) ──────────────────────────────────────────────────
	if ext == ".obs" || ext == ".o" {
		if c.IsRINEX3(filePath) {
			c.logger.Infof("File has %s ext but is RINEX 3 inside, converting...", ext)
			out := filepath.Join(workDir, "converted.obs")
			if err := c.ConvertRINEX3to2(filePath, out); err != nil {
				c.logger.Warnf("RINEX 3→2 failed: %v, using original", err)
				return filePath, nil
			}
			return out, nil
		}
		c.logger.Infof("File is RINEX 2, no conversion needed")
		return filePath, nil
	}

	// ── RINEX 3 (.rnx) ───────────────────────────────────────────────────────
	if ext == ".rnx" {
		out := filepath.Join(workDir, "converted.obs")
		if err := c.ConvertRINEX3to2(filePath, out); err != nil {
			c.logger.Warnf("RINEX 3→2 failed: %v, using original", err)
			return filePath, nil
		}
		return out, nil
	}

	// ── Hatanaka .crx ─────────────────────────────────────────────────────────
	if ext == ".crx" {
		out := filepath.Join(workDir, "decompressed.rnx")
		if err := c.ConvertCRX2RNX(filePath, out); err != nil {
			return "", err
		}
		// После распаковки рекурсивно проверяем версию RINEX
		return c.ConvertFile(out, workDir)
	}

	// ── Hatanaka compact RINEX 2 (.YYd / .YYD) ───────────────────────────────
	// Расширение вида .24d, .23d и т.п.
	if isHatanakaExt(ext) {
		c.logger.Infof("Detected Hatanaka compact RINEX 2 (.YYd): %s", filePath)
		out := filepath.Join(workDir, "decompressed.obs")
		if err := c.ConvertCRX2RNX(filePath, out); err != nil {
			return "", fmt.Errorf("Hatanaka (.YYd) decompression failed: %w", err)
		}
		// .YYd после распаковки — всегда RINEX 2, но на всякий случай проверяем
		if c.IsRINEX3(out) {
			c.logger.Infof("Decompressed .YYd appears to be RINEX 3, converting...")
			out2 := filepath.Join(workDir, "converted.obs")
			if err := c.ConvertRINEX3to2(out, out2); err != nil {
				c.logger.Warnf("RINEX 3→2 after .YYd failed: %v, using decompressed", err)
				return out, nil
			}
			return out2, nil
		}
		return out, nil
	}

	// ── GZ архив ─────────────────────────────────────────────────────────────
	if ext == ".gz" {
		unpacked := filePath[:len(filePath)-3]
		if err := c.unpackGzip(filePath, unpacked); err != nil {
			return "", err
		}
		return c.ConvertFile(unpacked, workDir)
	}

	return "", fmt.Errorf("unknown file format: %s", filePath)
}

// unpackGzip распаковывает gzip файл через gunzip
func (c *ConverterService) unpackGzip(src, dst string) error {
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
