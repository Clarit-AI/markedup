// Package setup provides a BubbleTea-based setup wizard for markedup
// configuration. It walks through provider selection, endpoint configuration,
// model choice, API key entry, and saves the result.
package setup

import "github.com/charmbracelet/lipgloss"

// Colors — reuse the same palette as the main TUI (internal/tui/styles.go).
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

	successStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)
)
