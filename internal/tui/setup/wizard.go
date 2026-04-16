package setup

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/textinput"
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

	m := NewWizardModel()
	p := tea.NewProgram(m, tea.WithAltScreen())

	result, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("setup wizard error: %w", err)
	}

	wm, ok := result.(WizardModel)
	if !ok {
		return nil, nil
	}

	if wm.cancelled {
		return nil, nil
	}

	return wm.config, nil
}

// WizardModel is the root BubbleTea model for the 7-step setup wizard.
type WizardModel struct {
	step         int   // 0=welcome, 1=embed, 2=llm, 3=rerank, 4=triplex, 5=keys, 6=confirm
	stepHistory  []int // stack of previous step indices for Esc back-navigation
	config       *config.Config
	detected     []config.Endpoint
	cancelled    bool
	width        int
	height       int

	// Sub-models for each step.
	welcome  welcomeStep
	embed    providerStep
	llm      providerStep
	rerank   providerStep
	triplex  triplexStep
	keys     keysStep
	confirm  confirmStep

	// Track which services need API keys.
	needEmbedKey    bool
	needLLMKey      bool
	needRerankKey   bool
	needTriplexKey  bool

	// Track selected provider labels for key step labeling.
	embedProviderLabel  string
	llmProviderLabel    string
	rerankProviderLabel string
	triplexProviderLabel string

	// Track selected provider endpoints for key deduplication.
	embedEndpoint   string
	llmEndpoint     string
	rerankEndpoint  string
	triplexEndpoint string

	// embedded is true when the wizard is running inside the main TUI
	// rather than as a standalone program. When true, Esc at step 0
	// sets cancelled but does not send tea.Quit.
	embedded bool
}

// NewWizardModel creates a new setup wizard model.
func NewWizardModel() WizardModel {
	return WizardModel{
		step:    0,
		config:  &config.Config{},
		welcome: newWelcomeStep(),
	}
}

// Saved returns true if the wizard completed and saved the configuration.
func (m WizardModel) Saved() bool { return m.confirm.saved }

// Cancelled returns true if the user cancelled the wizard.
func (m WizardModel) Cancelled() bool { return m.cancelled }

// Config returns the configuration built by the wizard.
func (m WizardModel) Config() *config.Config { return m.config }

// Format returns the reranker format value selected during wizard setup.
func (m WizardModel) Format() string { return m.rerank.formatVal }

// CollectedKeys returns the API keys collected during wizard setup.
func (m WizardModel) CollectedKeys() map[string]string { return m.keys.collectedKeys }

// SetEmbedded marks the wizard as running embedded inside a parent TUI.
// When embedded, Esc at step 0 sets cancelled without sending tea.Quit.
func (m *WizardModel) SetEmbedded(v bool) { m.embedded = v }

// SetSize sets the width and height for the wizard model.
func (m *WizardModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// Init implements tea.Model.
func (m WizardModel) Init() tea.Cmd {
	return m.welcome.Init()
}

// Update implements tea.Model.
func (m WizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Global quit at any step.
		if msg.String() == "ctrl+c" {
			m.cancelled = true
			if m.embedded {
				return m, nil
			}
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
		return m.updateTriplex(msg)
	case 5:
		return m.updateKeys(msg)
	case 6:
		return m.updateConfirm(msg)
	}

	return m, nil
}

func (m WizardModel) handleEsc() (tea.Model, tea.Cmd) {
	// If the current step sub-model has an active sub-phase, let it handle Esc
	// internally (e.g. going from model-entry back to provider list).
	switch m.step {
	case 1:
		if m.embed.phase > 0 {
			return m, nil // will be handled by step update
		}
	case 2:
		if m.llm.phase > 0 {
			return m, nil
		}
	case 3:
		if m.rerank.phase > 0 {
			return m, nil
		}
	// case 4 (triplex): no sub-phases, fall through to pop history
	}

	// Step 0 (welcome): Esc exits cleanly.
	if m.step == 0 {
		m.cancelled = true
		if m.embedded {
			return m, nil
		}
		return m, tea.Quit
	}

	// Pop the step history stack to go back to the previous step.
	if len(m.stepHistory) > 0 {
		m.step = m.stepHistory[len(m.stepHistory)-1]
		m.stepHistory = m.stepHistory[:len(m.stepHistory)-1]
	} else {
		// Fallback: go back one step.
		m.step--
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Step update helpers
// ---------------------------------------------------------------------------

func (m WizardModel) updateWelcome(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle Enter to advance when detection is done.
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "enter" && !m.welcome.detecting {
		m.detected = m.welcome.detected
		m.embed = newProviderStep(
			"Choose a text embedding model",
			"Converts text to vectors for similarity search\ne.g. nomic-embed-text (local), text-embedding-3-small (OpenAI)",
			m.detected, "embed", false, false,
		)
		m.stepHistory = append(m.stepHistory, m.step)
		m.step = 1
		return m, nil
	}

	var cmd tea.Cmd
	m.welcome, cmd = m.welcome.Update(msg)
	return m, cmd
}

func (m WizardModel) updateEmbed(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.embed, cmd = m.embed.Update(msg)

	if m.embed.done {
		m.config.Embed = m.embed.selected
		m.needEmbedKey = m.embed.needsKey
		m.embedEndpoint = m.embed.selected.Endpoint
		m.embedProviderLabel = m.embed.selectedProviderLabel()
		m.llm = newProviderStep(
			"LLM Provider",
			"Choose an LLM endpoint for enrichment and reasoning.\ne.g. granite-3.1-dense:2b (local), gpt-4o-mini (cloud)",
			m.detected, "llm", false, false,
		)
		m.stepHistory = append(m.stepHistory, m.step)
		m.step = 2
		return m, nil
	}

	return m, cmd
}

func (m WizardModel) updateLLM(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.llm, cmd = m.llm.Update(msg)

	if m.llm.done {
		m.config.LLM = m.llm.selected
		m.needLLMKey = m.llm.needsKey
		m.llmEndpoint = m.llm.selected.Endpoint
		m.llmProviderLabel = m.llm.selectedProviderLabel()
		m.rerank = newProviderStep(
			"Reranker (optional)",
			"Configure a reranker model to improve search quality by re-scoring results.\ne.g. bge-reranker-v2-m3 (local TEI), jina-reranker-v2-base-multilingual (cloud)",
			m.detected, "rerank", true, true,
		)
		m.stepHistory = append(m.stepHistory, m.step)
		m.step = 3
		return m, nil
	}

	return m, cmd
}

func (m WizardModel) updateRerank(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.rerank, cmd = m.rerank.Update(msg)

	if m.rerank.done {
		m.config.Rerank = config.RerankConfig{
			ServiceConfig: m.rerank.selected,
			Format:        m.rerank.formatVal,
		}
		m.needRerankKey = m.rerank.needsKey
		m.rerankEndpoint = m.rerank.selected.Endpoint
		m.rerankProviderLabel = m.rerank.selectedProviderLabel()
		m.stepHistory = append(m.stepHistory, m.step)

		// Advance to Triplex step (step 4).
		m.triplex = newTriplexStep(m.detected)
		m.step = 4
		return m, textinput.Blink
	}

	return m, cmd
}

func (m WizardModel) updateTriplex(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.triplex, cmd = m.triplex.Update(msg)

	if m.triplex.done {
		if !m.triplex.skip {
			// Configured: store endpoint + model. Format is auto-set to "triplex"
			// by the enrich pipeline based on the model name (FormatTriplex).
			m.config.Triplex = m.triplex.selected
			m.needTriplexKey = m.triplex.needsKey
			m.triplexEndpoint = m.triplex.selected.Endpoint
			m.triplexProviderLabel = "Triplex"
		}
		// When skipped: config.Triplex stays zero value, needTriplexKey = false.

		m.stepHistory = append(m.stepHistory, m.step)

		// Skip keys step if no cloud providers selected.
		if !m.needEmbedKey && !m.needLLMKey && !m.needRerankKey && !m.needTriplexKey {
			m.keys = keysStep{done: true, collectedKeys: map[string]string{}}
			m.confirm = newConfirmStep(m.config, m.rerank.formatVal, nil)
			m.step = 6
			return m, nil
		}

		m.keys = newKeysStep(
			m.needEmbedKey, m.embedProviderLabel, m.embedEndpoint,
			m.needLLMKey, m.llmProviderLabel, m.llmEndpoint,
			m.needRerankKey, m.rerankProviderLabel, m.rerankEndpoint,
			m.needTriplexKey, m.triplexProviderLabel, m.triplexEndpoint,
		)
		m.stepHistory = append(m.stepHistory, m.step)
		m.step = 5
		return m, m.keys.Init()
	}

	return m, cmd
}

func (m WizardModel) updateKeys(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.keys, cmd = m.keys.Update(msg)

	if m.keys.done {
		m.confirm = newConfirmStep(m.config, m.rerank.formatVal, m.keys.collectedKeys)
		m.stepHistory = append(m.stepHistory, m.step)
		m.step = 6
		return m, nil
	}

	return m, cmd
}

func (m WizardModel) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
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
				if k, ok := m.keys.collectedKeys["triplex"]; ok {
					m.config.Triplex.APIKey = k
				}

				m.confirm.saved = true
				return m, nil
			}
			// Already saved — quit (or signal parent if embedded).
			if m.embedded {
				return m, nil
			}
			return m, tea.Quit
		}
	}

	return m, nil
}

// View implements tea.Model.
func (m WizardModel) View() string {
	width := m.width
	if width == 0 {
		width = 80
	}

	// Step indicator.
	steps := []string{"Welcome", "Embed", "LLM", "Rerank", "Triplex", "Keys", "Confirm"}
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
		view += m.triplex.View(width)
	case 5:
		view += m.keys.View(width)
	case 6:
		view += m.confirm.View(width)
	}

	return view
}
