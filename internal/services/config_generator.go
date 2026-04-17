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
	"collaborative/internal/parsers"

	"go.uber.org/zap"
)

type ProcessingFiles struct {
	NavigationFile string
	EphemerisFile  string
	ClockFile      string
	DCBFile        string
	ERPFile        string
	BIAFile        string
	BLQFile        string
	BaseStationObs string
}

// AntennaInfo содержит тип антенны и смещения из заголовка RINEX
type AntennaInfo struct {
	Type   string  // тип + купол: "TRM57971.00     NONE"
	DeltaH float64 // вертикаль (H в RINEX = U в RTKLIB) → ant1-antdelu
	DeltaE float64 // восток                              → ant1-antdele
	DeltaN float64 // север                               → ant1-antdeln
}

type ConfigGenerator struct {
	templateDir string
	workDir     string
	logger      *zap.SugaredLogger
	rinexParser *parsers.RINEXParser
}

func NewConfigGenerator(templateDir, workDir string, logger *zap.SugaredLogger) *ConfigGenerator {
	return &ConfigGenerator{
		templateDir: templateDir,
		workDir:     workDir,
		logger:      logger,
		rinexParser: parsers.NewRINEXParser(),
	}
}

type SNRConfig struct {
	Enabled bool
	MaskL1  string
	MaskL2  string
	MaskL5  string
}

func (g *ConfigGenerator) getSNRConfig(rinexPath string) SNRConfig {
	snrInfo := g.rinexParser.ParseSNRMapping(rinexPath)

	if !snrInfo.Present {
		g.logger.Info("SNR mapping not found in RINEX header, SNR mask disabled")
		return SNRConfig{Enabled: false}
	}

	thresholds := g.rinexParser.GetSNRMaskValues(snrInfo)
	if len(thresholds) != 9 {
		g.logger.Warn("Failed to parse SNR thresholds, SNR mask disabled")
		return SNRConfig{Enabled: false}
	}

	// Формируем строки масок
	maskStr := fmt.Sprintf("%d,%d,%d,%d,%d,%d,%d,%d,%d",
		thresholds[0], thresholds[1], thresholds[2],
		thresholds[3], thresholds[4], thresholds[5],
		thresholds[6], thresholds[7], thresholds[8])

	g.logger.Infof("SNR mapping found, mask values: %s", maskStr)

	return SNRConfig{
		Enabled: true,
		MaskL1:  maskStr,
		MaskL2:  maskStr,
		MaskL5:  maskStr,
	}
}

// GenerateConfig генерирует конфиг с информацией об антенне из RINEX файла.
//
// rinexPath — сконвертированный файл для RTK-процессора.
// antennaSourcePath (опционально) — оригинальный файл пользователя для чтения
// антенны. Нужен потому что convbin при конвертации RINEX3→RINEX2 может изменить
// заголовок и затереть ANT # / TYPE. Если не передан — используется rinexPath.
func (g *ConfigGenerator) GenerateConfig(
	config model.UserProcessingConfig,
	taskID string,
	date time.Time,
	files *ProcessingFiles,
	rinexPath string,
	antennaSourcePath ...string,
) (string, error) {
	// Выбираем файл-источник для антенны
	antFile := rinexPath
	if len(antennaSourcePath) > 0 && antennaSourcePath[0] != "" {
		antFile = antennaSourcePath[0]
	}

	var ant AntennaInfo
	if antFile != "" {
		ant = g.extractAntennaInfo(antFile)
	} else {
		g.logger.Warnf("[%s] No RINEX file provided for antenna extraction", taskID)
		ant.Type = "NONE"
	}

	// Мобильное устройство: переопределяем антенну из пользовательского ввода
	if config.DeviceType == "mobile" {
		antType := strings.TrimSpace(config.AntennaType)
		if antType == "" {
			antType = "UNKNOWN"
		}
		ant = AntennaInfo{
			Type:   antType,
			DeltaH: config.AntennaDeltaU, // U пользователя → DeltaH → {{ANT_DELTA_U}}
			DeltaE: config.AntennaDeltaE,
			DeltaN: config.AntennaDeltaN,
		}
		g.logger.Infof("[%s] Mobile device override: ant=%q dU=%.4f dE=%.4f dN=%.4f",
			taskID, ant.Type, ant.DeltaH, ant.DeltaE, ant.DeltaN)
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

	content := g.replaceParameters(string(templateData), config, date, files, ant, rinexPath)

	configPath := filepath.Join(g.workDir, taskID, fmt.Sprintf("%s_config.conf", taskID))
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write config: %w", err)
	}

	g.logger.Infof("[%s] Generated config: method=%s antenna=%q dH=%.4f dE=%.4f dN=%.4f",
		taskID, config.Method, ant.Type, ant.DeltaH, ant.DeltaE, ant.DeltaN)
	return configPath, nil
}

// extractAntennaInfo читает заголовок RINEX-файла и возвращает тип антенны и смещения.
func (g *ConfigGenerator) extractAntennaInfo(filePath string) AntennaInfo {
	ant := AntennaInfo{Type: "NONE"}

	f, err := os.Open(filePath)
	if err != nil {
		g.logger.Warnf("extractAntennaInfo: cannot open %q: %v", filePath, err)
		return ant
	}
	defer f.Close()

	g.logger.Debugf("extractAntennaInfo: scanning %q", filePath)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")

		if strings.Contains(line, "END OF HEADER") {
			break
		}

		if strings.Contains(line, "ANT # / TYPE") {
			t := parseAntennaTypeLine(line)
			g.logger.Infof("extractAntennaInfo: ANT line=%q parsed=%q", line, t)
			if t != "" {
				ant.Type = t
			}
		}

		if strings.Contains(line, "ANTENNA: DELTA H/E/N") {
			h, e, n := parseAntennaDeltaLine(line)
			g.logger.Infof("extractAntennaInfo: DELTA line=%q H=%.4f E=%.4f N=%.4f", line, h, e, n)
			ant.DeltaH = h
			ant.DeltaE = e
			ant.DeltaN = n
		}
	}

	if err := scanner.Err(); err != nil {
		g.logger.Warnf("extractAntennaInfo: scan error %q: %v", filePath, err)
	}

	g.logger.Infof("extractAntennaInfo: result type=%q dH=%.4f dE=%.4f dN=%.4f",
		ant.Type, ant.DeltaH, ant.DeltaE, ant.DeltaN)
	return ant
}

// parseAntennaTypeLine извлекает "тип+купол" из строки заголовка "ANT # / TYPE".
//
// Формат RINEX (фиксированные колонки):
//
//	col  0–19  серийный номер антенны
//	col 20–59  тип антенны + купол  ← нужное поле
//	col 60–79  метка "ANT # / TYPE"
//
// Пример:
//
//	"1441116772          TRM57971.00     NONE                    ANT # / TYPE"
//	 ^col 0              ^col 20                                 ^col 60
func parseAntennaTypeLine(line string) string {
	labelIdx := strings.Index(line, "ANT # / TYPE")
	if labelIdx < 0 {
		return ""
	}

	// Поле данных заканчивается перед меткой
	if labelIdx > 20 {
		field := strings.TrimSpace(line[20:labelIdx])
		if field != "" {
			return field
		}
	}

	// Резервный вариант: парсинг по словам
	// words[0]=серийник, words[1]=тип, words[2]=купол
	words := strings.Fields(line[:labelIdx])
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	case 2:
		return words[1]
	default:
		return words[1] + "     " + words[2]
	}
}

// parseAntennaDeltaLine извлекает смещения H, E, N из строки "ANTENNA: DELTA H/E/N".
//
// Формат RINEX (каждое поле — 14 символов, 4 знака):
//
//	col  0–13  delta H (вертикаль вверх)
//	col 14–27  delta E (восток)
//	col 28–41  delta N (север)
//
// Пример:
//
//	"        6.1661        0.0000        0.0000                  ANTENNA: DELTA H/E/N"
func parseAntennaDeltaLine(line string) (dH, dE, dN float64) {
	parseF := func(s string) float64 {
		v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return v
	}

	// Правая граница данных — начало метки "ANTENNA:"
	end := len(line)
	if idx := strings.Index(line, "ANTENNA:"); idx > 0 {
		end = idx
	}
	data := line[:end]

	// Дополняем до минимум 42 символов пробелами, если данные короче
	for len(data) < 42 {
		data += " "
	}

	// Фиксированные позиции
	dH = parseF(data[0:14])
	dE = parseF(data[14:28])
	dN = parseF(data[28:42])
	return
}

// extractAntennaType оставлен для обратной совместимости
func (g *ConfigGenerator) extractAntennaType(filePath string) string {
	return g.extractAntennaInfo(filePath).Type
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
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".obs") ||
			strings.HasSuffix(lower, ".rnx") ||
			strings.HasSuffix(lower, ".o") ||
			strings.HasSuffix(lower, "converted.obs") {
			return filepath.Join(workDir, name)
		}
	}
	return ""
}

// isUnknownOrSmartphone проверяет, является ли антенна неизвестной или встроенной
func (g *ConfigGenerator) isUnknownOrSmartphone(antennaData string) bool {
	lower := strings.ToLower(antennaData)
	markers := []string{
		"phone", "mobile", "smartphone", "iphone", "android",
		"samsung", "huawei", "xiaomi", "oppo", "vivo", "realme",
		"internal", "built-in", "builtin", "onboard", "on-board",
		"gnss receiver", "chipset", "soc", "qualcomm", "broadcom",
		"mtk", "mediatek", "helix", "sige", "u-blox", "ublox",
		"unknown", "n/a", "na", "not specified", "notspecified",
		"no antenna", "noantenna", "test", "sim",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// replaceParameters заменяет плейсхолдеры в шаблоне конфига
func (g *ConfigGenerator) replaceParameters(
	content string,
	config model.UserProcessingConfig,
	date time.Time,
	files *ProcessingFiles,
	ant AntennaInfo,
	rinexPath string,
) string {
	fmtDelta := func(v float64) string {
		return strconv.FormatFloat(v, 'f', 4, 64)
	}

	replacements := map[string]string{
		"{{POS_MODE}}":         g.getPosMode(config),
		"{{FREQUENCY}}":        string(config.Frequency),
		"{{ELEVATION_MASK}}":   strconv.FormatFloat(config.ElevationMask, 'f', 1, 64),
		"{{IONO_MODEL}}":       string(config.IonoModel),
		"{{TROP_MODEL}}":       string(config.TropModel),
		"{{AR_MODE}}":          string(config.ARMode),
		"{{TIDE_CORR}}":        g.boolToStr(config.TideCorr),
		"{{SATELLITE_SYSTEM}}": strconv.Itoa(config.SatelliteSystem),
		"{{YEAR}}":             strconv.Itoa(date.Year()),
		"{{DOY}}":              strconv.Itoa(date.YearDay()),
		// Антенна: H из RINEX = U (вертикаль вверх) в RTKLIB
		"{{ANT_TYPE}}":    ant.Type,
		"{{ANT_DELTA_U}}": fmtDelta(ant.DeltaH),
		"{{ANT_DELTA_E}}": fmtDelta(ant.DeltaE),
		"{{ANT_DELTA_N}}": fmtDelta(ant.DeltaN),
	}

	snrConfig := g.getSNRConfig(rinexPath)

	snrMaskR := "off"
	snrMaskB := "off"
	if snrConfig.Enabled {
		snrMaskR = "on"
		snrMaskB = "on"
	}

	snrReplacements := map[string]string{
		"{{SNR_MASK_R}}":  snrMaskR,
		"{{SNR_MASK_B}}":  snrMaskB,
		"{{SNR_MASK_L1}}": snrConfig.MaskL1,
		"{{SNR_MASK_L2}}": snrConfig.MaskL2,
		"{{SNR_MASK_L5}}": snrConfig.MaskL5,
	}

	for placeholder, value := range snrReplacements {
		replacements[placeholder] = value
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
	if files.BLQFile != "" {
		replacements["{{BLQ_FILE}}"] = files.BLQFile
	}

	for placeholder, value := range replacements {
		content = strings.ReplaceAll(content, placeholder, value)
	}

	return content
}

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

func (g *ConfigGenerator) boolToStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
