package tui

import (
	"strings"
	"time"

	syscanim "github.com/Nomadcxx/sysc-Go/animations"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Clarit-AI/markedup/assets"
)

const (
	splashFrameInterval = 50 * time.Millisecond
	splashMaxFrames     = 30
)

type splashTickMsg struct{}

type splashModel struct {
	width       int
	height      int
	frame       int
	done        bool
	useFallback bool
	effect      *syscanim.BeamTextEffect
}

func newSplashModel() splashModel {
	return splashModel{}
}

func (m splashModel) Init() tea.Cmd {
	return splashTick()
}

func (m *splashModel) SetSize(width, height int) {
	if m.width == width && m.height == height && m.effect != nil {
		return
	}
	m.width = width
	m.height = height
	m.effect = nil
	m.useFallback = shouldUseSplashFallback(width, height, assets.MarkedupASCIIArt())
	if !m.useFallback {
		m.effect = newBeamTextEffect(width, height)
	}
}

func (m splashModel) Update(msg tea.Msg) (splashModel, tea.Cmd) {
	switch msg.(type) {
	case splashTickMsg:
		if m.done {
			return m, nil
		}
		if m.effect != nil {
			m.effect.Update()
		}
		m.frame++
		if m.frame >= splashMaxFrames {
			m.done = true
			return m, nil
		}
		return m, splashTick()
	}
	return m, nil
}

func (m splashModel) Done() bool {
	return m.done
}

func (m splashModel) Skip() splashModel {
	m.done = true
	return m
}

func (m splashModel) View() string {
	if m.effect != nil {
		return m.effect.Render()
	}
	return renderFallbackSplash(m.width, m.height)
}

func splashTick() tea.Cmd {
	return tea.Tick(splashFrameInterval, func(_ time.Time) tea.Msg {
		return splashTickMsg{}
	})
}

func newBeamTextEffect(width, height int) *syscanim.BeamTextEffect {
	return syscanim.NewBeamTextEffect(syscanim.BeamTextConfig{
		Width:                width,
		Height:               height,
		Text:                 assets.MarkedupASCIIArt(),
		Display:              true,
		BeamRowSymbols:       []rune{'▂', '▁', '_'},
		BeamColumnSymbols:    []rune{'▌', '▍', '▎', '▏'},
		BeamDelay:            2,
		BeamRowSpeedRange:    [2]int{20, 80},
		BeamColumnSpeedRange: [2]int{15, 30},
		BeamGradientStops:    []string{"#ffffff", "#88c0d0", "#81a1c1"},
		BeamGradientSteps:    5,
		BeamGradientFrames:   1,
		FinalGradientStops:   []string{"#434c5e", "#88c0d0", "#eceff4"},
		FinalGradientSteps:   8,
		FinalGradientFrames:  1,
		FinalWipeSpeed:       3,
	})
}

func shouldUseSplashFallback(width, height int, art string) bool {
	if width <= 0 || height <= 0 {
		return true
	}
	artWidth, artHeight := textDimensions(art)
	return width < artWidth || height < artHeight
}

func textDimensions(text string) (int, int) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	maxWidth := 0
	for _, line := range lines {
		lineWidth := lipgloss.Width(line)
		if lineWidth > maxWidth {
			maxWidth = lineWidth
		}
	}
	return maxWidth, len(lines)
}

func renderFallbackSplash(width, height int) string {
	title := titleStyle.Render("markedup")
	subtitle := subtitleStyle.Render("starting...")
	if width <= 0 {
		return title + "\n" + subtitle
	}

	block := title + "\n" + subtitle
	if height <= 2 {
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, title)
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, block)
}
