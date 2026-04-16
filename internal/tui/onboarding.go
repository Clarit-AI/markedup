package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// onboardingModel is shown when the knowledge index has zero pages.
// It explains the steps to get started and exits on q / ctrl+c.
type onboardingModel struct {
	width  int
	height int
}

func newOnboardingModel() onboardingModel {
	return onboardingModel{}
}

func (m onboardingModel) Init() tea.Cmd {
	return nil
}

func (m onboardingModel) Update(msg tea.Msg) (onboardingModel, tea.Cmd) {
	return m, nil
}

func (m onboardingModel) View() string {
	var b strings.Builder

	header := headerStyle.Width(m.width).Render("Getting Started")
	b.WriteString(header)
	b.WriteString("\n\n")

	b.WriteString(warningStyle.Render("No pages found in this knowledge base."))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Your markdown files need to be indexed first."))
	b.WriteString("\n\n")

	b.WriteString(labelStyle.Render("Follow these steps to index your knowledge base:"))
	b.WriteString("\n\n")

	b.WriteString(titleStyle.Render("1."))
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("Auto-generate frontmatter for plain markdown files:"))
	b.WriteString("\n")
	b.WriteString("     ")
	b.WriteString(selectedStyle.Render("markedup enrich ."))
	b.WriteString("\n\n")

	b.WriteString(titleStyle.Render("2."))
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("(Optional) Generate vectors for semantic search:"))
	b.WriteString("\n")
	b.WriteString("     ")
	b.WriteString(selectedStyle.Render("markedup embed ."))
	b.WriteString("\n\n")

	b.WriteString(titleStyle.Render("3."))
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("Re-launch the TUI to search and explore:"))
	b.WriteString("\n")
	b.WriteString("     ")
	b.WriteString(selectedStyle.Render("markedup tui"))
	b.WriteString("\n\n")

	b.WriteString(helpStyle.Render("q / ctrl+c: exit"))

	return b.String()
}
