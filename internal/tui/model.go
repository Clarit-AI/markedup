package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/KHAEntertainment/markedup/index"
	"github.com/KHAEntertainment/markedup/schema"
)

// view represents which screen the TUI is currently showing.
type view int

const (
	viewSearch view = iota
	viewDocument
	viewExplore
)

// Model is the top-level BubbleTea model that composes all views.
type Model struct {
	idx     *index.KnowledgeIndex
	current view
	search  searchModel
	doc     docModel
	explore exploreModel
	width   int
	height  int
	empty   bool // true when index has zero pages
}

// NewModel creates the root TUI model from a loaded knowledge index.
func NewModel(idx *index.KnowledgeIndex) Model {
	empty := idx.Pages() == 0
	return Model{
		idx:     idx,
		current: viewSearch,
		search:  newSearchModel(idx),
		empty:   empty,
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return m.search.Init()
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.search.width = msg.Width
		m.search.height = msg.Height
		m.explore.width = msg.Width
		m.explore.height = msg.Height

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
			// Only quit from search/explore, not from doc (q might be typed in search).
			if m.current != viewSearch {
				if m.current == viewDocument {
					return m, tea.Quit
				}
				return m, tea.Quit
			}
			// In search view, only quit if input is empty.
			if m.search.input.Value() == "" {
				return m, tea.Quit
			}
		}
	}

	// Delegate to current view.
	switch m.current {
	case viewSearch:
		return m.updateSearch(msg)
	case viewDocument:
		return m.updateDocument(msg)
	case viewExplore:
		return m.updateExplore(msg)
	}

	return m, nil
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
	if m.empty {
		return warningStyle.Render("No markdown files found in the knowledge index.") +
			"\n\n" +
			mutedStyle.Render("Add markdown files with YAML frontmatter, then try again.") +
			"\n" +
			mutedStyle.Render("See: markedup init") +
			"\n\n" +
			helpStyle.Render("Press q or ctrl+c to exit.")
	}

	switch m.current {
	case viewSearch:
		return m.search.View()
	case viewDocument:
		return m.doc.View()
	case viewExplore:
		return m.explore.View()
	}
	return ""
}
