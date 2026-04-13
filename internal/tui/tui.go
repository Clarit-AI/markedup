package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"github.com/KHAEntertainment/markedup/index"
)

// Run launches the TUI with the given knowledge index. It returns an error
// if stdout is not a terminal or if the TUI encounters a fatal error.
// The TUI package has no dependency on cobra — it receives only the index.
func Run(idx *index.KnowledgeIndex) error {
	if !isatty.IsTerminal(os.Stdout.Fd()) && !isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		return fmt.Errorf("TUI requires a terminal (stdout is not a TTY); use plain CLI commands instead")
	}

	model := NewModel(idx)
	p := tea.NewProgram(model, tea.WithAltScreen())

	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}
