package tui

import (
	"context"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Clarit-AI/markedup/config"
	"github.com/Clarit-AI/markedup/index"
	"github.com/Clarit-AI/markedup/internal/tui/setup"
	"github.com/Clarit-AI/markedup/schema"
)

// view represents which screen the TUI is currently showing.
type view int

const (
	viewHome view = iota
	viewSearch
	viewDocument
	viewExplore
	viewOnboarding
	viewSetup
)

// homeMenuSearch is the menu index for Search.
const homeMenuSearch = 0

// homeMenuExplore is the menu index for Explore.
const homeMenuExplore = 1

// homeMenuEnrich is the menu index for Enrich.
const homeMenuEnrich = 2

// homeMenuEmbed is the menu index for Embed.
const homeMenuEmbed = 3

// homeMenuSettings is the menu index for Settings.
const homeMenuSettings = 4

// Model is the top-level BubbleTea model that composes all views.
type Model struct {
	idx         *index.KnowledgeIndex
	kbDir       string
	cfg         *config.Config
	current     view
	home        homeModel
	search      searchModel
	doc         docModel
	explore     exploreModel
	onboarding  onboardingModel
	wizard      setup.WizardModel
	status      statusModel
	taskRunning bool
	width       int
	height      int
	empty       bool // true when index has zero pages
}

// NewModel creates the root TUI model from a loaded knowledge index.
// If the index has zero pages, the onboarding screen is shown first.
// Otherwise, the home/menu screen is shown.
func NewModel(idx *index.KnowledgeIndex, kbDir string, cfg *config.Config) Model {
	empty := idx.Pages() == 0
	initial := viewHome
	if empty {
		initial = viewOnboarding
	}
	return Model{
		idx:        idx,
		kbDir:      kbDir,
		cfg:        cfg,
		current:    initial,
		home:       newHomeModel(),
		search:     newSearchModel(idx),
		onboarding: newOnboardingModel(),
		empty:      empty,
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	if m.current == viewOnboarding {
		return m.onboarding.Init()
	}
	if m.current == viewHome {
		return m.home.Init()
	}
	return m.search.Init()
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.home.width = msg.Width
		m.home.height = msg.Height
		m.search.width = msg.Width
		m.search.height = msg.Height
		m.explore.width = msg.Width
		m.explore.height = msg.Height
		m.onboarding.width = msg.Width
		m.onboarding.height = msg.Height

		// Re-init doc viewport on resize if viewing doc.
		if m.current == viewDocument {
			m.doc.width = msg.Width
			m.doc.height = msg.Height
			m.doc.initViewport()
		}

		// Propagate resize to the wizard if active.
		if m.current == viewSetup {
			m.wizard.SetSize(msg.Width, msg.Height)
		}
		return m, nil

	case statusTickMsg:
		if m.status.shouldDismiss() {
			m.status = statusModel{state: taskIdle}
			m.taskRunning = false
			m.home.taskRunning = false
			return m, nil
		}
		m.status = m.status.tick()
		if m.status.state == taskRunning {
			return m, spinnerTick()
		}
		// Done/error: keep ticking for auto-dismiss.
		return m, dismissTick()

	case taskDoneMsg:
		m.taskRunning = false
		m.home.taskRunning = false
		if msg.err != nil {
			// Extract last non-empty line from output as error detail.
			errDetail := msg.err.Error()
			if msg.output != "" {
				lines := strings.Split(strings.TrimSpace(msg.output), "\n")
				for i := len(lines) - 1; i >= 0; i-- {
					if strings.TrimSpace(lines[i]) != "" {
						errDetail = strings.TrimSpace(lines[i])
						break
					}
				}
			}
			m.status = statusModel{
				state:     taskError,
				taskName:  msg.taskName,
				message:   errDetail,
				dismissAt: time.Now().Add(8 * time.Second),
			}
		} else {
			m.status = statusModel{
				state:     taskDone,
				taskName:  msg.taskName,
				message:   lastMeaningfulLine(msg.output),
				dismissAt: time.Now().Add(8 * time.Second),
			}
			// Reload the index so newly enriched/embedded pages appear.
			if m.kbDir != "" {
				result, err := index.Load(context.Background(), m.kbDir, index.WithIgnoreErrors(true))
				if err == nil {
					m.idx = result.Index
					m.search = newSearchModel(m.idx)
					m.search.width = m.width
					m.search.height = m.height
				}
			}
		}
		return m, dismissTick()

	case tea.KeyMsg:
		// When the setup wizard is active, only intercept ctrl+c at the
		// top level (the embedded wizard handles its own ctrl+c/esc).
		if m.current == viewSetup {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			// All other keys go to the wizard.
			break
		}

		// Global quit keys.
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			// In onboarding and home, always quit.
			if m.current == viewOnboarding || m.current == viewHome {
				return m, tea.Quit
			}
			// In search view, only quit if input is empty.
			if m.current == viewSearch {
				if m.search.input.Value() == "" {
					return m, tea.Quit
				}
				// Non-empty input: let 'q' fall through to the text input.
			} else {
				// In document or explore view, q quits.
				return m, tea.Quit
			}
		case "h":
			// From any non-home, non-onboarding view, h returns to home.
			if m.current != viewHome && m.current != viewOnboarding {
				// Don't intercept 'h' when the search input is focused (user is typing).
				if m.current == viewSearch && m.search.input.Value() != "" {
					break
				}
				m.current = viewHome
				return m, nil
			}
		}
	}

	// Delegate to current view.
	switch m.current {
	case viewHome:
		return m.updateHome(msg)
	case viewOnboarding:
		return m.updateOnboarding(msg)
	case viewSearch:
		return m.updateSearch(msg)
	case viewDocument:
		return m.updateDocument(msg)
	case viewExplore:
		return m.updateExplore(msg)
	case viewSetup:
		return m.updateSetup(msg)
	}

	return m, nil
}

func (m Model) updateHome(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "enter":
			switch m.home.SelectedIndex() {
			case homeMenuSearch:
				m.current = viewSearch
				m.search.exploreMode = false
				m.search.input.Focus()
				return m, nil
			case homeMenuExplore:
				// Go to search with explore mode active.
				m.current = viewSearch
				m.search.exploreMode = true
				m.search.input.Focus()
				return m, nil
			case homeMenuEnrich:
				if m.taskRunning {
					return m, nil
				}
				m.taskRunning = true
				m.home.taskRunning = true
				m.status = statusModel{
					state:    taskRunning,
					taskName: "Enrich",
				}
				return m, tea.Batch(
					runBackgroundTask(m.kbDir, "Enrich", "markedup", "enrich"),
					spinnerTick(),
				)
			case homeMenuEmbed:
				if m.taskRunning {
					return m, nil
				}
				m.taskRunning = true
				m.home.taskRunning = true
				m.status = statusModel{
					state:    taskRunning,
					taskName: "Embed",
				}
				return m, tea.Batch(
					runBackgroundTask(m.kbDir, "Embed", "markedup", "embed"),
					spinnerTick(),
				)
			case homeMenuSettings:
				m.wizard = setup.NewWizardModel()
				m.wizard.SetEmbedded(true)
				m.wizard.SetSize(m.width, m.height)
				// Reconfigure mode (#115/#116): pass the loaded config so the
				// wizard shows a section menu and pre-fills inputs.
				if m.cfg != nil {
					m.wizard.SetReconfigure(m.cfg)
				}
				m.current = viewSetup
				return m, m.wizard.Init()
			}
		}
	}

	var cmd tea.Cmd
	m.home, cmd = m.home.Update(msg)
	return m, cmd
}

func (m Model) updateSetup(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	result, cmd := m.wizard.Update(msg)
	m.wizard = result.(setup.WizardModel)

	if m.wizard.Saved() {
		// Reload config from disk (the wizard already saved it).
		if newCfg, err := config.Load(m.kbDir); err == nil {
			m.cfg = newCfg
		}

		// Reload index so any changes are visible.
		if m.kbDir != "" {
			result, err := index.Load(context.Background(), m.kbDir, index.WithIgnoreErrors(true))
			if err == nil {
				m.idx = result.Index
				m.search = newSearchModel(m.idx)
				m.search.width = m.width
				m.search.height = m.height
			}
		}

		m.current = viewHome
		return m, nil
	}

	if m.wizard.Cancelled() {
		m.current = viewHome
		return m, nil
	}

	return m, cmd
}

func (m Model) updateOnboarding(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.onboarding, cmd = m.onboarding.Update(msg)
	return m, cmd
}

func (m Model) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "enter":
			if result, ok := m.search.Selected(); ok {
				m.doc = newDocModel(result.Page, m.width, m.height)
				m.current = viewDocument
				return m, nil
			}
		case "tab":
			if result, ok := m.search.Selected(); ok {
				m.explore = newExploreModel(m.idx, result.Page)
				m.explore.width = m.width
				m.explore.height = m.height
				m.current = viewExplore
				return m, nil
			}
		case "esc":
			// Esc from search with empty input returns to home.
			if m.search.input.Value() == "" {
				m.current = viewHome
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	return m, cmd
}

func (m Model) updateDocument(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "esc", "backspace":
			m.current = viewSearch
			m.search.input.Focus()
			return m, nil
		case "tab":
			if m.doc.page != nil {
				m.explore = newExploreModel(m.idx, m.doc.page)
				m.explore.width = m.width
				m.explore.height = m.height
				m.current = viewExplore
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.doc, cmd = m.doc.Update(msg)
	return m, cmd
}

func (m Model) updateExplore(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "esc", "backspace":
			m.current = viewSearch
			m.search.input.Focus()
			return m, nil
		case "enter":
			if page, ok := m.explore.Selected(); ok {
				m.navigateToNode(page)
				return m, nil
			}
		case "tab":
			if page, ok := m.explore.Selected(); ok {
				m.doc = newDocModel(page, m.width, m.height)
				m.current = viewDocument
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.explore, cmd = m.explore.Update(msg)
	return m, cmd
}

// navigateToNode follows a link in the explore view.
func (m *Model) navigateToNode(page *schema.Page) {
	m.explore = newExploreModel(m.idx, page)
	m.explore.width = m.width
	m.explore.height = m.height
}

// View implements tea.Model.
func (m Model) View() string {
	var content string
	switch m.current {
	case viewHome:
		content = m.home.View()
	case viewOnboarding:
		content = m.onboarding.View()
	case viewSearch:
		content = m.search.View()
	case viewDocument:
		content = m.doc.View()
	case viewExplore:
		content = m.explore.View()
	case viewSetup:
		content = m.wizard.View()
	}

	// Append status bar if there is something to show.
	statusLine := m.status.View(m.width)
	if statusLine != "" {
		return content + "\n" + statusLine
	}
	return content
}

// lastMeaningfulLine returns the last non-empty line from output.
func lastMeaningfulLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

// runBackgroundTask runs a subprocess to completion in a goroutine and returns
// a taskDoneMsg when it finishes. The kbDir is appended as the last argument.
func runBackgroundTask(kbDir, taskName, binary string, subArgs ...string) tea.Cmd {
	return func() tea.Msg {
		args := append(subArgs, kbDir)
		cmd := exec.Command(binary, args...)
		out, err := cmd.CombinedOutput()
		return taskDoneMsg{
			taskName: taskName,
			err:      err,
			output:   string(out),
		}
	}
}

// spinnerTick returns a Cmd that sends statusTickMsg after 200ms.
func spinnerTick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(_ time.Time) tea.Msg {
		return statusTickMsg{}
	})
}

// dismissTick returns a Cmd that sends statusTickMsg after 1s (for auto-dismiss polling).
func dismissTick() tea.Cmd {
	return tea.Tick(1*time.Second, func(_ time.Time) tea.Msg {
		return statusTickMsg{}
	})
}
