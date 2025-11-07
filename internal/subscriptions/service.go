package subscriptions

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"podsink/internal/domain"
	"podsink/internal/feeds"
	"podsink/internal/itunes"
	"podsink/internal/opml"
	"podsink/internal/repository"
)

var (
	ErrMissingPodcastID        = errors.New("podcast ID cannot be empty")
	ErrMissingFeedURL          = errors.New("podcast feed URL missing")
	ErrAlreadySubscribed       = errors.New("already subscribed")
	ErrNoSubscriptionsToExport = errors.New("no subscriptions to export")
	ErrNoSubscriptionsInOPML   = errors.New("no subscriptions found in OPML file")
)

type SubscribeResult struct {
	Title string
	Added int
}

type ImportResult struct {
	Imported int
	Skipped  int
	Errors   []string
}

type Service struct {
	store      *repository.Store
	httpClient *http.Client
	itunes     *itunes.Client
}

func NewService(store *repository.Store, client *http.Client, itunesClient *itunes.Client) *Service {
	return &Service{store: store, httpClient: client, itunes: itunesClient}
}

func (s *Service) Summaries(ctx context.Context) ([]domain.SubscriptionSummary, error) {
	return s.store.ListSubscriptionSummaries(ctx)
}

func (s *Service) IsSubscribed(ctx context.Context, podcastID string) (bool, string, error) {
	return s.store.SubscriptionExists(ctx, podcastID)
}

func (s *Service) Subscribe(ctx context.Context, podcast itunes.Podcast) (SubscribeResult, error) {
	podcastID := strings.TrimSpace(podcast.ID)
	if podcastID == "" {
		return SubscribeResult{}, ErrMissingPodcastID
	}

	exists, title, err := s.store.SubscriptionExists(ctx, podcastID)
	if err != nil {
		return SubscribeResult{}, err
	}
	if exists {
		if title == "" {
			title = fallbackTitle(podcast.Title, podcastID)
		}
		return SubscribeResult{Title: title}, ErrAlreadySubscribed
	}

	meta := podcast
	if strings.TrimSpace(meta.FeedURL) == "" {
		if s.itunes == nil {
			return SubscribeResult{}, ErrMissingFeedURL
		}
		meta, err = s.itunes.LookupPodcast(ctx, podcastID)
		if err != nil {
			return SubscribeResult{}, err
		}
	}

	feedURL := strings.TrimSpace(meta.FeedURL)
	if feedURL == "" {
		return SubscribeResult{}, ErrMissingFeedURL
	}

	feedInfo, episodes, err := feeds.Fetch(ctx, s.httpClient, feedURL)
	if err != nil {
		return SubscribeResult{}, err
	}

	title = fallbackTitle(feedInfo.Title, fallbackTitle(meta.Title, podcastID))

	data := domain.SubscriptionData{
		Podcast: domain.Podcast{
			ID:        meta.ID,
			Title:     title,
			FeedURL:   feedURL,
			CreatedAt: time.Now().UTC(),
		},
		Episodes: make([]domain.EpisodeInput, 0, len(episodes)),
	}

	for _, ep := range episodes {
		var published *time.Time
		if !ep.PublishedAt.IsZero() {
			t := ep.PublishedAt.UTC()
			published = &t
		}
		data.Episodes = append(data.Episodes, domain.EpisodeInput{
			ID:          strings.TrimSpace(ep.ID),
			Title:       ep.Title,
			Description: ep.Description,
			PublishedAt: published,
			Enclosure:   ep.Enclosure,
		})
	}

	added, err := s.store.SaveSubscription(ctx, data)
	if err != nil {
		return SubscribeResult{}, err
	}
	return SubscribeResult{Title: title, Added: added}, nil
}

func (s *Service) Unsubscribe(ctx context.Context, podcastID string) (bool, error) {
	podcastID = strings.TrimSpace(podcastID)
	if podcastID == "" {
		return false, ErrMissingPodcastID
	}
	return s.store.DeleteSubscription(ctx, podcastID)
}

func (s *Service) ExportOPML(ctx context.Context, filePath string) (int, error) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return 0, errors.New("file path cannot be empty")
	}

	exports, err := s.store.ListPodcastExports(ctx)
	if err != nil {
		return 0, err
	}
	if len(exports) == 0 {
		return 0, ErrNoSubscriptionsToExport
	}

	file, err := os.Create(filePath)
	if err != nil {
		return 0, fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	subs := make([]opml.Subscription, len(exports))
	for i, export := range exports {
		subs[i] = opml.Subscription{Title: export.Title, FeedURL: export.FeedURL}
	}

	if err := opml.Export(file, subs); err != nil {
		return 0, err
	}

	return len(subs), nil
}

func (s *Service) ImportOPML(ctx context.Context, filePath string) (ImportResult, error) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return ImportResult{}, errors.New("file path cannot be empty")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return ImportResult{}, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	subs, err := opml.Import(file)
	if err != nil {
		return ImportResult{}, err
	}
	if len(subs) == 0 {
		return ImportResult{}, ErrNoSubscriptionsInOPML
	}

	var result ImportResult
	for _, sub := range subs {
		has, err := s.store.HasSubscriptionByFeedURL(ctx, sub.FeedURL)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", sub.Title, err))
			continue
		}
		if has {
			result.Skipped++
			continue
		}

		feedInfo, episodes, err := feeds.Fetch(ctx, s.httpClient, sub.FeedURL)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", sub.Title, err))
			continue
		}

		podcastID := fmt.Sprintf("opml-%x", sha256.Sum256([]byte(sub.FeedURL)))[:16]
		title := fallbackTitle(feedInfo.Title, fallbackTitle(sub.Title, "Untitled Podcast"))

		data := domain.SubscriptionData{
			Podcast: domain.Podcast{
				ID:        podcastID,
				Title:     title,
				FeedURL:   sub.FeedURL,
				CreatedAt: time.Now().UTC(),
			},
			Episodes: make([]domain.EpisodeInput, 0, len(episodes)),
		}

		for _, ep := range episodes {
			var published *time.Time
			if !ep.PublishedAt.IsZero() {
				t := ep.PublishedAt.UTC()
				published = &t
			}
			data.Episodes = append(data.Episodes, domain.EpisodeInput{
				ID:          strings.TrimSpace(ep.ID),
				Title:       ep.Title,
				Description: ep.Description,
				PublishedAt: published,
				Enclosure:   ep.Enclosure,
			})
		}

		if _, err := s.store.SaveSubscription(ctx, data); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", title, err))
			continue
		}

		result.Imported++
	}

	return result, nil
}

func fallbackTitle(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return "Untitled Podcast"
}
