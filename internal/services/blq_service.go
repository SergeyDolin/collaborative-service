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
// The generated file is consumed by RTKLIB when pos1-tidecorr = otl (2) to
// apply Ocean Tide Loading (OTL) corrections during GNSS processing.
//
// BLQ generation is best-effort: if pyfes is not installed, the FES model
// data is missing, or the station is outside the ocean domain (e.g. inland),
// GenerateBLQ returns an error and the caller should continue without OTL.
type BLQService struct {
	scriptPath  string // path to cmd/solver/src/generate_blq.py
	pyFESConfig string // path to fes_ocean_loading.yml
	workDir     string
	logger      *zap.SugaredLogger
}

// NewBLQService creates a BLQService.
//
// scriptPath  — path to generate_blq.py
// pyFESConfig — path to the pyfes YAML config; overridable via PYFES_CONFIG env var
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

// GenerateBLQ runs generate_blq.py and returns the path to the BLQ file.
//
// Returns an error (and empty string) when:
//   - PYFES_CONFIG is not set / pyFESConfig is empty
//   - pyfes is not installed in the Python environment
//   - FES model NetCDF files are missing
//   - The station coordinates are outside the ocean loading domain
func (s *BLQService) GenerateBLQ(stationName string, lat, lon float64, taskID string) (string, error) {
	if s.pyFESConfig == "" {
		return "", fmt.Errorf("pyFESConfig is empty; set PYFES_CONFIG env var or provide the path explicitly")
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
