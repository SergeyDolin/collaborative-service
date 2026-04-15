package services

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"go.uber.org/zap"
)

// BLQService generates BLQ ocean-tide loading files using the pyfes Python
// library (github.com/sergeydolin/aviso-fes).
//
// Generation is best-effort: errors are logged but do not abort processing.
type BLQService struct {
	scriptPath  string // path to generate_blq.py
	pyFESConfig string // path to fes_ocean_loading.yml
	workDir     string
	logger      *zap.SugaredLogger
}

// NewBLQService creates a BLQService.
// pyFESConfig can be overridden at runtime via the PYFES_CONFIG env variable.
func NewBLQService(scriptPath, pyFESConfig, workDir string, logger *zap.SugaredLogger) *BLQService {
	if env := os.Getenv("PYFES_CONFIG"); env != "" {
		pyFESConfig = env
	}
	return &BLQService{
		scriptPath:  scriptPath,
		pyFESConfig: pyFESConfig,
		workDir:     workDir,
		logger:      logger,
	}
}

// GenerateBLQ calls generate_blq.py and returns the path to the BLQ file.
// Returns ("", error) when pyfes/model data is unavailable or location is on land.
func (s *BLQService) GenerateBLQ(stationName string, lat, lon float64, taskID string) (string, error) {
	if s.pyFESConfig == "" {
		return "", fmt.Errorf("pyFESConfig is empty; set PYFES_CONFIG env var")
	}

	outputPath := filepath.Join(s.workDir, taskID, fmt.Sprintf("%s.blq", taskID))

	cmd := exec.Command("python3",
		s.scriptPath,
		stationName,
		fmt.Sprintf("%.6f", lon),
		fmt.Sprintf("%.6f", lat),
		s.pyFESConfig,
		outputPath,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		s.logger.Warnf("[%s] generate_blq.py failed: %v\nstderr: %s",
			taskID, err, stderr.String())
		return "", fmt.Errorf("BLQ generation failed: %w", err)
	}

	s.logger.Infof("[%s] BLQ generated: %s (station=%q lat=%.4f lon=%.4f)",
		taskID, outputPath, stationName, lat, lon)
	return outputPath, nil
}
