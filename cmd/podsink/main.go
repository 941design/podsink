package main

import (
	"context"
	"flag"
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
	importOPML := flag.String("import-opml", "", "import subscriptions from an OPML file and exit")
	exportOPML := flag.String("export-opml", "", "export subscriptions to an OPML file and exit")
	flag.Parse()

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
	defer application.Close()

	if *importOPML != "" && *exportOPML != "" {
		fmt.Fprintln(os.Stderr, "error: --import-opml and --export-opml cannot be used together")
		os.Exit(1)
	}

	if *exportOPML != "" {
		count, err := application.ExportOPML(ctx, *exportOPML)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error exporting OPML: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "Exported %d subscriptions to %s.\n", count, *exportOPML)
		return
	}

	if *importOPML != "" {
		result, err := application.ImportOPML(ctx, *importOPML)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error importing OPML: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "Imported %d subscriptions, skipped %d already subscribed.\n", result.Imported, result.Skipped)
		if len(result.Errors) > 0 {
			fmt.Fprintln(os.Stdout, "Errors encountered:")
			for _, msg := range result.Errors {
				fmt.Fprintf(os.Stdout, "  %s\n", msg)
			}
		}
		return
	}

	if err := repl.Run(ctx, application); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
