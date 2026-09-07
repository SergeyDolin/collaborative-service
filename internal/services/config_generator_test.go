package services

import (
	"collaborative/internal/model"
	"fmt"
	"go.uber.org/zap"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProfileAntennaOverrideIsExplicit(t *testing.T) {
	dir := t.TempDir()
	template := "{{ANT_TYPE}}|{{ANT_DELTA_E}}|{{ANT_DELTA_N}}|{{ANT_DELTA_U}}"
	if err := os.WriteFile(filepath.Join(dir, "single.conf"), []byte(template), 0600); err != nil {
		t.Fatal(err)
	}
	obs := filepath.Join(dir, "sample.obs")
	header := fmt.Sprintf("%-20s%-40s%s\n", "", "FROM RINEX", "ANT # / TYPE")
	if err := os.WriteFile(obs, []byte(header), 0600); err != nil {
		t.Fatal(err)
	}
	g := NewConfigGenerator(dir, dir, zap.NewNop().Sugar())
	for _, source := range []string{"", "profile"} {
		task := "task" + source
		if err := os.Mkdir(filepath.Join(dir, task), 0700); err != nil {
			t.Fatal(err)
		}
		c := model.UserProcessingConfig{Method: model.MethodSingle, DeviceType: "gnss", AntennaSource: source, AntennaType: "FROM PROFILE", AntennaDeltaE: 1.25, AntennaDeltaN: -2.5, AntennaDeltaU: 3.75}
		path, err := g.GenerateConfig(c, task, time.Now(), &ProcessingFiles{}, obs)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if source == "" && !strings.HasPrefix(string(data), "FROM RINEX|") {
			t.Fatalf("implicit override: %s", data)
		}
		if source == "profile" && (!strings.HasPrefix(string(data), "FROM PROFILE|") || !strings.Contains(string(data), "-2.5")) {
			t.Fatalf("profile not used: %s", data)
		}
	}
}
