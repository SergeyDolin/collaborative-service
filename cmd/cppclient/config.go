package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config хранится в $HOME/.config/cppclient/config.json
type Config struct {
	ServiceURL  string `json:"serviceURL"`
	Token       string `json:"token"`
	UserLogin   string `json:"userLogin"`
	RtkrcvPath  string `json:"rtkrcvPath"`
	Str2strPath string `json:"str2strPath"`
	ATXFile     string `json:"atxFile"`
	OutputPort  int    `json:"outputPort"`
	WorkDir     string `json:"workDir"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cppclient", "config.json")
}

func LoadConfig() *Config {
	cfg := &Config{
		ServiceURL: "http://localhost:8000",
		OutputPort: 5200,
	}
	home, _ := os.UserHomeDir()
	cfg.WorkDir = filepath.Join(home, ".local", "share", "cppclient", "work")

	data, err := os.ReadFile(configPath())
	if err == nil {
		_ = json.Unmarshal(data, cfg)
	}
	return cfg
}

func SaveConfig(cfg *Config) error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}
