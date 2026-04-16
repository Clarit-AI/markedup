package tui

import (
	"strings"

	"github.com/KHAEntertainment/markedup/config"
)

// settingsModel is a read-only view of the current markedup configuration.
type settingsModel struct {
	cfg    *config.Config
	width  int
	height int
}

func newSettingsModel(cfg *config.Config, width, height int) settingsModel {
	return settingsModel{cfg: cfg, width: width, height: height}
}

func (m settingsModel) View() string {
	var b strings.Builder

	header := headerStyle.Width(m.width).Render("Settings")
	b.WriteString(header)
	b.WriteString("\n\n")

	b.WriteString(subtitleStyle.Render("Current Configuration"))
	b.WriteString("\n\n")

	cfg := m.cfg
	if cfg == nil {
		cfg = &config.Config{}
	}

	// Embed section.
	b.WriteString(labelStyle.Render("Embed"))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("endpoint: "))
	if cfg.Embed.Endpoint != "" {
		b.WriteString(cfg.Embed.Endpoint)
	} else {
		b.WriteString(mutedStyle.Render("not configured"))
	}
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("model:    "))
	if cfg.Embed.Model != "" {
		b.WriteString(cfg.Embed.Model)
	} else {
		b.WriteString(mutedStyle.Render("not configured"))
	}
	b.WriteString("\n\n")

	// LLM section.
	b.WriteString(labelStyle.Render("LLM"))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("endpoint: "))
	if cfg.LLM.Endpoint != "" {
		b.WriteString(cfg.LLM.Endpoint)
	} else {
		b.WriteString(mutedStyle.Render("not configured"))
	}
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("model:    "))
	if cfg.LLM.Model != "" {
		b.WriteString(cfg.LLM.Model)
	} else {
		b.WriteString(mutedStyle.Render("not configured"))
	}
	b.WriteString("\n\n")

	// Rerank section.
	b.WriteString(labelStyle.Render("Rerank"))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("endpoint: "))
	if cfg.Rerank.Endpoint != "" {
		b.WriteString(cfg.Rerank.Endpoint)
	} else {
		b.WriteString(mutedStyle.Render("not configured"))
	}
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("model:    "))
	if cfg.Rerank.Model != "" {
		b.WriteString(cfg.Rerank.Model)
	} else {
		b.WriteString(mutedStyle.Render("not configured"))
	}
	b.WriteString("\n\n")

	// Triplex section.
	b.WriteString(labelStyle.Render("Triplex"))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("endpoint: "))
	if cfg.Triplex.Endpoint != "" {
		b.WriteString(cfg.Triplex.Endpoint)
	} else {
		b.WriteString(mutedStyle.Render("not configured"))
	}
	b.WriteString("\n\n")

	b.WriteString(helpStyle.Render("To reconfigure, exit and run: markedup setup  |  esc/h: back"))

	return b.String()
}
