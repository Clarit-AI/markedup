package setup

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/KHAEntertainment/markedup/config"
)

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// detectDoneMsg is sent when DetectLocal finishes.
type detectDoneMsg struct {
	endpoints []config.Endpoint
}

// detectCmd runs config.DetectLocal as a tea.Cmd.
func detectCmd() tea.Msg {
	eps := config.DetectLocal(context.Background())
	return detectDoneMsg{endpoints: eps}
}

// ---------------------------------------------------------------------------
// Provider choice
// ---------------------------------------------------------------------------

// providerChoice is one option in a provider-selection list.
type providerChoice struct {
	label    string
	endpoint string // preset URL, or "" for detected / custom / skip
	kind     string // "detected", "cloud", "custom", "skip"
	models   []string
}

// Cloud provider presets.
var cloudProviders = []providerChoice{
	{label: "OpenRouter", endpoint: "https://openrouter.ai/api", kind: "cloud"},
	{label: "OpenAI", endpoint: "https://api.openai.com", kind: "cloud"},
	{label: "Ollama Cloud", endpoint: "https://api.ollama.com", kind: "cloud"},
}

// rerankFormats are the internal config values for reranker request formats.
// These must NOT be changed — they are stored in config YAML.
var rerankFormats = []string{"jina", "openai"}

// rerankFormatLabels are the display labels for rerankFormats, parallel by index.
var rerankFormatLabels = []string{
	"Standard (/v1/rerank)",
	"Score API (/v1/score)",
}

// rerankFormatHelper is shown below the format selector.
const rerankFormatHelper = "Most cloud providers (Jina, Cohere via OpenRouter) use Standard."

// embedModelNote is shown when auto-detected model list contains no known embedding models.
const embedModelNote = "Note: select an embedding model (e.g. nomic-embed-text, all-minilm, bge-*). LLM models will not work here."

// ---------------------------------------------------------------------------
// Welcome + Detect step (step 0)
// ---------------------------------------------------------------------------

type welcomeStep struct {
	spinner   spinner.Model
	detecting bool
	detected  []config.Endpoint
	done      bool
}

func newWelcomeStep() welcomeStep {
	s := spinner.New()
	s.Spinner = spinner.Dot
	return welcomeStep{
		spinner:   s,
		detecting: true,
	}
}

func (w welcomeStep) Init() tea.Cmd {
	return tea.Batch(w.spinner.Tick, detectCmd)
}

func (w welcomeStep) Update(msg tea.Msg) (welcomeStep, tea.Cmd) {
	switch msg := msg.(type) {
	case detectDoneMsg:
		w.detected = msg.endpoints
		w.detecting = false
		w.done = false
		return w, nil
	case spinner.TickMsg:
		if w.detecting {
			var cmd tea.Cmd
			w.spinner, cmd = w.spinner.Update(msg)
			return w, cmd
		}
	}
	return w, nil
}

func (w welcomeStep) View(width int) string {
	var b strings.Builder

	b.WriteString(headerStyle.Width(width).Render("Welcome to markedup setup!"))
	b.WriteString("\n\n")
	b.WriteString("This wizard will configure your embedding, LLM, and reranker services.\n\n")

	if w.detecting {
		b.WriteString(w.spinner.View())
		b.WriteString(" Detecting local model servers...")
	} else {
		if len(w.detected) == 0 {
			b.WriteString(mutedStyle.Render("No local model servers detected."))
		} else {
			b.WriteString(successStyle.Render(fmt.Sprintf("Found %d local service(s):", len(w.detected))))
			b.WriteString("\n")
			for _, ep := range w.detected {
				models := ""
				if len(ep.Models) > 0 {
					models = " — models: " + strings.Join(ep.Models, ", ")
				}
				b.WriteString(fmt.Sprintf("  %s  %s (%s)%s\n",
					successStyle.Render("*"),
					ep.Name, ep.URL, models))
			}
		}
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("Press Enter to continue, Ctrl+C to cancel"))
	}

	return b.String()
}

// ---------------------------------------------------------------------------
// Provider selection step (steps 1, 2, 3)
// ---------------------------------------------------------------------------

type providerStep struct {
	title         string
	description   string
	optional      bool // if true, shows "skip" first and is pre-selected
	choices       []providerChoice
	cursor        int
	phase         int // 0=choosing provider, 1=entering endpoint, 2=entering model, 3=choosing format (rerank only)
	endpointIn    textinput.Model
	modelIn       textinput.Model
	needsKey      bool   // true if a cloud provider was selected
	formatIdx     int    // rerank format selector index
	showFormat    bool   // whether to show format selection
	selected      config.ServiceConfig
	formatVal     string
	done          bool
	embedWarning  bool   // true if detected models look like LLM models, not embedding models
}

// selectedProviderLabel returns a human-readable label for the chosen provider,
// used to label API key inputs.
func (p providerStep) selectedProviderLabel() string {
	if p.cursor < len(p.choices) {
		c := p.choices[p.cursor]
		switch c.kind {
		case "detected":
			return c.label
		case "cloud":
			return c.label
		case "custom":
			return "Custom"
		}
	}
	return "API"
}

func newProviderStep(title, desc string, detected []config.Endpoint, filterType string, optional bool, showFormat bool) providerStep {
	choices := make([]providerChoice, 0, len(detected)+5)

	// Add detected endpoints matching the filter type.
	for _, ep := range detected {
		if ep.Type == filterType || ep.Type == "multi" {
			choices = append(choices, providerChoice{
				label:    fmt.Sprintf("%s (local — %s)", ep.Name, ep.URL),
				endpoint: ep.URL,
				kind:     "detected",
				models:   ep.Models,
			})
		}
	}

	// Add cloud presets.
	choices = append(choices, cloudProviders...)

	// Add custom + skip.
	choices = append(choices, providerChoice{label: "Custom endpoint", kind: "custom"})
	choices = append(choices, providerChoice{label: "Skip", kind: "skip"})

	// Endpoint text input.
	ei := textinput.New()
	ei.Placeholder = "https://..."
	ei.CharLimit = 256
	ei.Width = 50

	// Model text input.
	mi := textinput.New()
	mi.Placeholder = "model-name"
	mi.CharLimit = 128
	mi.Width = 50

	startCursor := 0
	if optional {
		// Pre-select "Skip" for optional steps.
		startCursor = len(choices) - 1
	}

	// For the embed step, check whether the detected model lists appear to be
	// LLM-named models rather than embedding models. If so, surface a warning.
	embedWarning := false
	if filterType == "embed" {
		for _, c := range choices {
			if c.kind == "detected" {
				for _, m := range c.models {
					if !looksLikeEmbedModel(m) {
						embedWarning = true
						break
					}
				}
				if embedWarning {
					break
				}
			}
		}
	}

	return providerStep{
		title:        title,
		description:  desc,
		optional:     optional,
		choices:      choices,
		cursor:       startCursor,
		endpointIn:   ei,
		modelIn:      mi,
		showFormat:   showFormat,
		embedWarning: embedWarning,
	}
}

// looksLikeEmbedModel returns true if the model name contains patterns that
// indicate it is an embedding model (rather than a generative LLM).
func looksLikeEmbedModel(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "embed") ||
		strings.Contains(lower, "minilm") ||
		strings.Contains(lower, "bge") ||
		strings.Contains(lower, "e5") ||
		strings.Contains(lower, "nomic")
}

func (p providerStep) Update(msg tea.Msg) (providerStep, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch p.phase {
		case 0: // choosing provider
			switch msg.String() {
			case "up":
				if p.cursor > 0 {
					p.cursor--
				}
			case "down":
				if p.cursor < len(p.choices)-1 {
					p.cursor++
				}
			case "enter":
				choice := p.choices[p.cursor]
				switch choice.kind {
				case "skip":
					p.done = true
					return p, nil
				case "detected":
					p.selected.Endpoint = choice.endpoint
					if len(choice.models) > 0 {
						p.selected.Model = choice.models[0]
					}
					// Go to model entry to let user confirm/override.
					p.phase = 2
					p.modelIn.SetValue(p.selected.Model)
					p.modelIn.Focus()
					return p, textinput.Blink
				case "cloud":
					p.selected.Endpoint = choice.endpoint
					p.needsKey = true
					p.phase = 2
					p.modelIn.Focus()
					return p, textinput.Blink
				case "custom":
					p.phase = 1
					p.endpointIn.Focus()
					return p, textinput.Blink
				}
			}
		case 1: // entering custom endpoint
			switch msg.String() {
			case "enter":
				val := strings.TrimSpace(p.endpointIn.Value())
				if val == "" {
					return p, nil
				}
				p.selected.Endpoint = val
				p.needsKey = true // assume custom needs a key
				p.phase = 2
				p.modelIn.Focus()
				return p, textinput.Blink
			case "esc":
				p.phase = 0
				return p, nil
			default:
				var cmd tea.Cmd
				p.endpointIn, cmd = p.endpointIn.Update(msg)
				return p, cmd
			}
		case 2: // entering model name
			switch msg.String() {
			case "enter":
				val := strings.TrimSpace(p.modelIn.Value())
				if val == "" {
					return p, nil
				}
				p.selected.Model = val
				if p.showFormat {
					p.phase = 3
					return p, nil
				}
				p.done = true
				return p, nil
			case "esc":
				p.phase = 0
				return p, nil
			default:
				var cmd tea.Cmd
				p.modelIn, cmd = p.modelIn.Update(msg)
				return p, cmd
			}
		case 3: // choosing rerank format
			switch msg.String() {
			case "up", "left":
				if p.formatIdx > 0 {
					p.formatIdx--
				}
			case "down", "right":
				if p.formatIdx < len(rerankFormats)-1 {
					p.formatIdx++
				}
			case "enter":
				p.formatVal = rerankFormats[p.formatIdx]
				p.done = true
				return p, nil
			case "esc":
				p.phase = 2
				return p, nil
			}
		}
	default:
		// Forward non-key messages to active text inputs.
		switch p.phase {
		case 1:
			var cmd tea.Cmd
			p.endpointIn, cmd = p.endpointIn.Update(msg)
			return p, cmd
		case 2:
			var cmd tea.Cmd
			p.modelIn, cmd = p.modelIn.Update(msg)
			return p, cmd
		}
	}
	return p, nil
}

func (p providerStep) View(width int) string {
	var b strings.Builder

	b.WriteString(headerStyle.Width(width).Render(p.title))
	b.WriteString("\n\n")
	if p.description != "" {
		b.WriteString(p.description)
		b.WriteString("\n\n")
	}

	switch p.phase {
	case 0:
		// UX-01: Show a note when detected models appear to be LLM models.
		if p.embedWarning {
			b.WriteString(warningStyle.Render(embedModelNote))
			b.WriteString("\n\n")
		}
		for i, c := range p.choices {
			prefix := "  "
			style := subtitleStyle
			if i == p.cursor {
				prefix = "> "
				style = selectedStyle
			}
			b.WriteString(style.Render(prefix + c.label))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("up/down: navigate  |  Enter: select  |  Esc: back  |  Ctrl+C: cancel"))

	case 1:
		b.WriteString(labelStyle.Render("Endpoint URL: "))
		b.WriteString("\n")
		b.WriteString(p.endpointIn.View())
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("Enter: confirm  |  Esc: back  |  Ctrl+C: cancel"))

	case 2:
		b.WriteString(mutedStyle.Render(fmt.Sprintf("Endpoint: %s", p.selected.Endpoint)))
		b.WriteString("\n\n")
		b.WriteString(labelStyle.Render("Model name: "))
		b.WriteString("\n")
		b.WriteString(p.modelIn.View())
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("Enter: confirm  |  Esc: back  |  Ctrl+C: cancel"))

	case 3:
		b.WriteString(mutedStyle.Render(fmt.Sprintf("Endpoint: %s  |  Model: %s", p.selected.Endpoint, p.selected.Model)))
		b.WriteString("\n\n")
		b.WriteString(labelStyle.Render("Reranker format:"))
		b.WriteString("\n")
		// UX-11: Use display labels; internal values (rerankFormats) are unchanged.
		for i, label := range rerankFormatLabels {
			prefix := "  "
			style := subtitleStyle
			if i == p.formatIdx {
				prefix = "> "
				style = selectedStyle
			}
			b.WriteString(style.Render(prefix + label))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(rerankFormatHelper))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("up/down: navigate  |  Enter: select  |  Esc: back  |  Ctrl+C: cancel"))
	}

	return b.String()
}

// ---------------------------------------------------------------------------
// Triplex step (step 4)
// ---------------------------------------------------------------------------

// triplexModelName is the fixed model name for the Triplex fine-tune.
// This matches the model identifier used in enrich/model.go (FormatTriplex).
const triplexModelName = "Phi-3-mini-128k-instruct/triplex"

// triplexDescription explains what Triplex does and why it is optional.
const triplexDescription = "Triplex extracts entities and relationships from your notes to build a\nrich knowledge graph. It is optional but highly recommended — without it,\nmarkedup uses your general LLM for extraction (lower quality).\n\nTriplex is a Phi-3-mini-128k-instruct fine-tune and runs locally via Ollama\nor a compatible OpenAI-compatible endpoint. You do not choose the model name\n— it is fixed."

// triplexStep is the wizard sub-model for configuring the Triplex extraction model.
// Unlike providerStep, there is no model picker — the model name is fixed.
type triplexStep struct {
	endpointIn textinput.Model
	skip       bool
	done       bool
	needsKey   bool
	selected   config.ServiceConfig
	// skipCursor: 0 = endpoint input focused, 1 = Skip option highlighted
	skipCursor int
}

// newTriplexStep creates the Triplex configuration step.
// If Ollama was among the detected endpoints, the endpoint is pre-filled.
func newTriplexStep(detected []config.Endpoint) triplexStep {
	ei := textinput.New()
	ei.Placeholder = "http://localhost:11434"
	ei.CharLimit = 256
	ei.Width = 50

	// Pre-fill Ollama endpoint if detected.
	for _, ep := range detected {
		if ep.URL == "http://localhost:11434" || ep.Type == "multi" {
			ei.SetValue(ep.URL)
			break
		}
	}

	ei.Focus()

	return triplexStep{
		endpointIn: ei,
	}
}

func (t triplexStep) Update(msg tea.Msg) (triplexStep, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "s", "S":
			// S key skips directly.
			t.skip = true
			t.done = true
			return t, nil
		case "tab", "down":
			// Move focus between endpoint input and skip option.
			if t.skipCursor == 0 {
				t.endpointIn.Blur()
				t.skipCursor = 1
			} else {
				t.skipCursor = 0
				t.endpointIn.Focus()
				return t, textinput.Blink
			}
		case "up":
			if t.skipCursor == 1 {
				t.skipCursor = 0
				t.endpointIn.Focus()
				return t, textinput.Blink
			}
		case "enter":
			if t.skipCursor == 1 {
				// Skip selected.
				t.skip = true
				t.done = true
				return t, nil
			}
			// Confirm endpoint.
			val := strings.TrimSpace(t.endpointIn.Value())
			if val == "" {
				return t, nil
			}
			t.selected.Endpoint = val
			t.selected.Model = triplexModelName
			// Cloud endpoint needs an API key; localhost does not.
			t.needsKey = !strings.Contains(val, "localhost") && !strings.Contains(val, "127.0.0.1")
			t.done = true
			return t, nil
		case "esc":
			// Esc is handled by the wizard orchestrator (goes back to rerank).
			return t, nil
		default:
			if t.skipCursor == 0 {
				var cmd tea.Cmd
				t.endpointIn, cmd = t.endpointIn.Update(msg)
				return t, cmd
			}
		}
	default:
		if t.skipCursor == 0 {
			var cmd tea.Cmd
			t.endpointIn, cmd = t.endpointIn.Update(msg)
			return t, cmd
		}
	}
	return t, nil
}

func (t triplexStep) View(width int) string {
	var b strings.Builder

	b.WriteString(headerStyle.Width(width).Render("Triple Extraction (Triplex)"))
	b.WriteString("\n\n")
	b.WriteString(triplexDescription)
	b.WriteString("\n\n")

	// Fixed model display.
	b.WriteString(labelStyle.Render("Model: "))
	b.WriteString(mutedStyle.Render("Phi-3-mini-128k-instruct (Triplex fine-tune)"))
	b.WriteString("\n\n")

	// Endpoint input.
	b.WriteString(labelStyle.Render("Endpoint URL:"))
	b.WriteString("\n")
	b.WriteString(t.endpointIn.View())
	b.WriteString("\n\n")

	// Skip option.
	skipLabel := "  Skip — use LLM for extraction instead (lower quality)"
	if t.skipCursor == 1 {
		b.WriteString(selectedStyle.Render("> " + strings.TrimSpace(skipLabel)))
	} else {
		b.WriteString(subtitleStyle.Render(skipLabel))
	}
	b.WriteString("\n\n")

	b.WriteString(helpStyle.Render("Enter: confirm  |  S: skip  |  Tab/↑↓: navigate  |  Esc: back  |  Ctrl+C: cancel"))
	return b.String()
}

// ---------------------------------------------------------------------------
// API Keys step (step 5)
// ---------------------------------------------------------------------------

// keyEntry represents one API key to collect.
type keyEntry struct {
	service   string   // "embed", "llm", "rerank"
	label     string   // display label
	endpoint  string   // provider endpoint, used for deduplication
	services  []string // additional services sharing this key (for deduplication)
}

type keysStep struct {
	entries        []keyEntry
	inputs         []textinput.Model
	cursor         int
	keyringOK      bool
	done           bool
	collectedKeys  map[string]string // service -> key value
}

// newKeysStep builds the API key collection step. For each service that needs
// a key, provide needX=true, providerLabelX (shown in the input label), and
// endpointX (used to deduplicate — if two services share the same endpoint,
// only one key input is shown and the key is applied to both services).
func newKeysStep(
	needEmbed bool, embedLabel, embedEndpoint string,
	needLLM bool, llmLabel, llmEndpoint string,
	needRerank bool, rerankLabel, rerankEndpoint string,
	needTriplex bool, triplexLabel, triplexEndpoint string,
) keysStep {
	type svcInfo struct {
		service  string
		label    string
		endpoint string
	}

	var candidates []svcInfo
	if needEmbed {
		candidates = append(candidates, svcInfo{"embed", embedLabel, embedEndpoint})
	}
	if needLLM {
		candidates = append(candidates, svcInfo{"llm", llmLabel, llmEndpoint})
	}
	if needRerank {
		candidates = append(candidates, svcInfo{"rerank", rerankLabel, rerankEndpoint})
	}
	if needTriplex {
		candidates = append(candidates, svcInfo{"triplex", triplexLabel, triplexEndpoint})
	}

	// Deduplicate by endpoint. The first service to claim an endpoint becomes
	// the "primary" entry; subsequent services sharing that endpoint are added
	// to its services list so the collected key is applied to all of them.
	seen := map[string]int{} // endpoint -> entries index
	var entries []keyEntry
	for _, c := range candidates {
		if idx, ok := seen[c.endpoint]; ok && c.endpoint != "" {
			// Same endpoint already has an entry — add this service to it.
			entries[idx].services = append(entries[idx].services, c.service)
		} else {
			// Build a label like "OpenRouter API Key".
			displayLabel := c.label
			if displayLabel == "" {
				displayLabel = "API"
			}
			e := keyEntry{
				service:  c.service,
				label:    displayLabel + " API Key",
				endpoint: c.endpoint,
			}
			if c.endpoint != "" {
				seen[c.endpoint] = len(entries)
			}
			entries = append(entries, e)
		}
	}

	inputs := make([]textinput.Model, len(entries))
	for i := range entries {
		ti := textinput.New()
		ti.Placeholder = "sk-..."
		ti.CharLimit = 256
		ti.Width = 50
		ti.EchoMode = textinput.EchoPassword
		inputs[i] = ti
	}
	if len(inputs) > 0 {
		inputs[0].Focus()
	}

	return keysStep{
		entries:       entries,
		inputs:        inputs,
		keyringOK:     config.KeyringAvailable(),
		collectedKeys: make(map[string]string),
	}
}

func (k keysStep) Init() tea.Cmd {
	if len(k.inputs) > 0 {
		return textinput.Blink
	}
	return nil
}

func (k keysStep) Update(msg tea.Msg) (keysStep, tea.Cmd) {
	if len(k.entries) == 0 {
		k.done = true
		return k, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			val := strings.TrimSpace(k.inputs[k.cursor].Value())
			if val != "" {
				entry := k.entries[k.cursor]
				k.collectedKeys[entry.service] = val
				// UX-12b: Apply the same key to all services sharing this endpoint.
				for _, svc := range entry.services {
					k.collectedKeys[svc] = val
				}
			}
			if k.cursor < len(k.entries)-1 {
				k.inputs[k.cursor].Blur()
				k.cursor++
				k.inputs[k.cursor].Focus()
				return k, textinput.Blink
			}
			k.done = true
			return k, nil
		case "tab":
			if k.cursor < len(k.entries)-1 {
				val := strings.TrimSpace(k.inputs[k.cursor].Value())
				if val != "" {
					entry := k.entries[k.cursor]
					k.collectedKeys[entry.service] = val
					for _, svc := range entry.services {
						k.collectedKeys[svc] = val
					}
				}
				k.inputs[k.cursor].Blur()
				k.cursor++
				k.inputs[k.cursor].Focus()
				return k, textinput.Blink
			}
		default:
			var cmd tea.Cmd
			k.inputs[k.cursor], cmd = k.inputs[k.cursor].Update(msg)
			return k, cmd
		}
	default:
		if k.cursor < len(k.inputs) {
			var cmd tea.Cmd
			k.inputs[k.cursor], cmd = k.inputs[k.cursor].Update(msg)
			return k, cmd
		}
	}
	return k, nil
}

func (k keysStep) View(width int) string {
	var b strings.Builder

	b.WriteString(headerStyle.Width(width).Render("API Keys"))
	b.WriteString("\n\n")

	if k.keyringOK {
		b.WriteString(successStyle.Render("Keys will be stored in your OS keychain."))
	} else {
		b.WriteString(warningStyle.Render("OS keychain not available. Set keys via environment variables instead."))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("(MARKEDUP_EMBED_API_KEY, MARKEDUP_LLM_API_KEY, MARKEDUP_RERANK_API_KEY)"))
	}
	b.WriteString("\n\n")

	for i, entry := range k.entries {
		if i == k.cursor {
			b.WriteString(selectedStyle.Render(entry.label + ":"))
		} else {
			b.WriteString(labelStyle.Render(entry.label + ":"))
		}
		b.WriteString("\n")
		b.WriteString(k.inputs[i].View())
		b.WriteString("\n\n")
	}

	b.WriteString(helpStyle.Render("Enter/Tab: next  |  Ctrl+C: cancel"))
	return b.String()
}

// ---------------------------------------------------------------------------
// Confirm step (step 6)
// ---------------------------------------------------------------------------

type confirmStep struct {
	cfg       *config.Config
	format    string
	keys      map[string]string
	keyringOK bool
	saved     bool
	err       error
}

func newConfirmStep(cfg *config.Config, format string, keys map[string]string) confirmStep {
	return confirmStep{
		cfg:       cfg,
		format:    format,
		keys:      keys,
		keyringOK: config.KeyringAvailable(),
	}
}

func (c confirmStep) View(width int) string {
	var b strings.Builder

	b.WriteString(headerStyle.Width(width).Render("Configuration Summary"))
	b.WriteString("\n\n")

	// Embedding.
	b.WriteString(labelStyle.Render("Embedding:"))
	b.WriteString("\n")
	if c.cfg.Embed.Endpoint != "" {
		// UX-10: show effective URL with known path suffix for display only.
		b.WriteString(fmt.Sprintf("  URL:      %s\n", c.cfg.Embed.Endpoint+"/v1/embeddings"))
		b.WriteString(fmt.Sprintf("  Model:    %s\n", c.cfg.Embed.Model))
	} else {
		b.WriteString(mutedStyle.Render("  (not configured)"))
		b.WriteString("\n")
	}

	// LLM.
	b.WriteString(labelStyle.Render("LLM:"))
	b.WriteString("\n")
	if c.cfg.LLM.Endpoint != "" {
		b.WriteString(fmt.Sprintf("  URL:      %s\n", c.cfg.LLM.Endpoint+"/v1/chat/completions"))
		b.WriteString(fmt.Sprintf("  Model:    %s\n", c.cfg.LLM.Model))
	} else {
		b.WriteString(mutedStyle.Render("  (not configured)"))
		b.WriteString("\n")
	}

	// Reranker.
	b.WriteString(labelStyle.Render("Reranker:"))
	b.WriteString("\n")
	if c.cfg.Rerank.Endpoint != "" {
		b.WriteString(fmt.Sprintf("  URL:      %s\n", c.cfg.Rerank.Endpoint+"/v1/rerank"))
		b.WriteString(fmt.Sprintf("  Model:    %s\n", c.cfg.Rerank.Model))
		if c.format != "" {
			b.WriteString(fmt.Sprintf("  Format:   %s\n", c.format))
		}
	} else {
		b.WriteString(mutedStyle.Render("  (not configured)"))
		b.WriteString("\n")
	}

	// Triplex.
	b.WriteString(labelStyle.Render("Triplex:"))
	b.WriteString("\n")
	if c.cfg.Triplex.Endpoint != "" {
		b.WriteString(fmt.Sprintf("  URL:      %s\n", c.cfg.Triplex.Endpoint))
		b.WriteString(fmt.Sprintf("  Model:    %s\n", c.cfg.Triplex.Model))
	} else {
		b.WriteString(mutedStyle.Render("  not configured (using LLM for extraction)"))
		b.WriteString("\n")
	}

	// Keys.
	if len(c.keys) > 0 {
		b.WriteString("\n")
		b.WriteString(labelStyle.Render("API Keys:"))
		b.WriteString("\n")
		for svc := range c.keys {
			b.WriteString(fmt.Sprintf("  %s: %s\n", svc, successStyle.Render("provided")))
		}
	}

	b.WriteString("\n")

	if c.saved {
		b.WriteString(successStyle.Render("Configuration saved to " + config.GlobalPath()))
		b.WriteString("\n")
	} else if c.err != nil {
		b.WriteString(warningStyle.Render("Error: " + c.err.Error()))
		b.WriteString("\n")
	} else {
		b.WriteString(fmt.Sprintf("Save to %s?\n\n", config.GlobalPath()))
		// UX-13: Show "Press Enter to save and continue" hint.
		b.WriteString(helpStyle.Render("Press Enter to save and continue  |  Esc: cancel  |  Ctrl+C: cancel"))
	}

	return b.String()
}
