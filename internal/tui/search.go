package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KHAEntertainment/markedup/index"
)

const debounceInterval = 150 * time.Millisecond

// debounceMsg is sent after the debounce timer fires.
type debounceMsg struct {
	query string
}

// searchModel implements the search view: a text input with real-time
// filtered results.
type searchModel struct {
	idx      *index.KnowledgeIndex
	input    textinput.Model
	results  []index.Result
	cursor   int
	width    int
	height   int
	lastQuery string
	debounceTimer *time.Timer
}

func newSearchModel(idx *index.KnowledgeIndex) searchModel {
	ti := textinput.New()
	ti.Placeholder = "Search knowledge base..."
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 60

	return searchModel{
		idx:   idx,
		input: ti,
	}
}

func (m searchModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m searchModel) Update(msg tea.Msg) (searchModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down":
			if m.cursor < len(m.results)-1 {
				m.cursor++
			}
			return m, nil
		case "enter":
			// Selection handled by parent model.
			return m, nil
		case "tab":
			// Switch to explore mode handled by parent.
			return m, nil
		}
	case debounceMsg:
		if msg.query == m.input.Value() {
			m.results = index.Search(m.idx, msg.query, index.WithLimit(50))
			m.cursor = 0
		}
		return m, nil
	}

	// Update text input.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)

	// Check if query changed — debounce search.
	currentQuery := m.input.Value()
	if currentQuery != m.lastQuery {
		m.lastQuery = currentQuery
		if m.debounceTimer != nil {
			m.debounceTimer.Stop()
		}
		if currentQuery == "" {
			m.results = nil
			m.cursor = 0
		} else {
			q := currentQuery
			cmds = append(cmds, func() tea.Msg {
				time.Sleep(debounceInterval)
				return debounceMsg{query: q}
			})
		}
	}

	return m, tea.Batch(cmds...)
}

func (m searchModel) View() string {
	var b strings.Builder

	// Header.
	header := headerStyle.Width(m.width).Render("Search")
	b.WriteString(header)
	b.WriteString("\n\n")

	// Input.
	b.WriteString(m.input.View())
	b.WriteString("\n\n")

	// Results.
	if m.input.Value() == "" {
		pages := m.idx.Pages()
		b.WriteString(mutedStyle.Render(fmt.Sprintf(
			"%d pages in index. Start typing to search.",
			pages,
		)))
	} else if len(m.results) == 0 {
		b.WriteString(mutedStyle.Render("No results found."))
	} else {
		// Calculate visible results based on height.
		maxVisible := m.height - 8 // header + input + help + padding
		if maxVisible < 3 {
			maxVisible = 3
		}

		// Scroll window around cursor.
		start := 0
		if m.cursor >= maxVisible {
			start = m.cursor - maxVisible + 1
		}
		end := start + maxVisible
		if end > len(m.results) {
			end = len(m.results)
		}

		b.WriteString(mutedStyle.Render(fmt.Sprintf(
			"%d results (showing %d-%d):",
			len(m.results), start+1, end,
		)))
		b.WriteString("\n")

		for i := start; i < end; i++ {
			r := m.results[i]
			fm := r.Page.Frontmatter

			prefix := "  "
			style := lipgloss.NewStyle()
			if i == m.cursor {
				prefix = "> "
				style = selectedStyle
			}

			line := fmt.Sprintf("%s%s", prefix, fm.Title)
			if fm.EntityType != "" {
				line += fmt.Sprintf(" (%s)", fm.EntityType)
			}
			line += fmt.Sprintf("  [%.2f]", r.Score)

			b.WriteString(style.Render(line))
			b.WriteString("\n")
		}
	}

	// Help.
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("enter: view  |  tab: explore  |  q/ctrl+c: quit"))

	return b.String()
}

// Selected returns the currently selected result, if any.
func (m searchModel) Selected() (*index.Result, bool) {
	if len(m.results) == 0 || m.cursor >= len(m.results) {
		return nil, false
	}
	return &m.results[m.cursor], true
}
