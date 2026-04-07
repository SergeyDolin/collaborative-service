package services

import (
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

func (g *ConfigGenerator) GenerateConfig(config model.UserProcessingConfig, taskID string, date time.Time, files *ProcessingFiles) (string, error) {
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
	content = g.replaceParameters(content, config, date, files)

	configPath := filepath.Join(g.workDir, fmt.Sprintf("%s_config.conf", taskID))
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write config: %w", err)
	}

	g.logger.Infof("Generated config for method %s: %s", config.Method, configPath)
	return configPath, nil
}

func (g *ConfigGenerator) replaceParameters(content string, config model.UserProcessingConfig, date time.Time, files *ProcessingFiles) string {
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
