package repository_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"podsink/internal/domain"
	"podsink/internal/repository"
	"podsink/internal/storage"
)

func newTestStore(t *testing.T) (*repository.Store, func(context.Context, string) int) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	store := repository.New(db)
	t.Cleanup(func() {
		db.Close()
	})

	lookupRetry := func(ctx context.Context, episodeID string) int {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT retry_count FROM episodes WHERE id = ?", episodeID).Scan(&count); err != nil {
			t.Fatalf("query retry_count: %v", err)
		}
		return count
	}

	return store, lookupRetry
}

func TestStoreSaveSubscriptionAndSummaries(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	now := time.Now().UTC().Truncate(time.Second)
	published := now.Add(-time.Hour)
	data := domain.SubscriptionData{
		Podcast: domain.Podcast{
			ID:        "pod-1",
			Title:     "Test Podcast",
			FeedURL:   "http://example.com/feed.xml",
			CreatedAt: now,
		},
		Episodes: []domain.EpisodeInput{
			{
				ID:          "ep-1",
				Title:       "Episode One",
				Description: "First episode description",
				PublishedAt: &published,
				Enclosure:   "http://example.com/ep1.mp3",
			},
			{
				Title:     "Episode Two",
				Enclosure: "http://example.com/ep2.mp3",
			},
		},
	}

	added, err := store.SaveSubscription(ctx, data)
	if err != nil {
		t.Fatalf("SaveSubscription: %v", err)
	}
	if added != 2 {
		t.Fatalf("expected 2 new episodes, got %d", added)
	}

	summaries, err := store.ListSubscriptionSummaries(ctx)
	if err != nil {
		t.Fatalf("ListSubscriptionSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	summary := summaries[0]
	if summary.ID != data.Podcast.ID {
		t.Errorf("summary id = %s, want %s", summary.ID, data.Podcast.ID)
	}
	if summary.Title != data.Podcast.Title {
		t.Errorf("summary title = %s, want %s", summary.Title, data.Podcast.Title)
	}
	if summary.NewCount != 2 {
		t.Errorf("summary new count = %d, want 2", summary.NewCount)
	}
	if summary.UnplayedCount != 2 {
		t.Errorf("summary unplayed count = %d, want 2", summary.UnplayedCount)
	}
	if summary.TotalCount != 2 {
		t.Errorf("summary total count = %d, want 2", summary.TotalCount)
	}

	episodes, err := store.ListEpisodes(ctx)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(episodes) != 2 {
		t.Fatalf("expected 2 episodes, got %d", len(episodes))
	}

	byID := make(map[string]domain.EpisodeResult)
	for _, result := range episodes {
		byID[result.Episode.ID] = result
	}

	ep1, ok := byID["ep-1"]
	if !ok {
		t.Fatalf("episode ep-1 not returned")
	}
	if ep1.PodcastID != data.Podcast.ID {
		t.Errorf("ep-1 podcast id = %s, want %s", ep1.PodcastID, data.Podcast.ID)
	}
	if !ep1.Episode.HasPublish {
		t.Error("ep-1 should have publication date")
	}
	if ep1.Episode.State != domain.EpisodeStateNew {
		t.Errorf("ep-1 state = %s, want %s", ep1.Episode.State, domain.EpisodeStateNew)
	}

	var generatedID string
	for id := range byID {
		if id != "ep-1" {
			generatedID = id
		}
	}
	if generatedID == "" {
		t.Fatalf("generated episode id not found")
	}
	ep2 := byID[generatedID]
	if ep2.PodcastTitle != data.Podcast.Title {
		t.Errorf("ep-2 podcast title = %s, want %s", ep2.PodcastTitle, data.Podcast.Title)
	}
	if ep2.Episode.HasPublish {
		t.Error("ep-2 should not have publication date")
	}

	info, err := store.GetEpisodeInfo(ctx, "ep-1")
	if err != nil {
		t.Fatalf("GetEpisodeInfo: %v", err)
	}
	if info.Description == "" {
		t.Error("episode info description should not be empty")
	}
	if info.EnclosureURL != "http://example.com/ep1.mp3" {
		t.Errorf("enclosure url = %s, want %s", info.EnclosureURL, "http://example.com/ep1.mp3")
	}
	if info.PodcastTitle != data.Podcast.Title {
		t.Errorf("info podcast title = %s, want %s", info.PodcastTitle, data.Podcast.Title)
	}

	exists, err := store.HasSubscriptionByFeedURL(ctx, data.Podcast.FeedURL)
	if err != nil {
		t.Fatalf("HasSubscriptionByFeedURL: %v", err)
	}
	if !exists {
		t.Error("subscription should exist by feed URL")
	}

	deleted, err := store.DeleteSubscription(ctx, data.Podcast.ID)
	if err != nil {
		t.Fatalf("DeleteSubscription: %v", err)
	}
	if !deleted {
		t.Error("expected delete to report true")
	}

	summaries, err = store.ListSubscriptionSummaries(ctx)
	if err != nil {
		t.Fatalf("ListSubscriptionSummaries after delete: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("expected no summaries after delete, got %d", len(summaries))
	}

	deleted, err = store.DeleteSubscription(ctx, data.Podcast.ID)
	if err != nil {
		t.Fatalf("DeleteSubscription second call: %v", err)
	}
	if deleted {
		t.Error("expected delete to report false when subscription missing")
	}
}

func TestStoreQueueAndDownloadLifecycle(t *testing.T) {
	ctx := context.Background()
	store, lookupRetry := newTestStore(t)

	now := time.Now().UTC()
	data := domain.SubscriptionData{
		Podcast: domain.Podcast{
			ID:        "queue-pod",
			Title:     "Queue Podcast",
			FeedURL:   "http://example.com/queue.xml",
			CreatedAt: now,
		},
		Episodes: []domain.EpisodeInput{
			{
				ID:        "queue-ep-1",
				Title:     "Queue Episode",
				Enclosure: "http://example.com/queue.mp3",
			},
		},
	}

	if _, err := store.SaveSubscription(ctx, data); err != nil {
		t.Fatalf("SaveSubscription: %v", err)
	}

	if err := store.IncrementRetryCount(ctx, "queue-ep-1"); err != nil {
		t.Fatalf("IncrementRetryCount: %v", err)
	}
	if retry := lookupRetry(ctx, "queue-ep-1"); retry != 1 {
		t.Fatalf("retry count = %d, want 1", retry)
	}

	if err := store.EnqueueEpisode(ctx, "queue-ep-1"); err != nil {
		t.Fatalf("EnqueueEpisode: %v", err)
	}
	if retry := lookupRetry(ctx, "queue-ep-1"); retry != 0 {
		t.Fatalf("retry count after enqueue = %d, want 0", retry)
	}

	info, err := store.GetEpisodeInfo(ctx, "queue-ep-1")
	if err != nil {
		t.Fatalf("GetEpisodeInfo: %v", err)
	}
	if info.State != domain.EpisodeStateQueued {
		t.Fatalf("state after enqueue = %s, want %s", info.State, domain.EpisodeStateQueued)
	}

	claimed, err := store.ClaimNextDownload(ctx)
	if err != nil {
		t.Fatalf("ClaimNextDownload: %v", err)
	}
	if claimed != "queue-ep-1" {
		t.Fatalf("claimed episode = %s, want queue-ep-1", claimed)
	}

	if _, err := store.ClaimNextDownload(ctx); !errors.Is(err, repository.ErrNoDownloadTask) {
		t.Fatalf("expected ErrNoDownloadTask, got %v", err)
	}

	finalPath := "/downloads/queue-ep-1.mp3"
	hash := "hash123"
	if err := store.PersistDownloadResult(ctx, "queue-ep-1", finalPath, hash); err != nil {
		t.Fatalf("PersistDownloadResult: %v", err)
	}

	info, err = store.GetEpisodeInfo(ctx, "queue-ep-1")
	if err != nil {
		t.Fatalf("GetEpisodeInfo after persist: %v", err)
	}
	if info.State != domain.EpisodeStateDownloaded {
		t.Fatalf("state after persist = %s, want %s", info.State, domain.EpisodeStateDownloaded)
	}
	if info.FilePath != finalPath {
		t.Fatalf("file path = %s, want %s", info.FilePath, finalPath)
	}
	if info.Hash != hash {
		t.Fatalf("hash = %s, want %s", info.Hash, hash)
	}

	if err := store.EnqueueEpisode(ctx, "queue-ep-1"); err != nil {
		t.Fatalf("EnqueueEpisode second time: %v", err)
	}
	if err := store.RemoveFromQueue(ctx, "queue-ep-1"); err != nil {
		t.Fatalf("RemoveFromQueue: %v", err)
	}
	if _, err := store.ClaimNextDownload(ctx); !errors.Is(err, repository.ErrNoDownloadTask) {
		t.Fatalf("expected ErrNoDownloadTask after removal, got %v", err)
	}
}

func TestStoreSearchEpisodes(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	now := time.Now().UTC()
	sub1 := domain.SubscriptionData{
		Podcast: domain.Podcast{
			ID:        "pod-search-1",
			Title:     "Go Time",
			FeedURL:   "http://example.com/go.xml",
			CreatedAt: now,
		},
		Episodes: []domain.EpisodeInput{
			{
				ID:          "go-1",
				Title:       "Concurrency Patterns",
				Description: "Discussing Go concurrency",
				Enclosure:   "http://example.com/go1.mp3",
			},
			{
				ID:          "go-2",
				Title:       "Testing Strategies",
				Description: "Deep dive into testing",
				Enclosure:   "http://example.com/go2.mp3",
			},
		},
	}
	if _, err := store.SaveSubscription(ctx, sub1); err != nil {
		t.Fatalf("SaveSubscription go: %v", err)
	}

	sub2 := domain.SubscriptionData{
		Podcast: domain.Podcast{
			ID:        "pod-search-2",
			Title:     "Rustacean Station",
			FeedURL:   "http://example.com/rust.xml",
			CreatedAt: now,
		},
		Episodes: []domain.EpisodeInput{
			{
				ID:          "rust-1",
				Title:       "Ownership Basics",
				Description: "Explaining ownership",
				Enclosure:   "http://example.com/rust1.mp3",
			},
		},
	}
	if _, err := store.SaveSubscription(ctx, sub2); err != nil {
		t.Fatalf("SaveSubscription rust: %v", err)
	}

	results, err := store.SearchEpisodes(ctx, "concurrency")
	if err != nil {
		t.Fatalf("SearchEpisodes concurrency: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Episode.ID != "go-1" {
		t.Errorf("expected go-1, got %s", results[0].Episode.ID)
	}

	byPodcast, err := store.SearchEpisodes(ctx, "rustacean")
	if err != nil {
		t.Fatalf("SearchEpisodes rustacean: %v", err)
	}
	if len(byPodcast) != 1 {
		t.Fatalf("expected 1 result for podcast match, got %d", len(byPodcast))
	}
	if byPodcast[0].Episode.ID != "rust-1" {
		t.Errorf("expected rust-1, got %s", byPodcast[0].Episode.ID)
	}
}

func TestListQueuedEpisodesIncludesDownloadedEpisodes(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	now := time.Now().UTC()

	data := domain.SubscriptionData{
		Podcast: domain.Podcast{
			ID:        "podcast-queue-test",
			Title:     "Queue Test Podcast",
			FeedURL:   "http://example.com/feed.xml",
			CreatedAt: now,
		},
		Episodes: []domain.EpisodeInput{
			{
				ID:        "ep-queued",
				Title:     "Queued Episode",
				Enclosure: "http://example.com/queued.mp3",
			},
			{
				ID:        "ep-downloaded",
				Title:     "Downloaded Episode",
				Enclosure: "http://example.com/downloaded.mp3",
			},
		},
	}

	if _, err := store.SaveSubscription(ctx, data); err != nil {
		t.Fatalf("SaveSubscription: %v", err)
	}

	// Queue both episodes
	if err := store.EnqueueEpisode(ctx, "ep-queued"); err != nil {
		t.Fatalf("EnqueueEpisode ep-queued: %v", err)
	}
	if err := store.EnqueueEpisode(ctx, "ep-downloaded"); err != nil {
		t.Fatalf("EnqueueEpisode ep-downloaded: %v", err)
	}

	// Verify both appear in queue
	queued, err := store.ListQueuedEpisodes(ctx)
	if err != nil {
		t.Fatalf("ListQueuedEpisodes before download: %v", err)
	}
	if len(queued) != 2 {
		t.Fatalf("queued episodes before download = %d, want 2", len(queued))
	}

	// Download one episode
	claimed, err := store.ClaimNextDownload(ctx)
	if err != nil {
		t.Fatalf("ClaimNextDownload: %v", err)
	}
	if err := store.PersistDownloadResult(ctx, claimed, "/downloads/"+claimed+".mp3", "hash123"); err != nil {
		t.Fatalf("PersistDownloadResult: %v", err)
	}

	// Verify downloaded episode's state changed to DOWNLOADED
	info, err := store.GetEpisodeInfo(ctx, claimed)
	if err != nil {
		t.Fatalf("GetEpisodeInfo: %v", err)
	}
	if info.State != domain.EpisodeStateDownloaded {
		t.Fatalf("downloaded episode state = %s, want %s", info.State, domain.EpisodeStateDownloaded)
	}

	// FIXED: Downloaded episodes should NOT appear in the queue list
	// They are removed from the downloads table when successfully downloaded
	queued, err = store.ListQueuedEpisodes(ctx)
	if err != nil {
		t.Fatalf("ListQueuedEpisodes after download: %v", err)
	}
	if len(queued) != 1 {
		t.Errorf("queued episodes after download = %d, want 1 (only QUEUED episodes should appear)", len(queued))
	}

	// Verify only the QUEUED episode remains in queue
	if len(queued) == 1 && queued[0].Episode.State != domain.EpisodeStateQueued {
		t.Errorf("remaining episode state = %s, want %s", queued[0].Episode.State, domain.EpisodeStateQueued)
	}

	// Verify the downloaded episode appears in downloads list
	downloaded, err := store.ListDownloadedEpisodes(ctx)
	if err != nil {
		t.Fatalf("ListDownloadedEpisodes: %v", err)
	}
	if len(downloaded) != 1 {
		t.Errorf("downloaded episodes = %d, want 1", len(downloaded))
	}
	if len(downloaded) == 1 && downloaded[0].Episode.State != domain.EpisodeStateDownloaded {
		t.Errorf("downloaded episode state = %s, want %s", downloaded[0].Episode.State, domain.EpisodeStateDownloaded)
	}
}
