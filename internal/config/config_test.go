package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := Defaults()
	original.DownloadRoot = filepath.Join(dir, "downloads")

	if err := Save(path, original); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.DownloadRoot != original.DownloadRoot {
		t.Fatalf("DownloadRoot mismatch: got %q want %q", loaded.DownloadRoot, original.DownloadRoot)
	}
}

func TestEnsureCreatesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	ctx := context.Background()
	downloadDir := filepath.Join(dir, "downloads")
	t.Setenv("PODSINK_DOWNLOAD_ROOT", downloadDir)

	cfg, err := Ensure(ctx, path)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	if cfg.DownloadRoot == "" {
		t.Fatalf("expected download root to be set")
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file to exist: %v", err)
	}

	if _, err := os.Stat(downloadDir); err != nil {
		t.Fatalf("expected download directory to be created: %v", err)
	}
}
