package domain

import "time"

const (
	EpisodeStateNew        = "NEW"
	EpisodeStateSeen       = "SEEN"
	EpisodeStateIgnored    = "IGNORED"
	EpisodeStateQueued     = "QUEUED"
	EpisodeStateDownloaded = "DOWNLOADED"
)

type SubscriptionSummary struct {
	ID            string
	Title         string
	NewCount      int
	UnplayedCount int
	TotalCount    int
}

type EpisodeRow struct {
	ID          string
	Title       string
	State       string
	PublishedAt time.Time
	HasPublish  bool
	SizeBytes   int64
}

type EpisodeResult struct {
	Episode      EpisodeRow
	PodcastTitle string
	PodcastID    string
}

type EpisodeInfo struct {
	ID           string
	Title        string
	Description  string
	State        string
	PublishedAt  time.Time
	HasPublish   bool
	FilePath     string
	EnclosureURL string
	Hash         string
	PodcastID    string
	PodcastTitle string
	SizeBytes    int64
}

type EpisodeDetail struct {
	ID           string
	Title        string
	Description  string
	State        string
	PublishedAt  time.Time
	HasPublish   bool
	FilePath     string
	EnclosureURL string
	PodcastID    string
	PodcastTitle string
	SizeBytes    int64
}

type QueuedEpisodeResult struct {
	Episode      EpisodeRow
	PodcastTitle string
	PodcastID    string
	RetryCount   int
	EnqueuedAt   time.Time
}

type Podcast struct {
	ID        string
	Title     string
	FeedURL   string
	CreatedAt time.Time
}

type EpisodeInput struct {
	ID          string
	Title       string
	Description string
	PublishedAt *time.Time
	Enclosure   string
	SizeBytes   int64
}

type SubscriptionData struct {
	Podcast  Podcast
	Episodes []EpisodeInput
}

type PodcastExport struct {
	Title   string
	FeedURL string
}
