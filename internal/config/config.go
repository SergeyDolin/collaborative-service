package config

import (
	"flag"
	"os"
)

type Config struct {
	RunAddr     string
	DSN         string
	JWTSecret   string
	TokenExpiry int // hours
}

func LoadConfig() *Config {
	var cfg Config

	// Server flags
	flag.StringVar(&cfg.RunAddr, "a", "localhost:8000", "address and port to run server")

	// Database flags
	flag.StringVar(&cfg.DSN, "d", "postgres://postgres:1337@localhost:5432/gnssservice?sslmode=disable", "DB address")

	// JWT flags
	flag.StringVar(&cfg.JWTSecret, "jwt-secret", "your-secret-key-change-in-production", "JWT secret key")
	flag.IntVar(&cfg.TokenExpiry, "jwt-expiry", 24, "JWT token expiry in hours")

	flag.Parse()

	// Override with environment variables
	if address := os.Getenv("ADDRESS"); address != "" {
		cfg.RunAddr = address
	}
	if dsn := os.Getenv("DSN"); dsn != "" {
		cfg.DSN = dsn
	}
	if jwtSecret := os.Getenv("JWT_SECRET"); jwtSecret != "" {
		cfg.JWTSecret = jwtSecret
	}

	return &cfg
}
