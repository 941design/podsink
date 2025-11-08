package repl

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jaytaylor/html2text"

	"podsink/internal/app"
	"podsink/internal/itunes"
	"podsink/internal/theme"
)

type searchView struct {
	active  bool
	results []app.SearchResult
	cursor  int
	title   string
	hint    string
	context string
	details detailView

	prevResults []app.SearchResult
	prevTitle   string
	prevHint    string
	prevContext string
	prevCursor  int
}

type detailView struct {
	active  bool
	podcast app.SearchResult
}

type episodeView struct {
	active     bool
	results    []app.EpisodeResult
	cursor     int
	scroll     int
	details    episodeDetailView
	filterMode string // "all", "ignored", "downloaded", or "" (default: not ignored)

	previousResults []app.EpisodeResult
	previousCursor  int
	previousScroll  int
	showingSearch   bool
}

type episodeDetailView struct {
	active bool
	detail app.EpisodeDetail
	scroll int
	lines  []string
}

type queueView struct {
	active  bool
	results []app.QueuedEpisodeResult
	cursor  int
}

type downloadsView struct {
	active        bool
	results       []app.EpisodeResult
	danglingFiles []app.DanglingFile
	cursor        int
	scroll        int
}

type commandMenuItem struct {
	name        string
	usage       string
	description string
	shorthand   string
}

type commandMenuView struct {
	active bool
	items  []commandMenuItem
	cursor int
}

type model struct {
	ctx      context.Context
	app      *app.App
	input    textinput.Model
	quitting bool
	theme    theme.Theme
	width    int

	searchInputMode bool // When true, input is shown for entering search query
	searchTarget    string
	searchReturn    string
	searchParent    string
	commandMenu     commandMenuView
	search          searchView
	episodes        episodeView
	queue           queueView
	downloads       downloadsView

	queueCount     int
	downloadsCount int

	longDescCache map[string]string
}

func newModel(ctx context.Context, application *app.App) model {
	cfg := application.Config()
	th := theme.ForName(cfg.ColorTheme)
	ti := textinput.New()
	ti.Placeholder = "Enter podcast search query..."
	ti.Blur() // Start with menu, not input
	ti.Prompt = "search> "
	ti.CharLimit = 512
	ti.Width = 80

	// Build command menu items
	commandItems := []commandMenuItem{
		{name: "list", usage: "podcasts", description: "List all podcast subscriptions", shorthand: "[p]"},
		{name: "episodes", usage: "episodes", description: "View recent episodes across subscriptions", shorthand: "[e]"},
		{name: "queue", usage: "queue", description: "View download queue status", shorthand: "[q]"},
		{name: "downloads", usage: "downloads", description: "View all downloaded episodes", shorthand: "[d]"},
		{name: "config", usage: "config [show]", description: "View or edit application configuration", shorthand: "[c]"},
		{name: "exit", usage: "exit", description: "Exit the application", shorthand: "[x]"},
	}

	m := model{
		ctx:   ctx,
		app:   application,
		input: ti,
		theme: th,
		commandMenu: commandMenuView{
			active: true,
			items:  commandItems,
			cursor: 0,
		},
		longDescCache: make(map[string]string),
	}

	// Fetch initial counts
	m.refreshCounts()

	return m
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		// Re-format episode description if in episode details mode
		if m.episodes.details.active {
			m.episodes.details.lines = formatEpisodeDescription(m.episodes.details.detail.Description, msg.Width)
			m.episodes.details.scroll = 0
		}
		return m, nil
	case tea.KeyMsg:
		if !m.searchInputMode {
			if len(m.episodes.previousResults) > 0 && msg.String() == "x" {
				if m.restoreEpisodeList() {
					return m, nil
				}
			}
			if m.search.active && msg.String() == "x" {
				if m.searchParent == "subscriptions" && m.restoreSubscriptionsView() {
					return m, nil
				}
			}
		}
		// Handle command menu mode navigation
		if m.commandMenu.active {
			switch msg.String() {
			case "ctrl+c", "esc", "x":
				m.quitting = true
				return m, tea.Quit
			case "up", "k":
				// Move cursor up with wraparound
				if m.commandMenu.cursor > 0 {
					m.commandMenu.cursor--
				} else {
					m.commandMenu.cursor = len(m.commandMenu.items) - 1
				}
				return m, nil
			case "down", "j":
				// Move cursor down with wraparound
				if m.commandMenu.cursor < len(m.commandMenu.items)-1 {
					m.commandMenu.cursor++
				} else {
					m.commandMenu.cursor = 0
				}
				return m, nil
			case "enter":
				// Execute selected command
				if m.commandMenu.cursor < len(m.commandMenu.items) {
					selectedItem := m.commandMenu.items[m.commandMenu.cursor]
					m.commandMenu.active = false
					m.input.Focus()

					// For commands that need arguments, prompt for input
					switch selectedItem.name {
					case "list":
						// Execute "list subscriptions" directly
						result, err := m.app.Execute(m.ctx, "list subscriptions")
						if err != nil {
							// Error: return to menu
							return m, nil
						}
						return m.handleCommandResult(result)
					default:
						// Execute the command directly
						result, err := m.app.Execute(m.ctx, selectedItem.name)
						if err != nil {
							// Error: return to menu
							return m, nil
						}
						return m.handleCommandResult(result)
					}
				}
				return m, nil
			case "p":
				// Shortcut for list podcasts
				m.commandMenu.active = false
				m.input.Focus()
				result, err := m.app.Execute(m.ctx, "list subscriptions")
				if err != nil {
					// Error: return to menu
					m.commandMenu.active = true
					m.input.Blur()
					return m, nil
				}
				return m.handleCommandResult(result)
			case "e":
				// Shortcut for episodes
				m.commandMenu.active = false
				m.input.Focus()
				result, err := m.app.Execute(m.ctx, "episodes")
				if err != nil {
					// Error: return to menu
					m.commandMenu.active = true
					m.input.Blur()
					return m, nil
				}
				return m.handleCommandResult(result)
			case "c":
				// Shortcut for config
				m.commandMenu.active = false
				m.input.Focus()
				result, err := m.app.Execute(m.ctx, "config")
				if err != nil {
					// Error: return to menu
					m.commandMenu.active = true
					m.input.Blur()
					return m, nil
				}
				return m.handleCommandResult(result)
			case "q":
				// Shortcut for queue
				m.commandMenu.active = false
				m.input.Focus()
				result, err := m.app.Execute(m.ctx, "queue")
				if err != nil {
					// Error: return to menu
					m.commandMenu.active = true
					m.input.Blur()
					return m, nil
				}
				return m.handleCommandResult(result)
			case "d":
				// Shortcut for downloads
				m.commandMenu.active = false
				m.input.Focus()
				result, err := m.app.Execute(m.ctx, "downloads")
				if err != nil {
					// Error: return to menu
					m.commandMenu.active = true
					m.input.Blur()
					return m, nil
				}
				return m.handleCommandResult(result)
			}
			return m, nil
		}

		// Handle search details mode navigation
		if m.search.details.active {
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "esc", "x":
				// Exit details mode, return to search list
				m.search.details.active = false
				return m, nil
			case "s":
				// Subscribe to podcast
				return m.handleSearchSubscribe()
			case "u":
				// Unsubscribe from podcast
				return m.handleSearchUnsubscribe()
			}
			return m, nil
		}

		if m.episodes.details.active {
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "esc", "x":
				m.episodes.details.active = false
				m.episodes.details.scroll = 0
				m.episodes.details.lines = nil
				return m, nil
			case "down", "j":
				m.adjustEpisodeDetailScroll(1)
				return m, nil
			case "up", "k":
				m.adjustEpisodeDetailScroll(-1)
				return m, nil
			case "pgdown", "ctrl+f":
				m.adjustEpisodeDetailScroll(m.maxEpisodeDescriptionLines())
				return m, nil
			case "pgup", "ctrl+b":
				m.adjustEpisodeDetailScroll(-m.maxEpisodeDescriptionLines())
				return m, nil
			case "end":
				if total := len(m.episodes.details.lines); total > 0 {
					max := m.maxEpisodeDescriptionLines()
					if max <= 0 {
						max = 12
					}
					maxOffset := total - max
					if maxOffset < 0 {
						maxOffset = 0
					}
					m.episodes.details.scroll = maxOffset
				}
				return m, nil
			case "home":
				m.episodes.details.scroll = 0
				return m, nil
			}
			return m, nil
		}

		// Handle search mode navigation
		if m.search.active && !m.searchInputMode {
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "x":
				if m.searchParent == "subscriptions" && m.restoreSubscriptionsView() {
					return m, nil
				}
				fallthrough
			case "esc", "q":
				// Exit search mode - return to main menu
				m.exitSearchViewToMenu()
				return m, nil
			case "up", "k":
				if m.search.cursor > 0 {
					m.search.cursor--
				}
				return m, nil
			case "down", "j":
				if m.search.cursor < len(m.search.results)-1 {
					m.search.cursor++
				}
				return m, nil
			case "enter":
				// Enter details mode for selected podcast
				if m.search.cursor < len(m.search.results) {
					m.search.details.active = true
					m.search.details.podcast = m.search.results[m.search.cursor]

					// Fetch long description if not already cached
					podcastID := m.search.details.podcast.Podcast.ID
					if _, cached := m.longDescCache[podcastID]; !cached {
						// Try to fetch the full podcast details from iTunes API
						if fullPodcast, err := m.app.LookupPodcast(m.ctx, podcastID); err == nil {
							m.longDescCache[podcastID] = fullPodcast.LongDescription
							// Update the current podcast with the long description
							m.search.details.podcast.Podcast.LongDescription = fullPodcast.LongDescription
						}
					} else {
						// Use cached long description
						m.search.details.podcast.Podcast.LongDescription = m.longDescCache[podcastID]
					}
				}
				return m, nil
			case "s":
				if m.search.context == "subscriptions" {
					m.beginSearchInput("podcasts", "search> ", "Enter podcast search query...", "subscriptions")
					return m, nil
				}
				// Subscribe directly from list view
				return m.handleSearchSubscribe()
			case "u":
				// Unsubscribe directly from list view
				return m.handleSearchUnsubscribe()
			}
			return m, nil
		}

		// Handle episode mode navigation
		if m.episodes.active && !m.searchInputMode {
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "x":
				if len(m.episodes.previousResults) > 0 && m.restoreEpisodeList() {
					return m, nil
				}
				fallthrough
			case "esc", "q":
				// Exit episode mode - return to main menu
				m.episodes.active = false
				m.episodes.results = nil
				m.episodes.details.active = false
				m.episodes.cursor = 0
				m.episodes.scroll = 0
				m.episodes.showingSearch = false
				m.clearEpisodeBackup()
				m.refreshCounts()
				m.commandMenu.active = true
				m.input.Blur()
				return m, nil
			case "enter":
				if m.episodes.cursor < len(m.episodes.results) {
					selected := m.episodes.results[m.episodes.cursor]
					detail, err := m.app.EpisodeDetails(m.ctx, selected.Episode.ID)
					if err != nil {
						// Error: stay in episode list
						return m, nil
					}
					m.enterEpisodeDetails(detail)
				}
				return m, nil
			case "up", "k":
				if m.episodes.cursor > 0 {
					m.episodes.cursor--
					// Scroll up when cursor moves above visible window
					cfg := m.app.Config()
					maxVisible := cfg.MaxEpisodes
					if maxVisible <= 0 {
						maxVisible = 12
					}
					if m.episodes.cursor < m.episodes.scroll {
						m.episodes.scroll = m.episodes.cursor
					}
				}
				return m, nil
			case "down", "j":
				if m.episodes.cursor < len(m.episodes.results)-1 {
					m.episodes.cursor++
					// Scroll down when cursor moves below visible window
					cfg := m.app.Config()
					maxVisible := cfg.MaxEpisodes
					if maxVisible <= 0 {
						maxVisible = 12
					}
					if m.episodes.cursor >= m.episodes.scroll+maxVisible {
						m.episodes.scroll = m.episodes.cursor - maxVisible + 1
					}
				}
				return m, nil
			case "i":
				// Ignore/unignore the selected episode
				if m.episodes.cursor < len(m.episodes.results) {
					selected := m.episodes.results[m.episodes.cursor]
					_, err := m.app.Execute(m.ctx, "ignore "+selected.Episode.ID)
					if err != nil {
						// Error: stay in episode list
						return m, nil
					}
					// Refresh the episode list
					result, err := m.app.Execute(m.ctx, "episodes")
					if err != nil {
						// Error: stay in episode list
						return m, nil
					}
					return m.handleCommandResult(result)
				}
				return m, nil
			case "a":
				// Show all episodes
				m.episodes.filterMode = "all"
				// Refresh the episode list
				result, err := m.app.Execute(m.ctx, "episodes")
				if err != nil {
					// Error: stay in episode list
					return m, nil
				}
				return m.handleCommandResult(result)
			case "shift+i":
				// Show only ignored episodes
				m.episodes.filterMode = "ignored"
				// Refresh the episode list
				result, err := m.app.Execute(m.ctx, "episodes")
				if err != nil {
					// Error: stay in episode list
					return m, nil
				}
				return m.handleCommandResult(result)
			case "shift+d":
				// Show only downloaded episodes
				m.episodes.filterMode = "downloaded"
				// Refresh the episode list
				result, err := m.app.Execute(m.ctx, "episodes")
				if err != nil {
					// Error: stay in episode list
					return m, nil
				}
				return m.handleCommandResult(result)
			case "d":
				// Download/queue the selected episode for download
				if m.episodes.cursor < len(m.episodes.results) {
					selected := m.episodes.results[m.episodes.cursor]
					_, err := m.app.Execute(m.ctx, "queue "+selected.Episode.ID)
					if err != nil {
						// Error: stay in episode list
						return m, nil
					}
					// Refresh the episode list
					result, err := m.app.Execute(m.ctx, "episodes")
					if err != nil {
						// Error: stay in episode list
						return m, nil
					}
					return m.handleCommandResult(result)
				}
				return m, nil
			case "s":
				// Enter episode search mode
				m.beginSearchInput("episodes", "episodes search> ", "Enter episode search query...", "episodes")
				return m, nil
			}
		}

		// Handle queue mode navigation
		if m.queue.active {
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "esc", "x":
				// Exit queue mode - return to main menu
				m.queue.active = false
				m.queue.results = nil
				m.queue.cursor = 0
				m.refreshCounts()
				m.commandMenu.active = true
				m.input.Blur()
				return m, nil
			case "up", "k":
				if m.queue.cursor > 0 {
					m.queue.cursor--
				}
				return m, nil
			case "down", "j":
				if m.queue.cursor < len(m.queue.results)-1 {
					m.queue.cursor++
				}
				return m, nil
			}
			return m, nil
		}

		// Handle downloads mode navigation
		if m.downloads.active {
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "esc", "x":
				// Exit downloads mode - return to main menu
				m.downloads.active = false
				m.downloads.results = nil
				m.downloads.cursor = 0
				m.downloads.scroll = 0
				m.refreshCounts()
				m.commandMenu.active = true
				m.input.Blur()
				return m, nil
			case "up", "k":
				if m.downloads.cursor > 0 {
					m.downloads.cursor--
					// Scroll up when cursor moves above visible window
					cfg := m.app.Config()
					maxVisible := cfg.MaxEpisodes
					if maxVisible <= 0 {
						maxVisible = 12
					}
					if m.downloads.cursor < m.downloads.scroll {
						m.downloads.scroll = m.downloads.cursor
					}
				}
				return m, nil
			case "down", "j":
				if m.downloads.cursor < len(m.downloads.results)-1 {
					m.downloads.cursor++
					// Scroll down when cursor moves below visible window
					cfg := m.app.Config()
					maxVisible := cfg.MaxEpisodes
					if maxVisible <= 0 {
						maxVisible = 12
					}
					if m.downloads.cursor >= m.downloads.scroll+maxVisible {
						m.downloads.scroll = m.downloads.cursor - maxVisible + 1
					}
				}
				return m, nil
			}
			return m, nil
		}

		// Handle search input mode
		if m.searchInputMode {
			switch msg.Type {
			case tea.KeyCtrlC:
				m.quitting = true
				return m, tea.Quit
			case tea.KeyEsc:
				// Exit search input mode - return to previous view
				returnState := m.searchReturn
				m.searchInputMode = false
				m.searchTarget = ""
				m.searchReturn = ""
				// Don't clear searchParent or backup for subscriptions - they're needed for 'x' to restore
				if returnState == "episodes" {
					if m.episodes.showingSearch {
						m.restoreEpisodeList()
					}
					m.clearEpisodeBackup()
				}
				m.input.SetValue("")
				m.restoreAfterSearchInput(returnState)
				return m, nil
			case tea.KeyEnter:
				// Execute search with the query
				query := strings.TrimSpace(m.input.Value())
				target := m.searchTarget
				returnState := m.searchReturn
				parent := m.searchParent
				m.searchInputMode = false
				m.searchTarget = ""
				m.searchReturn = ""
				m.input.SetValue("")

				if query == "" {
					if returnState == "subscriptions" {
						m.searchParent = ""
						m.restoreSubscriptionsView()
					}
					if returnState == "episodes" {
						if m.episodes.showingSearch {
							m.restoreEpisodeList()
						}
						m.clearEpisodeBackup()
					}
					m.restoreAfterSearchInput(returnState)
					m.searchInputMode = false
					return m, nil
				}

				command := "search " + query
				if target == "episodes" {
					command = "search episodes " + query
				}

				result, err := m.app.Execute(m.ctx, command)
				if err != nil {
					if returnState == "subscriptions" {
						m.searchParent = ""
						m.clearSubscriptionBackup()
					}
					if returnState == "episodes" {
						if m.episodes.showingSearch {
							m.restoreEpisodeList()
						}
						m.clearEpisodeBackup()
					}
					m.restoreAfterSearchInput(returnState)
					return m, nil
				}

				if target == "episodes" {
					if len(result.EpisodeResults) > 0 {
						m.episodes.showingSearch = true
					} else {
						m.episodes.showingSearch = false
						m.clearEpisodeBackup()
					}
					m.searchParent = ""
				} else if parent == "subscriptions" {
					if len(result.SearchResults) > 0 {
						m.searchParent = "subscriptions"
					} else {
						m.searchParent = ""
						m.clearSubscriptionBackup()
					}
				} else {
					m.searchParent = ""
				}
				return m.handleCommandResult(result)
			}
			// Let the input handle other keys
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	}

	// If we reach here, no handler processed the message
	return m, nil
}

func (m *model) beginSearchInput(target, prompt, placeholder, returnState string) {
	switch returnState {
	case "subscriptions":
		m.searchParent = "subscriptions"
		// Only backup if we don't already have a backup (prevents overwriting original subscriptions)
		if len(m.search.results) > 0 && len(m.search.prevResults) == 0 {
			m.backupSubscriptionList()
		}
	case "episodes":
		m.searchParent = ""
		if !m.episodes.showingSearch && len(m.episodes.results) > 0 {
			m.backupEpisodeList()
		}
	default:
		m.searchParent = ""
	}
	m.searchInputMode = true
	m.searchTarget = target
	m.searchReturn = returnState
	m.commandMenu.active = false
	m.input.Focus()
	m.input.Prompt = prompt
	m.input.Placeholder = placeholder
	m.input.SetValue("")
	m.input.SetCursor(0)
}

func (m *model) restoreAfterSearchInput(returnState string) {
	m.searchInputMode = false
	switch returnState {
	case "subscriptions":
		m.commandMenu.active = false
		if !m.search.active && len(m.search.results) > 0 {
			m.search.active = true
		}
	case "episodes":
		m.commandMenu.active = false
		if !m.episodes.active && len(m.episodes.results) > 0 {
			m.episodes.active = true
		}
	default:
		m.refreshCounts()
		m.commandMenu.active = true
	}
	m.input.Blur()
}

func (m *model) backupSubscriptionList() {
	m.search.prevResults = append([]app.SearchResult(nil), m.search.results...)
	m.search.prevTitle = m.search.title
	m.search.prevHint = m.search.hint
	m.search.prevContext = m.search.context
	m.search.prevCursor = m.search.cursor
}

func (m *model) clearSubscriptionBackup() {
	m.search.prevResults = nil
	m.search.prevTitle = ""
	m.search.prevHint = ""
	m.search.prevContext = ""
	m.search.prevCursor = 0
}

func (m *model) restoreSubscriptionsView() bool {
	if len(m.search.prevResults) == 0 {
		return false
	}
	m.search.results = append([]app.SearchResult(nil), m.search.prevResults...)
	m.search.cursor = m.search.prevCursor
	if m.search.cursor >= len(m.search.results) {
		m.search.cursor = len(m.search.results) - 1
		if m.search.cursor < 0 {
			m.search.cursor = 0
		}
	}
	m.search.title = m.search.prevTitle
	m.search.hint = m.search.prevHint
	if m.search.prevContext != "" {
		m.search.context = m.search.prevContext
	} else {
		m.search.context = "subscriptions"
	}
	m.search.details = detailView{}
	m.search.active = true
	m.commandMenu.active = false
	m.searchParent = ""
	m.clearSubscriptionBackup()
	return true
}

func (m *model) exitSearchViewToMenu() {
	m.search.active = false
	m.search.results = nil
	m.search.title = ""
	m.search.hint = ""
	m.search.context = ""
	m.search.details = detailView{}
	m.searchParent = ""
	m.clearSubscriptionBackup()
	m.refreshCounts()
	m.commandMenu.active = true
	m.input.Blur()
}

func (m *model) backupEpisodeList() {
	m.episodes.previousResults = append([]app.EpisodeResult(nil), m.episodes.results...)
	m.episodes.previousCursor = m.episodes.cursor
	m.episodes.previousScroll = m.episodes.scroll
}

func (m *model) clearEpisodeBackup() {
	m.episodes.previousResults = nil
	m.episodes.previousCursor = 0
	m.episodes.previousScroll = 0
	m.episodes.showingSearch = false
}

func (m *model) restoreEpisodeList() bool {
	if len(m.episodes.previousResults) == 0 {
		return false
	}
	m.episodes.results = append([]app.EpisodeResult(nil), m.episodes.previousResults...)
	m.episodes.cursor = m.episodes.previousCursor
	if m.episodes.cursor >= len(m.episodes.results) {
		m.episodes.cursor = len(m.episodes.results) - 1
		if m.episodes.cursor < 0 {
			m.episodes.cursor = 0
		}
	}
	m.episodes.scroll = m.episodes.previousScroll
	m.episodes.details = episodeDetailView{}
	m.episodes.showingSearch = false
	m.clearEpisodeBackup()
	return true
}

func (m model) View() string {
	// If in command menu mode, render the menu
	if m.commandMenu.active {
		return m.renderCommandMenu()
	}

	// If in search input mode, render the search input
	if m.searchInputMode {
		var b strings.Builder
		title := "Search for Podcasts"
		hint := "Type a search query and press Enter. Submit blank to return."
		switch m.searchTarget {
		case "episodes":
			title = "Search Episodes"
			hint = "Type keywords and press Enter. Submit blank to return."
		case "podcasts":
			title = "Search for Podcasts"
			hint = "Type a search query and press Enter. Submit blank to return."
		}
		b.WriteString(m.theme.Header.Render(title))
		b.WriteString("\n")
		b.WriteString(m.theme.Dim.Render(hint))
		b.WriteString("\n\n")
		b.WriteString(m.input.View())
		b.WriteString("\n")
		return b.String()
	}

	// If in details mode, render the podcast details
	if m.search.details.active {
		return m.renderSearchDetails()
	}

	// If in search mode, render the interactive list
	if m.search.active {
		return m.renderSearchList()
	}

	if m.episodes.details.active {
		return m.renderEpisodeDetails()
	}

	// If in episode mode, render the episode list
	if m.episodes.active {
		return m.renderEpisodeList()
	}

	// If in queue mode, render the queue list
	if m.queue.active {
		return m.renderQueueList()
	}

	// If in downloads mode, render the downloads list
	if m.downloads.active {
		return m.renderDownloadsList()
	}

	// Fallback: should not reach here, return to menu
	return m.renderCommandMenu()
}

func (m model) handleCommandResult(result app.CommandResult) (tea.Model, tea.Cmd) {
	m.searchInputMode = false
	// Check if we got interactive search results
	if len(result.SearchResults) > 0 {
		m.search.active = true
		m.search.results = result.SearchResults
		m.search.cursor = 0
		m.search.title = result.SearchTitle
		m.search.hint = result.SearchHint
		m.search.context = result.SearchContext
		m.search.details = detailView{}
		m.input.Blur()
		return m, nil
	}

	// Check if we got interactive episode results
	if len(result.EpisodeResults) > 0 {
		if len(m.episodes.previousResults) > 0 {
			m.episodes.showingSearch = true
		}
		m.episodes.active = true
		m.episodes.results = result.EpisodeResults
		m.episodes.cursor = 0
		m.episodes.scroll = 0
		m.episodes.details.active = false
		m.episodes.details = episodeDetailView{}
		m.input.Blur()
		return m, nil
	}

	// Check if we got queued episode results (even if empty)
	if result.QueuedEpisodeResults != nil {
		m.queue.active = true
		m.queue.results = result.QueuedEpisodeResults
		m.queue.cursor = 0
		m.input.Blur()
		return m, nil
	}

	// Check if we got downloaded episode results (even if empty)
	if result.DownloadedEpisodeResults != nil {
		m.downloads.active = true
		m.downloads.results = result.DownloadedEpisodeResults
		m.downloads.danglingFiles = result.DanglingFiles
		m.downloads.cursor = 0
		m.downloads.scroll = 0
		m.input.Blur()
		return m, nil
	}

	if result.Quit {
		m.quitting = true
		return m, tea.Quit
	}

	// If we got here, the command returned a message with no special view
	// Return to the command menu
	m.refreshCounts()
	m.commandMenu.active = true
	m.input.Blur()
	return m, nil
}

func (m model) handleSearchSubscribe() (tea.Model, tea.Cmd) {
	var podcast itunes.Podcast
	var currentResult *app.SearchResult

	// Get podcast from either details mode or list mode
	if m.search.details.active {
		podcast = m.search.details.podcast.Podcast
		currentResult = &m.search.details.podcast
	} else if m.search.cursor < len(m.search.results) {
		podcast = m.search.results[m.search.cursor].Podcast
		currentResult = &m.search.results[m.search.cursor]
	} else {
		return m, nil
	}

	// Execute subscribe action
	_, err := m.app.SubscribePodcast(m.ctx, podcast)

	if err != nil {
		// Stay in current mode on error
		return m, nil
	}

	// Update subscription status in the current result
	if currentResult != nil {
		currentResult.IsSubscribed = true
		// If in details mode, update the detailsPodcast
		if m.search.details.active {
			m.search.details.podcast.IsSubscribed = true
		}
		// If in list mode, update the search results list
		if m.search.active && m.search.cursor < len(m.search.results) {
			m.search.results[m.search.cursor].IsSubscribed = true
		}
	}

	// Navigation logic after subscribe:
	// - If in details view, return to list view
	// - If in list view, stay in list view
	if m.search.details.active {
		m.search.details.active = false
		// Stay in search mode (list view)
	}
	// If in list view (not details mode), we do nothing - stay in list view

	return m, nil
}

func (m model) handleSearchUnsubscribe() (tea.Model, tea.Cmd) {
	var podcast itunes.Podcast
	var currentResult *app.SearchResult

	// Get podcast from either details mode or list mode
	if m.search.details.active {
		podcast = m.search.details.podcast.Podcast
		currentResult = &m.search.details.podcast
	} else if m.search.cursor < len(m.search.results) {
		podcast = m.search.results[m.search.cursor].Podcast
		currentResult = &m.search.results[m.search.cursor]
	} else {
		return m, nil
	}

	// Execute unsubscribe action
	_, err := m.app.UnsubscribePodcast(m.ctx, podcast.ID)

	if err != nil {
		// Stay in current mode on error
		return m, nil
	}

	// Update subscription status in the current result
	if currentResult != nil {
		currentResult.IsSubscribed = false
		// If in details mode, update the detailsPodcast
		if m.search.details.active {
			m.search.details.podcast.IsSubscribed = false
		}
		// If in list mode, update the search results list
		if m.search.active && m.search.cursor < len(m.search.results) {
			m.search.results[m.search.cursor].IsSubscribed = false
		}
	}

	// Navigation logic after unsubscribe:
	// - If in details view, return to list view
	// - If listing subscriptions, remove from the list
	// - Otherwise stay in list view
	if m.search.details.active {
		m.search.details.active = false
	}

	if m.search.context == "subscriptions" {
		if m.search.cursor < len(m.search.results) {
			m.search.results = append(m.search.results[:m.search.cursor], m.search.results[m.search.cursor+1:]...)
			if m.search.cursor >= len(m.search.results) && m.search.cursor > 0 {
				m.search.cursor--
			}
		}
		if len(m.search.results) == 0 {
			m.search.active = false
			m.search.title = ""
			m.search.hint = ""
			m.search.context = ""
			m.input.Focus()
		}
	}

	return m, nil
}

func (m model) renderSearchList() string {
	var b strings.Builder

	headerStyle := m.theme.Header
	cursorStyle := m.theme.Cursor
	normalStyle := m.theme.Normal
	dimStyle := m.theme.Dim
	subscribedStyle := m.theme.Subscribed
	unsubscribedStyle := m.theme.Unsubscribed

	title := m.search.title
	if title == "" {
		title = "Search Results"
	}
	hint := m.search.hint
	if hint == "" {
		hint = "Use ↑↓/jk to navigate, Enter for details, [s] subscribe, [u] unsubscribe, [x] to return, Esc to exit"
	}

	b.WriteString(headerStyle.Render(title))
	b.WriteString("\n")
	if hint != "" {
		b.WriteString(dimStyle.Render(hint))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	for i, result := range m.search.results {
		podcast := result.Podcast
		cursor := "  "

		// Choose style based on subscription status and cursor position
		var style lipgloss.Style
		if i == m.search.cursor {
			cursor = "→ "
			style = cursorStyle
		} else if result.IsSubscribed {
			style = subscribedStyle
		} else {
			style = unsubscribedStyle
		}

		// Truncate author if too long
		author := podcast.Author
		if m.search.context == "subscriptions" {
			author = fmt.Sprintf("new: %d | unplayed: %d | total: %d", result.NewCount, result.UnplayedCount, result.TotalCount)
		}
		if author == "" {
			author = "Unknown"
		}
		if len(author) > 40 {
			author = author[:37] + "..."
		}

		// Add subscription status suffix
		statusSuffix := ""
		if result.IsSubscribed {
			statusSuffix = " [subscribed]"
		}

		// Format: → Title (by Author) [subscribed]
		line := cursor + style.Render(podcast.Title) + normalStyle.Render(" (by "+author+")") + subscribedStyle.Render(statusSuffix)
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) renderSearchDetails() string {
	var b strings.Builder

	headerStyle := m.theme.Header
	normalStyle := m.theme.Normal
	dimStyle := m.theme.Dim
	subscribedStyle := m.theme.Subscribed
	descStyle := m.theme.Description

	podcast := m.search.details.podcast.Podcast

	b.WriteString(headerStyle.Render("Podcast Details"))
	b.WriteString("\n")
	if m.search.details.podcast.IsSubscribed {
		b.WriteString(dimStyle.Render("Press [u] to unsubscribe, [x]/Esc to return"))
	} else {
		b.WriteString(dimStyle.Render("Press [s] to subscribe, [x]/Esc to return"))
	}
	b.WriteString("\n\n")

	// Podcast title with subscription status
	statusSuffix := ""
	var titleStyle lipgloss.Style
	if m.search.details.podcast.IsSubscribed {
		statusSuffix = " [subscribed]"
		titleStyle = subscribedStyle
	} else {
		titleStyle = normalStyle.Bold(true)
	}
	b.WriteString(titleStyle.Render(podcast.Title) + subscribedStyle.Render(statusSuffix))
	b.WriteString("\n\n")

	// Author
	if podcast.Author != "" {
		b.WriteString(normalStyle.Render("Author: " + podcast.Author))
		b.WriteString("\n")
	}

	// Genre
	if podcast.Genre != "" {
		b.WriteString(normalStyle.Render("Genre: " + podcast.Genre))
		b.WriteString("\n")
	}

	if m.search.context == "subscriptions" {
		b.WriteString(normalStyle.Render(fmt.Sprintf("New: %d | Unplayed: %d | Total: %d", m.search.details.podcast.NewCount, m.search.details.podcast.UnplayedCount, m.search.details.podcast.TotalCount)))
		b.WriteString("\n")
	}

	// Language & Country
	if podcast.Language != "" || podcast.Country != "" {
		info := ""
		if podcast.Language != "" {
			info = "Language: " + podcast.Language
		}
		if podcast.Country != "" {
			if info != "" {
				info += " | "
			}
			info += "Country: " + podcast.Country
		}
		b.WriteString(normalStyle.Render(info))
		b.WriteString("\n")
	}

	// Description - show long description if available, otherwise show short description
	descToShow := podcast.LongDescription
	if descToShow == "" {
		descToShow = podcast.Description
	}

	if descToShow != "" {
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("Description:"))
		b.WriteString("\n")
		b.WriteString(descStyle.Render(descToShow))
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) renderEpisodeList() string {
	var b strings.Builder

	headerStyle := m.theme.Header
	cursorStyle := m.theme.Cursor
	normalStyle := m.theme.Normal
	dimStyle := m.theme.Dim
	dateStyle := m.theme.Date

	// Calculate window bounds
	cfg := m.app.Config()
	maxVisible := cfg.MaxEpisodes
	if maxVisible <= 0 {
		maxVisible = 12
	}

	// Filter episodes based on filter mode
	visibleResults := m.episodes.results
	if m.episodes.filterMode != "" {
		filtered := make([]app.EpisodeResult, 0, len(m.episodes.results))
		switch m.episodes.filterMode {
		case "all":
			visibleResults = m.episodes.results
		case "ignored":
			for _, result := range m.episodes.results {
				if result.Episode.State == "IGNORED" {
					filtered = append(filtered, result)
				}
			}
			visibleResults = filtered
		case "downloaded":
			for _, result := range m.episodes.results {
				if result.Episode.State == "DOWNLOADED" {
					filtered = append(filtered, result)
				}
			}
			visibleResults = filtered
		}
	} else {
		// Default: hide ignored episodes
		filtered := make([]app.EpisodeResult, 0, len(m.episodes.results))
		for _, result := range m.episodes.results {
			if result.Episode.State != "IGNORED" {
				filtered = append(filtered, result)
			}
		}
		visibleResults = filtered
	}

	totalEpisodes := len(visibleResults)
	start := m.episodes.scroll
	end := start + maxVisible
	if end > totalEpisodes {
		end = totalEpisodes
	}

	// Adjust scroll if it's out of bounds after filtering
	if start >= totalEpisodes && totalEpisodes > 0 {
		start = 0
		end = maxVisible
		if end > totalEpisodes {
			end = totalEpisodes
		}
	}

	// Header
	viewMode := "Episodes"
	switch m.episodes.filterMode {
	case "all":
		viewMode = "All Episodes"
	case "ignored":
		viewMode = "Ignored Episodes"
	case "downloaded":
		viewMode = "Downloaded Episodes"
	default:
		viewMode = "Episodes (hiding ignored)"
	}
	if totalEpisodes > 0 {
		if totalEpisodes > maxVisible {
			b.WriteString(headerStyle.Render(fmt.Sprintf("%s (Newest First) - showing %d-%d of %d", viewMode, start+1, end, totalEpisodes)))
		} else {
			b.WriteString(headerStyle.Render(fmt.Sprintf("%s (Newest First) - %d total", viewMode, totalEpisodes)))
		}
		b.WriteString("\n")
	} else {
		b.WriteString(headerStyle.Render("No episodes to display"))
		b.WriteString("\n")
	}
	b.WriteString(dimStyle.Render("Use ↑↓/jk to navigate, Enter for details, [i] ignore, [A] all, [I] ignored, [D] downloaded, [d] download, [s] search, [x]/Esc to exit"))
	b.WriteString("\n\n")

	// Column abbreviation settings
	podcastMaxLen := cfg.PodcastNameMaxLength
	if podcastMaxLen <= 0 {
		podcastMaxLen = 16
	}
	episodeMaxLen := cfg.EpisodeNameMaxLength
	if episodeMaxLen <= 0 {
		episodeMaxLen = 40
	}

	// Only render the visible window
	for i := start; i < end; i++ {
		result := visibleResults[i]
		ep := result.Episode
		cursor := "  "
		style := normalStyle

		if i == m.episodes.cursor {
			cursor = "→ "
			style = cursorStyle
		}

		// Format published date
		published := "Unknown   "
		if ep.HasPublish {
			published = ep.PublishedAt.Format("2006-01-02")
		}

		// Abbreviate podcast name
		podcastName := result.PodcastTitle
		if podcastName == "" {
			podcastName = "Unknown"
		}
		if len(podcastName) > podcastMaxLen {
			podcastName = podcastName[:podcastMaxLen-3] + "..."
		}
		// Pad to fixed width for alignment
		podcastName = fmt.Sprintf("%-*s", podcastMaxLen, podcastName)

		// Abbreviate episode title
		episodeTitle := ep.Title
		if len(episodeTitle) > episodeMaxLen {
			episodeTitle = episodeTitle[:episodeMaxLen-3] + "..."
		}

		// Format size in MB
		var sizeStr string
		if ep.SizeBytes > 0 {
			sizeMB := float64(ep.SizeBytes) / (1024 * 1024)
			sizeStr = fmt.Sprintf("%6.1f MB", sizeMB)
		} else {
			sizeStr = "       --"
		}

		// Format: → DATE PODCAST_NAME EPISODE_TITLE SIZE
		line := cursor + dateStyle.Render(published) + " " +
			dimStyle.Render(podcastName) + " " + style.Render(episodeTitle) + " " +
			dimStyle.Render(sizeStr)

		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) renderQueueList() string {
	var b strings.Builder

	headerStyle := m.theme.Header
	cursorStyle := m.theme.Cursor
	normalStyle := m.theme.Normal
	dimStyle := m.theme.Dim
	dateStyle := m.theme.Date

	totalQueued := len(m.queue.results)

	// Header
	if totalQueued > 0 {
		b.WriteString(headerStyle.Render(fmt.Sprintf("Download Queue - %d episode(s)", totalQueued)))
		b.WriteString("\n")
	} else {
		b.WriteString(headerStyle.Render("Download Queue - Empty"))
		b.WriteString("\n")
	}
	b.WriteString(dimStyle.Render("Use ↑↓/jk to navigate, [x]/Esc to return to main menu"))
	b.WriteString("\n\n")

	// Column abbreviation settings
	cfg := m.app.Config()
	podcastMaxLen := cfg.PodcastNameMaxLength
	if podcastMaxLen <= 0 {
		podcastMaxLen = 16
	}
	episodeMaxLen := cfg.EpisodeNameMaxLength
	if episodeMaxLen <= 0 {
		episodeMaxLen = 40
	}

	for i, result := range m.queue.results {
		ep := result.Episode
		cursor := "  "
		style := normalStyle

		if i == m.queue.cursor {
			cursor = "→ "
			style = cursorStyle
		}

		// Format enqueued time
		enqueued := "Unknown   "
		if !result.EnqueuedAt.IsZero() {
			enqueued = result.EnqueuedAt.Format("2006-01-02")
		}

		// Abbreviate podcast name
		podcastName := result.PodcastTitle
		if podcastName == "" {
			podcastName = "Unknown"
		}
		if len(podcastName) > podcastMaxLen {
			podcastName = podcastName[:podcastMaxLen-3] + "..."
		}
		// Pad to fixed width for alignment
		podcastName = fmt.Sprintf("%-*s", podcastMaxLen, podcastName)

		// Abbreviate episode title
		episodeTitle := ep.Title
		if len(episodeTitle) > episodeMaxLen {
			episodeTitle = episodeTitle[:episodeMaxLen-3] + "..."
		}

		// Format status
		var statusStr string
		if result.RetryCount > 0 {
			statusStr = fmt.Sprintf("Error (retries: %d)", result.RetryCount)
		} else {
			statusStr = "Queued"
		}
		statusStr = fmt.Sprintf("%-20s", statusStr)

		// Format: → DATE PODCAST_NAME EPISODE_TITLE STATUS
		line := cursor + dateStyle.Render(enqueued) + " " +
			dimStyle.Render(podcastName) + " " + style.Render(episodeTitle) + " " +
			dimStyle.Render(statusStr)

		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) renderDownloadsList() string {
	var b strings.Builder

	headerStyle := m.theme.Header
	cursorStyle := m.theme.Cursor
	normalStyle := m.theme.Normal
	dimStyle := m.theme.Dim
	dateStyle := m.theme.Date

	// Calculate window bounds
	cfg := m.app.Config()
	maxVisible := cfg.MaxEpisodes
	if maxVisible <= 0 {
		maxVisible = 12
	}

	totalDownloaded := len(m.downloads.results)
	start := m.downloads.scroll
	end := start + maxVisible
	if end > totalDownloaded {
		end = totalDownloaded
	}

	// Adjust scroll if it's out of bounds
	if start >= totalDownloaded && totalDownloaded > 0 {
		start = 0
		end = maxVisible
		if end > totalDownloaded {
			end = totalDownloaded
		}
	}

	// Header
	if totalDownloaded > 0 {
		if totalDownloaded > maxVisible {
			b.WriteString(headerStyle.Render(fmt.Sprintf("Downloaded Episodes - showing %d-%d of %d", start+1, end, totalDownloaded)))
		} else {
			b.WriteString(headerStyle.Render(fmt.Sprintf("Downloaded Episodes - %d total", totalDownloaded)))
		}
		b.WriteString("\n")
	} else {
		b.WriteString(headerStyle.Render("Downloaded Episodes - Empty"))
		b.WriteString("\n")
	}
	b.WriteString(dimStyle.Render("Use ↑↓/jk to navigate, [x]/Esc to return to main menu"))
	b.WriteString("\n\n")

	// Column abbreviation settings
	podcastMaxLen := cfg.PodcastNameMaxLength
	if podcastMaxLen <= 0 {
		podcastMaxLen = 16
	}
	episodeMaxLen := cfg.EpisodeNameMaxLength
	if episodeMaxLen <= 0 {
		episodeMaxLen = 40
	}

	// Only render the visible window
	for i := start; i < end; i++ {
		result := m.downloads.results[i]
		ep := result.Episode
		cursor := "  "
		style := normalStyle

		if i == m.downloads.cursor {
			cursor = "→ "
			style = cursorStyle
		}

		// Format published date
		published := "Unknown   "
		if ep.HasPublish {
			published = ep.PublishedAt.Format("2006-01-02")
		}

		// Abbreviate podcast name
		podcastName := result.PodcastTitle
		if podcastName == "" {
			podcastName = "Unknown"
		}
		if len(podcastName) > podcastMaxLen {
			podcastName = podcastName[:podcastMaxLen-3] + "..."
		}
		// Pad to fixed width for alignment
		podcastName = fmt.Sprintf("%-*s", podcastMaxLen, podcastName)

		// Abbreviate episode title
		episodeTitle := ep.Title
		if len(episodeTitle) > episodeMaxLen {
			episodeTitle = episodeTitle[:episodeMaxLen-3] + "..."
		}

		// Format size in MB
		var sizeStr string
		if ep.SizeBytes > 0 {
			sizeMB := float64(ep.SizeBytes) / (1024 * 1024)
			sizeStr = fmt.Sprintf("%6.1f MB", sizeMB)
		} else {
			sizeStr = "       --"
		}

		// Add state indicator (DOWNLOADED vs DELETED)
		stateIndicator := ""
		if ep.State == "DELETED" {
			stateIndicator = " [DELETED]"
		}

		// Format: → DATE PODCAST_NAME EPISODE_TITLE SIZE [DELETED]
		line := cursor + dateStyle.Render(published) + " " +
			dimStyle.Render(podcastName) + " " + style.Render(episodeTitle) + " " +
			dimStyle.Render(sizeStr) + dimStyle.Render(stateIndicator)

		b.WriteString(line)
		b.WriteString("\n")
	}

	// Display dangling files section if any
	if len(m.downloads.danglingFiles) > 0 {
		b.WriteString("\n")
		b.WriteString(headerStyle.Render(fmt.Sprintf("Dangling Files - %d untracked file(s)", len(m.downloads.danglingFiles))))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Files in download directory not tracked in database:"))
		b.WriteString("\n\n")

		for _, file := range m.downloads.danglingFiles {
			sizeMB := float64(file.SizeBytes) / (1024 * 1024)
			line := dimStyle.Render("  ") + normalStyle.Render(file.Path) + " " + dimStyle.Render(fmt.Sprintf("(%6.1f MB)", sizeMB))
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (m model) renderEpisodeDetails() string {
	var b strings.Builder

	detail := m.episodes.details.detail
	headerStyle := m.theme.Header
	normalStyle := m.theme.Normal
	dimStyle := m.theme.Dim
	stateStyle := m.theme.State
	dateStyle := m.theme.Date

	b.WriteString(headerStyle.Render(detail.Title))
	b.WriteString("\n\n")

	if detail.PodcastTitle != "" {
		b.WriteString(normalStyle.Render(fmt.Sprintf("Podcast: %s (%s)", detail.PodcastTitle, detail.PodcastID)))
		b.WriteString("\n")
	}

	b.WriteString(stateStyle.Render(fmt.Sprintf("State: %s", detail.State)))
	b.WriteString("\n")

	if detail.HasPublish {
		b.WriteString(dateStyle.Render("Published: " + detail.PublishedAt.Format("2006-01-02 15:04")))
		b.WriteString("\n")
	}

	if detail.SizeBytes > 0 {
		sizeMB := float64(detail.SizeBytes) / (1024 * 1024)
		b.WriteString(normalStyle.Render(fmt.Sprintf("Size: %.1f MB", sizeMB)))
		b.WriteString("\n")
	}

	if detail.FilePath != "" {
		b.WriteString(normalStyle.Render("Downloaded to: " + detail.FilePath))
		b.WriteString("\n")
	} else {
		b.WriteString(dimStyle.Render("Not downloaded yet"))
		b.WriteString("\n")
	}

	if detail.EnclosureURL != "" {
		b.WriteString(dimStyle.Render("Source: " + detail.EnclosureURL))
		b.WriteString("\n")
	}

	if len(m.episodes.details.lines) > 0 {
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("Description:"))
		b.WriteString("\n")

		maxLines := m.maxEpisodeDescriptionLines()
		if maxLines <= 0 {
			maxLines = 12
		}
		totalLines := len(m.episodes.details.lines)
		start := m.episodes.details.scroll
		maxOffset := totalLines - maxLines
		if maxOffset < 0 {
			maxOffset = 0
		}
		if start > maxOffset {
			start = maxOffset
		}
		if start < 0 {
			start = 0
		}
		end := start + maxLines
		if end > totalLines {
			end = totalLines
		}

		for i := start; i < end; i++ {
			b.WriteString(normalStyle.Render(m.episodes.details.lines[i]))
			b.WriteString("\n")
		}

		if totalLines > maxLines {
			b.WriteString(dimStyle.Render(fmt.Sprintf("Showing lines %d-%d of %d. Use ↑↓/jk to scroll.", start+1, end, totalLines)))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Use ↑↓/jk to scroll. Press [x]/Esc to return to the episode list."))
	b.WriteString("\n")

	return b.String()
}

func (m *model) enterEpisodeDetails(detail app.EpisodeDetail) {
	m.episodes.details.active = true
	m.episodes.details.detail = detail
	m.episodes.details.scroll = 0
	m.episodes.details.lines = formatEpisodeDescription(detail.Description, m.width)
}

func (m model) maxEpisodeDescriptionLines() int {
	if m.app == nil {
		return 12
	}
	maxLines := m.app.Config().MaxEpisodeDescriptionLines
	if maxLines <= 0 {
		maxLines = 12
	}
	return maxLines
}

func (m *model) adjustEpisodeDetailScroll(delta int) {
	total := len(m.episodes.details.lines)
	if total == 0 {
		m.episodes.details.scroll = 0
		return
	}
	maxLines := m.maxEpisodeDescriptionLines()
	if maxLines <= 0 {
		maxLines = 12
	}
	maxOffset := total - maxLines
	if maxOffset < 0 {
		maxOffset = 0
	}
	newScroll := m.episodes.details.scroll + delta
	if newScroll < 0 {
		newScroll = 0
	}
	if newScroll > maxOffset {
		newScroll = maxOffset
	}
	m.episodes.details.scroll = newScroll
}

func formatEpisodeDescription(desc string, width int) []string {
	cleaned := strings.TrimSpace(desc)
	if cleaned == "" {
		return nil
	}

	plainText, err := html2text.FromString(cleaned, html2text.Options{
		PrettyTables: true,
		OmitLinks:    false,
	})
	if err == nil {
		cleaned = strings.TrimSpace(plainText)
	}
	if cleaned == "" {
		return nil
	}

	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "\n")

	lines := strings.Split(cleaned, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}

	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	// Wrap lines at terminal width
	if width <= 0 {
		width = 80 // Default width
	}
	wrappedLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			wrappedLines = append(wrappedLines, line)
			continue
		}
		wrapped := wrapLine(line, width)
		wrappedLines = append(wrappedLines, wrapped...)
	}

	return wrappedLines
}

// wrapLine wraps a single line at the specified width
func wrapLine(line string, width int) []string {
	if len(line) <= width {
		return []string{line}
	}

	var result []string
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{line}
	}

	currentLine := ""
	for _, word := range words {
		// If word itself is longer than width, break it
		if len(word) > width {
			if currentLine != "" {
				result = append(result, currentLine)
				currentLine = ""
			}
			// Break long word across lines
			for len(word) > width {
				result = append(result, word[:width])
				word = word[width:]
			}
			if word != "" {
				currentLine = word
			}
			continue
		}

		// Try adding word to current line
		testLine := currentLine
		if testLine != "" {
			testLine += " "
		}
		testLine += word

		if len(testLine) <= width {
			currentLine = testLine
		} else {
			// Word doesn't fit, start new line
			if currentLine != "" {
				result = append(result, currentLine)
			}
			currentLine = word
		}
	}

	if currentLine != "" {
		result = append(result, currentLine)
	}

	return result
}

func (m *model) refreshCounts() {
	// Fetch queue count
	if count, err := m.app.CountQueued(m.ctx); err == nil {
		m.queueCount = count
	}
	// Fetch downloads count
	if count, err := m.app.CountDownloaded(m.ctx); err == nil {
		m.downloadsCount = count
	}
}

func (m model) renderCommandMenu() string {
	var b strings.Builder

	headerStyle := m.theme.Header
	cursorStyle := m.theme.Cursor
	normalStyle := m.theme.Normal
	dimStyle := m.theme.Dim

	b.WriteString(headerStyle.Render("Podsink - Podcast Manager"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Use ↑↓/jk to navigate, Enter to select, [p]odcasts [e]pisodes [q]ueue [d]ownloads [c]onfig, ESC/[x] to exit"))
	b.WriteString("\n\n")

	for i, item := range m.commandMenu.items {
		cursor := "  "
		style := normalStyle

		if i == m.commandMenu.cursor {
			cursor = "→ "
			style = cursorStyle
		}

		// Format: → [p] podcasts - List all podcast subscriptions
		shorthand := item.shorthand
		if shorthand == "" {
			shorthand = "   "
		} else {
			shorthand = shorthand + " "
		}

		// Add counts for queue and downloads
		usage := item.usage
		if item.name == "queue" && m.queueCount > 0 {
			usage = fmt.Sprintf("%s (%d)", item.usage, m.queueCount)
		} else if item.name == "downloads" && m.downloadsCount > 0 {
			usage = fmt.Sprintf("%s (%d)", item.usage, m.downloadsCount)
		}

		line := cursor + dimStyle.Render(shorthand) + style.Render(usage)
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}
