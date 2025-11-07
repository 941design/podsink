package repl

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"podsink/internal/app"
	"podsink/internal/config"
	"podsink/internal/itunes"
	"podsink/internal/storage"
	"podsink/internal/theme"
)

// Helper to create a test app
func newTestApp(t *testing.T) *app.App {
	t.Helper()

	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.ParallelDownloads = 0
	cfg.DownloadRoot = filepath.Join(dir, "downloads")
	cfg.TmpDir = filepath.Join(dir, "tmp")

	httpClient := &http.Client{Transport: stubTransport{}}

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

	deps := app.Dependencies{
		HTTPClient: httpClient,
	}
	application := app.NewWithDependencies(cfg, filepath.Join(dir, "config.yaml"), db, deps)
	t.Cleanup(func() {
		application.Close()
	})
	return application
}

type stubTransport struct{}

func (stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rss := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Stub Podcast</title>
    <description>Example description</description>
    <item>
      <guid>stub-episode</guid>
      <title>Stub Episode</title>
      <description>Example episode</description>
      <enclosure url="http://example.com/audio.mp3" type="audio/mpeg" />
    </item>
  </channel>
</rss>`

	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": []string{"application/rss+xml"}},
		Body:          io.NopCloser(strings.NewReader(rss)),
		ContentLength: int64(len(rss)),
		Request:       req,
	}, nil
}

// TestSubscribeNavigationFromListView verifies that subscribing from list view keeps the user in list view
func TestSubscribeNavigationFromListView(t *testing.T) {
	a := newTestApp(t)

	m := model{
		ctx:         context.Background(),
		app:         a,
		input:       textinput.New(),
		searchMode:  true,  // In list view
		detailsMode: false, // Not in details view
		searchResults: []app.SearchResult{
			{
				Podcast: itunes.Podcast{
					ID:      "12345",
					Title:   "Test Podcast",
					Author:  "Test Artist",
					FeedURL: "http://example.com/feed.xml",
				},
				IsSubscribed: false,
			},
		},
		searchCursor: 0,
		theme:        theme.ForName(a.Config().ColorTheme),
	}

	// Execute
	updatedModel, _ := m.handleSearchSubscribe()
	m = updatedModel.(model)

	// Assert: Should stay in list view
	if !m.searchMode {
		t.Error("Expected to stay in search mode (list view) after subscribing from list view")
	}
	if m.detailsMode {
		t.Error("Should not be in details mode after subscribing from list view")
	}
	if len(m.searchResults) == 0 {
		t.Error("Search results should not be cleared when subscribing from list view")
	}
}

// TestUnsubscribeNavigationFromListView verifies that unsubscribing from list view keeps the user in list view
func TestUnsubscribeNavigationFromListView(t *testing.T) {
	a := newTestApp(t)

	// Subscribe first
	if _, err := a.SubscribePodcast(context.Background(), itunes.Podcast{ID: "12345", Title: "Test Podcast", FeedURL: "http://example.com/feed.xml"}); err != nil {
		t.Fatalf("SubscribePodcast() error = %v", err)
	}

	m := model{
		ctx:         context.Background(),
		app:         a,
		input:       textinput.New(),
		searchMode:  true,  // In list view
		detailsMode: false, // Not in details view
		searchResults: []app.SearchResult{
			{
				Podcast: itunes.Podcast{
					ID:      "12345",
					Title:   "Test Podcast",
					Author:  "Test Artist",
					FeedURL: "http://example.com/feed.xml",
				},
				IsSubscribed: true,
			},
		},
		searchCursor: 0,
		theme:        theme.ForName(a.Config().ColorTheme),
	}

	// Execute
	updatedModel, _ := m.handleSearchUnsubscribe()
	m = updatedModel.(model)

	// Assert: Should stay in list view
	if !m.searchMode {
		t.Error("Expected to stay in search mode (list view) after unsubscribing from list view")
	}
	if m.detailsMode {
		t.Error("Should not be in details mode after unsubscribing from list view")
	}
	if len(m.searchResults) == 0 {
		t.Error("Search results should not be cleared when unsubscribing from list view")
	}
}

// NOTE: Additional tests for details view navigation would require mocking the iTunes API
// and RSS feed fetching. The navigation logic for details view is implemented in the code
// (model.go lines 333-340 and 386-393), and follows the same pattern as list view navigation.
// Manual testing should verify:
// - Subscribing from details view returns to list view
// - Unsubscribing from details view returns to list view

// TestUnsubscribeUpdatesStatusInListView verifies that subscription status is properly updated in list view
func TestUnsubscribeUpdatesStatusInListView(t *testing.T) {
	a := newTestApp(t)

	// Subscribe first
	if _, err := a.SubscribePodcast(context.Background(), itunes.Podcast{ID: "12345", Title: "Test Podcast", FeedURL: "http://example.com/feed.xml"}); err != nil {
		t.Fatalf("SubscribePodcast() error = %v", err)
	}

	m := model{
		ctx:         context.Background(),
		app:         a,
		input:       textinput.New(),
		searchMode:  true,
		detailsMode: false,
		searchResults: []app.SearchResult{
			{
				Podcast: itunes.Podcast{
					ID:      "12345",
					Title:   "Test Podcast",
					Author:  "Test Artist",
					FeedURL: "http://example.com/feed.xml",
				},
				IsSubscribed: true,
			},
		},
		searchCursor: 0,
		theme:        theme.ForName(a.Config().ColorTheme),
	}

	// Execute
	updatedModel, _ := m.handleSearchUnsubscribe()
	m = updatedModel.(model)

	// Assert: Subscription status should be updated
	if m.searchResults[0].IsSubscribed {
		t.Error("Expected IsSubscribed to be false after unsubscribing from list view")
	}
}

func TestEpisodeListEnterShowsDetails(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	if _, err := a.SubscribePodcast(ctx, itunes.Podcast{ID: "stub", Title: "Stub Podcast", FeedURL: "http://example.com/feed.xml"}); err != nil {
		t.Fatalf("SubscribePodcast() error = %v", err)
	}

	res, err := a.Execute(ctx, "episodes")
	if err != nil {
		t.Fatalf("Execute(episodes) error = %v", err)
	}
	if len(res.EpisodeResults) == 0 {
		t.Fatal("expected at least one episode result")
	}

	m := model{
		ctx:            ctx,
		app:            a,
		input:          textinput.New(),
		episodeMode:    true,
		episodeResults: res.EpisodeResults,
		episodeCursor:  0,
		theme:          theme.ForName(a.Config().ColorTheme),
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if !m.episodeDetailsMode {
		t.Fatal("expected to enter episode details mode after pressing enter")
	}
	if m.episodeDetail.ID != res.EpisodeResults[0].Episode.ID {
		t.Fatalf("expected episode details for %s, got %s", res.EpisodeResults[0].Episode.ID, m.episodeDetail.ID)
	}
}
