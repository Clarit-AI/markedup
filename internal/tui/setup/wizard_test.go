package setup

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/KHAEntertainment/markedup/config"
)

func TestNewWizardModel_InitialState(t *testing.T) {
	m := newWizardModel()
	assert.Equal(t, 0, m.step)
	assert.NotNil(t, m.config)
	assert.False(t, m.cancelled)
	assert.True(t, m.welcome.detecting)
}

func TestWizardModel_CtrlC_Cancels(t *testing.T) {
	m := newWizardModel()

	// Ctrl+C at any step should cancel.
	for step := 0; step < 6; step++ {
		m.step = step
		result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		wm := result.(wizardModel)
		assert.True(t, wm.cancelled, "step %d should cancel on Ctrl+C", step)
		require.NotNil(t, cmd)
		msg := cmd()
		_, isQuit := msg.(tea.QuitMsg)
		assert.True(t, isQuit, "step %d should quit on Ctrl+C", step)
	}
}

func TestWizardModel_WindowResize(t *testing.T) {
	m := newWizardModel()

	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	wm := result.(wizardModel)
	assert.Equal(t, 120, wm.width)
	assert.Equal(t, 40, wm.height)
}

func TestWizardModel_WelcomeStep_DetectDone(t *testing.T) {
	m := newWizardModel()

	// Simulate detection completing.
	endpoints := []config.Endpoint{
		{Name: "Ollama", URL: "http://localhost:11434", Type: "multi", Healthy: true, Models: []string{"llama3"}},
	}
	result, _ := m.Update(detectDoneMsg{endpoints: endpoints})
	wm := result.(wizardModel)
	assert.False(t, wm.welcome.detecting)
	assert.Len(t, wm.welcome.detected, 1)
}

func TestWizardModel_WelcomeStep_EnterAdvances(t *testing.T) {
	m := newWizardModel()

	// Simulate detection completing.
	result, _ := m.Update(detectDoneMsg{endpoints: nil})
	m = result.(wizardModel)

	// Press Enter to advance.
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	wm := result.(wizardModel)
	assert.Equal(t, 1, wm.step)
}

func TestWizardModel_WelcomeStep_EnterDuringDetectDoesNotAdvance(t *testing.T) {
	m := newWizardModel()
	// Still detecting, press Enter.
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	wm := result.(wizardModel)
	assert.Equal(t, 0, wm.step, "should not advance while detecting")
}

func TestWizardModel_EscAtStep0_Cancels(t *testing.T) {
	m := newWizardModel()
	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	wm := result.(wizardModel)
	assert.True(t, wm.cancelled)
	require.NotNil(t, cmd)
}

func TestWizardModel_EscAtStep1_GoesBack(t *testing.T) {
	m := newWizardModel()
	// Fast-forward to step 1.
	m.step = 1
	m.embed = newProviderStep("Embed", "", nil, "embed", false, false)

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	wm := result.(wizardModel)
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
	ks := newKeysStep(false, false, false)
	ks, _ = ks.Update(nil)
	assert.True(t, ks.done)
}

func TestKeysStep_WithKeys(t *testing.T) {
	ks := newKeysStep(true, false, false)
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
	m := newWizardModel()

	// Complete detection.
	result, _ := m.Update(detectDoneMsg{endpoints: nil})
	m = result.(wizardModel)

	// Advance to embed step.
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(wizardModel)
	assert.Equal(t, 1, m.step)

	// Skip embed.
	// Navigate to "Skip" (last item).
	for m.embed.cursor < len(m.embed.choices)-1 {
		result, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = result.(wizardModel)
	}
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(wizardModel)
	assert.Equal(t, 2, m.step) // LLM step

	// Skip LLM.
	for m.llm.cursor < len(m.llm.choices)-1 {
		result, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = result.(wizardModel)
	}
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(wizardModel)
	assert.Equal(t, 3, m.step) // Rerank step

	// Skip Rerank (pre-selected on Skip for optional).
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(wizardModel)
	// Should jump to confirm since no keys needed.
	assert.Equal(t, 5, m.step)
}

func TestWizardModel_View_AllSteps(t *testing.T) {
	m := newWizardModel()
	m.width = 80
	m.height = 40

	// Each step should render without panicking.
	for step := 0; step < 6; step++ {
		m.step = step
		switch step {
		case 1:
			m.embed = newProviderStep("Embed", "", nil, "embed", false, false)
		case 2:
			m.llm = newProviderStep("LLM", "", nil, "llm", false, false)
		case 3:
			m.rerank = newProviderStep("Rerank", "", nil, "rerank", true, true)
		case 4:
			m.keys = newKeysStep(true, false, false)
		case 5:
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
