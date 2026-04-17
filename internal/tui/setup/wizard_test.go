package setup

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Clarit-AI/markedup/config"
)

func TestNewWizardModel_InitialState(t *testing.T) {
	m := NewWizardModel()
	assert.Equal(t, 0, m.step)
	assert.NotNil(t, m.config)
	assert.False(t, m.cancelled)
	assert.True(t, m.welcome.detecting)
}

func TestWizardModel_CtrlC_Cancels(t *testing.T) {
	m := NewWizardModel()

	// Ctrl+C at any step should cancel.
	for step := 0; step < 7; step++ {
		m.step = step
		result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		wm := result.(WizardModel)
		assert.True(t, wm.cancelled, "step %d should cancel on Ctrl+C", step)
		require.NotNil(t, cmd)
		msg := cmd()
		_, isQuit := msg.(tea.QuitMsg)
		assert.True(t, isQuit, "step %d should quit on Ctrl+C", step)
	}
}

func TestWizardModel_WindowResize(t *testing.T) {
	m := NewWizardModel()

	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	wm := result.(WizardModel)
	assert.Equal(t, 120, wm.width)
	assert.Equal(t, 40, wm.height)
}

func TestWizardModel_WelcomeStep_DetectDone(t *testing.T) {
	m := NewWizardModel()

	// Simulate detection completing.
	endpoints := []config.Endpoint{
		{Name: "Ollama", URL: "http://localhost:11434", Type: "multi", Healthy: true, Models: []string{"llama3"}},
	}
	result, _ := m.Update(detectDoneMsg{endpoints: endpoints})
	wm := result.(WizardModel)
	assert.False(t, wm.welcome.detecting)
	assert.Len(t, wm.welcome.detected, 1)
}

func TestWizardModel_WelcomeStep_EnterAdvances(t *testing.T) {
	m := NewWizardModel()

	// Simulate detection completing.
	result, _ := m.Update(detectDoneMsg{endpoints: nil})
	m = result.(WizardModel)

	// Press Enter to advance.
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	wm := result.(WizardModel)
	assert.Equal(t, 1, wm.step)
}

func TestWizardModel_WelcomeStep_EnterDuringDetectDoesNotAdvance(t *testing.T) {
	m := NewWizardModel()
	// Still detecting, press Enter.
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	wm := result.(WizardModel)
	assert.Equal(t, 0, wm.step, "should not advance while detecting")
}

func TestWizardModel_EscAtStep0_Cancels(t *testing.T) {
	m := NewWizardModel()
	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	wm := result.(WizardModel)
	assert.True(t, wm.cancelled)
	require.NotNil(t, cmd)
}

func TestWizardModel_EscAtStep1_GoesBack(t *testing.T) {
	m := NewWizardModel()
	// Fast-forward to step 1.
	m.step = 1
	m.embed = newProviderStep("Embed", "", nil, "embed", false, false)

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	wm := result.(WizardModel)
	assert.Equal(t, 0, wm.step)
}

func TestProviderStep_SkipSelection(t *testing.T) {
	ps := newProviderStep("Test", "desc", nil, "embed", true, false)

	// Cursor should start at "Skip" (last item).
	assert.Equal(t, len(ps.choices)-1, ps.cursor)
	assert.Equal(t, "skip", ps.choices[ps.cursor].kind)

	// Press Enter to select Skip.
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, ps.done)
}

func TestProviderStep_CloudSelection(t *testing.T) {
	ps := newProviderStep("Test", "", nil, "embed", false, false)

	// First items are cloud providers. Find OpenRouter.
	found := false
	for i, c := range ps.choices {
		if c.label == "OpenRouter" {
			ps.cursor = i
			found = true
			break
		}
	}
	require.True(t, found)

	// Select it.
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 2, ps.phase) // should be in model entry phase
	assert.Equal(t, "https://openrouter.ai/api", ps.selected.Endpoint)
	assert.True(t, ps.needsKey)
}

func TestProviderStep_DetectedEndpoint(t *testing.T) {
	detected := []config.Endpoint{
		{Name: "Ollama", URL: "http://localhost:11434", Type: "multi", Healthy: true, Models: []string{"nomic-embed-text"}},
	}
	ps := newProviderStep("Test", "", detected, "embed", false, false)

	// First choice should be the detected endpoint.
	assert.Equal(t, "detected", ps.choices[0].kind)
	assert.Contains(t, ps.choices[0].label, "Ollama")

	// Select it.
	ps.cursor = 0
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 2, ps.phase)
	assert.Equal(t, "http://localhost:11434", ps.selected.Endpoint)
	assert.Equal(t, "nomic-embed-text", ps.selected.Model)
	assert.False(t, ps.needsKey)
}

func TestProviderStep_CustomEndpoint(t *testing.T) {
	ps := newProviderStep("Test", "", nil, "embed", false, false)

	// Find "Custom endpoint".
	for i, c := range ps.choices {
		if c.kind == "custom" {
			ps.cursor = i
			break
		}
	}

	// Select custom.
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 1, ps.phase) // endpoint input phase

	// Esc should go back.
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, 0, ps.phase)
}

func TestProviderStep_RerankFormat(t *testing.T) {
	ps := newProviderStep("Rerank", "", nil, "rerank", true, true)

	// Select a cloud provider for reranking.
	for i, c := range ps.choices {
		if c.label == "OpenRouter" {
			ps.cursor = i
			break
		}
	}
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 2, ps.phase) // model input

	// Type a model name.
	ps.modelIn.SetValue("reranker-v1")
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 3, ps.phase) // format selection
	assert.False(t, ps.done)

	// Select jina format.
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, ps.done)
	assert.Equal(t, "jina", ps.formatVal)
}

func TestKeysStep_NoKeys(t *testing.T) {
	ks := newKeysStep(false, "", "", false, "", "", false, "", "", false, "", "", "")
	ks, _ = ks.Update(nil)
	assert.True(t, ks.done)
}

func TestKeysStep_WithKeys(t *testing.T) {
	ks := newKeysStep(true, "Ollama", "http://localhost:11434", false, "", "", false, "", "", false, "", "", "")
	assert.Len(t, ks.entries, 1)
	assert.Equal(t, "embed", ks.entries[0].service)
}

func TestConfirmStep_View(t *testing.T) {
	cfg := &config.Config{
		Embed: config.ServiceConfig{Endpoint: "http://localhost:11434", Model: "nomic"},
		LLM:   config.ServiceConfig{Endpoint: "https://openrouter.ai/api", Model: "llama3"},
	}
	cs := newConfirmStep(cfg, "", map[string]string{"llm": "sk-test"})
	view := cs.View(80)
	assert.Contains(t, view, "http://localhost:11434")
	assert.Contains(t, view, "nomic")
	assert.Contains(t, view, "https://openrouter.ai/api")
	assert.Contains(t, view, "llama3")
	assert.Contains(t, view, "provided")
}

func TestWelcomeStep_View(t *testing.T) {
	ws := newWelcomeStep()
	// While detecting.
	view := ws.View(80)
	assert.Contains(t, view, "Welcome to markedup setup!")
	assert.Contains(t, view, "Detecting local model servers")

	// After detection with results.
	ws.detecting = false
	ws.detected = []config.Endpoint{
		{Name: "Ollama", URL: "http://localhost:11434", Type: "multi", Healthy: true},
	}
	view = ws.View(80)
	assert.Contains(t, view, "Ollama")
	assert.Contains(t, view, "Enter to continue")

	// After detection with no results.
	ws.detected = nil
	view = ws.View(80)
	assert.Contains(t, view, "No local model servers detected")
}

func TestWizardModel_FullFlow_SkipAll(t *testing.T) {
	m := NewWizardModel()

	// Complete detection.
	result, _ := m.Update(detectDoneMsg{endpoints: nil})
	m = result.(WizardModel)

	// Advance to embed step.
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(WizardModel)
	assert.Equal(t, 1, m.step)

	// Skip embed.
	// Navigate to "Skip" (last item).
	for m.embed.cursor < len(m.embed.choices)-1 {
		result, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = result.(WizardModel)
	}
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(WizardModel)
	assert.Equal(t, 2, m.step) // LLM step

	// Skip LLM.
	for m.llm.cursor < len(m.llm.choices)-1 {
		result, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = result.(WizardModel)
	}
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(WizardModel)
	assert.Equal(t, 3, m.step) // Rerank step

	// Skip Rerank (pre-selected on Skip for optional).
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(WizardModel)
	assert.Equal(t, 4, m.step) // Triplex step

	// Skip Triplex: navigate to Skip option (tab twice), then Enter.
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // endpoint→model
	m = result.(WizardModel)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // model→skip
	m = result.(WizardModel)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(WizardModel)
	// Should jump to confirm since no keys needed.
	assert.Equal(t, 6, m.step)
}

func TestWizardModel_View_AllSteps(t *testing.T) {
	m := NewWizardModel()
	m.width = 80
	m.height = 40

	// Each step should render without panicking.
	for step := 0; step < 7; step++ {
		m.step = step
		switch step {
		case 1:
			m.embed = newProviderStep("Embed", "", nil, "embed", false, false)
		case 2:
			m.llm = newProviderStep("LLM", "", nil, "llm", false, false)
		case 3:
			m.rerank = newProviderStep("Rerank", "", nil, "rerank", true, true)
		case 4:
			m.triplex = newTriplexStep(nil)
		case 5:
			m.keys = newKeysStep(true, "Ollama", "http://localhost:11434", false, "", "", false, "", "", false, "", "", "")
		case 6:
			m.confirm = newConfirmStep(m.config, "", nil)
		}

		view := m.View()
		assert.NotEmpty(t, view, "step %d view should not be empty", step)
		// Step indicator should be present.
		assert.Contains(t, view, "Welcome")
		assert.Contains(t, view, "Embed")
		assert.Contains(t, view, "Confirm")
	}
}

// ---------------------------------------------------------------------------
// Triplex step tests
// ---------------------------------------------------------------------------

func TestTriplexStep_Skip(t *testing.T) {
	ts := newTriplexStep(nil)

	// Navigate to Skip option (skipCursor=2), then press S to skip.
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyTab}) // 0→1
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyTab}) // 1→2
	assert.Equal(t, 2, ts.skipCursor, "should be on Skip option")

	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	assert.True(t, ts.done, "skip should mark done")
	assert.True(t, ts.skip, "skip should set skip=true")
	assert.Equal(t, "", ts.selected.Endpoint, "selected endpoint should be zero value")
	assert.Equal(t, "", ts.selected.Model, "selected model should be zero value")
	assert.False(t, ts.needsKey, "skipped triplex should not need a key")
}

func TestTriplexStep_Configure_Localhost(t *testing.T) {
	ts := newTriplexStep(nil)
	ts.endpointIn.SetValue("http://localhost:11434")

	// Press Enter to confirm.
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, ts.done)
	assert.False(t, ts.skip)
	assert.Equal(t, "http://localhost:11434", ts.selected.Endpoint)
	assert.Equal(t, triplexModelName, ts.selected.Model)
	assert.False(t, ts.needsKey, "localhost should not need a key")
}

func TestTriplexStep_Configure_CloudEndpoint(t *testing.T) {
	ts := newTriplexStep(nil)
	ts.endpointIn.SetValue("https://api.example.com")

	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, ts.done)
	assert.False(t, ts.skip)
	assert.Equal(t, "https://api.example.com", ts.selected.Endpoint)
	assert.Equal(t, triplexModelName, ts.selected.Model)
	assert.True(t, ts.needsKey, "cloud endpoint should need a key")
}

func TestTriplexStep_Configure_EmptyEndpoint_NoAdvance(t *testing.T) {
	ts := newTriplexStep(nil)
	ts.endpointIn.SetValue("")

	// Pressing Enter with empty endpoint should not advance.
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, ts.done, "empty endpoint should not confirm")
}

func TestTriplexStep_OllamaPreFill(t *testing.T) {
	detected := []config.Endpoint{
		{Name: "Ollama", URL: "http://localhost:11434", Type: "multi", Healthy: true},
	}
	ts := newTriplexStep(detected)
	assert.Equal(t, "http://localhost:11434", ts.endpointIn.Value(), "ollama endpoint should be pre-filled")
}

func TestTriplexStep_CursorNavigation(t *testing.T) {
	ts := newTriplexStep(nil)
	assert.Equal(t, 0, ts.skipCursor, "should start with endpoint focused")

	// Tab moves to model input.
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyTab})
	assert.Equal(t, 1, ts.skipCursor, "tab from endpoint should go to model")

	// Tab moves to skip option.
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyTab})
	assert.Equal(t, 2, ts.skipCursor, "tab from model should go to skip")

	// Tab wraps back to endpoint.
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyTab})
	assert.Equal(t, 0, ts.skipCursor, "tab from skip should wrap to endpoint")

	// Up from model goes to endpoint.
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyTab}) // 0→1
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 0, ts.skipCursor, "up from model should go to endpoint")

	// Up from skip goes to model.
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyTab}) // 0→1
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyTab}) // 1→2
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 1, ts.skipCursor, "up from skip should go to model")
}

func TestTriplexStep_View(t *testing.T) {
	ts := newTriplexStep(nil)
	view := ts.View(80)
	assert.Contains(t, view, "Structured Extractor")
	assert.Contains(t, view, "NuExtract-2.0")
	assert.Contains(t, view, "Model:")
	assert.Contains(t, view, triplexModelName, "editable model input should show default")
	assert.Contains(t, view, "Skip")
}

func TestWizardModel_TriplexStep(t *testing.T) {
	m := NewWizardModel()

	// Complete detection.
	result, _ := m.Update(detectDoneMsg{endpoints: nil})
	m = result.(WizardModel)

	// Advance to embed step.
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(WizardModel)
	assert.Equal(t, 1, m.step)

	// Skip embed (navigate to last item and press Enter).
	for m.embed.cursor < len(m.embed.choices)-1 {
		result, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = result.(WizardModel)
	}
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(WizardModel)
	assert.Equal(t, 2, m.step)

	// Skip LLM.
	for m.llm.cursor < len(m.llm.choices)-1 {
		result, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = result.(WizardModel)
	}
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(WizardModel)
	assert.Equal(t, 3, m.step)

	// Skip Rerank (pre-selected on Skip).
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(WizardModel)
	assert.Equal(t, 4, m.step, "should advance to triplex step")

	// Configure Triplex with localhost endpoint.
	m.triplex.endpointIn.SetValue("http://localhost:11434")
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(WizardModel)

	// No cloud keys needed — should jump straight to confirm (step 6).
	assert.Equal(t, 6, m.step, "no cloud keys → jump to confirm")
	assert.Equal(t, "http://localhost:11434", m.config.Triplex.Endpoint)
	assert.Equal(t, triplexModelName, m.config.Triplex.Model)
}

func TestWizardModel_EscFromTriplex_GoesBackToRerank(t *testing.T) {
	m := NewWizardModel()
	m.step = 4
	m.triplex = newTriplexStep(nil)
	// Push rerank (step 3) onto the history stack as the wizard would.
	m.stepHistory = []int{0, 1, 2, 3}

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	wm := result.(WizardModel)
	assert.Equal(t, 3, wm.step, "Esc from triplex should go back to rerank")
}

// ---------------------------------------------------------------------------
// Triplex editable model tests
// ---------------------------------------------------------------------------

func TestTriplexStep_CustomModel(t *testing.T) {
	ts := newTriplexStep(nil)

	// Model input should be pre-filled with the default.
	assert.Equal(t, triplexModelName, ts.modelIn.Value(), "model should default to triplexModelName")

	// Set a custom endpoint and custom model.
	ts.endpointIn.SetValue("http://localhost:11434")
	ts.modelIn.SetValue("my-custom-triplex-v2")

	// Press Enter to confirm.
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, ts.done)
	assert.False(t, ts.skip)
	assert.Equal(t, "http://localhost:11434", ts.selected.Endpoint)
	assert.Equal(t, "my-custom-triplex-v2", ts.selected.Model, "custom model name should be used")
	assert.False(t, ts.needsKey)
}

func TestTriplexStep_DefaultModel_WhenEmpty(t *testing.T) {
	ts := newTriplexStep(nil)

	ts.endpointIn.SetValue("http://localhost:11434")
	ts.modelIn.SetValue("") // empty model should fall back to default

	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, ts.done)
	assert.Equal(t, triplexModelName, ts.selected.Model, "empty model should fall back to default")
}

func TestTriplexStep_ModelPreFilled(t *testing.T) {
	ts := newTriplexStep(nil)
	assert.Equal(t, triplexModelName, ts.modelIn.Value(), "model input should be pre-filled with default")
}

func TestTriplexStep_SInEndpointInput_PassesThrough(t *testing.T) {
	ts := newTriplexStep(nil)
	assert.Equal(t, 0, ts.skipCursor, "should start on endpoint")

	// Pressing 's' in endpoint input should NOT skip — it should type 's'.
	ts, _ = ts.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	assert.False(t, ts.done, "pressing s in endpoint input should not skip")
	assert.False(t, ts.skip)
}

// ---------------------------------------------------------------------------
// Keyring pre-fill tests
// ---------------------------------------------------------------------------

func TestKeysStep_PrefilledFlag(t *testing.T) {
	// Create a keysStep and manually simulate pre-fill.
	ks := newKeysStep(true, "OpenRouter", "https://openrouter.ai/api", false, "", "", false, "", "", false, "", "", "")
	assert.Len(t, ks.entries, 1)

	// Manually set prefilled (since we can't use real keyring in tests).
	ks.entries[0].prefilled = true
	ks.inputs[0].SetValue("sk-existing-key")
	ks.collectedKeys["embed"] = "sk-existing-key"

	// Pressing Enter without changes should keep the pre-filled key.
	ks, _ = ks.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, ks.done)
	assert.Equal(t, "sk-existing-key", ks.collectedKeys["embed"])
}

func TestKeysStep_PrefilledView(t *testing.T) {
	ks := newKeysStep(true, "OpenRouter", "https://openrouter.ai/api", false, "", "", false, "", "", false, "", "", "")
	ks.entries[0].prefilled = true

	view := ks.View(80)
	assert.Contains(t, view, "Existing API keys found", "should show pre-fill note")
	assert.Contains(t, view, "(saved)", "should show saved indicator on entry")
}

func TestKeysStep_PrefilledOverwrite(t *testing.T) {
	ks := newKeysStep(true, "OpenRouter", "https://openrouter.ai/api", false, "", "", false, "", "", false, "", "", "")
	ks.entries[0].prefilled = true
	ks.inputs[0].SetValue("sk-old-key")
	ks.collectedKeys["embed"] = "sk-old-key"

	// Type a new key (simulate by setting value directly).
	ks.inputs[0].SetValue("sk-new-key")

	// Press Enter — should use the new value.
	ks, _ = ks.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, ks.done)
	assert.Equal(t, "sk-new-key", ks.collectedKeys["embed"], "new key should overwrite old")
}

func TestKeysStep_NoPrefillNote_WhenNoPrefill(t *testing.T) {
	ks := newKeysStep(true, "OpenRouter", "https://openrouter.ai/api", false, "", "", false, "", "", false, "", "", "")

	view := ks.View(80)
	assert.NotContains(t, view, "Existing API keys found", "should not show pre-fill note when no keys pre-filled")
	assert.NotContains(t, view, "(saved)")
}

// ---------------------------------------------------------------------------

func TestProviderStep_CursorBounds(t *testing.T) {
	ps := newProviderStep("Test", "", nil, "embed", false, false)

	// Move up when already at 0.
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 0, ps.cursor)

	// Move down to end.
	for i := 0; i < len(ps.choices)+5; i++ {
		ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	assert.Equal(t, len(ps.choices)-1, ps.cursor)
}

func TestWizard_TriplexModel_WritesToTriplexConfig(t *testing.T) {
	m := NewWizardModel()
	m.triplex = triplexStep{
		selected: config.ServiceConfig{
			Endpoint: "http://localhost:11434",
			Model:    "Phi-3-mini-128k-instruct/triplex",
		},
		done: true,
	}
	m.step = 4
	updated, _ := m.updateTriplex(tea.KeyMsg{})
	final, ok := updated.(WizardModel)
	require.True(t, ok)
	assert.Equal(t, "http://localhost:11434", final.config.Triplex.Endpoint)
	assert.Empty(t, final.config.NuExtract.Endpoint, "Triplex model should not populate NuExtract config")
	assert.Empty(t, final.config.Format, "Triplex model should not set Format explicitly")
}

func TestWizard_NuExtractModel_WritesToNuExtractConfig(t *testing.T) {
	m := NewWizardModel()
	m.triplex = triplexStep{
		selected: config.ServiceConfig{
			Endpoint: "https://xyz.hf.space",
			Model:    "numind/NuExtract-2.0-8B",
		},
		done: true,
	}
	m.step = 4
	updated, _ := m.updateTriplex(tea.KeyMsg{})
	final, ok := updated.(WizardModel)
	require.True(t, ok)
	assert.Equal(t, "https://xyz.hf.space", final.config.NuExtract.Endpoint)
	assert.Equal(t, "numind/NuExtract-2.0-8B", final.config.NuExtract.Model)
	assert.Equal(t, "nuextract", final.config.Format)
	assert.Empty(t, final.config.Triplex.Endpoint, "NuExtract model should not populate Triplex config")
}

func TestWizard_SwitchExtractor_NuExtractThenTriplex_ClearsStaleNuExtract(t *testing.T) {
	m := NewWizardModel()
	// Simulate prior NuExtract run leaving state.
	m.config.NuExtract = config.NuExtractConfig{
		ServiceConfig: config.ServiceConfig{Endpoint: "https://old.nx.space", Model: "numind/NuExtract-2.0-8B"},
	}
	m.config.Format = "nuextract"

	// User re-runs wizard and picks Triplex.
	m.triplex = triplexStep{
		selected: config.ServiceConfig{Endpoint: "http://localhost:11434", Model: "Phi-3-mini-128k-instruct/triplex"},
		done:     true,
	}
	m.step = 4
	updated, _ := m.updateTriplex(tea.KeyMsg{})
	final := updated.(WizardModel)

	assert.Equal(t, "http://localhost:11434", final.config.Triplex.Endpoint)
	assert.Empty(t, final.config.NuExtract.Endpoint, "stale NuExtract endpoint should be cleared")
	assert.Empty(t, final.config.NuExtract.Model, "stale NuExtract model should be cleared")
	assert.Empty(t, final.config.Format, "Format should reset when switching to Triplex")
	assert.Equal(t, "triplex", final.extractorService)
}

func TestWizard_SwitchExtractor_TriplexThenNuExtract_ClearsStaleTriplex(t *testing.T) {
	m := NewWizardModel()
	m.config.Triplex = config.ServiceConfig{Endpoint: "http://old.triplex", Model: "phi3"}

	m.triplex = triplexStep{
		selected: config.ServiceConfig{Endpoint: "https://hf.space", Model: "numind/NuExtract-2.0-8B"},
		done:     true,
	}
	m.step = 4
	updated, _ := m.updateTriplex(tea.KeyMsg{})
	final := updated.(WizardModel)

	assert.Empty(t, final.config.Triplex.Endpoint, "stale Triplex endpoint should be cleared")
	assert.Equal(t, "https://hf.space", final.config.NuExtract.Endpoint)
	assert.Equal(t, "nuextract", final.config.Format)
	assert.Equal(t, "nuextract", final.extractorService)
}

func TestWizard_SkipExtractor_ClearsBoth(t *testing.T) {
	m := NewWizardModel()
	m.config.Triplex = config.ServiceConfig{Endpoint: "http://old.triplex"}
	m.config.NuExtract = config.NuExtractConfig{
		ServiceConfig: config.ServiceConfig{Endpoint: "http://old.nuextract"},
	}
	m.config.Format = "triplex"

	m.triplex = triplexStep{skip: true, done: true}
	m.step = 4
	updated, _ := m.updateTriplex(tea.KeyMsg{})
	final := updated.(WizardModel)

	assert.Empty(t, final.config.Triplex.Endpoint, "skip should clear Triplex")
	assert.Empty(t, final.config.NuExtract.Endpoint, "skip should clear NuExtract")
	assert.Empty(t, final.config.Format, "skip should reset Format")
	assert.Empty(t, final.extractorService)
}

func TestNewKeysStep_NuExtractService(t *testing.T) {
	ks := newKeysStep(
		false, "", "",
		false, "", "",
		false, "", "",
		true, "NuExtract-2.0", "https://hf.space",
		"nuextract",
	)
	require.Len(t, ks.entries, 1)
	assert.Equal(t, "nuextract", ks.entries[0].service, "extractor service should be nuextract when passed")
}

func TestNewKeysStep_TriplexServiceDefault(t *testing.T) {
	ks := newKeysStep(
		false, "", "",
		false, "", "",
		false, "", "",
		true, "Triplex", "http://localhost:11434",
		"triplex",
	)
	require.Len(t, ks.entries, 1)
	assert.Equal(t, "triplex", ks.entries[0].service)
}
