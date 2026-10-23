package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"github.com/Clarit-AI/markedup/config"
	"github.com/Clarit-AI/markedup/index"
)

// Run launches the TUI with the given knowledge index, KB directory, and config.
// It returns an error if stdout is not a terminal or if the TUI encounters a
// fatal error. The TUI package has no dependency on cobra — it receives only
// the index, directory, and config.
func Run(idx *index.KnowledgeIndex, kbDir string, cfg *config.Config) error {
	if !isatty.IsTerminal(os.Stdout.Fd()) && !isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		return fmt.Errorf("TUI requires a terminal (stdout is not a TTY); use plain CLI commands instead")
	}

	model := NewModel(idx, kbDir, cfg)
	p := tea.NewProgram(model, tea.WithAltScreen())

	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

// RunStartupSplash shows the startup splash before TUI flows that do not use
// the root Model, such as the first-run setup wizard. The returned boolean is
// true when the user cancelled with ctrl+c during the splash.
func RunStartupSplash() (bool, error) {
	if !isatty.IsTerminal(os.Stdout.Fd()) && !isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		return false, fmt.Errorf("TUI requires a terminal (stdout is not a TTY); use plain CLI commands instead")
	}

	p := tea.NewProgram(newStartupSplashModel(), tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		return false, fmt.Errorf("TUI splash error: %w", err)
	}

	model, ok := result.(startupSplashModel)
	return ok && model.cancelled, nil
}

type startupSplashModel struct {
	splash    splashModel
	cancelled bool
}

func newStartupSplashModel() startupSplashModel {
	return startupSplashModel{splash: newSplashModel()}
}

func (m startupSplashModel) Init() tea.Cmd {
	return m.splash.Init()
}

func (m startupSplashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.splash.SetSize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.cancelled = true
		} else {
			m.splash = m.splash.Skip()
		}
		return m, tea.Quit
	case splashTickMsg:
		var cmd tea.Cmd
		m.splash, cmd = m.splash.Update(msg)
		if m.splash.Done() {
			return m, tea.Quit
		}
		return m, cmd
	default:
		return m, nil
	}
}

func (m startupSplashModel) View() string {
	return m.splash.View()
}
