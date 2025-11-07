package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"podsink/internal/config"
	"podsink/internal/itunes"
	"podsink/internal/storage"
)

func TestHelpListsKeyCommands(t *testing.T) {
	app := newTestApp(t)

	result, err := app.Execute(context.Background(), "help")
	if err != nil {
		t.Fatalf("Execute(help) error = %v", err)
	}

	for _, expected := range []string{"search <query>", "list subscriptions", "config [show]"} {
		if !strings.Contains(result.Message, expected) {
			t.Errorf("help output missing %q\n%s", expected, result.Message)
		}
	}
}

func TestExitCommandSetsQuit(t *testing.T) {
	app := newTestApp(t)

	result, err := app.Execute(context.Background(), "quit")
	if err != nil {
		t.Fatalf("Execute(quit) error = %v", err)
	}

	if !result.Quit {
		t.Fatal("expected quit result")
	}
}

func TestListCommandUsage(t *testing.T) {
	app := newTestApp(t)

	result, err := app.Execute(context.Background(), "list")
	if err != nil {
		t.Fatalf("Execute(list) error = %v", err)
	}

	if !strings.Contains(result.Message, "Usage: list subscriptions") {
		t.Fatalf("unexpected list response: %s", result.Message)
	}
}

func TestConfigShowRendersYaml(t *testing.T) {
	app := newTestApp(t)

	result, err := app.Execute(context.Background(), "config show")
	if err != nil {
		t.Fatalf("Execute(config show) error = %v", err)
	}

	if !strings.Contains(result.Message, "download_root:") {
		t.Fatalf("expected download_root in config output: %s", result.Message)
	}
}

func TestPodcastLifecycle(t *testing.T) {
	ctx := context.Background()
	server := newMockPodcastServer(t)

	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.DownloadRoot = filepath.Join(dir, "downloads")
	cfg.TmpDir = filepath.Join(dir, "tmp")

	if err := os.MkdirAll(cfg.DownloadRoot, 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}

	db, err := storage.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() {
		db.Close()
	})

	httpClient := server.Client()
	deps := Dependencies{
		HTTPClient: httpClient,
		ITunes:     itunes.NewClient(httpClient, server.URL),
	}

	application := NewWithDependencies(cfg, filepath.Join(dir, "config.yaml"), db, deps)

	exec := func(command string) CommandResult {
		result, err := application.Execute(ctx, command)
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", command, err)
		}
		return result
	}

	if msg := exec("search Example").Message; !strings.Contains(msg, "12345") {
		t.Fatalf("search output missing podcast id: %s", msg)
	}

	if msg := exec("subscribe 12345").Message; !strings.Contains(msg, "Subscribed to Example Podcast") {
		t.Fatalf("subscribe output unexpected: %s", msg)
	}

	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM episodes").Scan(&count); err != nil {
		t.Fatalf("query episodes count: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 episodes, got %d", count)
	}

	if msg := exec("list subscriptions").Message; !strings.Contains(msg, "Example Podcast") {
		t.Fatalf("list output missing subscription: %s", msg)
	}

	episodesMsg := exec("episodes 12345").Message
	if !strings.Contains(episodesMsg, "Episode One") || !strings.Contains(episodesMsg, "Episode Two") {
		t.Fatalf("episodes output missing titles: %s", episodesMsg)
	}

	if state := episodeState(t, ctx, db, "ep1"); state != stateSeen {
		t.Fatalf("expected ep1 state %s after viewing episodes, got %s", stateSeen, state)
	}

	if msg := exec("queue ep1").Message; !strings.Contains(msg, "queued") {
		t.Fatalf("queue output unexpected: %s", msg)
	}
	if state := episodeState(t, ctx, db, "ep1"); state != stateQueued {
		t.Fatalf("expected ep1 state %s after queue, got %s", stateQueued, state)
	}

	downloadMsg := exec("download ep1").Message
	if !strings.Contains(downloadMsg, "Downloaded Episode One") {
		t.Fatalf("download output unexpected: %s", downloadMsg)
	}
	if state := episodeState(t, ctx, db, "ep1"); state != stateDownloaded {
		t.Fatalf("expected ep1 state %s after download, got %s", stateDownloaded, state)
	}
	var filePath string
	if err := db.QueryRowContext(ctx, "SELECT file_path FROM episodes WHERE id = ?", "ep1").Scan(&filePath); err != nil {
		t.Fatalf("query file_path: %v", err)
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("downloaded file not found: %v", err)
	}

	if msg := exec("ignore ep2").Message; !strings.Contains(msg, "ignored") {
		t.Fatalf("ignore output unexpected: %s", msg)
	}
	if state := episodeState(t, ctx, db, "ep2"); state != stateIgnored {
		t.Fatalf("expected ep2 state %s after ignore, got %s", stateIgnored, state)
	}

	if msg := exec("ignore ep2").Message; !strings.Contains(msg, "unignored") {
		t.Fatalf("second ignore output unexpected: %s", msg)
	}
	if state := episodeState(t, ctx, db, "ep2"); state != stateSeen {
		t.Fatalf("expected ep2 state %s after unignore, got %s", stateSeen, state)
	}
}

func newTestApp(t *testing.T) *App {
	t.Helper()

	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.DownloadRoot = filepath.Join(dir, "downloads")

	db, err := storage.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() {
		db.Close()
	})

	return New(cfg, filepath.Join(dir, "config.yaml"), db)
}

func newMockPodcastServer(t *testing.T) *httptest.Server {
	t.Helper()

	const podcastID = "12345"
	handler := func(serverURL string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/search":
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"results":[{"collectionId":%s,"collectionName":"Example Podcast","artistName":"Example Author","feedUrl":"%s/feed"}]}`, podcastID, serverURL)
			case "/lookup":
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"results":[{"collectionId":%s,"collectionName":"Example Podcast","artistName":"Example Author","feedUrl":"%s/feed"}]}`, podcastID, serverURL)
			case "/feed":
				w.Header().Set("Content-Type", "application/rss+xml")
				fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Example Podcast</title>
    <description>Example description</description>
    <item>
      <guid>ep1</guid>
      <title>Episode One</title>
      <pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate>
      <enclosure url="%s/audio/ep1.mp3" length="100" type="audio/mpeg" />
    </item>
    <item>
      <guid>ep2</guid>
      <title>Episode Two</title>
      <pubDate>Tue, 03 Jan 2006 15:04:05 -0700</pubDate>
      <enclosure url="%s/audio/ep2.mp3" length="100" type="audio/mpeg" />
    </item>
  </channel>
</rss>`, serverURL, serverURL)
			case "/audio/ep1.mp3":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("episode-one"))
			case "/audio/ep2.mp3":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("episode-two"))
			default:
				http.NotFound(w, r)
			}
		})
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(srv.URL).ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func episodeState(t *testing.T, ctx context.Context, db *sql.DB, episodeID string) string {
	t.Helper()
	var state string
	if err := db.QueryRowContext(ctx, "SELECT state FROM episodes WHERE id = ?", episodeID).Scan(&state); err != nil {
		t.Fatalf("query episode state: %v", err)
	}
	return state
}
