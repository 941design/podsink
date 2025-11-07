package downloads

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"podsink/internal/domain"
	"podsink/internal/repository"
)

type EpisodeInfoProvider interface {
	FetchEpisodeInfo(ctx context.Context, episodeID string) (domain.EpisodeInfo, error)
}

type Manager struct {
	downloads *Service
	episodes  EpisodeInfoProvider
	wakeCh    chan struct{}
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func NewManager(downloads *Service, episodes EpisodeInfoProvider, workers int) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		downloads: downloads,
		episodes:  episodes,
		wakeCh:    make(chan struct{}, workers*2),
		cancel:    cancel,
	}
	for i := 0; i < workers; i++ {
		manager.wg.Add(1)
		go manager.worker(ctx)
	}
	return manager
}

func (m *Manager) Notify() {
	if m == nil {
		return
	}
	select {
	case m.wakeCh <- struct{}{}:
	default:
	}
}

func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.cancel()
	m.Notify()
	m.wg.Wait()
}

func (m *Manager) worker(ctx context.Context) {
	defer m.wg.Done()
	for {
		if ctx.Err() != nil {
			return
		}

		episodeID, err := m.downloads.ClaimNextDownload(ctx)
		if err != nil {
			if errors.Is(err, repository.ErrNoDownloadTask) {
				if err := m.waitForWork(ctx); err != nil {
					return
				}
				continue
			}
			log.Printf("download queue claim failed: %v", err)
			if err := waitWithContext(ctx, time.Second); err != nil {
				return
			}
			continue
		}

		info, err := m.episodes.FetchEpisodeInfo(ctx, episodeID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("download queue fetch info %s: %v", episodeID, err)
			}
			continue
		}
		if strings.TrimSpace(info.EnclosureURL) == "" {
			log.Printf("episode %s missing enclosure URL", episodeID)
			continue
		}
		if _, err := m.downloads.DownloadEpisode(ctx, info); err != nil {
			log.Printf("download %s failed: %v", episodeID, err)
			if err := m.downloads.RequeueEpisode(ctx, episodeID); err != nil {
				log.Printf("requeue %s failed: %v", episodeID, err)
			}
		}
	}
}

func (m *Manager) waitForWork(ctx context.Context) error {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.wakeCh:
		return nil
	case <-timer.C:
		return nil
	}
}

func waitWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
