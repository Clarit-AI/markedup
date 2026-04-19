package setup

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"github.com/Clarit-AI/markedup/config"
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

// stepSectionMenu is the synthetic step index used in reconfigure mode (issues
// #115/#116) for the jump-to-section menu. It sits between welcome (0) and the
// concrete edit steps (1..6). Picking a menu entry sets m.step to the chosen
// edit step; ESC inside an edit step returns here instead of popping back
// through linear history.
const stepSectionMenu = 100

// WizardModel is the root BubbleTea model for the 7-step setup wizard.
type WizardModel struct {
	step         int   // 0=welcome, 1=embed, 2=llm, 3=rerank, 4=triplex, 5=keys, 6=confirm, 100=section menu (reconfigure)
	stepHistory  []int // stack of previous step indices for Esc back-navigation
	config       *config.Config
	detected     []config.Endpoint
	cancelled    bool
	width        int
	height       int

	// Reconfigure mode (issues #115 + #116). Enabled via SetReconfigure when
	// the wizard is launched from the home Settings menu rather than first-run.
	// In this mode the welcome/detect step is followed by a section menu
	// instead of the linear walkthrough; each section reads its initial values
	// from currentConfig and ESC after editing returns to the menu.
	reconfigure   bool
	currentConfig *config.Config
	currentFormat string // current rerank format (cfg.Rerank.Format) for pre-fill
	sectionCursor int    // selected entry in the section menu

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
	// extractorService is the keyring service name for the selected extractor:
	// "triplex" or "nuextract". Empty when extractor is skipped.
	extractorService string

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

// SetReconfigure enables reconfigure mode (issues #115 + #116). The wizard
// will show a jump-to-section menu after the welcome step, pre-filling each
// step's inputs from cfg. Pass nil cfg to disable. Must be called before Init.
func (m *WizardModel) SetReconfigure(cfg *config.Config) {
	if cfg == nil {
		m.reconfigure = false
		m.currentConfig = nil
		return
	}
	m.reconfigure = true
	m.currentConfig = cfg
	m.currentFormat = cfg.Rerank.Format
	// Seed the working config so any sections the user does NOT visit retain
	// their existing values when the wizard saves.
	cfgCopy := *cfg
	m.config = &cfgCopy
}

// Reconfigure returns true when the wizard is in reconfigure mode.
func (m WizardModel) Reconfigure() bool { return m.reconfigure }

// SetSize sets the width and height for the wizard model.
func (m *WizardModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// Init implements tea.Model.
func (m WizardModel) Init() tea.Cmd {
	return m.welcome.Init()
}

// viewSectionMenu renders the reconfigure-mode jump-to-section menu (issue
// #115). Each entry shows the section name plus a one-line summary of the
// current value so the user can decide what to edit at a glance.
func (m WizardModel) viewSectionMenu(width int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Width(width).Render("Settings — choose a section to edit"))
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render("Tab/↑↓: navigate  |  Enter: edit section  |  Esc: cancel  |  Ctrl+C: cancel"))
	b.WriteString("\n\n")

	entries := m.sectionMenuEntries()
	cur := m.currentConfig
	if cur == nil {
		cur = &config.Config{}
	}

	for i, entry := range entries {
		prefix := "  "
		style := subtitleStyle
		if i == m.sectionCursor {
			prefix = "> "
			style = selectedStyle
		}
		b.WriteString(style.Render(prefix + entry.label))
		// Append a one-line current-value summary.
		summary := ""
		switch entry.target {
		case 1:
			summary = serviceSummary(cur.Embed)
		case 2:
			summary = serviceSummary(cur.LLM)
		case 3:
			summary = serviceSummary(cur.Rerank.ServiceConfig)
			if summary != "" && cur.Rerank.Format != "" {
				summary += "  (" + cur.Rerank.Format + ")"
			}
		case 4:
			switch {
			case cur.NuExtract.Endpoint != "":
				summary = serviceSummary(cur.NuExtract.ServiceConfig)
			case cur.Triplex.Endpoint != "":
				summary = serviceSummary(cur.Triplex)
			}
		case 5:
			if config.KeyringAvailable() {
				summary = "stored in OS keychain"
			} else {
				summary = "set via env vars"
			}
		case 6:
			summary = "write changes to " + config.GlobalPath()
		}
		if summary != "" {
			b.WriteString("  ")
			b.WriteString(mutedStyle.Render("— " + summary))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// serviceSummary returns a compact "model @ endpoint" summary or "(not
// configured)" when both fields are empty.
func serviceSummary(s config.ServiceConfig) string {
	if s.Endpoint == "" && s.Model == "" {
		return mutedStyle.Render("(not configured)")
	}
	if s.Model == "" {
		return s.Endpoint
	}
	if s.Endpoint == "" {
		return s.Model
	}
	return s.Model + " @ " + s.Endpoint
}

// sectionEntry describes one row in the reconfigure-mode jump-to-section menu.
type sectionEntry struct {
	label  string
	target int // wizard step to jump to
}

// sectionMenuEntries returns the section list for reconfigure mode. The list
// adapts to the current config: when an extractor is configured the entry is
// labeled "NuExtract" or "Triplex"; otherwise "Extractor".
func (m WizardModel) sectionMenuEntries() []sectionEntry {
	extractorLabel := "Extractor (NuExtract / Triplex)"
	if m.currentConfig != nil {
		switch {
		case m.currentConfig.NuExtract.Endpoint != "":
			extractorLabel = "NuExtract"
		case m.currentConfig.Triplex.Endpoint != "":
			extractorLabel = "Triplex"
		}
	}
	return []sectionEntry{
		{label: "Embed", target: 1},
		{label: "LLM", target: 2},
		{label: "Rerank", target: 3},
		{label: extractorLabel, target: 4},
		{label: "API Keys", target: 5},
		{label: "Save & Exit", target: 6},
	}
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
	case stepSectionMenu:
		return m.updateSectionMenu(msg)
	}

	return m, nil
}

// updateSectionMenu handles input in the reconfigure-mode jump-to-section menu.
func (m WizardModel) updateSectionMenu(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	entries := m.sectionMenuEntries()
	switch keyMsg.String() {
	case "up", "shift+tab":
		if m.sectionCursor > 0 {
			m.sectionCursor--
		}
	case "down", "tab":
		if m.sectionCursor < len(entries)-1 {
			m.sectionCursor++
		}
	case "enter":
		target := entries[m.sectionCursor].target
		return m.enterSection(target)
	}
	return m, nil
}

// enterSection initializes the chosen section's sub-model with values pre-
// filled from m.currentConfig and jumps to it.
func (m WizardModel) enterSection(target int) (tea.Model, tea.Cmd) {
	cur := m.currentConfig
	if cur == nil {
		cur = &config.Config{}
	}
	switch target {
	case 1: // Embed
		m.embed = newProviderStepWithCurrent(
			"Choose a text embedding model",
			"Converts text to vectors for similarity search\ne.g. nomic-embed-text (local), text-embedding-3-small (OpenAI)",
			m.detected, "embed", false, false, cur.Embed,
		)
		m.step = 1
		return m, nil
	case 2: // LLM
		m.llm = newProviderStepWithCurrent(
			"LLM Provider",
			"Choose an LLM endpoint for enrichment and reasoning.\ne.g. granite-3.1-dense:2b (local), gpt-4o-mini (cloud)",
			m.detected, "llm", false, false, cur.LLM,
		)
		m.step = 2
		return m, nil
	case 3: // Rerank
		m.rerank = newProviderStepWithCurrent(
			"Reranker (optional)",
			"Configure a reranker model to improve search quality by re-scoring results.\ne.g. bge-reranker-v2-m3 (local TEI), jina-reranker-v2-base-multilingual (cloud)",
			m.detected, "rerank", true, true, cur.Rerank.ServiceConfig,
		)
		// Pre-select the existing rerank format if configured.
		if m.currentFormat != "" {
			for i, f := range rerankFormats {
				if f == m.currentFormat {
					m.rerank.formatIdx = i
					break
				}
			}
		}
		m.step = 3
		return m, nil
	case 4: // Extractor
		// Use whichever extractor block is currently populated; NuExtract takes
		// precedence (it's the newer config path).
		var extractorCfg config.ServiceConfig
		switch {
		case cur.NuExtract.Endpoint != "":
			extractorCfg = cur.NuExtract.ServiceConfig
		case cur.Triplex.Endpoint != "":
			extractorCfg = cur.Triplex
		}
		m.triplex = newTriplexStepWithCurrent(m.detected, extractorCfg)
		m.step = 4
		return m, textinput.Blink
	case 5: // Keys
		// Keys step needs label/endpoint metadata for each configured service.
		// Derive from the current config.
		needEmbed := cur.Embed.Endpoint != "" && needsKeyForEndpoint(cur.Embed.Endpoint)
		needLLM := cur.LLM.Endpoint != "" && needsKeyForEndpoint(cur.LLM.Endpoint)
		needRerank := cur.Rerank.Endpoint != "" && needsKeyForEndpoint(cur.Rerank.Endpoint)
		var extractorEndpoint, extractorService string
		switch {
		case cur.NuExtract.Endpoint != "":
			extractorEndpoint = cur.NuExtract.Endpoint
			extractorService = "nuextract"
		case cur.Triplex.Endpoint != "":
			extractorEndpoint = cur.Triplex.Endpoint
			extractorService = "triplex"
		}
		needExtractor := extractorEndpoint != "" && needsKeyForEndpoint(extractorEndpoint)
		if !needEmbed && !needLLM && !needRerank && !needExtractor {
			// Nothing to collect — return immediately to the menu.
			return m, nil
		}
		m.keys = newKeysStep(
			needEmbed, providerLabelForEndpoint(cur.Embed.Endpoint), cur.Embed.Endpoint,
			needLLM, providerLabelForEndpoint(cur.LLM.Endpoint), cur.LLM.Endpoint,
			needRerank, providerLabelForEndpoint(cur.Rerank.Endpoint), cur.Rerank.Endpoint,
			needExtractor, providerLabelForEndpoint(extractorEndpoint), extractorEndpoint,
			extractorService,
		)
		m.step = 5
		return m, m.keys.Init()
	case 6: // Save & Exit (reuse confirm step)
		format := m.currentFormat
		if m.config.Rerank.Format != "" {
			format = m.config.Rerank.Format
		}
		// Carry over any keys collected via the Keys section so they get
		// written to the keyring on save.
		m.confirm = newConfirmStep(m.config, format, m.keys.collectedKeys)
		m.step = 6
		return m, nil
	}
	return m, nil
}

// needsKeyForEndpoint returns true when the endpoint is a remote URL that
// typically requires an API key (i.e. not localhost/127.0.0.1).
func needsKeyForEndpoint(endpoint string) bool {
	if endpoint == "" {
		return false
	}
	lower := strings.ToLower(endpoint)
	return !strings.Contains(lower, "localhost") && !strings.Contains(lower, "127.0.0.1")
}

// providerLabelForEndpoint returns a human-readable label for the API key
// input based on the endpoint URL. Falls back to "Custom" when the URL does
// not match a known cloud preset.
func providerLabelForEndpoint(endpoint string) string {
	for _, c := range cloudProviders {
		if c.endpoint == endpoint {
			return c.label
		}
	}
	if endpoint == "" {
		return "API"
	}
	return "Custom"
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

	// Reconfigure mode (issue #115): ESC inside an edit step returns to the
	// section menu. ESC at the section menu cancels back to home.
	if m.reconfigure {
		if m.step == stepSectionMenu {
			m.cancelled = true
			if m.embedded {
				return m, nil
			}
			return m, tea.Quit
		}
		if m.step >= 1 && m.step <= 6 {
			m.step = stepSectionMenu
			return m, nil
		}
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
		// Reconfigure mode (issue #115): show jump-to-section menu instead of
		// the linear walkthrough. First-run flow is unchanged.
		if m.reconfigure {
			m.stepHistory = append(m.stepHistory, m.step)
			m.step = stepSectionMenu
			return m, nil
		}
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
		if m.reconfigure {
			m.step = stepSectionMenu
			return m, nil
		}
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
		if m.reconfigure {
			m.step = stepSectionMenu
			return m, nil
		}
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
		if m.reconfigure {
			m.currentFormat = m.rerank.formatVal
			m.step = stepSectionMenu
			return m, nil
		}
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
		// Always clear BOTH extractor blocks first so switching between
		// extractor types (or re-running the wizard over an existing config)
		// cannot leave stale state. Explicitly reset Format.
		m.config.Triplex = config.ServiceConfig{}
		m.config.NuExtract = config.NuExtractConfig{}
		m.config.Format = ""

		if !m.triplex.skip {
			// Route config based on the model id the user entered.
			// NuExtract-2.0 family → config.NuExtract + Format="nuextract".
			// Anything else → config.Triplex (backward compat).
			if config.IsNuExtractModel(m.triplex.selected.Model) {
				m.config.NuExtract = config.NuExtractConfig{
					ServiceConfig: m.triplex.selected,
					// Transport and Mode left empty → enrich defaults (parallel, auto transport).
				}
				m.config.Format = "nuextract"
				m.triplexProviderLabel = "NuExtract-2.0"
				m.extractorService = "nuextract"
			} else {
				m.config.Triplex = m.triplex.selected
				m.triplexProviderLabel = "Triplex"
				m.extractorService = "triplex"
			}
			m.needTriplexKey = m.triplex.needsKey
			m.triplexEndpoint = m.triplex.selected.Endpoint
		} else {
			// Explicit skip: ensure key/endpoint state is also cleared.
			m.needTriplexKey = false
			m.triplexEndpoint = ""
			m.triplexProviderLabel = ""
			m.extractorService = ""
		}

		if m.reconfigure {
			m.step = stepSectionMenu
			return m, nil
		}

		m.stepHistory = append(m.stepHistory, m.step)

		// Skip keys step if no cloud providers selected.
		if !m.needEmbedKey && !m.needLLMKey && !m.needRerankKey && !m.needTriplexKey {
			m.keys = keysStep{done: true, collectedKeys: map[string]string{}}
			m.confirm = newConfirmStep(m.config, m.rerank.formatVal, nil)
			m.step = 6
			return m, nil
		}

		extractorSvc := m.extractorService
		if extractorSvc == "" {
			extractorSvc = "triplex" // legacy default if no extractor selected (no-op since needTriplexKey=false)
		}
		m.keys = newKeysStep(
			m.needEmbedKey, m.embedProviderLabel, m.embedEndpoint,
			m.needLLMKey, m.llmProviderLabel, m.llmEndpoint,
			m.needRerankKey, m.rerankProviderLabel, m.rerankEndpoint,
			m.needTriplexKey, m.triplexProviderLabel, m.triplexEndpoint,
			extractorSvc,
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
		if m.reconfigure {
			m.step = stepSectionMenu
			return m, nil
		}
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

				// Store API keys in keyring, one entry per unique endpoint
				// (issue #117). Iterate the deduped keysStep.entries rather
				// than collectedKeys so a shared endpoint produces a single
				// keyring write.
				if config.KeyringAvailable() {
					written := map[string]bool{}
					for _, entry := range m.keys.entries {
						key, ok := m.keys.collectedKeys[entry.service]
						if !ok || key == "" {
							continue
						}
						keyName := config.KeyNameForEndpoint(entry.endpoint)
						if keyName == "" {
							// No endpoint configured — skip silently; the
							// caller can still set the key via env var.
							continue
						}
						if written[keyName] {
							continue
						}
						if err := config.StoreKey(keyName, key); err != nil {
							m.confirm.err = fmt.Errorf("failed to store %s key: %w", entry.service, err)
							return m, nil
						}
						written[keyName] = true
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
				if k, ok := m.keys.collectedKeys["nuextract"]; ok {
					m.config.NuExtract.APIKey = k
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

	// Reconfigure-mode section menu (issue #115).
	if m.step == stepSectionMenu {
		return m.viewSectionMenu(width)
	}

	// Step indicator.
	steps := []string{"Welcome", "Embed", "LLM", "Rerank", "Triplex", "Keys", "Confirm"}
	stepLine := ""
	stepIdx := m.step
	if stepIdx < 0 || stepIdx > 6 {
		stepIdx = 0
	}
	for i, name := range steps {
		if i == stepIdx {
			stepLine += selectedStyle.Render(fmt.Sprintf("[%s]", name))
		} else if i < stepIdx {
			stepLine += successStyle.Render(fmt.Sprintf("[%s]", name))
		} else {
			stepLine += mutedStyle.Render(fmt.Sprintf("[%s]", name))
		}
		if i < len(steps)-1 {
			stepLine += mutedStyle.Render(" > ")
		}
	}
	view := stepLine + "\n\n"
	if m.reconfigure {
		view += mutedStyle.Render("Reconfigure mode — Esc returns to section menu") + "\n\n"
	}

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
