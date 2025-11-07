package downloads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"podsink/internal/config"
	"podsink/internal/domain"
	"podsink/internal/repository"
)

var invalidPathChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type SleepFunc func(context.Context, time.Duration) error

type Service struct {
	cfg        config.Config
	store      *repository.Store
	httpClient *http.Client
	sleep      SleepFunc
}

func NewService(cfg config.Config, store *repository.Store, client *http.Client, sleep SleepFunc) *Service {
	if sleep == nil {
		sleep = defaultSleep
	}
	return &Service{cfg: cfg, store: store, httpClient: client, sleep: sleep}
}

func (s *Service) EnqueueEpisode(ctx context.Context, episodeID string) error {
	return s.store.EnqueueEpisode(ctx, episodeID)
}

func (s *Service) RemoveFromQueue(ctx context.Context, episodeID string) error {
	return s.store.RemoveFromQueue(ctx, episodeID)
}

func (s *Service) RequeueEpisode(ctx context.Context, episodeID string) error {
	return s.store.RequeueEpisode(ctx, episodeID)
}

func (s *Service) PersistDownloadResult(ctx context.Context, episodeID, finalPath, hash string) error {
	return s.store.PersistDownloadResult(ctx, episodeID, finalPath, hash)
}

func (s *Service) IncrementRetryCount(ctx context.Context, episodeID string) error {
	return s.store.IncrementRetryCount(ctx, episodeID)
}

func (s *Service) ClaimNextDownload(ctx context.Context) (string, error) {
	return s.store.ClaimNextDownload(ctx)
}

func (s *Service) DownloadEpisode(ctx context.Context, info domain.EpisodeInfo) (string, error) {
	finalPath, err := s.episodeFilePath(info)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.cfg.TmpDir, 0o755); err != nil {
		return "", err
	}

	attempts := s.cfg.RetryCount + 1
	if attempts <= 0 {
		attempts = 1
	}

	partialPath := s.episodePartialPath(info)
	var attemptErr error
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		resultPath, err := s.downloadOnce(ctx, info, finalPath, partialPath)
		if err == nil {
			return resultPath, nil
		}

		attemptErr = err
		if err := s.store.IncrementRetryCount(ctx, info.ID); err != nil {
			return "", err
		}

		if i == attempts-1 {
			break
		}

		backoff := time.Second << i
		maxBackoff := time.Duration(s.cfg.RetryBackoffMaxSec) * time.Second
		if maxBackoff > 0 && backoff > maxBackoff {
			backoff = maxBackoff
		}
		if backoff > 0 {
			if err := s.sleep(ctx, backoff); err != nil {
				return "", err
			}
		}
	}
	return "", attemptErr
}

func (s *Service) downloadOnce(ctx context.Context, info domain.EpisodeInfo, finalPath, partialPath string) (string, error) {
	if _, err := os.Stat(finalPath); err == nil {
		existingHash, err := computeFileHash(finalPath)
		if err == nil && existingHash == info.Hash && info.Hash != "" {
			return finalPath, nil
		}
	}

	file, err := os.OpenFile(partialPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return "", err
	}
	existingSize := stat.Size()
	if _, err := file.Seek(existingSize, io.SeekStart); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.EnclosureURL, nil)
	if err != nil {
		return "", err
	}
	if ua := strings.TrimSpace(s.cfg.UserAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if existingSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download episode: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		if existingSize > 0 {
			if err := file.Truncate(0); err != nil {
				return "", err
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return "", err
			}
		}
	case http.StatusPartialContent:
	default:
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}

	if _, err := io.Copy(file, resp.Body); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}

	hash, err := computeFileHash(partialPath)
	if err != nil {
		return "", fmt.Errorf("compute hash: %w", err)
	}

	if _, err := os.Stat(finalPath); err == nil {
		existingHash, err := computeFileHash(finalPath)
		if err == nil && existingHash == hash {
			os.Remove(partialPath)
			return finalPath, nil
		}
	}

	if err := moveFile(partialPath, finalPath); err != nil {
		return "", err
	}

	if err := s.store.PersistDownloadResult(ctx, info.ID, finalPath, hash); err != nil {
		return "", err
	}

	return finalPath, nil
}

func (s *Service) episodeFilePath(info domain.EpisodeInfo) (string, error) {
	root := strings.TrimSpace(s.cfg.DownloadRoot)
	if root == "" {
		return "", fmt.Errorf("download root is not configured")
	}
	podcastName := safeFilename(info.PodcastTitle)
	if podcastName == "" {
		podcastName = "podcast"
	}
	episodeName := safeFilename(info.Title)
	if episodeName == "" {
		episodeName = safeFilename(info.ID)
	}
	if episodeName == "" {
		episodeName = "episode"
	}
	ext := fileExtension(info.EnclosureURL)
	return filepath.Join(root, podcastName, episodeName+ext), nil
}

func (s *Service) episodePartialPath(info domain.EpisodeInfo) string {
	name := safeFilename(info.ID)
	if name == "" {
		name = safeFilename(info.Title)
	}
	if name == "" {
		name = "episode"
	}
	return filepath.Join(s.cfg.TmpDir, fmt.Sprintf("podsink-%s.partial", name))
}

func safeFilename(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	cleaned := invalidPathChars.ReplaceAllString(value, "_")
	cleaned = strings.Trim(cleaned, "._- ")
	if cleaned == "" {
		return ""
	}
	if len(cleaned) > 128 {
		cleaned = cleaned[:128]
	}
	return cleaned
}

func fileExtension(rawURL string) string {
	if rawURL == "" {
		return ".mp3"
	}
	u, err := url.Parse(rawURL)
	if err == nil {
		ext := path.Ext(u.Path)
		if ext != "" && len(ext) <= 10 {
			return ext
		}
	}
	return ".mp3"
}

func computeFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func moveFile(src, dst string) error {
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		var linkErr *os.LinkError
		if errors.As(err, &linkErr) && linkErr.Err == syscall.EXDEV {
			in, err := os.Open(src)
			if err != nil {
				return err
			}
			defer in.Close()

			out, err := os.Create(dst)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
			if err := os.Remove(src); err != nil {
				return err
			}
			return nil
		}
		return err
	}
	return nil
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
