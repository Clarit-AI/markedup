package setup

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"github.com/KHAEntertainment/markedup/config"
)

// Run launches the setup wizard and returns the completed config.
// Returns nil config if the user cancels (Ctrl+C / Esc at confirm).
func Run() (*config.Config, error) {
	if !isatty.IsTerminal(os.Stdout.Fd()) && !isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		return nil, fmt.Errorf("setup wizard requires a terminal (stdout is not a TTY)")
	}

	m := newWizardModel()
	p := tea.NewProgram(m, tea.WithAltScreen())

	result, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("setup wizard error: %w", err)
	}

	wm, ok := result.(wizardModel)
	if !ok {
		return nil, nil
	}

	if wm.cancelled {
		return nil, nil
	}

	return wm.config, nil
}

// wizardModel is the root BubbleTea model for the 6-step setup wizard.
type wizardModel struct {
	step      int // 0=welcome, 1=embed, 2=llm, 3=rerank, 4=keys, 5=confirm
	config    *config.Config
	detected  []config.Endpoint
	cancelled bool
	width     int
	height    int

	// Sub-models for each step.
	welcome  welcomeStep
	embed    providerStep
	llm      providerStep
	rerank   providerStep
	keys     keysStep
	confirm  confirmStep

	// Track which services need API keys.
	needEmbedKey  bool
	needLLMKey    bool
	needRerankKey bool
}

func newWizardModel() wizardModel {
	return wizardModel{
		step:    0,
		config:  &config.Config{},
		welcome: newWelcomeStep(),
	}
}

// Init implements tea.Model.
func (m wizardModel) Init() tea.Cmd {
	return m.welcome.Init()
}

// Update implements tea.Model.
func (m wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Global quit at any step.
		if msg.String() == "ctrl+c" {
			m.cancelled = true
			return m, tea.Quit
		}

		// Esc goes back one step (or cancels at step 0).
		if msg.String() == "esc" {
			return m.handleEsc()
		}
	}

	// Delegate to current step.
	switch m.step {
	case 0:
		return m.updateWelcome(msg)
	case 1:
		return m.updateEmbed(msg)
	case 2:
		return m.updateLLM(msg)
	case 3:
		return m.updateRerank(msg)
	case 4:
		return m.updateKeys(msg)
	case 5:
		return m.updateConfirm(msg)
	}

	return m, nil
}

func (m wizardModel) handleEsc() (tea.Model, tea.Cmd) {
	switch m.step {
	case 0:
		m.cancelled = true
		return m, tea.Quit
	case 1:
		// Check if the embed step is in a sub-phase. If so, let it handle esc.
		if m.embed.phase > 0 {
			return m, nil // will be handled by step update
		}
		m.step = 0
	case 2:
		if m.llm.phase > 0 {
			return m, nil
		}
		m.step = 1
	case 3:
		if m.rerank.phase > 0 {
			return m, nil
		}
		m.step = 2
	case 4:
		m.step = 3
	case 5:
		m.cancelled = true
		return m, tea.Quit
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Step update helpers
// ---------------------------------------------------------------------------

func (m wizardModel) updateWelcome(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle Enter to advance when detection is done.
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "enter" && !m.welcome.detecting {
		m.detected = m.welcome.detected
		m.embed = newProviderStep(
			"Embedding Provider",
			"Choose an embedding endpoint for semantic search.",
			m.detected, "embed", false, false,
		)
		m.step = 1
		return m, nil
	}

	var cmd tea.Cmd
	m.welcome, cmd = m.welcome.Update(msg)
	return m, cmd
}

func (m wizardModel) updateEmbed(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.embed, cmd = m.embed.Update(msg)

	if m.embed.done {
		m.config.Embed = m.embed.selected
		m.needEmbedKey = m.embed.needsKey
		m.llm = newProviderStep(
			"LLM Provider",
			"Choose an LLM endpoint for enrichment and reasoning.",
			m.detected, "llm", false, false,
		)
		m.step = 2
		return m, nil
	}

	return m, cmd
}

func (m wizardModel) updateLLM(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.llm, cmd = m.llm.Update(msg)

	if m.llm.done {
		m.config.LLM = m.llm.selected
		m.needLLMKey = m.llm.needsKey
		m.rerank = newProviderStep(
			"Reranker (optional)",
			"Configure a reranker? Improves search quality by re-scoring results.",
			m.detected, "rerank", true, true,
		)
		m.step = 3
		return m, nil
	}

	return m, cmd
}

func (m wizardModel) updateRerank(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.rerank, cmd = m.rerank.Update(msg)

	if m.rerank.done {
		m.config.Rerank = config.RerankConfig{
			ServiceConfig: m.rerank.selected,
			Format:        m.rerank.formatVal,
		}
		m.needRerankKey = m.rerank.needsKey

		// Skip keys step if no cloud providers selected.
		if !m.needEmbedKey && !m.needLLMKey && !m.needRerankKey {
			m.keys = keysStep{done: true, collectedKeys: map[string]string{}}
			m.confirm = newConfirmStep(m.config, m.rerank.formatVal, nil)
			m.step = 5
			return m, nil
		}

		m.keys = newKeysStep(m.needEmbedKey, m.needLLMKey, m.needRerankKey)
		m.step = 4
		return m, m.keys.Init()
	}

	return m, cmd
}

func (m wizardModel) updateKeys(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.keys, cmd = m.keys.Update(msg)

	if m.keys.done {
		m.confirm = newConfirmStep(m.config, m.rerank.formatVal, m.keys.collectedKeys)
		m.step = 5
		return m, nil
	}

	return m, cmd
}

func (m wizardModel) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "enter":
			if !m.confirm.saved {
				// Save config file.
				if err := config.Save(m.config, config.GlobalPath()); err != nil {
					m.confirm.err = err
					return m, nil
				}

				// Store API keys in keyring.
				if config.KeyringAvailable() {
					for svc, key := range m.keys.collectedKeys {
						keyName := svc + "-api-key"
						if err := config.StoreKey(keyName, key); err != nil {
							m.confirm.err = fmt.Errorf("failed to store %s key: %w", svc, err)
							return m, nil
						}
					}
				}

				// Set API keys on the config for the returned value.
				if k, ok := m.keys.collectedKeys["embed"]; ok {
					m.config.Embed.APIKey = k
				}
				if k, ok := m.keys.collectedKeys["llm"]; ok {
					m.config.LLM.APIKey = k
				}
				if k, ok := m.keys.collectedKeys["rerank"]; ok {
					m.config.Rerank.APIKey = k
				}

				m.confirm.saved = true
				return m, nil
			}
			// Already saved — quit.
			return m, tea.Quit
		}
	}

	return m, nil
}

// View implements tea.Model.
func (m wizardModel) View() string {
	width := m.width
	if width == 0 {
		width = 80
	}

	// Step indicator.
	steps := []string{"Welcome", "Embed", "LLM", "Rerank", "Keys", "Confirm"}
	stepLine := ""
	for i, name := range steps {
		if i == m.step {
			stepLine += selectedStyle.Render(fmt.Sprintf("[%s]", name))
		} else if i < m.step {
			stepLine += successStyle.Render(fmt.Sprintf("[%s]", name))
		} else {
			stepLine += mutedStyle.Render(fmt.Sprintf("[%s]", name))
		}
		if i < len(steps)-1 {
			stepLine += mutedStyle.Render(" > ")
		}
	}
	view := stepLine + "\n\n"

	switch m.step {
	case 0:
		view += m.welcome.View(width)
	case 1:
		view += m.embed.View(width)
	case 2:
		view += m.llm.View(width)
	case 3:
		view += m.rerank.View(width)
	case 4:
		view += m.keys.View(width)
	case 5:
		view += m.confirm.View(width)
	}

	return view
}
