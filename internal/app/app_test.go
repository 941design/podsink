package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"podsink/internal/config"
	"podsink/internal/itunes"
	"podsink/internal/storage"
)

type recordingSleeper struct {
	calls []time.Duration
}

func (r *recordingSleeper) Sleep(_ context.Context, d time.Duration) error {
	r.calls = append(r.calls, d)
	return nil
}

func TestExportOPMLRequiresSubscriptions(t *testing.T) {
	app := newTestApp(t)

	_, err := app.ExportOPML(context.Background(), filepath.Join(t.TempDir(), "subs.opml"))
	if !errors.Is(err, ErrNoSubscriptionsToExport) {
		t.Fatalf("expected ErrNoSubscriptionsToExport, got %v", err)
	}
}

func TestExportOPMLWritesFile(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()

	if _, err := app.db.ExecContext(ctx, `INSERT INTO podcasts (id, title, feed_url, subscribed_at) VALUES (?, ?, ?, ?)`,
		"pod1", "Example Podcast", "http://example.com/feed", time.Now().UTC()); err != nil {
		t.Fatalf("insert podcast: %v", err)
	}

	filePath := filepath.Join(t.TempDir(), "subs.opml")
	count, err := app.ExportOPML(ctx, filePath)
	if err != nil {
		t.Fatalf("ExportOPML error = %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 exported subscription, got %d", count)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	contents := string(data)
	if !strings.Contains(contents, "<opml") || !strings.Contains(contents, "http://example.com/feed") {
		t.Fatalf("unexpected OPML contents: %s", contents)
	}
}

func TestImportOPML(t *testing.T) {
	ctx := context.Background()
	server := newMockPodcastServer(t)

	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.ParallelDownloads = 0
	cfg.DownloadRoot = filepath.Join(dir, "downloads")
	cfg.TmpDir = filepath.Join(dir, "tmp")

	if err := os.MkdirAll(cfg.DownloadRoot, 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}
	if err := os.MkdirAll(cfg.TmpDir, 0o755); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}

	db, err := storage.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() {
		db.Close()
	})

	deps := Dependencies{
		HTTPClient: server.Client(),
		ITunes:     itunes.NewClient(server.Client(), server.URL),
	}

	application := NewWithDependencies(cfg, filepath.Join(dir, "config.yaml"), db, deps)
	t.Cleanup(func() {
		application.Close()
	})

	opmlPath := filepath.Join(dir, "import.opml")
	contents := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<opml version="1.0">
  <head><title>Subscriptions</title></head>
  <body>
    <outline type="rss" text="Example Podcast" xmlUrl="%s/feed" />
  </body>
</opml>`, server.URL)
	if err := os.WriteFile(opmlPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("write OPML file: %v", err)
	}

	result, err := application.ImportOPML(ctx, opmlPath)
	if err != nil {
		t.Fatalf("ImportOPML error = %v", err)
	}
	if result.Imported != 1 {
		t.Fatalf("expected 1 imported subscription, got %d", result.Imported)
	}
	if result.Skipped != 0 {
		t.Fatalf("expected 0 skipped subscriptions, got %d", result.Skipped)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %v", result.Errors)
	}

	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM podcasts").Scan(&count); err != nil {
		t.Fatalf("query podcasts: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 podcast in database, got %d", count)
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

func TestSearchEpisodesCommand(t *testing.T) {
	ctx := context.Background()
	server := newMockPodcastServer(t)

	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.ParallelDownloads = 0
	cfg.DownloadRoot = filepath.Join(dir, "downloads")
	cfg.TmpDir = filepath.Join(dir, "tmp")

	if err := os.MkdirAll(cfg.DownloadRoot, 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}
	if err := os.MkdirAll(cfg.TmpDir, 0o755); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}

	db, err := storage.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() {
		db.Close()
	})

	deps := Dependencies{
		HTTPClient: server.Client(),
		ITunes:     itunes.NewClient(server.Client(), server.URL),
	}

	application := NewWithDependencies(cfg, filepath.Join(dir, "config.yaml"), db, deps)
	t.Cleanup(func() {
		application.Close()
	})

	exec := func(command string) CommandResult {
		result, err := application.Execute(ctx, command)
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", command, err)
		}
		return result
	}

	searchResult := exec("search Example")
	if len(searchResult.SearchResults) == 0 {
		t.Fatal("expected podcast search results")
	}

	var podcast itunes.Podcast
	for _, sr := range searchResult.SearchResults {
		if sr.Podcast.Title == "Example Podcast" {
			podcast = sr.Podcast
			break
		}
	}
	if podcast.ID == "" {
		t.Fatal("failed to locate podcast in search results")
	}

	if _, err := application.SubscribePodcast(ctx, podcast); err != nil {
		t.Fatalf("SubscribePodcast() error = %v", err)
	}

	episodesResult := exec("search episodes Episode")
	if len(episodesResult.EpisodeResults) != 2 {
		t.Fatalf("expected 2 episode search results, got %d", len(episodesResult.EpisodeResults))
	}

	filtered := exec("search episodes Two")
	if len(filtered.EpisodeResults) != 1 {
		t.Fatalf("expected 1 filtered episode, got %d", len(filtered.EpisodeResults))
	}
	if filtered.EpisodeResults[0].Episode.Title != "Episode Two" {
		t.Fatalf("expected Episode Two, got %s", filtered.EpisodeResults[0].Episode.Title)
	}

	none := exec("search episodes Missing")
	if none.EpisodeResults != nil && len(none.EpisodeResults) > 0 {
		t.Fatalf("expected no episode results, got %d", len(none.EpisodeResults))
	}
	if !strings.Contains(none.Message, "No episodes found.") {
		t.Fatalf("expected no episodes message, got %q", none.Message)
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
	cfg.ParallelDownloads = 0
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
	t.Cleanup(func() {
		application.Close()
	})

	exec := func(command string) CommandResult {
		result, err := application.Execute(ctx, command)
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", command, err)
		}
		return result
	}

	subscribe := func(podcast itunes.Podcast) CommandResult {
		result, err := application.SubscribePodcast(ctx, podcast)
		if err != nil {
			t.Fatalf("SubscribePodcast(%s) error = %v", podcast.ID, err)
		}
		return result
	}

	searchResult := exec("search Example")
	found := false
	var targetPodcast itunes.Podcast
	for _, sr := range searchResult.SearchResults {
		if sr.Podcast.ID == "12345" {
			found = true
			targetPodcast = sr.Podcast
			break
		}
	}
	if !found {
		t.Fatalf("search results missing podcast id 12345")
	}

	if msg := subscribe(targetPodcast).Message; !strings.Contains(msg, "Subscribed to Example Podcast") {
		t.Fatalf("subscribe output unexpected: %s", msg)
	}

	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM episodes").Scan(&count); err != nil {
		t.Fatalf("query episodes count: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 episodes, got %d", count)
	}

	listResult := exec("list subscriptions")
	if len(listResult.SearchResults) == 0 {
		t.Fatal("list subscriptions should return search results")
	}
	listFound := false
	for _, sr := range listResult.SearchResults {
		if sr.Podcast.Title == "Example Podcast" {
			listFound = true
			break
		}
	}
	if !listFound {
		t.Fatalf("list output missing subscription: %+v", listResult.SearchResults)
	}

	episodesResult := exec("episodes")
	if len(episodesResult.EpisodeResults) != 2 {
		t.Fatalf("expected 2 episodes, got %d", len(episodesResult.EpisodeResults))
	}
	if episodesResult.EpisodeResults[0].Episode.Title != "Episode Two" {
		t.Fatalf("expected most recent episode first, got %s", episodesResult.EpisodeResults[0].Episode.Title)
	}
	if episodesResult.EpisodeResults[0].PodcastTitle != "Example Podcast" {
		t.Fatalf("expected podcast title to be included, got %s", episodesResult.EpisodeResults[0].PodcastTitle)
	}
	foundEp1, foundEp2 := false, false
	for _, ep := range episodesResult.EpisodeResults {
		if ep.Episode.Title == "Episode One" {
			foundEp1 = true
		}
		if ep.Episode.Title == "Episode Two" {
			foundEp2 = true
		}
	}
	if !foundEp1 || !foundEp2 {
		t.Fatalf("episodes missing Episode One or Episode Two")
	}

	if state := episodeState(t, ctx, db, "ep1"); state != stateSeen {
		t.Fatalf("expected ep1 state %s after viewing episodes, got %s", stateSeen, state)
	}

	usage := exec("episodes 12345")
	if usage.Message != "Usage: episodes" {
		t.Fatalf("expected usage message for extra args, got %q", usage.Message)
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

func TestDownloadRetriesAndResume(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.ParallelDownloads = 0
	cfg.DownloadRoot = filepath.Join(dir, "downloads")
	cfg.TmpDir = filepath.Join(dir, "tmp")
	cfg.RetryCount = 2
	cfg.RetryBackoffMaxSec = 2

	if err := os.MkdirAll(cfg.DownloadRoot, 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}
	if err := os.MkdirAll(cfg.TmpDir, 0o755); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}

	db, err := storage.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() {
		db.Close()
	})

	const (
		podcastID   = "555"
		episodeID   = "ep1"
		content     = "hello world"
		partialSize = 6
	)

	var (
		requests      int
		receivedRange []string
	)

	handler := func(baseURL string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/search", "/lookup":
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"results":[{"collectionId":%s,"collectionName":"Retry Podcast","artistName":"Retry Author","feedUrl":"%s/feed"}]}`, podcastID, baseURL)
			case "/feed":
				w.Header().Set("Content-Type", "application/rss+xml")
				fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Retry Podcast</title>
    <item>
      <guid>%s</guid>
      <title>Episode One</title>
      <enclosure url="%s/audio/%s.mp3" length="%d" type="audio/mpeg" />
    </item>
  </channel>
</rss>`, episodeID, baseURL, episodeID, len(content))
			case "/audio/" + episodeID + ".mp3":
				requests++
				receivedRange = append(receivedRange, r.Header.Get("Range"))
				if requests == 1 {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if r.Header.Get("Range") != fmt.Sprintf("bytes=%d-", partialSize) {
					t.Fatalf("expected range header bytes=%d-, got %q", partialSize, r.Header.Get("Range"))
				}
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)-partialSize))
				w.WriteHeader(http.StatusPartialContent)
				w.Write([]byte(content[partialSize:]))
			default:
				http.NotFound(w, r)
			}
		})
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(server.URL).ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	sleeper := &recordingSleeper{}
	deps := Dependencies{
		HTTPClient: server.Client(),
		ITunes:     itunes.NewClient(server.Client(), server.URL),
		Sleep:      sleeper.Sleep,
	}
	app := NewWithDependencies(cfg, filepath.Join(dir, "config.yaml"), db, deps)
	t.Cleanup(func() {
		app.Close()
	})

	exec := func(command string) CommandResult {
		result, err := app.Execute(ctx, command)
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", command, err)
		}
		return result
	}

	subscribe := func(podcast itunes.Podcast) CommandResult {
		result, err := app.SubscribePodcast(ctx, podcast)
		if err != nil {
			t.Fatalf("SubscribePodcast(%s) error = %v", podcast.ID, err)
		}
		return result
	}

	searchResult := exec("search Retry")
	found := false
	var targetPodcast itunes.Podcast
	for _, sr := range searchResult.SearchResults {
		if sr.Podcast.ID == podcastID {
			found = true
			targetPodcast = sr.Podcast
			break
		}
	}
	if !found {
		t.Fatalf("search results missing podcast id %s", podcastID)
	}
	if msg := subscribe(targetPodcast).Message; !strings.Contains(msg, "Subscribed") {
		t.Fatalf("subscribe output unexpected: %s", msg)
	}

	partialPath := filepath.Join(cfg.TmpDir, "podsink-"+episodeID+".partial")
	if err := os.WriteFile(partialPath, []byte(content[:partialSize]), 0o600); err != nil {
		t.Fatalf("write partial: %v", err)
	}

	downloadMsg := exec("download " + episodeID).Message
	if !strings.Contains(downloadMsg, "Downloaded") {
		t.Fatalf("download message unexpected: %s", downloadMsg)
	}

	if requests != 2 {
		t.Fatalf("expected 2 download attempts, got %d", requests)
	}
	if len(receivedRange) != 2 {
		t.Fatalf("expected 2 recorded ranges, got %d", len(receivedRange))
	}
	if receivedRange[0] != fmt.Sprintf("bytes=%d-", partialSize) {
		t.Fatalf("first range header unexpected: %q", receivedRange[0])
	}

	if len(sleeper.calls) != 1 {
		t.Fatalf("expected one backoff call, got %d", len(sleeper.calls))
	}
	if sleeper.calls[0] != time.Second {
		t.Fatalf("expected 1s backoff, got %v", sleeper.calls[0])
	}

	var filePath string
	if err := db.QueryRowContext(ctx, "SELECT file_path FROM episodes WHERE id = ?", episodeID).Scan(&filePath); err != nil {
		t.Fatalf("query file_path: %v", err)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != content {
		t.Fatalf("downloaded file mismatch: got %q", string(data))
	}

	var retryCount int
	if err := db.QueryRowContext(ctx, "SELECT retry_count FROM episodes WHERE id = ?", episodeID).Scan(&retryCount); err != nil {
		t.Fatalf("query retry_count: %v", err)
	}
	if retryCount != 0 {
		t.Fatalf("expected retry_count reset to 0, got %d", retryCount)
	}

	if _, err := os.Stat(partialPath); !os.IsNotExist(err) {
		t.Fatalf("partial file should be removed, stat err=%v", err)
	}
}

func TestDownloadQueueProcessesInParallel(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.DownloadRoot = filepath.Join(dir, "downloads")
	cfg.TmpDir = filepath.Join(dir, "tmp")
	cfg.ParallelDownloads = 2

	if err := os.MkdirAll(cfg.DownloadRoot, 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}
	if err := os.MkdirAll(cfg.TmpDir, 0o755); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}

	db, err := storage.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })

	const podcastID = "777"

	startCh := make(chan string, 4)
	releaseCh := make(chan struct{}, 4)
	var (
		mu        sync.Mutex
		active    int
		maxActive int
	)

	handler := func(baseURL string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/search", "/lookup":
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"results":[{"collectionId":%s,"collectionName":"Parallel Podcast","artistName":"Parallel Author","feedUrl":"%s/feed"}]}`, podcastID, baseURL)
			case "/feed":
				w.Header().Set("Content-Type", "application/rss+xml")
				fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Parallel Podcast</title>
    <item>
      <guid>ep1</guid>
      <title>Episode One</title>
      <enclosure url="%s/audio/ep1.mp3" length="100" type="audio/mpeg" />
    </item>
    <item>
      <guid>ep2</guid>
      <title>Episode Two</title>
      <enclosure url="%s/audio/ep2.mp3" length="100" type="audio/mpeg" />
    </item>
  </channel>
</rss>`, baseURL, baseURL)
			case "/audio/ep1.mp3", "/audio/ep2.mp3":
				mu.Lock()
				active++
				if active > maxActive {
					maxActive = active
				}
				mu.Unlock()
				startCh <- r.URL.Path
				<-releaseCh
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, "data-%s", strings.TrimPrefix(r.URL.Path, "/audio/"))
				mu.Lock()
				active--
				mu.Unlock()
			default:
				http.NotFound(w, r)
			}
		})
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(server.URL).ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	deps := Dependencies{
		HTTPClient: server.Client(),
		ITunes:     itunes.NewClient(server.Client(), server.URL),
	}
	app := NewWithDependencies(cfg, filepath.Join(dir, "config.yaml"), db, deps)
	t.Cleanup(func() {
		app.Close()
	})

	exec := func(command string) CommandResult {
		result, err := app.Execute(ctx, command)
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", command, err)
		}
		return result
	}

	subscribe := func(podcast itunes.Podcast) CommandResult {
		result, err := app.SubscribePodcast(ctx, podcast)
		if err != nil {
			t.Fatalf("SubscribePodcast(%s) error = %v", podcast.ID, err)
		}
		return result
	}

	searchResult := exec("search Parallel")
	found := false
	var targetPodcast itunes.Podcast
	for _, sr := range searchResult.SearchResults {
		if sr.Podcast.ID == podcastID {
			found = true
			targetPodcast = sr.Podcast
			break
		}
	}
	if !found {
		t.Fatalf("search results missing podcast id %s", podcastID)
	}
	if msg := subscribe(targetPodcast).Message; !strings.Contains(msg, "Subscribed") {
		t.Fatalf("subscribe output unexpected: %s", msg)
	}

	if msg := exec("queue ep1").Message; !strings.Contains(msg, "queued") {
		t.Fatalf("queue ep1 unexpected: %s", msg)
	}
	if msg := exec("queue ep2").Message; !strings.Contains(msg, "queued") {
		t.Fatalf("queue ep2 unexpected: %s", msg)
	}

	seen := make(map[string]struct{})
	deadline := time.After(3 * time.Second)
	for len(seen) < 2 {
		select {
		case path := <-startCh:
			seen[path] = struct{}{}
		case <-deadline:
			t.Fatalf("timeout waiting for parallel downloads, saw %d", len(seen))
		}
	}

	releaseCh <- struct{}{}
	releaseCh <- struct{}{}

	waitDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(waitDeadline) {
		if episodeState(t, ctx, db, "ep1") == stateDownloaded && episodeState(t, ctx, db, "ep2") == stateDownloaded {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if episodeState(t, ctx, db, "ep1") != stateDownloaded || episodeState(t, ctx, db, "ep2") != stateDownloaded {
		t.Fatalf("episodes not downloaded in time: ep1=%s ep2=%s", episodeState(t, ctx, db, "ep1"), episodeState(t, ctx, db, "ep2"))
	}

	mu.Lock()
	observed := maxActive
	mu.Unlock()
	if observed < 2 {
		t.Fatalf("expected at least 2 concurrent downloads, saw %d", observed)
	}

	for _, id := range []string{"ep1", "ep2"} {
		var filePath string
		if err := db.QueryRowContext(ctx, "SELECT file_path FROM episodes WHERE id = ?", id).Scan(&filePath); err != nil {
			t.Fatalf("query file path for %s: %v", id, err)
		}
		if _, err := os.Stat(filePath); err != nil {
			t.Fatalf("downloaded file missing for %s: %v", id, err)
		}
	}
}

func newTestApp(t *testing.T) *App {
	t.Helper()

	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.ParallelDownloads = 0
	cfg.DownloadRoot = filepath.Join(dir, "downloads")
	cfg.TmpDir = filepath.Join(dir, "tmp")

	db, err := storage.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() {
		db.Close()
	})

	if err := os.MkdirAll(cfg.DownloadRoot, 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}
	if err := os.MkdirAll(cfg.TmpDir, 0o755); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}

	app := New(cfg, filepath.Join(dir, "config.yaml"), db)
	t.Cleanup(func() {
		app.Close()
	})
	return app
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
