package theme

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme captures the lipgloss styles used across the TUI.
type Theme struct {
	Message      lipgloss.Style
	Header       lipgloss.Style
	Cursor       lipgloss.Style
	Normal       lipgloss.Style
	Dim          lipgloss.Style
	Subscribed   lipgloss.Style
	Unsubscribed lipgloss.Style
	Description  lipgloss.Style
	State        lipgloss.Style
	Date         lipgloss.Style
	Error        lipgloss.Style
}

// Default is the canonical name of the built-in default theme.
const Default = "default"

var themes = map[string]Theme{
	Default: {
		Message:      lipgloss.NewStyle().Foreground(lipgloss.Color("69")),
		Header:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99")),
		Cursor:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")),
		Normal:       lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		Dim:          lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Subscribed:   lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Bold(true),
		Unsubscribed: lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		Description:  lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Italic(true),
		State:        lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		Date:         lipgloss.NewStyle().Foreground(lipgloss.Color("246")),
		Error:        lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
	},
	"high_contrast": {
		Message:      lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true),
		Header:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")),
		Cursor:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196")),
		Normal:       lipgloss.NewStyle().Foreground(lipgloss.Color("15")),
		Dim:          lipgloss.NewStyle().Foreground(lipgloss.Color("7")),
		Subscribed:   lipgloss.NewStyle().Foreground(lipgloss.Color("118")).Bold(true),
		Unsubscribed: lipgloss.NewStyle().Foreground(lipgloss.Color("15")),
		Description:  lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Italic(true),
		State:        lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true),
		Date:         lipgloss.NewStyle().Foreground(lipgloss.Color("33")),
		Error:        lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true),
	},
}

// Names returns the sorted list of available theme names.
func Names() []string {
	names := make([]string, 0, len(themes))
	for name := range themes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ForName returns the theme with the provided name, defaulting if unknown.
func ForName(name string) Theme {
	key := strings.ToLower(strings.TrimSpace(name))
	if theme, ok := themes[key]; ok {
		return theme
	}
	return themes[Default]
}
