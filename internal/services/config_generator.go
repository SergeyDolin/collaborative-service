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

// GenerateConfig генерирует конфиг для обработки с информацией об антенне из RINEX файла
func (g *ConfigGenerator) GenerateConfig(config model.UserProcessingConfig, taskID string, date time.Time, files *ProcessingFiles) (string, error) {
	// Ищем RINEX файл в workDir
	rinexFile := g.findRINEXFile(taskID)
	var antennaType string

	if rinexFile != "" {
		antennaType = g.extractAntennaType(rinexFile)
		g.logger.Infof("Extracted antenna type from RINEX: %s", antennaType)
	} else {
		g.logger.Warnf("Could not find RINEX file for antenna extraction")
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

// extractAntennaType извлекает тип антенны и серийный номер из заголовка RINEX файла
// Формат: строка 1 имеет тип, строка 2 имеет серийный номер
// Пример:
// ANT  TYPE / SERIAL NO    TRM57971.00     NONE
//
//	SERIAL123456789
//
// Нужно извлечь: "TRM57971.00     NONE" из первой строки
// и может быть серийный номер из второй строки (если нужно)
func (g *ConfigGenerator) extractAntennaType(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		g.logger.Warnf("Failed to open RINEX file for antenna extraction: %v", err)
		return "NONE"
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var antennaType string
	var antennaSerial string
	foundAntenna := false

	for scanner.Scan() {
		line := scanner.Text()

		// Заголовок заканчивается на этой метке
		if strings.Contains(line, "END OF HEADER") {
			break
		}

		// Проверяем если это строка с типом антенны
		if strings.Contains(line, "ANT  TYPE") && len(line) >= 60 {
			foundAntenna = true
			// Первая строка содержит тип антенны в позициях 0-60
			antennaType = strings.TrimSpace(line[:60])
			g.logger.Debugf("Found ANT TYPE line: %s", antennaType)
			continue
		}

		// Если мы нашли тип антенны, следующие строки могут содержать серийный номер
		// Строки после "ANT  TYPE / SERIAL NO" содержат дополнительные данные об антенне
		if foundAntenna && !strings.Contains(line, "ANT  TYPE") &&
			!strings.Contains(line, "END OF HEADER") &&
			len(antennaSerial) == 0 {

			// Проверяем что это не другой заголовок (содержит "/ " в конце)
			if !strings.Contains(line[50:], "/") {
				// Это может быть строка серийного номера
				potentialSerial := strings.TrimSpace(line[:60])
				if potentialSerial != "" && !strings.Contains(potentialSerial, "/") {
					antennaSerial = potentialSerial
					g.logger.Debugf("Found antenna serial/additional: %s", antennaSerial)
				} else {
					// Если это не серийный номер, значит это конец информации об антенне
					break
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		g.logger.Warnf("Error reading RINEX file: %v", err)
	}

	// Если не нашли информацию об антенне
	if antennaType == "" {
		g.logger.Infof("Antenna type not found in RINEX header, using NONE")
		return "NONE"
	}

	// Если данные пусты
	if antennaType == "" {
		g.logger.Infof("Antenna data is empty, using NONE")
		return "NONE"
	}

	// Проверяем на маркеры смартфонов/неизвестных устройств
	if g.isUnknownOrSmartphone(antennaType) {
		g.logger.Infof("Antenna recognized as unknown/smartphone: %s", antennaType)
		return "NONE"
	}

	g.logger.Infof("Found antenna type: %s", antennaType)
	return antennaType
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
