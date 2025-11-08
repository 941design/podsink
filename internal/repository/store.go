package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"podsink/internal/domain"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) SubscriptionExists(ctx context.Context, podcastID string) (bool, string, error) {
	var title string
	err := s.db.QueryRowContext(ctx, "SELECT title FROM podcasts WHERE id = ?", podcastID).Scan(&title)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, "", nil
		}
		return false, "", err
	}
	return true, title, nil
}

func (s *Store) SaveSubscription(ctx context.Context, data domain.SubscriptionData) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	title := strings.TrimSpace(data.Podcast.Title)
	if title == "" {
		title = "Untitled Podcast"
	}

	subscribedAt := data.Podcast.CreatedAt
	if subscribedAt.IsZero() {
		subscribedAt = time.Now().UTC()
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO podcasts (id, title, feed_url, subscribed_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET title=excluded.title, feed_url=excluded.feed_url, subscribed_at=excluded.subscribed_at`,
		data.Podcast.ID, title, data.Podcast.FeedURL, subscribedAt); err != nil {
		return 0, err
	}

	added := 0
	for _, ep := range data.Episodes {
		if strings.TrimSpace(ep.Enclosure) == "" {
			continue
		}
		episodeID := strings.TrimSpace(ep.ID)
		if episodeID == "" {
			episodeID = fmt.Sprintf("%s-%s", data.Podcast.ID, ep.Title)
		}
		if episodeID == "" {
			continue
		}

		epTitle := strings.TrimSpace(ep.Title)
		if epTitle == "" {
			epTitle = "Untitled Episode"
		}
		description := strings.TrimSpace(ep.Description)
		var published interface{}
		if ep.PublishedAt != nil {
			published = ep.PublishedAt.UTC().Format(time.RFC3339Nano)
		}

		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO episodes
(id, podcast_id, title, description, state, published_at, enclosure_url, size_bytes)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			episodeID, data.Podcast.ID, epTitle, description, domain.EpisodeStateNew, published, ep.Enclosure, ep.SizeBytes)
		if err != nil {
			return 0, err
		}
		if rows, _ := res.RowsAffected(); rows > 0 {
			added++
		}

		if _, err := tx.ExecContext(ctx, `UPDATE episodes SET
podcast_id = ?,
title = ?,
description = ?,
enclosure_url = ?,
published_at = COALESCE(?, published_at),
size_bytes = ?
WHERE id = ?`,
			data.Podcast.ID, epTitle, description, ep.Enclosure, published, ep.SizeBytes, episodeID); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return added, nil
}

func (s *Store) DeleteSubscription(ctx context.Context, podcastID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM podcasts WHERE id = ?", podcastID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *Store) ListSubscriptionSummaries(ctx context.Context) ([]domain.SubscriptionSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
p.id,
p.title,
COALESCE(SUM(CASE WHEN e.state = ? THEN 1 ELSE 0 END), 0) AS new_count,
COALESCE(SUM(CASE WHEN e.state != ? AND e.id IS NOT NULL THEN 1 ELSE 0 END), 0) AS unplayed_count,
COUNT(e.id) AS total_count
FROM podcasts p
LEFT JOIN episodes e ON e.podcast_id = p.id
GROUP BY p.id, p.title
ORDER BY LOWER(p.title)`, domain.EpisodeStateNew, domain.EpisodeStateDownloaded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := make([]domain.SubscriptionSummary, 0, 8)
	for rows.Next() {
		var summary domain.SubscriptionSummary
		if err := rows.Scan(&summary.ID, &summary.Title, &summary.NewCount, &summary.UnplayedCount, &summary.TotalCount); err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (s *Store) ListEpisodes(ctx context.Context) ([]domain.EpisodeResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.title, e.state, e.published_at, e.size_bytes, p.id, p.title
FROM episodes e
JOIN podcasts p ON p.id = e.podcast_id
ORDER BY
    CASE WHEN e.published_at IS NULL OR e.published_at = '' THEN 1 ELSE 0 END,
    e.published_at DESC,
    LOWER(p.title),
    LOWER(e.title)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]domain.EpisodeResult, 0, 128)
	for rows.Next() {
		var episode domain.EpisodeRow
		var published sql.NullString
		var podcastID, podcastTitle string
		if err := rows.Scan(&episode.ID, &episode.Title, &episode.State, &published, &episode.SizeBytes, &podcastID, &podcastTitle); err != nil {
			return nil, err
		}
		if published.Valid {
			if parsed, err := time.Parse(time.RFC3339Nano, published.String); err == nil {
				episode.PublishedAt = parsed
				episode.HasPublish = true
			} else if parsed, err := time.Parse(time.RFC3339, published.String); err == nil {
				episode.PublishedAt = parsed
				episode.HasPublish = true
			}
		}
		results = append(results, domain.EpisodeResult{
			Episode:      episode,
			PodcastID:    podcastID,
			PodcastTitle: podcastTitle,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Store) SearchEpisodes(ctx context.Context, query string) ([]domain.EpisodeResult, error) {
	like := "%" + strings.ToLower(query) + "%"

	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.title, e.state, e.published_at, e.size_bytes, p.id, p.title
FROM episodes e
JOIN podcasts p ON p.id = e.podcast_id
WHERE LOWER(e.title) LIKE ? OR LOWER(p.title) LIKE ?
ORDER BY
    CASE WHEN e.published_at IS NULL OR e.published_at = '' THEN 1 ELSE 0 END,
    e.published_at DESC,
    LOWER(p.title),
    LOWER(e.title)`, like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]domain.EpisodeResult, 0, 32)
	for rows.Next() {
		var episode domain.EpisodeRow
		var published sql.NullString
		var podcastID, podcastTitle string
		if err := rows.Scan(&episode.ID, &episode.Title, &episode.State, &published, &episode.SizeBytes, &podcastID, &podcastTitle); err != nil {
			return nil, err
		}
		if published.Valid {
			if parsed, err := time.Parse(time.RFC3339Nano, published.String); err == nil {
				episode.PublishedAt = parsed
				episode.HasPublish = true
			} else if parsed, err := time.Parse(time.RFC3339, published.String); err == nil {
				episode.PublishedAt = parsed
				episode.HasPublish = true
			}
		}
		results = append(results, domain.EpisodeResult{
			Episode:      episode,
			PodcastID:    podcastID,
			PodcastTitle: podcastTitle,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Store) ListQueuedEpisodes(ctx context.Context) ([]domain.QueuedEpisodeResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.title, e.state, e.published_at, e.size_bytes, e.retry_count, p.id, p.title, d.enqueued_at
FROM episodes e
JOIN podcasts p ON p.id = e.podcast_id
JOIN downloads d ON d.episode_id = e.id
WHERE e.state = ?
ORDER BY d.priority DESC, d.enqueued_at`, domain.EpisodeStateQueued)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]domain.QueuedEpisodeResult, 0, 32)
	for rows.Next() {
		var episode domain.EpisodeRow
		var published sql.NullString
		var podcastID, podcastTitle string
		var retryCount int
		var enqueuedAt string
		if err := rows.Scan(&episode.ID, &episode.Title, &episode.State, &published, &episode.SizeBytes, &retryCount, &podcastID, &podcastTitle, &enqueuedAt); err != nil {
			return nil, err
		}
		if published.Valid {
			if parsed, err := time.Parse(time.RFC3339Nano, published.String); err == nil {
				episode.PublishedAt = parsed
				episode.HasPublish = true
			} else if parsed, err := time.Parse(time.RFC3339, published.String); err == nil {
				episode.PublishedAt = parsed
				episode.HasPublish = true
			}
		}
		var parsedEnqueuedAt time.Time
		if parsed, err := time.Parse(time.RFC3339Nano, enqueuedAt); err == nil {
			parsedEnqueuedAt = parsed
		} else if parsed, err := time.Parse(time.RFC3339, enqueuedAt); err == nil {
			parsedEnqueuedAt = parsed
		}
		results = append(results, domain.QueuedEpisodeResult{
			Episode:      episode,
			PodcastID:    podcastID,
			PodcastTitle: podcastTitle,
			RetryCount:   retryCount,
			EnqueuedAt:   parsedEnqueuedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// ListDownloadedEpisodes returns all episodes that have been downloaded (DOWNLOADED or DELETED state).
func (s *Store) ListDownloadedEpisodes(ctx context.Context) ([]domain.EpisodeResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.title, e.state, e.published_at, e.size_bytes, p.id, p.title
FROM episodes e
JOIN podcasts p ON p.id = e.podcast_id
WHERE e.state IN (?, ?)
ORDER BY
    CASE WHEN e.published_at IS NULL OR e.published_at = '' THEN 1 ELSE 0 END,
    e.published_at DESC,
    LOWER(p.title),
    LOWER(e.title)`, domain.EpisodeStateDownloaded, domain.EpisodeStateDeleted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]domain.EpisodeResult, 0, 128)
	for rows.Next() {
		var episode domain.EpisodeRow
		var published sql.NullString
		var podcastID, podcastTitle string
		if err := rows.Scan(&episode.ID, &episode.Title, &episode.State, &published, &episode.SizeBytes, &podcastID, &podcastTitle); err != nil {
			return nil, err
		}
		if published.Valid {
			if parsed, err := time.Parse(time.RFC3339Nano, published.String); err == nil {
				episode.PublishedAt = parsed
				episode.HasPublish = true
			} else if parsed, err := time.Parse(time.RFC3339, published.String); err == nil {
				episode.PublishedAt = parsed
				episode.HasPublish = true
			}
		}
		results = append(results, domain.EpisodeResult{
			Episode:      episode,
			PodcastID:    podcastID,
			PodcastTitle: podcastTitle,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// CountQueuedEpisodes returns the count of episodes in QUEUED state.
func (s *Store) CountQueuedEpisodes(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM episodes WHERE state = ?`, domain.EpisodeStateQueued).Scan(&count)
	return count, err
}

// CountDownloadedEpisodes returns the count of episodes in DOWNLOADED or DELETED state.
func (s *Store) CountDownloadedEpisodes(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM episodes WHERE state IN (?, ?)`, domain.EpisodeStateDownloaded, domain.EpisodeStateDeleted).Scan(&count)
	return count, err
}

// FindDanglingFiles scans the download directory and returns files that are not tracked in the database.
func (s *Store) FindDanglingFiles(ctx context.Context, downloadRoot string) ([]domain.DanglingFile, error) {
	if downloadRoot == "" {
		return nil, nil
	}

	// Get all file paths from the database
	rows, err := s.db.QueryContext(ctx, `SELECT file_path FROM episodes WHERE file_path IS NOT NULL AND file_path != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	knownFiles := make(map[string]bool)
	for rows.Next() {
		var filePath string
		if err := rows.Scan(&filePath); err != nil {
			return nil, err
		}
		knownFiles[filePath] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Scan download directory for all files
	var danglingFiles []domain.DanglingFile
	err = filepath.Walk(downloadRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}
		if info.IsDir() {
			return nil // Skip directories
		}
		// Check if this file is in the database
		if !knownFiles[path] {
			danglingFiles = append(danglingFiles, domain.DanglingFile{
				Path:      path,
				SizeBytes: info.Size(),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return danglingFiles, nil
}

func (s *Store) MarkAllEpisodesSeen(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "UPDATE episodes SET state = ? WHERE state = ?", domain.EpisodeStateSeen, domain.EpisodeStateNew)
	return err
}

func (s *Store) GetEpisodeInfo(ctx context.Context, episodeID string) (domain.EpisodeInfo, error) {
	var info domain.EpisodeInfo
	var published sql.NullString
	var filePath sql.NullString
	var hash sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT e.id, e.title, COALESCE(e.description, ''), e.state, e.published_at, e.file_path, e.enclosure_url, e.hash, e.size_bytes, p.id, p.title
FROM episodes e
JOIN podcasts p ON p.id = e.podcast_id
WHERE e.id = ?`, episodeID).
		Scan(&info.ID, &info.Title, &info.Description, &info.State, &published, &filePath, &info.EnclosureURL, &hash, &info.SizeBytes, &info.PodcastID, &info.PodcastTitle)
	if err != nil {
		return domain.EpisodeInfo{}, err
	}
	if published.Valid {
		if parsed, err := time.Parse(time.RFC3339Nano, published.String); err == nil {
			info.PublishedAt = parsed
			info.HasPublish = true
		} else if parsed, err := time.Parse(time.RFC3339, published.String); err == nil {
			info.PublishedAt = parsed
			info.HasPublish = true
		}
	}
	if filePath.Valid {
		info.FilePath = filePath.String
	}
	if hash.Valid {
		info.Hash = hash.String
	}
	return info, nil
}

func (s *Store) UpdateEpisodeState(ctx context.Context, episodeID, state string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE episodes SET state = ? WHERE id = ?", state, episodeID)
	return err
}

// CheckAndUpdateDeletedFiles checks all downloaded episodes and marks those with
// missing files as DELETED.
func (s *Store) CheckAndUpdateDeletedFiles(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, file_path FROM episodes WHERE state = ? AND file_path IS NOT NULL AND file_path != ''`, domain.EpisodeStateDownloaded)
	if err != nil {
		return err
	}
	defer rows.Close()

	var episodesToUpdate []string
	for rows.Next() {
		var id, filePath string
		if err := rows.Scan(&id, &filePath); err != nil {
			return err
		}
		// Check if file exists
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			episodesToUpdate = append(episodesToUpdate, id)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Update all episodes with missing files
	for _, id := range episodesToUpdate {
		if err := s.UpdateEpisodeState(ctx, id, domain.EpisodeStateDeleted); err != nil {
			return err
		}
	}

	return nil
}

// CorrectQueuedStates checks all queued episodes and updates their state to DOWNLOADED
// if the file already exists on the filesystem. This fixes episodes stuck in QUEUED state.
func (s *Store) CorrectQueuedStates(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, file_path FROM episodes WHERE state = ? AND file_path IS NOT NULL AND file_path != ''`, domain.EpisodeStateQueued)
	if err != nil {
		return err
	}
	defer rows.Close()

	var episodesToUpdate []string
	for rows.Next() {
		var id, filePath string
		if err := rows.Scan(&id, &filePath); err != nil {
			return err
		}
		// Check if file exists
		if _, err := os.Stat(filePath); err == nil {
			// File exists, should be marked as DOWNLOADED
			episodesToUpdate = append(episodesToUpdate, id)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Update all episodes with existing files
	for _, id := range episodesToUpdate {
		if err := s.UpdateEpisodeState(ctx, id, domain.EpisodeStateDownloaded); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) RemoveFromQueue(ctx context.Context, episodeID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM downloads WHERE episode_id = ?", episodeID)
	return err
}

func (s *Store) RequeueEpisode(ctx context.Context, episodeID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO downloads (episode_id, enqueued_at, priority)
VALUES (?, ?, 0)
ON CONFLICT(episode_id) DO UPDATE SET enqueued_at = excluded.enqueued_at`, episodeID, time.Now().UTC())
	return err
}

func (s *Store) EnqueueEpisode(ctx context.Context, episodeID string) error {
	return s.withRetry(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			if !committed {
				tx.Rollback()
			}
		}()

		if _, err := tx.ExecContext(ctx, "UPDATE episodes SET state = ?, retry_count = 0 WHERE id = ?", domain.EpisodeStateQueued, episodeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO downloads (episode_id, enqueued_at, priority)
VALUES (?, ?, 0)
ON CONFLICT(episode_id) DO UPDATE SET enqueued_at = excluded.enqueued_at`, episodeID, time.Now().UTC()); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	})
}

func (s *Store) PersistDownloadResult(ctx context.Context, episodeID, finalPath, hash string) error {
	return s.withRetry(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			if !committed {
				tx.Rollback()
			}
		}()

		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, "UPDATE episodes SET state = ?, downloaded_at = ?, file_path = ?, hash = ?, retry_count = 0 WHERE id = ?", domain.EpisodeStateDownloaded, now, finalPath, hash, episodeID); err != nil {
			return err
		}
		// Remove episode from downloads table since it's now successfully downloaded
		if _, err := tx.ExecContext(ctx, "DELETE FROM downloads WHERE episode_id = ?", episodeID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	})
}

func (s *Store) IncrementRetryCount(ctx context.Context, episodeID string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE episodes SET retry_count = retry_count + 1 WHERE id = ?", episodeID)
	return err
}

func (s *Store) ClaimNextDownload(ctx context.Context) (string, error) {
	var episodeID string
	err := s.withRetry(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			if !committed {
				tx.Rollback()
			}
		}()

		episodeID = ""
		now := time.Now().UTC().Format(time.RFC3339Nano)
		err = tx.QueryRowContext(ctx, `SELECT episode_id FROM downloads WHERE claimed_at IS NULL ORDER BY priority DESC, enqueued_at LIMIT 1`).Scan(&episodeID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNoDownloadTask
			}
			return err
		}

		res, err := tx.ExecContext(ctx, "UPDATE downloads SET claimed_at = ? WHERE episode_id = ? AND claimed_at IS NULL", now, episodeID)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrNoDownloadTask
		}

		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	})
	if err != nil {
		return "", err
	}
	return episodeID, nil
}

func (s *Store) HasSubscriptionByFeedURL(ctx context.Context, feedURL string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM podcasts WHERE feed_url = ?", feedURL).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) ListPodcastExports(ctx context.Context) ([]domain.PodcastExport, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT title, feed_url FROM podcasts ORDER BY LOWER(title)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	exports := make([]domain.PodcastExport, 0, 16)
	for rows.Next() {
		var export domain.PodcastExport
		if err := rows.Scan(&export.Title, &export.FeedURL); err != nil {
			return nil, err
		}
		exports = append(exports, export)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return exports, nil
}

var ErrNoDownloadTask = errors.New("no download task available")

func (s *Store) withRetry(ctx context.Context, fn func() error) error {
	const attempts = 5
	var err error
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = fn()
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrNoDownloadTask) {
			return err
		}
		if !isSQLiteBusy(err) {
			return err
		}
		backoff := 50 * time.Millisecond * time.Duration(1<<i)
		if err := waitWithContext(ctx, backoff); err != nil {
			return err
		}
	}
	return err
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

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}
