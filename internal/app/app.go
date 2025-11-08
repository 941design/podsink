package app

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/kballard/go-shellquote"
	"gopkg.in/yaml.v3"

	"podsink/internal/config"
	"podsink/internal/domain"
	"podsink/internal/downloads"
	"podsink/internal/episodes"
	"podsink/internal/fuzzy"
	"podsink/internal/itunes"
	"podsink/internal/repository"
	"podsink/internal/subscriptions"
)

type commandHandler func(context.Context, []string) (CommandResult, error)

type command struct {
	usage   string
	summary string
	handler commandHandler
}

const (
	stateNew        = domain.EpisodeStateNew
	stateSeen       = domain.EpisodeStateSeen
	stateIgnored    = domain.EpisodeStateIgnored
	stateQueued     = domain.EpisodeStateQueued
	stateDownloaded = domain.EpisodeStateDownloaded
	stateDeleted    = domain.EpisodeStateDeleted
)

type CommandResult struct {
	Message                  string
	Quit                     bool
	SearchResults            []SearchResult
	SearchTitle              string
	SearchHint               string
	SearchContext            string
	EpisodeResults           []domain.EpisodeResult
	QueuedEpisodeResults     []domain.QueuedEpisodeResult
	DownloadedEpisodeResults []domain.EpisodeResult
	DanglingFiles            []domain.DanglingFile
}

type SearchResult struct {
	Podcast       itunes.Podcast
	Score         float64
	IsSubscribed  bool
	NewCount      int
	UnplayedCount int
	TotalCount    int
}

type EpisodeResult = domain.EpisodeResult

type EpisodeDetail = domain.EpisodeDetail

type QueuedEpisodeResult = domain.QueuedEpisodeResult

type DanglingFile = domain.DanglingFile

var (
	ErrNoSubscriptionsToExport = subscriptions.ErrNoSubscriptionsToExport
	ErrNoSubscriptionsInOPML   = subscriptions.ErrNoSubscriptionsInOPML
)

type App struct {
	config        config.Config
	configPath    string
	db            *sql.DB
	httpClient    *http.Client
	itunes        *itunes.Client
	commands      map[string]*command
	subscriptions *subscriptions.Service
	episodes      *episodes.Service
	downloads     *downloads.Service
	downloadMgr   *downloads.Manager
}

type Dependencies struct {
	HTTPClient *http.Client
	ITunes     *itunes.Client
	Sleep      downloads.SleepFunc
}

type OPMLImportResult = subscriptions.ImportResult

func New(cfg config.Config, configPath string, db *sql.DB) *App {
	return NewWithDependencies(cfg, configPath, db, Dependencies{})
}

func NewWithDependencies(cfg config.Config, configPath string, db *sql.DB, deps Dependencies) *App {
	httpClient := deps.HTTPClient
	if httpClient == nil {
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: !cfg.TLSVerify},
		}
		if proxyURL := strings.TrimSpace(cfg.Proxy); proxyURL != "" {
			if parsed, err := url.Parse(proxyURL); err == nil {
				transport.Proxy = http.ProxyURL(parsed)
			}
		}
		httpClient = &http.Client{Timeout: 15 * time.Second, Transport: transport}
	}

	itunesClient := deps.ITunes
	if itunesClient == nil {
		itunesClient = itunes.NewClient(httpClient, "")
	}

	store := repository.New(db)

	subsSvc := subscriptions.NewService(store, httpClient, itunesClient)
	episodesSvc := episodes.NewService(store)
	downloadsSvc := downloads.NewService(cfg, store, httpClient, deps.Sleep)

	application := &App{
		config:        cfg,
		configPath:    configPath,
		db:            db,
		httpClient:    httpClient,
		itunes:        itunesClient,
		commands:      make(map[string]*command),
		subscriptions: subsSvc,
		episodes:      episodesSvc,
		downloads:     downloadsSvc,
	}
	application.registerCommands()

	workers := cfg.ParallelDownloads
	if workers < 0 {
		workers = 0
	}
	if workers > 0 {
		application.downloadMgr = downloads.NewManager(downloadsSvc, episodesSvc, workers)
		application.downloadMgr.Notify()
	}

	return application
}

func (a *App) Config() config.Config {
	return a.config
}

func (a *App) CommandNames() []string {
	names := make([]string, 0, len(a.commands))
	for name := range a.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a *App) Close() error {
	if a.downloadMgr != nil {
		a.downloadMgr.Stop()
	}
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// Initialize performs startup checks and corrections on the database state.
func (a *App) Initialize(ctx context.Context) error {
	// Correct episodes stuck in QUEUED state that are already downloaded
	if err := a.episodes.CorrectQueuedStates(ctx); err != nil {
		return fmt.Errorf("correct queued states: %w", err)
	}
	return nil
}

func (a *App) Execute(ctx context.Context, input string) (CommandResult, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return CommandResult{}, nil
	}

	args, err := shellquote.Split(input)
	if err != nil {
		return CommandResult{}, err
	}
	if len(args) == 0 {
		return CommandResult{}, nil
	}

	cmdName := strings.ToLower(args[0])
	cmd, ok := a.commands[cmdName]
	if !ok {
		return CommandResult{Message: fmt.Sprintf("unknown command: %s", args[0])}, nil
	}

	return cmd.handler(ctx, args[1:])
}

func (a *App) LookupPodcast(ctx context.Context, id string) (itunes.Podcast, error) {
	return a.itunes.LookupPodcast(ctx, id)
}

func (a *App) registerCommands() {
	a.registerCommand("config", "config [show]", "View or edit application configuration", a.configCommand)
	a.registerCommand("exit", "exit", "Exit the application", a.exitCommand, "quit")
	a.registerCommand("search", "search <query> | search episodes <query>", "Search for podcasts (default) or episodes", a.searchCommand, "s")
	a.registerCommand("list", "list subscriptions [filter]", "List all podcast subscriptions (optionally filtered)", a.listCommand, "ls")
	a.registerCommand("episodes", "episodes", "View recent episodes across subscriptions", a.episodesCommand, "e", "le")
	a.registerCommand("queue", "queue [episode_id]", "View download queue status or queue an episode", a.queueCommand, "q")
	a.registerCommand("downloads", "downloads", "View all downloaded episodes", a.downloadsCommand, "d")
	a.registerCommand("import", "import <file>", "Import subscriptions from an OPML file", a.importCommand)
	// Register download and ignore commands (available for shortcuts)
	a.commands["download"] = &command{usage: "download <episode_id>", summary: "Download an episode immediately", handler: a.downloadCommand}
	a.commands["ignore"] = &command{usage: "ignore <episode_id>", summary: "Toggle the ignored state for an episode", handler: a.ignoreCommand}
	a.registerCommand("export", "export <file>", "Export subscriptions to an OPML file", a.exportCommand)
}

func (a *App) registerCommand(name, usage, summary string, handler commandHandler, aliases ...string) {
	cmd := &command{usage: usage, summary: summary, handler: handler}
	names := append([]string{name}, aliases...)
	for _, alias := range names {
		a.commands[alias] = cmd
	}
}

func (a *App) configCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) == 0 {
		return CommandResult{Message: "Usage: config [show]"}, nil
	}
	switch strings.ToLower(args[0]) {
	case "show":
		data, err := yaml.Marshal(a.config)
		if err != nil {
			return CommandResult{}, err
		}
		return CommandResult{Message: string(data)}, nil
	default:
		return a.editConfig(ctx)
	}
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

func (a *App) exitCommand(_ context.Context, _ []string) (CommandResult, error) {
	return CommandResult{Quit: true}, nil
}

func (a *App) searchCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) == 0 {
		return CommandResult{Message: "Usage: search <query> | search episodes <query>"}, nil
	}

	if topic := strings.ToLower(args[0]); topic == "episodes" || topic == "episode" {
		if len(args) == 1 {
			return CommandResult{Message: "Usage: search episodes <query>"}, nil
		}
		term := strings.Join(args[1:], " ")
		results, err := a.episodes.Search(ctx, term)
		if err != nil {
			return CommandResult{}, err
		}
		if len(results) == 0 {
			return CommandResult{Message: "No episodes found."}, nil
		}
		return CommandResult{EpisodeResults: results}, nil
	}

	term := strings.Join(args, " ")
	results, err := a.itunes.Search(ctx, term, 25)
	if err != nil {
		return CommandResult{}, err
	}
	if len(results) == 0 {
		return CommandResult{Message: "No podcasts found."}, nil
	}

	type scoredResult struct {
		podcast itunes.Podcast
		score   float64
	}

	scored := make([]scoredResult, 0, len(results))
	for _, r := range results {
		titleScore := fuzzy.MatchScore(r.Title, term)
		authorScore := fuzzy.MatchScore(r.Author, term) * 0.5
		maxScore := titleScore
		if authorScore > maxScore {
			maxScore = authorScore
		}
		if maxScore > 0.3 {
			scored = append(scored, scoredResult{podcast: r, score: maxScore})
		}
	}
	if len(scored) == 0 {
		return CommandResult{Message: "No podcasts found."}, nil
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	maxResults := 10
	if len(scored) < maxResults {
		maxResults = len(scored)
	}

	searchResults := make([]SearchResult, maxResults)
	for i := 0; i < maxResults; i++ {
		subscribed, _, err := a.subscriptions.IsSubscribed(ctx, scored[i].podcast.ID)
		if err != nil {
			return CommandResult{}, err
		}
		searchResults[i] = SearchResult{
			Podcast:      scored[i].podcast,
			Score:        scored[i].score,
			IsSubscribed: subscribed,
		}
	}

	return CommandResult{
		SearchResults: searchResults,
		SearchTitle:   "Search Results",
		SearchHint:    "Use ↑↓/jk to navigate, Enter for details, [s] subscribe, [u] unsubscribe, [x]/Esc to search again",
		SearchContext: "search",
	}, nil
}

func (a *App) SubscribePodcast(ctx context.Context, podcast itunes.Podcast) (CommandResult, error) {
	result, err := a.subscriptions.Subscribe(ctx, podcast)
	if err != nil {
		switch {
		case errors.Is(err, subscriptions.ErrMissingPodcastID):
			return CommandResult{Message: "Podcast ID cannot be empty."}, nil
		case errors.Is(err, subscriptions.ErrAlreadySubscribed):
			title := result.Title
			if title == "" {
				title = strings.TrimSpace(podcast.Title)
			}
			if title == "" {
				title = strings.TrimSpace(podcast.ID)
			}
			return CommandResult{Message: fmt.Sprintf("Already subscribed to %s.", title)}, nil
		case errors.Is(err, subscriptions.ErrMissingFeedURL):
			return CommandResult{}, err
		default:
			return CommandResult{}, err
		}
	}
	return CommandResult{Message: fmt.Sprintf("Subscribed to %s (%d new episodes).", result.Title, result.Added)}, nil
}

func (a *App) UnsubscribePodcast(ctx context.Context, podcastID string) (CommandResult, error) {
	ok, err := a.subscriptions.Unsubscribe(ctx, podcastID)
	if err != nil {
		if errors.Is(err, subscriptions.ErrMissingPodcastID) {
			return CommandResult{Message: "Podcast ID cannot be empty."}, nil
		}
		return CommandResult{}, err
	}
	if !ok {
		return CommandResult{Message: "No subscription found for that podcast."}, nil
	}
	return CommandResult{Message: "Subscription removed."}, nil
}

func (a *App) listCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) == 0 {
		return CommandResult{Message: "Usage: list subscriptions [filter]"}, nil
	}

	switch strings.ToLower(args[0]) {
	case "subscriptions":
		summaries, err := a.subscriptions.Summaries(ctx)
		if err != nil {
			return CommandResult{}, err
		}
		if len(summaries) == 0 {
			return CommandResult{Message: "No subscriptions yet."}, nil
		}

		if len(args) > 1 {
			filter := strings.Join(args[1:], " ")
			filtered := make([]domain.SubscriptionSummary, 0, len(summaries))
			for _, s := range summaries {
				if fuzzy.ContainsFuzzy(s.Title, filter) || fuzzy.ContainsFuzzy(s.ID, filter) {
					filtered = append(filtered, s)
				}
			}
			summaries = filtered
			if len(summaries) == 0 {
				return CommandResult{Message: fmt.Sprintf("No subscriptions matching '%s'.", filter)}, nil
			}
		}

		results := make([]SearchResult, 0, len(summaries))
		for _, s := range summaries {
			results = append(results, SearchResult{
				Podcast: itunes.Podcast{
					ID:    s.ID,
					Title: s.Title,
				},
				IsSubscribed:  true,
				NewCount:      s.NewCount,
				UnplayedCount: s.UnplayedCount,
				TotalCount:    s.TotalCount,
			})
		}

		return CommandResult{
			SearchResults: results,
			SearchTitle:   "Subscriptions",
			SearchHint:    "Use ↑↓/jk to navigate, Enter for details, [s] search podcasts, [u] unsubscribe, [x]/Esc to exit",
			SearchContext: "subscriptions",
		}, nil
	default:
		return CommandResult{Message: fmt.Sprintf("unknown list target: %s", args[0])}, nil
	}
}

func (a *App) episodesCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) > 0 {
		return CommandResult{Message: "Usage: episodes"}, nil
	}

	episodes, err := a.episodes.List(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	if len(episodes) == 0 {
		return CommandResult{Message: "No episodes recorded yet."}, nil
	}

	if err := a.episodes.MarkAllSeen(ctx); err != nil {
		return CommandResult{}, err
	}

	return CommandResult{EpisodeResults: episodes}, nil
}

func (a *App) queueCommand(ctx context.Context, args []string) (CommandResult, error) {
	// With arguments: queue an episode
	if len(args) == 1 {
		episodeID := strings.TrimSpace(args[0])
		if episodeID == "" {
			return CommandResult{Message: "Episode ID cannot be empty."}, nil
		}

		info, err := a.episodes.FetchEpisodeInfo(ctx, episodeID)
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

		if err := a.downloads.EnqueueEpisode(ctx, info.ID); err != nil {
			return CommandResult{}, err
		}
		if a.downloadMgr != nil {
			a.downloadMgr.Notify()
		}

		if info.State == stateDownloaded {
			return CommandResult{Message: fmt.Sprintf("Episode %s queued for re-download.", info.ID)}, nil
		}
		return CommandResult{Message: fmt.Sprintf("Episode %s queued for download.", info.ID)}, nil
	}

	// Without arguments: list queued episodes
	if len(args) != 0 {
		return CommandResult{Message: "Usage: queue [episode_id]"}, nil
	}

	queuedEpisodes, err := a.episodes.ListQueued(ctx)
	if err != nil {
		return CommandResult{}, err
	}

	// Always return QueuedEpisodeResults, even if empty, so the queue view is activated
	return CommandResult{QueuedEpisodeResults: queuedEpisodes}, nil
}

func (a *App) downloadsCommand(ctx context.Context, args []string) (CommandResult, error) {
	// List all downloaded episodes (DOWNLOADED or DELETED state)
	if len(args) != 0 {
		return CommandResult{Message: "Usage: downloads"}, nil
	}

	// Check for deleted files and update states
	if err := a.episodes.CheckDeletedFiles(ctx); err != nil {
		return CommandResult{}, err
	}

	downloadedEpisodes, err := a.episodes.ListDownloaded(ctx)
	if err != nil {
		return CommandResult{}, err
	}

	// Find dangling files (files in download directory not tracked in database)
	danglingFiles, err := a.episodes.FindDanglingFiles(ctx, a.config.DownloadRoot)
	if err != nil {
		return CommandResult{}, err
	}

	// Always return DownloadedEpisodeResults and DanglingFiles, even if empty, so the downloads view is activated
	return CommandResult{
		DownloadedEpisodeResults: downloadedEpisodes,
		DanglingFiles:            danglingFiles,
	}, nil
}

func (a *App) downloadCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) != 1 {
		return CommandResult{Message: "Usage: download <episode_id>"}, nil
	}
	episodeID := strings.TrimSpace(args[0])
	if episodeID == "" {
		return CommandResult{Message: "Episode ID cannot be empty."}, nil
	}

	info, err := a.episodes.FetchEpisodeInfo(ctx, episodeID)
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

	isRedownload := info.State == stateDownloaded
	finalPath, err := a.downloads.DownloadEpisode(ctx, info)
	if err != nil {
		return CommandResult{}, err
	}

	if isRedownload {
		return CommandResult{Message: fmt.Sprintf("Re-downloaded %s to %s.", info.Title, finalPath)}, nil
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

	info, err := a.episodes.FetchEpisodeInfo(ctx, episodeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CommandResult{Message: "Episode not found."}, nil
		}
		return CommandResult{}, err
	}

	switch info.State {
	case stateIgnored:
		if err := a.episodes.UpdateEpisodeState(ctx, info.ID, stateSeen); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{Message: fmt.Sprintf("Episode %s unignored.", info.ID)}, nil
	default:
		if err := a.downloads.RemoveFromQueue(ctx, info.ID); err != nil {
			return CommandResult{}, err
		}
		if err := a.episodes.UpdateEpisodeState(ctx, info.ID, stateIgnored); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{Message: fmt.Sprintf("Episode %s ignored.", info.ID)}, nil
	}
}

func (a *App) exportCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) != 1 {
		return CommandResult{Message: "Usage: export <file>"}, nil
	}
	count, err := a.ExportOPML(ctx, args[0])
	if err != nil {
		return CommandResult{}, err
	}
	return CommandResult{Message: fmt.Sprintf("Exported %d subscriptions.", count)}, nil
}

func (a *App) importCommand(ctx context.Context, args []string) (CommandResult, error) {
	if len(args) != 1 {
		return CommandResult{Message: "Usage: import <file>"}, nil
	}
	result, err := a.ImportOPML(ctx, args[0])
	if err != nil {
		return CommandResult{}, err
	}
	msg := fmt.Sprintf("Imported %d subscriptions", result.Imported)
	if result.Skipped > 0 {
		msg += fmt.Sprintf(", skipped %d", result.Skipped)
	}
	if len(result.Errors) > 0 {
		msg += fmt.Sprintf(", %d errors", len(result.Errors))
	}
	return CommandResult{Message: msg}, nil
}

func (a *App) ExportOPML(ctx context.Context, filePath string) (int, error) {
	return a.subscriptions.ExportOPML(ctx, filePath)
}

func (a *App) ImportOPML(ctx context.Context, filePath string) (OPMLImportResult, error) {
	return a.subscriptions.ImportOPML(ctx, filePath)
}

func (a *App) EpisodeDetails(ctx context.Context, episodeID string) (EpisodeDetail, error) {
	return a.episodes.EpisodeDetails(ctx, episodeID)
}

// CountQueued returns the count of episodes in QUEUED state.
func (a *App) CountQueued(ctx context.Context) (int, error) {
	return a.episodes.CountQueued(ctx)
}

// CountDownloaded returns the count of episodes in DOWNLOADED or DELETED state.
func (a *App) CountDownloaded(ctx context.Context) (int, error) {
	return a.episodes.CountDownloaded(ctx)
}
