package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KHAEntertainment/markedup/index"
	"github.com/KHAEntertainment/markedup/schema"
)

// neighborEntry represents one item in the explore list.
type neighborEntry struct {
	page     *schema.Page
	relType  string
	strength float64
	depth    int
}

// exploreModel shows a graph neighborhood for a selected node.
type exploreModel struct {
	idx       *index.KnowledgeIndex
	rootPage  *schema.Page
	neighbors []neighborEntry
	cursor    int
	width     int
	height    int
}

func newExploreModel(idx *index.KnowledgeIndex, page *schema.Page) exploreModel {
	m := exploreModel{
		idx:      idx,
		rootPage: page,
	}
	m.loadNeighbors()
	return m
}

func (m *exploreModel) loadNeighbors() {
	if m.rootPage == nil {
		m.neighbors = nil
		return
	}

	result, err := index.Traverse(m.idx, m.rootPage.Frontmatter.ID, index.WithDepth(1), index.WithDirection(index.Both))
	if err != nil {
		m.neighbors = nil
		return
	}

	m.neighbors = nil
	for _, node := range result.Nodes {
		if node.Page.Frontmatter.ID == m.rootPage.Frontmatter.ID {
			continue // skip root
		}

		// Find the relationship type for this neighbor.
		relType := ""
		strength := 0.0
		for _, edge := range result.Edges {
			if edge.To == node.Page.Frontmatter.ID || edge.From == node.Page.Frontmatter.ID {
				relType = edge.Relationship.Type
				strength = edge.Relationship.Strength
				break
			}
		}

		m.neighbors = append(m.neighbors, neighborEntry{
			page:     node.Page,
			relType:  relType,
			strength: strength,
			depth:    node.Depth,
		})
	}
	m.cursor = 0
}

func (m exploreModel) Init() tea.Cmd {
	return nil
}

func (m exploreModel) Update(msg tea.Msg) (exploreModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down":
			if m.cursor < len(m.neighbors)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

func (m exploreModel) View() string {
	var b strings.Builder

	// Header.
	title := "Explore"
	if m.rootPage != nil {
		title = fmt.Sprintf("Explore: %s", m.rootPage.Frontmatter.Title)
	}
	header := headerStyle.Width(m.width).Render(title)
	b.WriteString(header)
	b.WriteString("\n\n")

	if m.rootPage == nil {
		b.WriteString(mutedStyle.Render("No node selected. Search for a page first."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("esc/backspace: search  |  q/ctrl+c: quit"))
		return b.String()
	}

	// Root node info.
	fm := m.rootPage.Frontmatter
	b.WriteString(titleStyle.Render(fm.Title))
	if fm.EntityType != "" {
		b.WriteString(subtitleStyle.Render(fmt.Sprintf(" (%s)", fm.EntityType)))
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("ID: %s  |  Confidence: %.2f", fm.ID, fm.Confidence)))
	b.WriteString("\n\n")

	// Neighbors.
	if len(m.neighbors) == 0 {
		b.WriteString(mutedStyle.Render("No connections found."))
	} else {
		b.WriteString(labelStyle.Render(fmt.Sprintf("Connections (%d):", len(m.neighbors))))
		b.WriteString("\n")

		maxVisible := m.height - 12
		if maxVisible < 3 {
			maxVisible = 3
		}

		start := 0
		if m.cursor >= maxVisible {
			start = m.cursor - maxVisible + 1
		}
		end := start + maxVisible
		if end > len(m.neighbors) {
			end = len(m.neighbors)
		}

		for i := start; i < end; i++ {
			n := m.neighbors[i]
			nfm := n.page.Frontmatter

			prefix := "  "
			style := lipgloss.NewStyle()
			if i == m.cursor {
				prefix = "> "
				style = selectedStyle
			}

			line := fmt.Sprintf("%s%s", prefix, nfm.Title)
			if nfm.EntityType != "" {
				line += fmt.Sprintf(" (%s)", nfm.EntityType)
			}
			if n.relType != "" {
				line += fmt.Sprintf("  [%s", n.relType)
				if n.strength > 0 {
					line += fmt.Sprintf(", %.1f", n.strength)
				}
				line += "]"
			}

			b.WriteString(style.Render(line))
			b.WriteString("\n")
		}
	}

	// Help.
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("enter: follow link  |  esc/backspace: search  |  tab: view doc  |  q/ctrl+c: quit"))

	return b.String()
}

// Selected returns the currently selected neighbor page, if any.
func (m exploreModel) Selected() (*schema.Page, bool) {
	if len(m.neighbors) == 0 || m.cursor >= len(m.neighbors) {
		return nil, false
	}
	return m.neighbors[m.cursor].page, true
}
