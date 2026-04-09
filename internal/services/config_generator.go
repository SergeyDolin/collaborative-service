package services

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"collaborative/internal/model"

	"go.uber.org/zap"
)

type ProcessingFiles struct {
	NavigationFile string
	EphemerisFile  string
	ClockFile      string
	DCBFile        string
	ERPFile        string
	BIAFile        string
	BaseStationObs string
}

type ConfigGenerator struct {
	templateDir string
	workDir     string
	logger      *zap.SugaredLogger
}

func NewConfigGenerator(templateDir, workDir string, logger *zap.SugaredLogger) *ConfigGenerator {
	return &ConfigGenerator{
		templateDir: templateDir,
		workDir:     workDir,
		logger:      logger,
	}
}

// GenerateConfig генерирует конфиг с информацией об антенне из RINEX файла
func (g *ConfigGenerator) GenerateConfig(config model.UserProcessingConfig, taskID string, date time.Time, files *ProcessingFiles, rinexPath string) (string, error) {
	var antennaType string

	// Извлекаем тип антенны из переданного RINEX файла
	if rinexPath != "" {
		antennaType = g.extractAntennaType(rinexPath)
		g.logger.Infof("Extracted antenna type from RINEX: %s", antennaType)
	} else {
		g.logger.Warnf("No RINEX file provided for antenna extraction")
		antennaType = "NONE"
	}

	var templateName string
	switch config.Method {
	case model.MethodRelative:
		templateName = "relative.conf"
	case model.MethodPPP:
		templateName = "ppp.conf"
	default:
		templateName = "single.conf"
	}

	templatePath := filepath.Join(g.templateDir, templateName)
	templateData, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("failed to read template: %w", err)
	}

	content := string(templateData)
	content = g.replaceParameters(content, config, date, files, antennaType)

	configPath := filepath.Join(g.workDir, fmt.Sprintf("%s_config.conf", taskID))
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write config: %w", err)
	}

	g.logger.Infof("Generated config for method %s: %s (antenna: %s)", config.Method, configPath, antennaType)
	return configPath, nil
}

// extractAntennaType извлекает тип антенны + купол из строки "ANT # / TYPE"
// заголовка RINEX 2/3 файла.
//
// Формат строки (RINEX 3, фиксированные столбцы по спецификации):
//
//	col  0–19  : серийный номер приёмника антенны (20 символов)
//	col 20–39  : тип антенны (20 символов), например "TRM57971.00     NONE"
//	col 40–59  : (padding / не используется)
//	col 60–79  : метка "ANT # / TYPE"
//
// Однако на практике некоторые конвертеры (convbin, teqc) записывают поля
// с отступом отличным от стандарта, поэтому сначала пробуем фиксированные
// позиции, а если результат пустой — парсим по словам.
func (g *ConfigGenerator) extractAntennaType(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		g.logger.Warnf("Failed to open file for antenna extraction: %v", err)
		return "NONE"
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Убираем \r на случай Windows-переносов строк
		line := strings.TrimRight(scanner.Text(), "\r")

		if strings.Contains(line, "END OF HEADER") {
			break
		}

		if !strings.Contains(line, "ANT # / TYPE") {
			continue
		}

		// --- Метод 1: фиксированные позиции RINEX-спецификации ---
		// Метка начинается с col 60; поле антенны — col 20..59 (40 символов).
		if len(line) >= 40 {
			end := 60
			if len(line) < end {
				end = len(line)
			}
			// Убираем метку из правой части если строка короче 60 символов
			fieldArea := line[20:end]
			// Отрезаем правые пробелы, но сохраняем внутренние
			// (rtklib читает тип и купол как два слова разделённых пробелами)
			candidate := strings.TrimRight(fieldArea, " ")
			if candidate != "" {
				g.logger.Infof("Antenna type (fixed-col): %q from file %s", candidate, filePath)
				return candidate
			}
		}

		// --- Метод 2: парсинг по словам ---
		// Отрезаем метку "ANT # / TYPE" и берём остаток слева.
		// Пример: "1441112501          TRM57971.00     NONE                    ANT # / TYPE"
		// После отрезания метки: "1441112501          TRM57971.00     NONE                    "
		labelIdx := strings.Index(line, "ANT # / TYPE")
		if labelIdx > 0 {
			beforeLabel := strings.TrimRight(line[:labelIdx], " ")
			// Теперь разбиваем на слова: [серийник, тип, купол]
			// Серийник — первое слово (или пустое если нет), тип и купол — следующие
			words := strings.Fields(beforeLabel)
			// words[0] = серийник ("1441112501")
			// words[1] = тип антенны ("TRM57971.00")
			// words[2] = купол ("NONE") — опционально
			if len(words) >= 2 {
				antType := words[1]
				if len(words) >= 3 {
					antType = words[1] + "     " + words[2] // сохраняем разделитель для rtklib
				}
				g.logger.Infof("Antenna type (word-parse): %q from file %s", antType, filePath)
				return antType
			}
		}

		g.logger.Warnf("ANT # / TYPE line found but could not parse antenna: %q", line)
		return "NONE"
	}

	if err := scanner.Err(); err != nil {
		g.logger.Warnf("Error reading file %s: %v", filePath, err)
	}

	g.logger.Infof("ANT # / TYPE not found in header of %s, using NONE", filePath)
	return "NONE"
}

// findRINEXFile ищет RINEX файл в папке задачи
func (g *ConfigGenerator) findRINEXFile(taskID string) string {
	workDir := filepath.Join(g.workDir, taskID)

	entries, err := os.ReadDir(workDir)
	if err != nil {
		g.logger.Warnf("Failed to read work directory: %v", err)
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		lowerName := strings.ToLower(name)

		// Ищем файлы с расширениями .obs, .rnx, .o
		if strings.HasSuffix(lowerName, ".obs") ||
			strings.HasSuffix(lowerName, ".rnx") ||
			strings.HasSuffix(lowerName, ".o") ||
			strings.HasSuffix(lowerName, "converted.obs") {
			return filepath.Join(workDir, name)
		}
	}

	return ""
}

// isUnknownOrSmartphone проверяет, является ли антенна неизвестной или встроенной в смартфон
func (g *ConfigGenerator) isUnknownOrSmartphone(antennaData string) bool {
	lowerData := strings.ToLower(antennaData)

	// Маркеры для определения отсутствия антенны или смартфона
	markers := []string{
		"phone",
		"mobile",
		"smartphone",
		"iphone",
		"android",
		"samsung",
		"huawei",
		"xiaomi",
		"oppo",
		"vivo",
		"realme",
		"internal",
		"built-in",
		"builtin",
		"onboard",
		"on-board",
		"gnss receiver",
		"chipset",
		"soc",
		"qualcomm",
		"broadcom",
		"mtk",
		"mediatek",
		"helix",
		"sige",
		"u-blox",
		"ublox",
		"unknown",
		"n/a",
		"na",
		"not specified",
		"notspecified",
		"no antenna",
		"noantenna",
		"test",
		"sim",
	}

	for _, marker := range markers {
		if strings.Contains(lowerData, marker) {
			return true
		}
	}

	return false
}

// replaceParameters заменяет плейсхолдеры в шаблоне конфига
func (g *ConfigGenerator) replaceParameters(content string, config model.UserProcessingConfig, date time.Time, files *ProcessingFiles, antennaType string) string {
	year := strconv.Itoa(date.Year())
	doy := strconv.Itoa(date.YearDay())

	replacements := map[string]string{
		"{{POS_MODE}}":         g.getPosMode(config),
		"{{FREQUENCY}}":        string(config.Frequency),
		"{{ELEVATION_MASK}}":   strconv.FormatFloat(config.ElevationMask, 'f', 1, 64),
		"{{IONO_MODEL}}":       string(config.IonoModel),
		"{{TROP_MODEL}}":       string(config.TropModel),
		"{{AR_MODE}}":          string(config.ARMode),
		"{{TIDE_CORR}}":        g.boolToStr(config.TideCorr),
		"{{SATELLITE_SYSTEM}}": strconv.Itoa(config.SatelliteSystem),
		"{{YEAR}}":             year,
		"{{DOY}}":              doy,
		"{{ANT_TYPE}}":         antennaType,
	}

	if files.NavigationFile != "" {
		replacements["{{NAV_FILE}}"] = files.NavigationFile
	}
	if files.EphemerisFile != "" {
		replacements["{{EPH_FILE}}"] = files.EphemerisFile
	}
	if files.ClockFile != "" {
		replacements["{{CLK_FILE}}"] = files.ClockFile
	}
	if files.DCBFile != "" {
		replacements["{{DCB_FILE}}"] = files.DCBFile
	}
	if files.ERPFile != "" {
		replacements["{{ERP_FILE}}"] = files.ERPFile
	}
	if files.BIAFile != "" {
		replacements["{{BIA_FILE}}"] = files.BIAFile
	}

	for placeholder, value := range replacements {
		content = strings.ReplaceAll(content, placeholder, value)
	}

	return content
}

// getPosMode определяет режим позиционирования
func (g *ConfigGenerator) getPosMode(config model.UserProcessingConfig) string {
	switch config.Method {
	case model.MethodSingle:
		return "single"
	case model.MethodRelative:
		if config.Mode == model.ModeStatic {
			return "static"
		}
		return "kinematic"
	case model.MethodPPP:
		if config.Mode == model.ModeStatic {
			return "ppp-static"
		}
		return "ppp-kine"
	default:
		return "single"
	}
}

// boolToStr конвертирует boolean в "on"/"off"
func (g *ConfigGenerator) boolToStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
