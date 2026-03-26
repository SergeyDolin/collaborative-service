package main

import (
	"flag"
	"os"
)

var (
	flagRunAddr string
	flagDSN     string
)

func parseFlags() {
	// server flag
	flag.StringVar(&flagRunAddr, "a", "localhost:8000", "address and port to run server")

	// database flag
	flag.StringVar(&flagDSN, "d", "postgres://postgres:1337@localhost:5432/gnssservice?sslmode=disable", "DB address")

	flag.Parse()

	if address := os.Getenv("ADDRESS"); address != "" {
		flagRunAddr = address
	}

	if dsn := os.Getenv("DSN"); dsn != "" {
		flagDSN = dsn
	}
}
