package opml

import (
	"bytes"
	"strings"
	"testing"
)

func TestExport(t *testing.T) {
	subs := []Subscription{
		{Title: "Test Podcast 1", FeedURL: "https://example.com/feed1.xml"},
		{Title: "Test Podcast 2", FeedURL: "https://example.com/feed2.xml"},
	}

	var buf bytes.Buffer
	if err := Export(&buf, subs); err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Test Podcast 1") {
		t.Errorf("Export() output missing 'Test Podcast 1'")
	}
	if !strings.Contains(output, "https://example.com/feed1.xml") {
		t.Errorf("Export() output missing feed URL")
	}
	if !strings.Contains(output, `version="2.0"`) {
		t.Errorf("Export() output missing OPML version")
	}
}

func TestImport(t *testing.T) {
	opmlData := `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head>
    <title>Test Subscriptions</title>
  </head>
  <body>
    <outline type="rss" text="Test Podcast 1" title="Test Podcast 1" xmlUrl="https://example.com/feed1.xml" />
    <outline type="rss" text="Test Podcast 2" xmlUrl="https://example.com/feed2.xml" />
    <outline type="rss" text="Invalid Podcast" />
  </body>
</opml>`

	subs, err := Import(strings.NewReader(opmlData))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	if len(subs) != 2 {
		t.Errorf("Import() returned %d subscriptions, expected 2", len(subs))
	}

	if subs[0].Title != "Test Podcast 1" {
		t.Errorf("Import() subs[0].Title = %q, want 'Test Podcast 1'", subs[0].Title)
	}
	if subs[0].FeedURL != "https://example.com/feed1.xml" {
		t.Errorf("Import() subs[0].FeedURL = %q", subs[0].FeedURL)
	}

	if subs[1].Title != "Test Podcast 2" {
		t.Errorf("Import() subs[1].Title = %q, want 'Test Podcast 2'", subs[1].Title)
	}
}

func TestImportEmpty(t *testing.T) {
	opmlData := `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head><title>Empty</title></head>
  <body></body>
</opml>`

	subs, err := Import(strings.NewReader(opmlData))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	if len(subs) != 0 {
		t.Errorf("Import() returned %d subscriptions, expected 0", len(subs))
	}
}

func TestRoundTrip(t *testing.T) {
	original := []Subscription{
		{Title: "Podcast A", FeedURL: "https://example.com/a.xml"},
		{Title: "Podcast B", FeedURL: "https://example.com/b.xml"},
	}

	var buf bytes.Buffer
	if err := Export(&buf, original); err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	imported, err := Import(&buf)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	if len(imported) != len(original) {
		t.Errorf("Round trip: got %d subscriptions, want %d", len(imported), len(original))
	}

	for i := range original {
		if imported[i].Title != original[i].Title {
			t.Errorf("Round trip: subs[%d].Title = %q, want %q", i, imported[i].Title, original[i].Title)
		}
		if imported[i].FeedURL != original[i].FeedURL {
			t.Errorf("Round trip: subs[%d].FeedURL = %q, want %q", i, imported[i].FeedURL, original[i].FeedURL)
		}
	}
}
