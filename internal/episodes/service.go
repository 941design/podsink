package episodes

import (
	"context"
	"strings"

	"podsink/internal/domain"
	"podsink/internal/repository"
)

type Service struct {
	store *repository.Store
}

func NewService(store *repository.Store) *Service {
	return &Service{store: store}
}

func (s *Service) List(ctx context.Context) ([]domain.EpisodeResult, error) {
	return s.store.ListEpisodes(ctx)
}

func (s *Service) ListQueued(ctx context.Context) ([]domain.QueuedEpisodeResult, error) {
	return s.store.ListQueuedEpisodes(ctx)
}

func (s *Service) ListDownloaded(ctx context.Context) ([]domain.EpisodeResult, error) {
	return s.store.ListDownloadedEpisodes(ctx)
}

func (s *Service) Search(ctx context.Context, query string) ([]domain.EpisodeResult, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return []domain.EpisodeResult{}, nil
	}
	return s.store.SearchEpisodes(ctx, trimmed)
}

func (s *Service) MarkAllSeen(ctx context.Context) error {
	return s.store.MarkAllEpisodesSeen(ctx)
}

func (s *Service) FetchEpisodeInfo(ctx context.Context, episodeID string) (domain.EpisodeInfo, error) {
	return s.store.GetEpisodeInfo(ctx, episodeID)
}

func (s *Service) EpisodeDetails(ctx context.Context, episodeID string) (domain.EpisodeDetail, error) {
	info, err := s.store.GetEpisodeInfo(ctx, episodeID)
	if err != nil {
		return domain.EpisodeDetail{}, err
	}
	return domain.EpisodeDetail{
		ID:           info.ID,
		Title:        info.Title,
		Description:  info.Description,
		State:        info.State,
		PublishedAt:  info.PublishedAt,
		HasPublish:   info.HasPublish,
		FilePath:     info.FilePath,
		EnclosureURL: info.EnclosureURL,
		PodcastID:    info.PodcastID,
		PodcastTitle: info.PodcastTitle,
		SizeBytes:    info.SizeBytes,
	}, nil
}

func (s *Service) UpdateEpisodeState(ctx context.Context, episodeID, state string) error {
	return s.store.UpdateEpisodeState(ctx, episodeID, state)
}

func (s *Service) CheckDeletedFiles(ctx context.Context) error {
	return s.store.CheckAndUpdateDeletedFiles(ctx)
}

func (s *Service) CorrectQueuedStates(ctx context.Context) error {
	return s.store.CorrectQueuedStates(ctx)
}

func (s *Service) CountQueued(ctx context.Context) (int, error) {
	return s.store.CountQueuedEpisodes(ctx)
}

func (s *Service) CountDownloaded(ctx context.Context) (int, error) {
	return s.store.CountDownloadedEpisodes(ctx)
}

func (s *Service) FindDanglingFiles(ctx context.Context, downloadRoot string) ([]domain.DanglingFile, error) {
	return s.store.FindDanglingFiles(ctx, downloadRoot)
}
