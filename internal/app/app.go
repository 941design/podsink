package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kballard/go-shellquote"
	"gopkg.in/yaml.v3"

	"podsink/internal/config"
	"podsink/internal/feeds"
	"podsink/internal/itunes"
)

type commandHandler func(context.Context, []string) (CommandResult, error)

type command struct {
	usage   string
	summary string
	handler commandHandler
}

const (
	stateNew        = "NEW"
	stateSeen       = "SEEN"
	stateIgnored    = "IGNORED"
	stateQueued     = "QUEUED"
	stateDownloaded = "DOWNLOADED"
)

var invalidPathChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type subscriptionSummary struct {
	ID            string
	Title         string
	NewCount      int
	UnplayedCount int
	TotalCount    int
}

type episodeRow struct {
	ID          string
	Title       string
	State       string
	PublishedAt time.Time
	HasPublish  bool
}

type episodeInfo struct {
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
}

// App encapsulates the long-lived dependencies shared across the REPL.
type App struct {
	config     config.Config
	configPath string
	db         *sql.DB
	httpClient *http.Client
	itunes     *itunes.Client

	commands    map[string]*command
	helpList    []*command
	sleep       sleepFunc
	downloadMgr *downloadManager
}

// Dependencies captures optional external dependencies for App construction.
type Dependencies struct {
	HTTPClient *http.Client
	ITunes     *itunes.Client
	Sleep      sleepFunc
}

// CommandResult captures the outcome of executing a command.
type CommandResult struct {
	Message string
	Quit    bool
}

// New constructs a new App instance.
func New(cfg config.Config, configPath string, db *sql.DB) *App {
	return NewWithDependencies(cfg, configPath, db, Dependencies{})
}

// NewWithDependencies constructs an App with optional dependency overrides.
func NewWithDependencies(cfg config.Config, configPath string, db *sql.DB, deps Dependencies) *App {
	httpClient := deps.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	itunesClient := deps.ITunes
	if itunesClient == nil {
		itunesClient = itunes.NewClient(httpClient, "")
	}
	sleeper := deps.Sleep
	if sleeper == nil {
		sleeper = defaultSleep
	}

	application := &App{
		config:     cfg,
		configPath: configPath,
		db:         db,
		httpClient: httpClient,
		itunes:     itunesClient,
		commands:   make(map[string]*command),
		helpList:   make([]*command, 0, 16),
		sleep:      sleeper,
	}
	application.registerCommands()
	workers := cfg.ParallelDownloads
	if workers < 0 {
		workers = 0
	}
	if workers > 0 {
		application.downloadMgr = newDownloadManager(application, workers)
		application.downloadMgr.Notify()
	}
	return application
}

// Config returns a copy of the active configuration.
func (a *App) Config() config.Config {
	return a.config
}

// Close releases background resources associated with the App.
func (a *App) Close() error {
	if a.downloadMgr != nil {
		a.downloadMgr.Stop()
	}
	return nil
}

// Execute processes a single REPL command string.
func (a *App) Execute(ctx context.Context, input string) (CommandResult, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return CommandResult{}, nil
	}

	parts, err := shellquote.Split(trimmed)
	if err != nil {
		return CommandResult{}, fmt.Errorf("parse command: %w", err)
	}
	if len(parts) == 0 {
		return CommandResult{}, nil
	}

	key := strings.ToLower(parts[0])
	cmd, ok := a.commands[key]
	if !ok {
		return CommandResult{Message: fmt.Sprintf("unknown command: %s", parts[0])}, nil
	}

	return cmd.handler(ctx, parts[1:])
}

func (a *App) editConfig(ctx context.Context) (CommandResult, error) {
	updated, err := config.EditInteractive(ctx, a.config)
	if err != nil {
		return CommandResult{}, err
	}
	if err := config.Save(a.configPath, updated); err != nil {
		return CommandResult{}, err
	}
	a.config = updated
	log.Println("configuration updated")
	return CommandResult{Message: "Configuration saved."}, nil
}

func (a *App) searchCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) == 0 {
		return CommandResult{Message: "Usage: search <query>"}, nil
	}

	term := strings.Join(args, " ")
	results, err := a.itunes.Search(ctx, term, 10)
	if err != nil {
		return CommandResult{}, err
	}

	if len(results) == 0 {
		return CommandResult{Message: "No podcasts found."}, nil
	}

	var builder strings.Builder
	builder.WriteString("Search results:\n")
	for _, r := range results {
		author := strings.TrimSpace(r.Author)
		if author == "" {
			author = "Unknown"
		}
		builder.WriteString(fmt.Sprintf("  %s  %s (by %s)\n", r.ID, r.Title, author))
	}

	return CommandResult{Message: strings.TrimRight(builder.String(), "\n")}, nil
}

func (a *App) subscribeCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) != 1 {
		return CommandResult{Message: "Usage: subscribe <podcast_id>"}, nil
	}
	podcastID := strings.TrimSpace(args[0])
	if podcastID == "" {
		return CommandResult{Message: "Podcast ID cannot be empty."}, nil
	}

	if exists, title, err := a.subscriptionExists(ctx, podcastID); err != nil {
		return CommandResult{}, err
	} else if exists {
		if title == "" {
			title = podcastID
		}
		return CommandResult{Message: fmt.Sprintf("Already subscribed to %s.", title)}, nil
	}

	meta, err := a.itunes.LookupPodcast(ctx, podcastID)
	if err != nil {
		return CommandResult{}, err
	}
	if strings.TrimSpace(meta.FeedURL) == "" {
		return CommandResult{}, fmt.Errorf("podcast feed URL missing")
	}

	feedInfo, episodes, err := feeds.Fetch(ctx, a.httpClient, meta.FeedURL)
	if err != nil {
		return CommandResult{}, err
	}
	title := feedInfo.Title
	if strings.TrimSpace(title) == "" {
		title = meta.Title
	}

	added, err := a.storeSubscription(ctx, meta, feedInfo, episodes)
	if err != nil {
		return CommandResult{}, err
	}

	return CommandResult{Message: fmt.Sprintf("Subscribed to %s (%d new episodes).", title, added)}, nil
}

func (a *App) unsubscribeCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) != 1 {
		return CommandResult{Message: "Usage: unsubscribe <podcast_id>"}, nil
	}
	podcastID := strings.TrimSpace(args[0])
	if podcastID == "" {
		return CommandResult{Message: "Podcast ID cannot be empty."}, nil
	}

	res, err := a.db.ExecContext(ctx, "DELETE FROM podcasts WHERE id = ?", podcastID)
	if err != nil {
		return CommandResult{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return CommandResult{}, err
	}
	if affected == 0 {
		return CommandResult{Message: "No subscription found for that podcast."}, nil
	}
	return CommandResult{Message: "Subscription removed."}, nil
}

func (a *App) registerCommands() {
	a.registerCommand("help", "help [command]", "Show information about available commands", a.helpCommand, "?")
	a.registerCommand("config", "config [show]", "View or edit application configuration", a.configCommand)
	a.registerCommand("exit", "exit", "Exit the application", a.exitCommand, "quit")
	a.registerCommand("search", "search <query>", "Search for podcasts via the iTunes API", a.searchCommand)
	a.registerCommand("subscribe", "subscribe <podcast_id>", "Subscribe to a podcast", a.subscribeCommand)
	a.registerCommand("unsubscribe", "unsubscribe <podcast_id>", "Unsubscribe from a podcast", a.unsubscribeCommand)
	a.registerCommand("list", "list subscriptions", "List all podcast subscriptions", a.listCommand, "ls")
	a.registerCommand("episodes", "episodes <podcast_id>", "View episodes for a podcast", a.episodesCommand)
	a.registerCommand("queue", "queue <episode_id>", "Queue an episode for download", a.queueCommand)
	a.registerCommand("download", "download <episode_id>", "Download an episode immediately", a.downloadCommand)
	a.registerCommand("ignore", "ignore <episode_id>", "Toggle the ignored state for an episode", a.ignoreCommand)
}

func (a *App) registerCommand(name, usage, summary string, handler commandHandler, aliases ...string) {
	key := strings.ToLower(name)
	cmd := &command{usage: usage, summary: summary, handler: handler}
	a.commands[key] = cmd
	a.helpList = append(a.helpList, cmd)
	for _, alias := range aliases {
		a.commands[strings.ToLower(alias)] = cmd
	}
}

func (a *App) helpCommand(_ context.Context, args []string) (CommandResult, error) {
	if len(args) > 0 {
		key := strings.ToLower(args[0])
		if cmd, ok := a.commands[key]; ok {
			return CommandResult{Message: fmt.Sprintf("%s\n  %s", cmd.usage, cmd.summary)}, nil
		}
		return CommandResult{Message: fmt.Sprintf("unknown command: %s", args[0])}, nil
	}

	var builder strings.Builder
	builder.WriteString("Available commands:\n")

	maxUsage := 0
	seen := make(map[*command]struct{})
	for _, cmd := range a.helpList {
		if _, ok := seen[cmd]; ok {
			continue
		}
		seen[cmd] = struct{}{}
		if len(cmd.usage) > maxUsage {
			maxUsage = len(cmd.usage)
		}
	}

	seen = make(map[*command]struct{})
	for _, cmd := range a.helpList {
		if _, ok := seen[cmd]; ok {
			continue
		}
		seen[cmd] = struct{}{}
		builder.WriteString("  ")
		builder.WriteString(fmt.Sprintf("%-*s  %s\n", maxUsage, cmd.usage, cmd.summary))
	}

	return CommandResult{Message: strings.TrimRight(builder.String(), "\n")}, nil
}

func (a *App) configCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) > 0 {
		if strings.EqualFold(args[0], "show") {
			rendered, err := yaml.Marshal(a.config)
			if err != nil {
				return CommandResult{}, fmt.Errorf("render config: %w", err)
			}
			return CommandResult{Message: fmt.Sprintf("Current configuration:\n%s", strings.TrimSpace(string(rendered)))}, nil
		}
		return CommandResult{Message: "Usage: config [show]"}, nil
	}
	return a.editConfig(ctx)
}

func (a *App) exitCommand(_ context.Context, _ []string) (CommandResult, error) {
	return CommandResult{Quit: true}, nil
}

func (a *App) listCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) == 0 {
		return CommandResult{Message: "Usage: list subscriptions"}, nil
	}

	switch strings.ToLower(args[0]) {
	case "subscriptions":
		summaries, err := a.fetchSubscriptionSummaries(ctx)
		if err != nil {
			return CommandResult{}, err
		}
		if len(summaries) == 0 {
			return CommandResult{Message: "No subscriptions yet."}, nil
		}

		maxID := len("ID")
		maxTitle := len("Title")
		for _, s := range summaries {
			if len(s.ID) > maxID {
				maxID = len(s.ID)
			}
			if len(s.Title) > maxTitle {
				maxTitle = len(s.Title)
			}
		}

		var builder strings.Builder
		builder.WriteString("Subscriptions:\n")
		builder.WriteString(fmt.Sprintf("  %-*s  %-*s  %5s  %8s  %5s\n", maxID, "ID", maxTitle, "Title", "New", "Unplayed", "Total"))
		builder.WriteString("  " + strings.Repeat("-", maxID) + "  " + strings.Repeat("-", maxTitle) + "  -----  --------  -----\n")
		for _, s := range summaries {
			builder.WriteString(fmt.Sprintf("  %-*s  %-*s  %5d  %8d  %5d\n", maxID, s.ID, maxTitle, s.Title, s.NewCount, s.UnplayedCount, s.TotalCount))
		}

		return CommandResult{Message: strings.TrimRight(builder.String(), "\n")}, nil
	default:
		return CommandResult{Message: fmt.Sprintf("unknown list target: %s", args[0])}, nil
	}
}

func (a *App) episodesCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) != 1 {
		return CommandResult{Message: "Usage: episodes <podcast_id>"}, nil
	}
	podcastID := strings.TrimSpace(args[0])
	if podcastID == "" {
		return CommandResult{Message: "Podcast ID cannot be empty."}, nil
	}

	title, err := a.podcastTitle(ctx, podcastID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CommandResult{Message: "No subscription found for that podcast."}, nil
		}
		return CommandResult{}, err
	}

	episodes, err := a.fetchEpisodes(ctx, podcastID)
	if err != nil {
		return CommandResult{}, err
	}
	if len(episodes) == 0 {
		return CommandResult{Message: fmt.Sprintf("No episodes recorded for %s.", title)}, nil
	}

	maxID := len("ID")
	maxState := len("STATE")
	for _, ep := range episodes {
		if len(ep.ID) > maxID {
			maxID = len(ep.ID)
		}
		if len(ep.State) > maxState {
			maxState = len(ep.State)
		}
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Episodes for %s (%s):\n", title, podcastID))
	builder.WriteString(fmt.Sprintf("  %-*s  %-*s  %-10s  %s\n", maxID, "ID", maxState, "STATE", "PUBLISHED", "TITLE"))
	builder.WriteString("  " + strings.Repeat("-", maxID) + "  " + strings.Repeat("-", maxState) + "  ----------  " + strings.Repeat("-", 20) + "\n")
	for _, ep := range episodes {
		published := "Unknown"
		if ep.HasPublish {
			published = ep.PublishedAt.Format("2006-01-02")
		}
		builder.WriteString(fmt.Sprintf("  %-*s  %-*s  %-10s  %s\n", maxID, ep.ID, maxState, ep.State, published, ep.Title))
	}

	if err := a.markEpisodesSeen(ctx, podcastID); err != nil {
		return CommandResult{}, err
	}

	return CommandResult{Message: strings.TrimRight(builder.String(), "\n")}, nil
}

func (a *App) queueCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) != 1 {
		return CommandResult{Message: "Usage: queue <episode_id>"}, nil
	}
	episodeID := strings.TrimSpace(args[0])
	if episodeID == "" {
		return CommandResult{Message: "Episode ID cannot be empty."}, nil
	}

	info, err := a.fetchEpisodeInfo(ctx, episodeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CommandResult{Message: "Episode not found."}, nil
		}
		return CommandResult{}, err
	}

	switch info.State {
	case stateIgnored:
		return CommandResult{Message: "Episode is ignored. Unignore before queueing."}, nil
	case stateQueued:
		return CommandResult{Message: "Episode is already queued."}, nil
	}

	if err := a.enqueueEpisode(ctx, info.ID); err != nil {
		return CommandResult{}, err
	}

	return CommandResult{Message: fmt.Sprintf("Episode %s queued for download.", info.ID)}, nil
}

func (a *App) downloadCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) != 1 {
		return CommandResult{Message: "Usage: download <episode_id>"}, nil
	}
	episodeID := strings.TrimSpace(args[0])
	if episodeID == "" {
		return CommandResult{Message: "Episode ID cannot be empty."}, nil
	}

	info, err := a.fetchEpisodeInfo(ctx, episodeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CommandResult{Message: "Episode not found."}, nil
		}
		return CommandResult{}, err
	}
	if strings.TrimSpace(info.EnclosureURL) == "" {
		return CommandResult{}, fmt.Errorf("episode is missing an enclosure URL")
	}
	if info.State == stateIgnored {
		return CommandResult{Message: "Episode is ignored. Unignore before downloading."}, nil
	}

	finalPath, err := a.downloadEpisode(ctx, info)
	if err != nil {
		return CommandResult{}, err
	}

	return CommandResult{Message: fmt.Sprintf("Downloaded %s to %s.", info.Title, finalPath)}, nil
}

func (a *App) ignoreCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) != 1 {
		return CommandResult{Message: "Usage: ignore <episode_id>"}, nil
	}
	episodeID := strings.TrimSpace(args[0])
	if episodeID == "" {
		return CommandResult{Message: "Episode ID cannot be empty."}, nil
	}

	info, err := a.fetchEpisodeInfo(ctx, episodeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CommandResult{Message: "Episode not found."}, nil
		}
		return CommandResult{}, err
	}

	switch info.State {
	case stateIgnored:
		if err := a.updateEpisodeState(ctx, info.ID, stateSeen); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{Message: fmt.Sprintf("Episode %s unignored.", info.ID)}, nil
	default:
		if err := a.removeFromQueue(ctx, info.ID); err != nil {
			return CommandResult{}, err
		}
		if err := a.updateEpisodeState(ctx, info.ID, stateIgnored); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{Message: fmt.Sprintf("Episode %s ignored.", info.ID)}, nil
	}
}

func (a *App) subscriptionExists(ctx context.Context, podcastID string) (bool, string, error) {
	var title string
	err := a.db.QueryRowContext(ctx, "SELECT title FROM podcasts WHERE id = ?", podcastID).Scan(&title)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, "", nil
		}
		return false, "", err
	}
	return true, title, nil
}

func (a *App) storeSubscription(ctx context.Context, meta itunes.Podcast, feedMeta feeds.Podcast, episodes []feeds.Episode) (int, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	title := strings.TrimSpace(feedMeta.Title)
	if title == "" {
		title = strings.TrimSpace(meta.Title)
	}
	if title == "" {
		title = "Untitled Podcast"
	}

	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `INSERT INTO podcasts (id, title, feed_url, subscribed_at)
                VALUES (?, ?, ?, ?)
                ON CONFLICT(id) DO UPDATE SET title=excluded.title, feed_url=excluded.feed_url, subscribed_at=excluded.subscribed_at`,
		meta.ID, title, meta.FeedURL, now); err != nil {
		return 0, err
	}

	added := 0
	for _, ep := range episodes {
		if strings.TrimSpace(ep.Enclosure) == "" {
			continue
		}
		episodeID := strings.TrimSpace(ep.ID)
		if episodeID == "" {
			episodeID = fmt.Sprintf("%s-%s", meta.ID, ep.Title)
		}
		if episodeID == "" {
			continue
		}

		title := strings.TrimSpace(ep.Title)
		if title == "" {
			title = "Untitled Episode"
		}
		description := strings.TrimSpace(ep.Description)
		var published interface{}
		if !ep.PublishedAt.IsZero() {
			published = ep.PublishedAt.UTC().Format(time.RFC3339Nano)
		}

		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO episodes
                        (id, podcast_id, title, description, state, published_at, enclosure_url)
                        VALUES (?, ?, ?, ?, ?, ?, ?)`,
			episodeID, meta.ID, title, description, stateNew, published, ep.Enclosure)
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
                        published_at = COALESCE(?, published_at)
                        WHERE id = ?`,
			meta.ID, title, description, ep.Enclosure, published, episodeID); err != nil {
			return 0, err
		}
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return added, nil
}

func (a *App) fetchSubscriptionSummaries(ctx context.Context) ([]subscriptionSummary, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT
            p.id,
            p.title,
            COALESCE(SUM(CASE WHEN e.state = ? THEN 1 ELSE 0 END), 0) AS new_count,
            COALESCE(SUM(CASE WHEN e.state != ? AND e.id IS NOT NULL THEN 1 ELSE 0 END), 0) AS unplayed_count,
            COUNT(e.id) AS total_count
        FROM podcasts p
        LEFT JOIN episodes e ON e.podcast_id = p.id
        GROUP BY p.id, p.title
        ORDER BY LOWER(p.title)`, stateNew, stateDownloaded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := make([]subscriptionSummary, 0, 8)
	for rows.Next() {
		var summary subscriptionSummary
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

func (a *App) podcastTitle(ctx context.Context, podcastID string) (string, error) {
	var title string
	if err := a.db.QueryRowContext(ctx, "SELECT title FROM podcasts WHERE id = ?", podcastID).Scan(&title); err != nil {
		return "", err
	}
	return title, nil
}

func (a *App) fetchEpisodes(ctx context.Context, podcastID string) ([]episodeRow, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id, title, state, published_at FROM episodes WHERE podcast_id = ?`, podcastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	episodes := make([]episodeRow, 0, 64)
	for rows.Next() {
		var row episodeRow
		var published sql.NullString
		if err := rows.Scan(&row.ID, &row.Title, &row.State, &published); err != nil {
			return nil, err
		}
		if published.Valid {
			if parsed, err := time.Parse(time.RFC3339Nano, published.String); err == nil {
				row.PublishedAt = parsed
				row.HasPublish = true
			} else if parsed, err := time.Parse(time.RFC3339, published.String); err == nil {
				row.PublishedAt = parsed
				row.HasPublish = true
			}
		}
		episodes = append(episodes, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(episodes, func(i, j int) bool {
		a := episodes[i]
		b := episodes[j]
		if a.HasPublish && b.HasPublish {
			if !a.PublishedAt.Equal(b.PublishedAt) {
				return a.PublishedAt.After(b.PublishedAt)
			}
			return strings.ToLower(a.Title) < strings.ToLower(b.Title)
		}
		if a.HasPublish {
			return true
		}
		if b.HasPublish {
			return false
		}
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	})

	return episodes, nil
}

func (a *App) markEpisodesSeen(ctx context.Context, podcastID string) error {
	_, err := a.db.ExecContext(ctx, "UPDATE episodes SET state = ? WHERE podcast_id = ? AND state = ?", stateSeen, podcastID, stateNew)
	return err
}

func (a *App) fetchEpisodeInfo(ctx context.Context, episodeID string) (episodeInfo, error) {
	var info episodeInfo
	var published sql.NullString
	var filePath sql.NullString
	err := a.db.QueryRowContext(ctx, `SELECT e.id, e.title, COALESCE(e.description, ''), e.state, e.published_at, e.file_path, e.enclosure_url, p.id, p.title
        FROM episodes e
        JOIN podcasts p ON p.id = e.podcast_id
        WHERE e.id = ?`, episodeID).Scan(&info.ID, &info.Title, &info.Description, &info.State, &published, &filePath, &info.EnclosureURL, &info.PodcastID, &info.PodcastTitle)
	if err != nil {
		return episodeInfo{}, err
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
	return info, nil
}

func (a *App) enqueueEpisode(ctx context.Context, episodeID string) error {
	err := withRetry(ctx, func() error {
		tx, err := a.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			if !committed {
				tx.Rollback()
			}
		}()

		if _, err := tx.ExecContext(ctx, "UPDATE episodes SET state = ?, retry_count = 0 WHERE id = ?", stateQueued, episodeID); err != nil {
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
	if err != nil {
		return err
	}
	if a.downloadMgr != nil {
		a.downloadMgr.Notify()
	}
	return nil
}

func (a *App) downloadEpisode(ctx context.Context, info episodeInfo) (string, error) {
	finalPath, err := a.episodeFilePath(info)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(a.config.TmpDir, 0o755); err != nil {
		return "", err
	}

	attempts := a.config.RetryCount + 1
	if attempts <= 0 {
		attempts = 1
	}

	partialPath := a.episodePartialPath(info)
	var attemptErr error
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		resultPath, err := a.downloadOnce(ctx, info, finalPath, partialPath)
		if err == nil {
			return resultPath, nil
		}

		attemptErr = err
		if err := a.incrementRetryCount(ctx, info.ID); err != nil {
			return "", err
		}

		if i == attempts-1 {
			break
		}

		backoff := time.Second << i
		maxBackoff := time.Duration(a.config.RetryBackoffMaxSec) * time.Second
		if maxBackoff > 0 && backoff > maxBackoff {
			backoff = maxBackoff
		}
		if backoff > 0 {
			if err := a.sleep(ctx, backoff); err != nil {
				return "", err
			}
		}
	}

	return "", attemptErr
}

func (a *App) updateEpisodeState(ctx context.Context, episodeID, state string) error {
	_, err := a.db.ExecContext(ctx, "UPDATE episodes SET state = ? WHERE id = ?", state, episodeID)
	return err
}

func (a *App) removeFromQueue(ctx context.Context, episodeID string) error {
	_, err := a.db.ExecContext(ctx, "DELETE FROM downloads WHERE episode_id = ?", episodeID)
	return err
}

func (a *App) requeueEpisode(ctx context.Context, episodeID string) error {
	err := withRetry(ctx, func() error {
		_, err := a.db.ExecContext(ctx, `INSERT INTO downloads (episode_id, enqueued_at, priority)
                VALUES (?, ?, 0)
                ON CONFLICT(episode_id) DO UPDATE SET enqueued_at = excluded.enqueued_at`, episodeID, time.Now().UTC())
		return err
	})
	if err == nil && a.downloadMgr != nil {
		a.downloadMgr.Notify()
	}
	return err
}

func (a *App) persistDownloadResult(ctx context.Context, episodeID, finalPath string) error {
	return withRetry(ctx, func() error {
		tx, err := a.db.BeginTx(ctx, nil)
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
		if _, err := tx.ExecContext(ctx, "UPDATE episodes SET state = ?, downloaded_at = ?, file_path = ?, retry_count = 0 WHERE id = ?", stateDownloaded, now, finalPath, episodeID); err != nil {
			return err
		}
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

func (a *App) episodeFilePath(info episodeInfo) (string, error) {
	root := strings.TrimSpace(a.config.DownloadRoot)
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

func (a *App) episodePartialPath(info episodeInfo) string {
	name := safeFilename(info.ID)
	if name == "" {
		name = safeFilename(info.Title)
	}
	if name == "" {
		name = "episode"
	}
	return filepath.Join(a.config.TmpDir, fmt.Sprintf("podsink-%s.partial", name))
}

func (a *App) downloadOnce(ctx context.Context, info episodeInfo, finalPath, partialPath string) (string, error) {
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
	if ua := strings.TrimSpace(a.config.UserAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if existingSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
	}

	resp, err := a.httpClient.Do(req)
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

	if err := moveFile(partialPath, finalPath); err != nil {
		return "", err
	}

	if err := a.persistDownloadResult(ctx, info.ID, finalPath); err != nil {
		return "", err
	}

	return finalPath, nil
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

func (a *App) incrementRetryCount(ctx context.Context, episodeID string) error {
	_, err := a.db.ExecContext(ctx, "UPDATE episodes SET retry_count = retry_count + 1 WHERE id = ?", episodeID)
	return err
}

var errNoDownloadTask = errors.New("no download task available")

type downloadManager struct {
	app    *App
	wakeCh chan struct{}
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newDownloadManager(app *App, workers int) *downloadManager {
	ctx, cancel := context.WithCancel(context.Background())
	manager := &downloadManager{
		app:    app,
		wakeCh: make(chan struct{}, workers*2),
		cancel: cancel,
	}
	for i := 0; i < workers; i++ {
		manager.wg.Add(1)
		go manager.worker(ctx)
	}
	return manager
}

func (m *downloadManager) Notify() {
	if m == nil {
		return
	}
	select {
	case m.wakeCh <- struct{}{}:
	default:
	}
}

func (m *downloadManager) Stop() {
	if m == nil {
		return
	}
	m.cancel()
	m.Notify()
	m.wg.Wait()
}

func (m *downloadManager) worker(ctx context.Context) {
	defer m.wg.Done()
	for {
		if ctx.Err() != nil {
			return
		}

		episodeID, err := m.claimNext(ctx)
		if err != nil {
			if errors.Is(err, errNoDownloadTask) {
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

		info, err := m.app.fetchEpisodeInfo(ctx, episodeID)
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
		if _, err := m.app.downloadEpisode(ctx, info); err != nil {
			log.Printf("download %s failed: %v", episodeID, err)
			if err := m.app.requeueEpisode(ctx, episodeID); err != nil {
				log.Printf("requeue %s failed: %v", episodeID, err)
			}
		}
	}
}

func (m *downloadManager) waitForWork(ctx context.Context) error {
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

func (m *downloadManager) claimNext(ctx context.Context) (string, error) {
	var episodeID string
	err := withRetry(ctx, func() error {
		tx, err := m.app.db.BeginTx(ctx, nil)
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
		err = tx.QueryRowContext(ctx, `SELECT episode_id FROM downloads ORDER BY priority DESC, enqueued_at LIMIT 1`).Scan(&episodeID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errNoDownloadTask
			}
			return err
		}

		res, err := tx.ExecContext(ctx, "DELETE FROM downloads WHERE episode_id = ?", episodeID)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return errNoDownloadTask
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

func withRetry(ctx context.Context, fn func() error) error {
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
		if errors.Is(err, errNoDownloadTask) {
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

type sleepFunc func(context.Context, time.Duration) error

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
