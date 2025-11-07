package repl

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"podsink/internal/app"
)

// Run starts the interactive REPL session.
func Run(ctx context.Context, application *app.App) error {
	program := tea.NewProgram(newModel(ctx, application), tea.WithContext(ctx))
	_, err := program.Run()
	return err
}
