package repl

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"podsink/internal/app"
	"podsink/internal/config"
	"podsink/internal/itunes"
	"podsink/internal/storage"
)

// Helper to create a test app
func newTestApp(t *testing.T) *app.App {
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

	application := app.New(cfg, filepath.Join(dir, "config.yaml"), db)
	t.Cleanup(func() {
		application.Close()
	})
	return application
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
	_, _ = a.Execute(context.Background(), "subscribe 12345")

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
	_, _ = a.Execute(context.Background(), "subscribe 12345")

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
	}

	// Execute
	updatedModel, _ := m.handleSearchUnsubscribe()
	m = updatedModel.(model)

	// Assert: Subscription status should be updated
	if m.searchResults[0].IsSubscribed {
		t.Error("Expected IsSubscribed to be false after unsubscribing from list view")
	}
}
