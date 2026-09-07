package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	cfg := LoadConfig()

	// Аргументы командной строки переопределяют конфиг
	if len(os.Args) > 1 {
		cfg.RtkrcvPath = os.Args[1]
	}
	if len(os.Args) > 2 {
		cfg.ATXFile = os.Args[2]
	}

	// Резолвим пути в абсолютные, чтобы они работали из любой рабочей директории
	if cfg.RtkrcvPath != "" {
		if abs, err := filepath.Abs(cfg.RtkrcvPath); err == nil {
			cfg.RtkrcvPath = abs
		}
	}
	if cfg.ATXFile != "" {
		if abs, err := filepath.Abs(cfg.ATXFile); err == nil {
			cfg.ATXFile = abs
		}
	}

	// ATX по умолчанию — рядом с rtkrcv в ../../cmd/solver/src/
	if cfg.ATXFile == "" && cfg.RtkrcvPath != "" {
		candidate := filepath.Join(filepath.Dir(cfg.RtkrcvPath), "..", "..", "cmd", "solver", "src", "igs20.atx")
		if abs, err := filepath.Abs(candidate); err == nil {
			if _, err := os.Stat(abs); err == nil {
				cfg.ATXFile = abs
			}
		}
	}

	model := newModel(cfg)
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка: %v\n", err)
		os.Exit(1)
	}
}
