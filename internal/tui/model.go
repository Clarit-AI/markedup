package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/KHAEntertainment/markedup/index"
	"github.com/KHAEntertainment/markedup/schema"
)

// view represents which screen the TUI is currently showing.
type view int

const (
	viewHome view = iota
	viewSearch
	viewDocument
	viewExplore
	viewOnboarding
)

// Model is the top-level BubbleTea model that composes all views.
type Model struct {
	idx        *index.KnowledgeIndex
	current    view
	home       homeModel
	search     searchModel
	doc        docModel
	explore    exploreModel
	onboarding onboardingModel
	width      int
	height     int
	empty      bool // true when index has zero pages
}

// NewModel creates the root TUI model from a loaded knowledge index.
// If the index has zero pages, the onboarding screen is shown first.
// Otherwise, the home/menu screen is shown.
func NewModel(idx *index.KnowledgeIndex) Model {
	empty := idx.Pages() == 0
	initial := viewHome
	if empty {
		initial = viewOnboarding
	}
	return Model{
		idx:        idx,
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
		return m, nil

	case tea.KeyMsg:
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
	}

	return m, nil
}

// homeMenuSearch is the menu index for Search.
const homeMenuSearch = 0

// homeMenuExplore is the menu index for Explore.
const homeMenuExplore = 1

// homeMenuSettings is the menu index for Settings / Reconfigure.
const homeMenuSettings = 3

func (m Model) updateHome(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "enter":
			switch m.home.SelectedIndex() {
			case homeMenuSearch:
				m.current = viewSearch
				m.search.input.Focus()
				return m, nil
			case homeMenuExplore:
				// Go to explore. If no page is loaded yet, go to search first
				// so the user can pick one.
				m.current = viewSearch
				m.search.input.Focus()
				return m, nil
			case homeMenuSettings:
				// Settings: exit and tell user to run `markedup setup`.
				// We can't re-launch the setup wizard from within the TUI without
				// a round-trip through the CLI, so we quit with a message.
				// The message is surfaced via the CLI's exit flow.
				return m, tea.Quit
			default:
				// Graph (placeholder) and any future items: no-op for now.
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.home, cmd = m.home.Update(msg)
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
	switch m.current {
	case viewHome:
		return m.home.View()
	case viewOnboarding:
		return m.onboarding.View()
	case viewSearch:
		return m.search.View()
	case viewDocument:
		return m.doc.View()
	case viewExplore:
		return m.explore.View()
	}
	return ""
}
