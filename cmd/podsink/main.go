package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"podsink/internal/app"
	"podsink/internal/config"
	"podsink/internal/logging"
	"podsink/internal/repl"
	"podsink/internal/storage"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("failed to resolve home directory: %v", err)
	}

	baseDir := filepath.Join(home, ".podsink")
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		log.Fatalf("failed to create config directory: %v", err)
	}

	logPath := filepath.Join(baseDir, "podsink.log")
	logging.Configure(logPath)

	configPath := filepath.Join(baseDir, "config.yaml")
	cfg, err := config.Ensure(ctx, configPath)
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	dbPath := filepath.Join(baseDir, "app.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	application := app.New(cfg, configPath, db)

	if err := repl.Run(ctx, application); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
