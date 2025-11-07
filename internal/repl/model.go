package repl

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"podsink/internal/app"
)

type model struct {
	ctx          context.Context
	app          *app.App
	input        textinput.Model
	history      []string
	messages     []string
	quitting     bool
	completions  []string
	completionIdx int
}

func newModel(ctx context.Context, application *app.App) model {
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
		messages: []string{
			lipgloss.NewStyle().Foreground(lipgloss.Color("69")).Render("Podsink CLI ready. Type 'help' for assistance."),
		},
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
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
		m.messages = append(m.messages, lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(err.Error()))
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
