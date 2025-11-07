package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open initialises the SQLite database and applies the base schema.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := applyPragmas(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := applySchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func applyPragmas(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("apply pragma %s: %w", pragma, err)
		}
	}
	return nil
}

func applySchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS podcasts (
            id TEXT PRIMARY KEY,
            title TEXT NOT NULL,
            feed_url TEXT NOT NULL,
            subscribed_at TIMESTAMP NOT NULL
        );`,
		`CREATE TABLE IF NOT EXISTS episodes (
            id TEXT PRIMARY KEY,
            podcast_id TEXT NOT NULL REFERENCES podcasts(id) ON DELETE CASCADE,
            title TEXT NOT NULL,
            description TEXT,
            state TEXT NOT NULL,
            published_at TIMESTAMP,
            downloaded_at TIMESTAMP,
            file_path TEXT,
            enclosure_url TEXT NOT NULL,
            hash TEXT,
            retry_count INTEGER DEFAULT 0
        );`,
		`CREATE INDEX IF NOT EXISTS idx_episodes_podcast ON episodes(podcast_id);`,
		`CREATE INDEX IF NOT EXISTS idx_episodes_state ON episodes(state);`,
		`CREATE TABLE IF NOT EXISTS downloads (
            episode_id TEXT PRIMARY KEY REFERENCES episodes(id) ON DELETE CASCADE,
            enqueued_at TIMESTAMP NOT NULL,
            priority INTEGER NOT NULL DEFAULT 0
        );`,
		`CREATE TABLE IF NOT EXISTS metadata (
            key TEXT PRIMARY KEY,
            value TEXT NOT NULL
        );`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
	}

	return nil
}
