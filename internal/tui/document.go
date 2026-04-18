package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Clarit-AI/markedup/schema"
)

// docModel displays a single document's frontmatter and body.
type docModel struct {
	page     *schema.Page
	viewport viewport.Model
	ready    bool
	width    int
	height   int
}

func newDocModel(page *schema.Page, width, height int) docModel {
	m := docModel{
		page:   page,
		width:  width,
		height: height,
	}
	m.initViewport()
	return m
}

func (m *docModel) initViewport() {
	if m.width == 0 || m.height == 0 {
		return
	}

	headerHeight := 3 // header + blank line
	footerHeight := 2 // help line
	vpHeight := m.height - headerHeight - footerHeight
	if vpHeight < 1 {
		vpHeight = 1
	}

	m.viewport = viewport.New(m.width, vpHeight)
	m.viewport.SetContent(m.renderContent())
	m.ready = true
}

func (m docModel) renderContent() string {
	if m.page == nil {
		return mutedStyle.Render("No page selected.")
	}

	fm := m.page.Frontmatter
	var b strings.Builder

	// Frontmatter section.
	b.WriteString(labelStyle.Render("ID:          "))
	b.WriteString(fm.ID)
	b.WriteString("\n")

	b.WriteString(labelStyle.Render("Title:       "))
	b.WriteString(fm.Title)
	b.WriteString("\n")

	if fm.EntityType != "" {
		b.WriteString(labelStyle.Render("Type:        "))
		b.WriteString(fm.EntityType)
		b.WriteString("\n")
	}

	b.WriteString(labelStyle.Render("Confidence:  "))
	b.WriteString(fmt.Sprintf("%.2f", fm.Confidence))
	b.WriteString("\n")

	if len(fm.Tags) > 0 {
		b.WriteString(labelStyle.Render("Tags:        "))
		b.WriteString(strings.Join(fm.Tags, ", "))
		b.WriteString("\n")
	}

	if len(fm.Entities) > 0 {
		b.WriteString(labelStyle.Render("Entities:    "))
		names := make([]string, len(fm.Entities))
		for i, e := range fm.Entities {
			names[i] = e.Name
		}
		b.WriteString(strings.Join(names, ", "))
		b.WriteString("\n")
	}

	if len(fm.Relationships) > 0 {
		b.WriteString(labelStyle.Render("Relations:   "))
		rels := make([]string, len(fm.Relationships))
		for i, r := range fm.Relationships {
			rels[i] = fmt.Sprintf("%s (%s, %.1f)", r.Target, r.Type, r.Strength)
		}
		b.WriteString(strings.Join(rels, ", "))
		b.WriteString("\n")
	}

	b.WriteString(labelStyle.Render("Source:      "))
	b.WriteString(m.page.SourcePath)
	b.WriteString("\n")

	// Body section.
	body := strings.TrimSpace(m.page.Body)
	if body != "" {
		b.WriteString("\n")
		b.WriteString(titleStyle.Render("--- Body ---"))
		b.WriteString("\n\n")
		b.WriteString(body)
		b.WriteString("\n")
	}

	return b.String()
}

func (m docModel) Init() tea.Cmd {
	return nil
}

func (m docModel) Update(msg tea.Msg) (docModel, tea.Cmd) {
	if !m.ready {
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m docModel) View() string {
	if !m.ready {
		return mutedStyle.Render("Loading...")
	}

	var b strings.Builder

	// Header.
	title := "Document"
	if m.page != nil {
		title = m.page.Frontmatter.Title
	}
	header := headerStyle.Width(m.width).Render(title)
	b.WriteString(header)
	b.WriteString("\n\n")

	// Viewport.
	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	// Help.
	b.WriteString(helpStyle.Render("esc/backspace: back  |  tab: explore  |  h: home  |  j/k/arrows: scroll  |  q/ctrl+c: quit"))

	return b.String()
}
