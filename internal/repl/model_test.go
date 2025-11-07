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
	return newTestAppWithConfig(t, nil)
}

func newTestAppWithConfig(t *testing.T, mutate func(*config.Config)) *app.App {
	t.Helper()

	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.ParallelDownloads = 0
	cfg.DownloadRoot = filepath.Join(dir, "downloads")
	cfg.TmpDir = filepath.Join(dir, "tmp")
	if mutate != nil {
		mutate(&cfg)
	}

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
		ctx:   context.Background(),
		app:   a,
		input: textinput.New(),
		search: searchView{
			active: true,
			results: []app.SearchResult{
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
			cursor: 0,
		},
		theme:         theme.ForName(a.Config().ColorTheme),
		longDescCache: make(map[string]string),
	}

	// Execute
	updatedModel, _ := m.handleSearchSubscribe()
	m = updatedModel.(model)

	// Assert: Should stay in list view
	if !m.search.active {
		t.Error("Expected to stay in search mode (list view) after subscribing from list view")
	}
	if m.search.details.active {
		t.Error("Should not be in details mode after subscribing from list view")
	}
	if len(m.search.results) == 0 {
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
		ctx:   context.Background(),
		app:   a,
		input: textinput.New(),
		search: searchView{
			active: true,
			results: []app.SearchResult{
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
			cursor: 0,
		},
		theme:         theme.ForName(a.Config().ColorTheme),
		longDescCache: make(map[string]string),
	}

	// Execute
	updatedModel, _ := m.handleSearchUnsubscribe()
	m = updatedModel.(model)

	// Assert: Should stay in list view
	if !m.search.active {
		t.Error("Expected to stay in search mode (list view) after unsubscribing from list view")
	}
	if m.search.details.active {
		t.Error("Should not be in details mode after unsubscribing from list view")
	}
	if len(m.search.results) == 0 {
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
		ctx:   context.Background(),
		app:   a,
		input: textinput.New(),
		search: searchView{
			active: true,
			results: []app.SearchResult{
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
			cursor: 0,
		},
		theme:         theme.ForName(a.Config().ColorTheme),
		longDescCache: make(map[string]string),
	}

	// Execute
	updatedModel, _ := m.handleSearchUnsubscribe()
	m = updatedModel.(model)

	// Assert: Subscription status should be updated
	if m.search.results[0].IsSubscribed {
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
		ctx:   ctx,
		app:   a,
		input: textinput.New(),
		episodes: episodeView{
			active:  true,
			results: res.EpisodeResults,
			cursor:  0,
		},
		theme:         theme.ForName(a.Config().ColorTheme),
		longDescCache: make(map[string]string),
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if !m.episodes.details.active {
		t.Fatal("expected to enter episode details mode after pressing enter")
	}
	if m.episodes.details.detail.ID != res.EpisodeResults[0].Episode.ID {
		t.Fatalf("expected episode details for %s, got %s", res.EpisodeResults[0].Episode.ID, m.episodes.details.detail.ID)
	}
	if len(m.episodes.details.lines) == 0 {
		t.Fatal("expected episode description lines to be prepared")
	}
}

func TestRenderEpisodeDetailsRespectsMaxLines(t *testing.T) {
	a := newTestAppWithConfig(t, func(cfg *config.Config) {
		cfg.MaxEpisodeDescriptionLines = 3
	})

	m := model{
		ctx:   context.Background(),
		app:   a,
		theme: theme.ForName(a.Config().ColorTheme),
		episodes: episodeView{
			details: episodeDetailView{
				active: true,
				detail: app.EpisodeDetail{ID: "ep-1", Title: "Episode One"},
				lines:  []string{"Line 1", "Line 2", "Line 3", "Line 4"},
				scroll: 0,
			},
		},
		longDescCache: make(map[string]string),
	}

	view := m.renderEpisodeDetails()
	if strings.Contains(view, "Line 4") {
		t.Fatalf("expected line 4 to be hidden initially:\n%s", view)
	}
	if !strings.Contains(view, "Line 1") || !strings.Contains(view, "Line 3") {
		t.Fatalf("expected first page lines to be visible:\n%s", view)
	}
	if !strings.Contains(view, "Showing lines 1-3 of 4") {
		t.Fatalf("expected range indicator on first page:\n%s", view)
	}

	m.episodes.details.scroll = 1
	view = m.renderEpisodeDetails()
	if !strings.Contains(view, "Line 4") {
		t.Fatalf("expected line 4 to appear after scrolling:\n%s", view)
	}
	if strings.Contains(view, "Line 1") {
		t.Fatalf("expected line 1 to be hidden after scrolling:\n%s", view)
	}
	if !strings.Contains(view, "Showing lines 2-4 of 4") {
		t.Fatalf("expected updated range indicator after scrolling:\n%s", view)
	}
}

func TestEpisodeDetailsScrollKeys(t *testing.T) {
	a := newTestAppWithConfig(t, func(cfg *config.Config) {
		cfg.MaxEpisodeDescriptionLines = 2
	})

	m := model{
		ctx:   context.Background(),
		app:   a,
		theme: theme.ForName(a.Config().ColorTheme),
		episodes: episodeView{
			details: episodeDetailView{
				active: true,
				detail: app.EpisodeDetail{ID: "ep-1", Title: "Episode"},
				lines:  []string{"L1", "L2", "L3"},
			},
		},
		longDescCache: make(map[string]string),
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(model)
	if m.episodes.details.scroll != 1 {
		t.Fatalf("expected scroll to advance by one, got %d", m.episodes.details.scroll)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(model)
	if m.episodes.details.scroll != 1 {
		t.Fatalf("expected scroll to clamp at max offset, got %d", m.episodes.details.scroll)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(model)
	if m.episodes.details.scroll != 0 {
		t.Fatalf("expected scroll to move back to 0, got %d", m.episodes.details.scroll)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(model)
	if m.episodes.details.scroll != 1 {
		t.Fatalf("expected pgdown to jump forward, got %d", m.episodes.details.scroll)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = updated.(model)
	if m.episodes.details.scroll != 0 {
		t.Fatalf("expected home to reset scroll, got %d", m.episodes.details.scroll)
	}
}
