// Package tui provides a BubbleTea-based terminal user interface for
// interactive search, exploration, and document viewing of the markedup
// knowledge graph.
package tui

import "github.com/charmbracelet/lipgloss"

// Colors used throughout the TUI.
var (
	colorPrimary   = lipgloss.Color("12")  // bright blue
	colorSecondary = lipgloss.Color("243") // gray
	colorAccent    = lipgloss.Color("10")  // green
	colorMuted     = lipgloss.Color("240") // dark gray
	colorWarning   = lipgloss.Color("11")  // yellow
)

// Shared styles.
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(colorSecondary)

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	labelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSecondary)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(colorMuted)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	warningStyle = lipgloss.NewStyle().
			Foreground(colorWarning)
)
