package services

import (
	"bytes"
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

// reRinex2ObsExt соответствует расширению .YYo / .YYO (RINEX 2 observation).
var reRinex2ObsExt = regexp.MustCompile(`\.\d{2}[oO]$`)

// isHatanakaExt возвращает true если расширение файла — Hatanaka (.YYd / .YYD).
func isHatanakaExt(lower string) bool {
	return reHatanakaExt.MatchString(lower)
}

// isRinex2ObsExt возвращает true если расширение — RINEX 2 observation (.YYo / .YYO).
func isRinex2ObsExt(lower string) bool {
	return reRinex2ObsExt.MatchString(lower)
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

	// crx2rnx принимает только stdin/stdout
	cmd := exec.Command(crx2rnxPath)
	inFile, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("open input: %w", err)
	}
	defer inFile.Close()
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer outFile.Close()
	cmd.Stdin = inFile
	cmd.Stdout = outFile
	var crxStderr bytes.Buffer
	cmd.Stderr = &crxStderr

	if err = cmd.Run(); err != nil {
		c.logger.Errorf("crx2rnx failed: %v\nstderr: %s", err, crxStderr.String())
		return fmt.Errorf("crx2rnx conversion failed: %w", err)
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
// Возвращает путь к готовому RINEX 3 файлу наблюдений.
//
// Поддерживаемые форматы на входе:
//
//	.obs / .o / .rnx — RINEX 3 наблюдений (без конвертации; если внутри RINEX 2 — convbin → RINEX 3)
//	.YYo / .YYO      — RINEX 2 observation (например NSK1.26o) → convbin → RINEX 3
//	.crx             — Hatanaka CRX → crx2rnx → RINEX 3
//	.YYd / .YYD      — Hatanaka compact RINEX 2 → crx2rnx → RINEX 2 → convbin → RINEX 3
//	.gz              — gzip → распаковка → рекурсивная обработка
func (c *ConverterService) ConvertFile(filePath, workDir string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	c.logger.Infof("ConvertFile: %s (ext=%s)", filePath, ext)

	// ── RINEX 2 observation (.YYo / .YYO, например NSK1.26o) ────────────────
	if isRinex2ObsExt(ext) {
		c.logger.Infof("Detected RINEX 2 observation (.YYo): %s", filePath)
		out := filepath.Join(workDir, "converted.rnx")
		if err := c.ConvertRINEX2to3(filePath, out); err != nil {
			c.logger.Warnf("RINEX 2→3 for .YYo failed: %v, using original", err)
			return filePath, nil
		}
		return out, nil
	}

	// ── RINEX 3 (.obs / .o / .rnx) ──────────────────────────────────────────
	if ext == ".obs" || ext == ".o" || ext == ".rnx" {
		// Проверяем, не RINEX 2 ли внутри
		if !c.IsRINEX3(filePath) {
			c.logger.Infof("File has %s ext but is RINEX 2 inside, converting to RINEX 3...", ext)
			out := filepath.Join(workDir, "converted.rnx")
			if err := c.ConvertRINEX2to3(filePath, out); err != nil {
				c.logger.Warnf("RINEX 2→3 failed: %v, using original", err)
				return filePath, nil
			}
			return out, nil
		}
		c.logger.Infof("File is RINEX 3, no conversion needed")
		return filePath, nil
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
		// .YYd после распаковки — RINEX 2, конвертируем в RINEX 3
		out3 := filepath.Join(workDir, "converted.rnx")
		if err := c.ConvertRINEX2to3(out, out3); err != nil {
			c.logger.Warnf("RINEX 2→3 after .YYd failed: %v, using decompressed", err)
			return out, nil
		}
		return out3, nil
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

// ConvertRINEX2to3 конвертирует RINEX 2 в RINEX 3
// Примечание: это заглушка, так как RTKLIB не имеет прямой конвертации 2→3
// В реальном проекте здесь может быть реализация через внешнюю утилиту или библиотеку
func (c *ConverterService) ConvertRINEX2to3(inputPath, outputPath string) error {
	c.logger.Infof("ConvertRINEX2to3: %s -> %s", inputPath, outputPath)

	// TODO: Реализовать конвертацию RINEX 2 → 3
	// Это может потребовать использования внешних инструментов или ручного парсинга

	return fmt.Errorf("RINEX 2→3 conversion not yet implemented")
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
