package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// menuItem represents one entry on the home menu.
type menuItem struct {
	label       string
	description string
}

var homeMenuItems = []menuItem{
	{label: "Search", description: "Search the knowledge base"},
	{label: "Explore", description: "Explore graph connections"},
	{label: "Graph", description: "Graph overview (coming soon)"},
	{label: "Settings / Reconfigure", description: "Reconfigure markedup"},
}

// homeModel is the home/menu screen shown on TUI launch.
type homeModel struct {
	cursor int
	width  int
	height int
}

func newHomeModel() homeModel {
	return homeModel{}
}

func (m homeModel) Init() tea.Cmd {
	return nil
}

func (m homeModel) Update(msg tea.Msg) (homeModel, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down":
			if m.cursor < len(homeMenuItems)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

// SelectedIndex returns the currently highlighted menu index.
func (m homeModel) SelectedIndex() int {
	return m.cursor
}

func (m homeModel) View() string {
	var b strings.Builder

	header := headerStyle.Width(m.width).Render("markedup")
	b.WriteString(header)
	b.WriteString("\n\n")

	b.WriteString(subtitleStyle.Render("Knowledge Graph Terminal UI"))
	b.WriteString("\n\n")

	for i, item := range homeMenuItems {
		prefix := "  "
		labelSty := lipgloss.NewStyle()
		descSty := mutedStyle

		if i == m.cursor {
			prefix = "> "
			labelSty = selectedStyle
			descSty = lipgloss.NewStyle().Foreground(colorSecondary)
		}

		line := prefix + item.label
		b.WriteString(labelSty.Render(line))
		b.WriteString("  ")
		b.WriteString(descSty.Render(item.description))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("up/down: navigate  |  enter: select  |  q/ctrl+c: quit"))

	return b.String()
}
