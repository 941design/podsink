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

type model struct {
	ctx           context.Context
	app           *app.App
	input         textinput.Model
	history       []string
	messages      []string
	quitting      bool
	completions   []string
	completionIdx int
	theme         theme.Theme

	// Interactive search list state
	searchMode    bool
	searchResults []app.SearchResult
	searchCursor  int
	expandedIndex int // -1 means nothing expanded
	searchTitle   string
	searchHint    string
	searchContext string

	// Interactive search details state
	detailsMode    bool // When true, show single podcast details
	detailsPodcast app.SearchResult

	// Interactive episode list state
	episodeMode         bool
	episodeResults      []app.EpisodeResult
	episodeCursor       int
	episodeScroll       int // Scroll offset for windowed view
	episodeDetailsMode  bool
	episodeDetail       app.EpisodeDetail
	episodeDetailScroll int
	episodeDetailLines  []string

	// Session-level cache for long descriptions
	longDescCache map[string]string // key: podcast ID, value: long description
}

func newModel(ctx context.Context, application *app.App) model {
	cfg := application.Config()
	th := theme.ForName(cfg.ColorTheme)
	ti := textinput.New()
	ti.Placeholder = "help"
	ti.Focus()
	ti.Prompt = "podsink> "
	ti.CharLimit = 512
	ti.Width = 80

	return model{
		ctx:     ctx,
		app:     application,
		input:   ti,
		history: make([]string, 0, 32),
		theme:   th,
		messages: []string{
			th.Message.Render("Podsink CLI ready. Type 'help' for assistance."),
		},
		longDescCache: make(map[string]string),
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle search details mode navigation
		if m.detailsMode {
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "esc", "x":
				// Exit details mode, return to search list
				m.detailsMode = false
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

		if m.episodeDetailsMode {
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "esc", "x":
				m.episodeDetailsMode = false
				m.episodeDetailScroll = 0
				m.episodeDetailLines = nil
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
				if total := len(m.episodeDetailLines); total > 0 {
					max := m.maxEpisodeDescriptionLines()
					if max <= 0 {
						max = 12
					}
					maxOffset := total - max
					if maxOffset < 0 {
						maxOffset = 0
					}
					m.episodeDetailScroll = maxOffset
				}
				return m, nil
			case "home":
				m.episodeDetailScroll = 0
				return m, nil
			}
			return m, nil
		}

		// Handle search mode navigation
		if m.searchMode {
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "esc", "q", "x":
				// Exit search mode
				m.searchMode = false
				m.searchResults = nil
				m.expandedIndex = -1
				m.searchTitle = ""
				m.searchHint = ""
				m.searchContext = ""
				m.input.Focus()
				return m, nil
			case "up", "k":
				if m.searchCursor > 0 {
					m.searchCursor--
				}
				return m, nil
			case "down", "j":
				if m.searchCursor < len(m.searchResults)-1 {
					m.searchCursor++
				}
				return m, nil
			case "enter":
				// Enter details mode for selected podcast
				if m.searchCursor < len(m.searchResults) {
					m.detailsMode = true
					m.detailsPodcast = m.searchResults[m.searchCursor]

					// Fetch long description if not already cached
					podcastID := m.detailsPodcast.Podcast.ID
					if _, cached := m.longDescCache[podcastID]; !cached {
						// Try to fetch the full podcast details from iTunes API
						if fullPodcast, err := m.app.LookupPodcast(m.ctx, podcastID); err == nil {
							m.longDescCache[podcastID] = fullPodcast.LongDescription
							// Update the current podcast with the long description
							m.detailsPodcast.Podcast.LongDescription = fullPodcast.LongDescription
						}
					} else {
						// Use cached long description
						m.detailsPodcast.Podcast.LongDescription = m.longDescCache[podcastID]
					}
				}
				return m, nil
			case "s":
				// Subscribe directly from list view
				return m.handleSearchSubscribe()
			case "u":
				// Unsubscribe directly from list view
				return m.handleSearchUnsubscribe()
			}
			return m, nil
		}

		// Handle episode mode navigation
		if m.episodeMode {
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "esc", "q", "x":
				// Exit episode mode
				m.episodeMode = false
				m.episodeResults = nil
				m.episodeDetailsMode = false
				m.episodeCursor = 0
				m.episodeScroll = 0
				m.input.Focus()
				return m, nil
			case "enter":
				if m.episodeCursor < len(m.episodeResults) {
					selected := m.episodeResults[m.episodeCursor]
					detail, err := m.app.EpisodeDetails(m.ctx, selected.Episode.ID)
					if err != nil {
						m.messages = append(m.messages, m.theme.Error.Render(err.Error()))
						return m, nil
					}
					m.enterEpisodeDetails(detail)
				}
				return m, nil
			case "up", "k":
				if m.episodeCursor > 0 {
					m.episodeCursor--
					// Scroll up when cursor moves above visible window
					cfg := m.app.Config()
					maxVisible := cfg.MaxEpisodes
					if maxVisible <= 0 {
						maxVisible = 12
					}
					if m.episodeCursor < m.episodeScroll {
						m.episodeScroll = m.episodeCursor
					}
				}
				return m, nil
			case "down", "j":
				if m.episodeCursor < len(m.episodeResults)-1 {
					m.episodeCursor++
					// Scroll down when cursor moves below visible window
					cfg := m.app.Config()
					maxVisible := cfg.MaxEpisodes
					if maxVisible <= 0 {
						maxVisible = 12
					}
					if m.episodeCursor >= m.episodeScroll+maxVisible {
						m.episodeScroll = m.episodeCursor - maxVisible + 1
					}
				}
				return m, nil
			}
		}

		// Normal mode key handling
		switch msg.Type {
		case tea.KeyCtrlC:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyEnter:
			m.completions = nil
			m.completionIdx = 0
			return m.handleSubmit()
		case tea.KeyTab:
			return m.handleTabComplete()
		default:
			// Reset completions on any other key
			m.completions = nil
			m.completionIdx = 0
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() string {
	// If in details mode, render the podcast details
	if m.detailsMode {
		return m.renderSearchDetails()
	}

	// If in search mode, render the interactive list
	if m.searchMode {
		return m.renderSearchList()
	}

	if m.episodeDetailsMode {
		return m.renderEpisodeDetails()
	}

	// If in episode mode, render the episode list
	if m.episodeMode {
		return m.renderEpisodeList()
	}

	// Normal mode: render messages and input
	var b strings.Builder
	for _, message := range m.messages {
		b.WriteString(message)
		b.WriteString("\n")
	}
	b.WriteString(m.input.View())
	if !m.quitting {
		b.WriteString("\n")
	}
	return b.String()
}

func (m model) handleSubmit() (tea.Model, tea.Cmd) {
	command := strings.TrimSpace(m.input.Value())
	if command != "" {
		m.history = append(m.history, command)
	}
	m.input.SetValue("")

	if command == "" {
		return m, nil
	}

	result, err := m.app.Execute(m.ctx, command)
	if err != nil {
		m.messages = append(m.messages, m.theme.Error.Render(err.Error()))
		return m, nil
	}

	// Check if we got interactive search results
	if len(result.SearchResults) > 0 {
		m.searchMode = true
		m.searchResults = result.SearchResults
		m.searchCursor = 0
		m.expandedIndex = -1
		m.searchTitle = result.SearchTitle
		m.searchHint = result.SearchHint
		m.searchContext = result.SearchContext
		m.input.Blur()
		return m, nil
	}

	// Check if we got interactive episode results
	if len(result.EpisodeResults) > 0 {
		m.episodeMode = true
		m.episodeResults = result.EpisodeResults
		m.episodeCursor = 0
		m.episodeScroll = 0
		m.episodeDetailsMode = false
		m.input.Blur()
		return m, nil
	}

	if result.Message != "" {
		m.messages = append(m.messages, result.Message)
	}

	if result.Quit {
		m.quitting = true
		return m, tea.Quit
	}

	return m, nil
}

func (m model) handleTabComplete() (tea.Model, tea.Cmd) {
	input := m.input.Value()
	words := strings.Fields(input)
	if len(words) == 0 {
		return m, nil
	}

	// Only complete the first word (command name)
	if len(words) > 1 {
		return m, nil
	}

	prefix := words[0]

	// Build or cycle through completions
	if m.completions == nil {
		commandNames := m.app.CommandNames()
		for _, name := range commandNames {
			if strings.HasPrefix(name, prefix) {
				m.completions = append(m.completions, name)
			}
		}
		if len(m.completions) == 0 {
			return m, nil
		}
		m.completionIdx = 0
	} else {
		// Cycle to next completion
		m.completionIdx = (m.completionIdx + 1) % len(m.completions)
	}

	// Apply completion
	m.input.SetValue(m.completions[m.completionIdx])
	m.input.SetCursor(len(m.completions[m.completionIdx]))

	return m, nil
}

func (m model) handleSearchSubscribe() (tea.Model, tea.Cmd) {
	var podcast itunes.Podcast
	var currentResult *app.SearchResult

	// Get podcast from either details mode or list mode
	if m.detailsMode {
		podcast = m.detailsPodcast.Podcast
		currentResult = &m.detailsPodcast
	} else if m.searchCursor < len(m.searchResults) {
		podcast = m.searchResults[m.searchCursor].Podcast
		currentResult = &m.searchResults[m.searchCursor]
	} else {
		return m, nil
	}

	// Execute subscribe action
	result, err := m.app.SubscribePodcast(m.ctx, podcast)

	if err != nil {
		m.messages = append(m.messages, m.theme.Error.Render(err.Error()))
		// Stay in current mode on error
		return m, nil
	}

	if result.Message != "" {
		m.messages = append(m.messages, result.Message)
	}

	// Update subscription status in the current result
	if currentResult != nil {
		currentResult.IsSubscribed = true
		// If in details mode, update the detailsPodcast
		if m.detailsMode {
			m.detailsPodcast.IsSubscribed = true
		}
		// If in list mode, update the search results list
		if m.searchMode && m.searchCursor < len(m.searchResults) {
			m.searchResults[m.searchCursor].IsSubscribed = true
		}
	}

	// Navigation logic after subscribe:
	// - If in details view, return to list view
	// - If in list view, stay in list view
	if m.detailsMode {
		m.detailsMode = false
		// Stay in search mode (list view)
	}
	// If in list view (not details mode), we do nothing - stay in list view

	return m, nil
}

func (m model) handleSearchUnsubscribe() (tea.Model, tea.Cmd) {
	var podcast itunes.Podcast
	var currentResult *app.SearchResult

	// Get podcast from either details mode or list mode
	if m.detailsMode {
		podcast = m.detailsPodcast.Podcast
		currentResult = &m.detailsPodcast
	} else if m.searchCursor < len(m.searchResults) {
		podcast = m.searchResults[m.searchCursor].Podcast
		currentResult = &m.searchResults[m.searchCursor]
	} else {
		return m, nil
	}

	// Execute unsubscribe action
	result, err := m.app.UnsubscribePodcast(m.ctx, podcast.ID)

	if err != nil {
		m.messages = append(m.messages, m.theme.Error.Render(err.Error()))
		// Stay in current mode on error
		return m, nil
	}

	if result.Message != "" {
		m.messages = append(m.messages, result.Message)
	}

	// Update subscription status in the current result
	if currentResult != nil {
		currentResult.IsSubscribed = false
		// If in details mode, update the detailsPodcast
		if m.detailsMode {
			m.detailsPodcast.IsSubscribed = false
		}
		// If in list mode, update the search results list
		if m.searchMode && m.searchCursor < len(m.searchResults) {
			m.searchResults[m.searchCursor].IsSubscribed = false
		}
	}

	// Navigation logic after unsubscribe:
	// - If in details view, return to list view
	// - If listing subscriptions, remove from the list
	// - Otherwise stay in list view
	if m.detailsMode {
		m.detailsMode = false
	}

	if m.searchContext == "subscriptions" {
		if m.searchCursor < len(m.searchResults) {
			m.searchResults = append(m.searchResults[:m.searchCursor], m.searchResults[m.searchCursor+1:]...)
			if m.searchCursor >= len(m.searchResults) && m.searchCursor > 0 {
				m.searchCursor--
			}
		}
		if len(m.searchResults) == 0 {
			m.searchMode = false
			m.searchTitle = ""
			m.searchHint = ""
			m.searchContext = ""
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

	title := m.searchTitle
	if title == "" {
		title = "Search Results"
	}
	hint := m.searchHint
	if hint == "" {
		hint = "Use ↑↓/jk to navigate, Enter for details, [s] subscribe, [u] unsubscribe, [x]/Esc to exit"
	}

	b.WriteString(headerStyle.Render(title))
	b.WriteString("\n")
	if hint != "" {
		b.WriteString(dimStyle.Render(hint))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	for i, result := range m.searchResults {
		podcast := result.Podcast
		cursor := "  "

		// Choose style based on subscription status and cursor position
		var style lipgloss.Style
		if i == m.searchCursor {
			cursor = "→ "
			style = cursorStyle
		} else if result.IsSubscribed {
			style = subscribedStyle
		} else {
			style = unsubscribedStyle
		}

		// Truncate author if too long
		author := podcast.Author
		if m.searchContext == "subscriptions" {
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

	podcast := m.detailsPodcast.Podcast

	b.WriteString(headerStyle.Render("Podcast Details"))
	b.WriteString("\n")
	if m.detailsPodcast.IsSubscribed {
		b.WriteString(dimStyle.Render("Press [u] to unsubscribe, [x]/Esc to return"))
	} else {
		b.WriteString(dimStyle.Render("Press [s] to subscribe, [x]/Esc to return"))
	}
	b.WriteString("\n\n")

	// Podcast title with subscription status
	statusSuffix := ""
	var titleStyle lipgloss.Style
	if m.detailsPodcast.IsSubscribed {
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

	if m.searchContext == "subscriptions" {
		b.WriteString(normalStyle.Render(fmt.Sprintf("New: %d | Unplayed: %d | Total: %d", m.detailsPodcast.NewCount, m.detailsPodcast.UnplayedCount, m.detailsPodcast.TotalCount)))
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
	stateStyle := m.theme.State
	dateStyle := m.theme.Date

	// Calculate window bounds
	cfg := m.app.Config()
	maxVisible := cfg.MaxEpisodes
	if maxVisible <= 0 {
		maxVisible = 12
	}

	totalEpisodes := len(m.episodeResults)
	start := m.episodeScroll
	end := start + maxVisible
	if end > totalEpisodes {
		end = totalEpisodes
	}

	if totalEpisodes > 0 {
		if totalEpisodes > maxVisible {
			b.WriteString(headerStyle.Render(fmt.Sprintf("All Episodes (Newest First) - showing %d-%d of %d", start+1, end, totalEpisodes)))
		} else {
			b.WriteString(headerStyle.Render(fmt.Sprintf("All Episodes (Newest First) - %d total", totalEpisodes)))
		}
		b.WriteString("\n")
	}
	b.WriteString(dimStyle.Render("Use ↑↓/jk to navigate, Enter for details, [x]/Esc to exit"))
	b.WriteString("\n\n")

	// Only render the visible window
	for i := start; i < end; i++ {
		result := m.episodeResults[i]
		ep := result.Episode
		cursor := "  "
		style := normalStyle

		if i == m.episodeCursor {
			cursor = "→ "
			style = cursorStyle
		}

		// Format published date
		published := "Unknown"
		if ep.HasPublish {
			published = ep.PublishedAt.Format("2006-01-02")
		}

		// Format: → [STATE] 2024-01-01 Episode Title
		line := cursor + stateStyle.Render(fmt.Sprintf("[%-11s]", ep.State)) + " " +
			dateStyle.Render(published) + " " + style.Render(ep.Title)
		if result.PodcastTitle != "" {
			line += " " + dimStyle.Render("· "+result.PodcastTitle)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) renderEpisodeDetails() string {
	var b strings.Builder

	detail := m.episodeDetail
	headerStyle := m.theme.Header
	normalStyle := m.theme.Normal
	dimStyle := m.theme.Dim
	stateStyle := m.theme.State
	dateStyle := m.theme.Date

	b.WriteString(headerStyle.Render(detail.Title))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("Episode ID: %s", detail.ID)))
	b.WriteString("\n")

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

	if len(m.episodeDetailLines) > 0 {
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("Description:"))
		b.WriteString("\n")

		maxLines := m.maxEpisodeDescriptionLines()
		if maxLines <= 0 {
			maxLines = 12
		}
		totalLines := len(m.episodeDetailLines)
		start := m.episodeDetailScroll
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
			b.WriteString(normalStyle.Render(m.episodeDetailLines[i]))
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
	m.episodeDetailsMode = true
	m.episodeDetail = detail
	m.episodeDetailScroll = 0
	m.episodeDetailLines = formatEpisodeDescription(detail.Description)
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
	total := len(m.episodeDetailLines)
	if total == 0 {
		m.episodeDetailScroll = 0
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
	newScroll := m.episodeDetailScroll + delta
	if newScroll < 0 {
		newScroll = 0
	}
	if newScroll > maxOffset {
		newScroll = maxOffset
	}
	m.episodeDetailScroll = newScroll
}

func formatEpisodeDescription(desc string) []string {
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

	return lines
}
