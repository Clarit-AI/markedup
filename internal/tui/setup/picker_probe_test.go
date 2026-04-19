package setup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Clarit-AI/markedup/config"
)

// ---------------------------------------------------------------------------
// #111: filterModelsByRole heuristic
// ---------------------------------------------------------------------------

func TestFilterModelsByRole_Embed(t *testing.T) {
	models := []string{
		"nomic-embed-text",
		"all-MiniLM-L6-v2",
		"bge-m3",
		"llama3:8b",
		"granite-3.1-dense:2b",
		"bge-reranker-v2-m3",
	}
	got := filterModelsByRole(models, "embed")
	assert.Contains(t, got, "nomic-embed-text")
	assert.Contains(t, got, "all-MiniLM-L6-v2")
	assert.Contains(t, got, "bge-m3")
	assert.NotContains(t, got, "llama3:8b")
	assert.NotContains(t, got, "granite-3.1-dense:2b")
	// Reranker model should NOT be classified as embed even though "bge" matches.
	assert.NotContains(t, got, "bge-reranker-v2-m3")
}

func TestFilterModelsByRole_LLM(t *testing.T) {
	models := []string{
		"llama3:8b",
		"granite-3.1-dense:2b",
		"nomic-embed-text",
		"bge-reranker-v2-m3",
	}
	got := filterModelsByRole(models, "llm")
	assert.Contains(t, got, "llama3:8b")
	assert.Contains(t, got, "granite-3.1-dense:2b")
	assert.NotContains(t, got, "nomic-embed-text")
	assert.NotContains(t, got, "bge-reranker-v2-m3")
}

func TestFilterModelsByRole_Rerank(t *testing.T) {
	models := []string{
		"bge-reranker-v2-m3",
		"jina-reranker-v2-base",
		"llama3:8b",
		"nomic-embed-text",
	}
	got := filterModelsByRole(models, "rerank")
	assert.Contains(t, got, "bge-reranker-v2-m3")
	assert.Contains(t, got, "jina-reranker-v2-base")
	assert.NotContains(t, got, "llama3:8b")
	assert.NotContains(t, got, "nomic-embed-text")
}

func TestFilterModelsByRole_Fallback(t *testing.T) {
	// No matches → return the full list.
	models := []string{"foo", "bar"}
	got := filterModelsByRole(models, "embed")
	assert.Equal(t, models, got)
}

func TestFilterModelsByRole_Empty(t *testing.T) {
	assert.Nil(t, filterModelsByRole(nil, "embed"))
}

// ---------------------------------------------------------------------------
// #111: model picker activates when detected endpoint has models
// ---------------------------------------------------------------------------

func TestProviderStep_PickerActivatesForDetected(t *testing.T) {
	detected := []config.Endpoint{
		{
			Name:    "Ollama",
			URL:     "http://localhost:11434",
			Type:    "multi",
			Healthy: true,
			Models:  []string{"nomic-embed-text", "all-MiniLM-L6-v2", "llama3:8b"},
		},
	}
	ps := newProviderStep("Embed", "", detected, "embed", false, false)
	require.Equal(t, "detected", ps.choices[0].kind)

	ps.cursor = 0
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.Equal(t, 2, ps.phase)
	assert.True(t, ps.pickerActive, "picker should be active for detected endpoint with models")
	// Heuristic should have filtered the LLM model out for embed role.
	assert.NotContains(t, ps.pickerModels, "llama3:8b")
	assert.Contains(t, ps.pickerModels, "nomic-embed-text")
	assert.Equal(t, ps.pickerModels[0], ps.selected.Model)
}

func TestProviderStep_PickerNavigateAndSelect(t *testing.T) {
	detected := []config.Endpoint{
		{
			Name: "Ollama", URL: "http://localhost:11434", Type: "multi", Healthy: true,
			Models: []string{"nomic-embed-text", "all-MiniLM-L6-v2"},
		},
	}
	ps := newProviderStep("Embed", "", detected, "embed", false, false)
	ps.cursor = 0
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.True(t, ps.pickerActive)
	require.Equal(t, "nomic-embed-text", ps.selected.Model)

	// Down → second model.
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, "all-MiniLM-L6-v2", ps.selected.Model)

	// Enter → done.
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, ps.done)
	assert.Equal(t, "all-MiniLM-L6-v2", ps.selected.Model)
}

func TestProviderStep_PickerSwitchToFreeText(t *testing.T) {
	detected := []config.Endpoint{
		{
			Name: "Ollama", URL: "http://localhost:11434", Type: "multi", Healthy: true,
			Models: []string{"nomic-embed-text"},
		},
	}
	ps := newProviderStep("Embed", "", detected, "embed", false, false)
	ps.cursor = 0
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.True(t, ps.pickerActive)

	// 'm' switches to free-text input.
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	assert.False(t, ps.pickerActive)
}

func TestProviderStep_PickerNotActiveForCloud(t *testing.T) {
	ps := newProviderStep("LLM", "", nil, "llm", false, false)
	for i, c := range ps.choices {
		if c.label == "OpenAI" {
			ps.cursor = i
			break
		}
	}
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 2, ps.phase)
	assert.False(t, ps.pickerActive, "cloud providers should keep free-text input")
}

// ---------------------------------------------------------------------------
// #118: Test Connection — t key in picker mode dispatches probe
// ---------------------------------------------------------------------------

func TestProviderStep_TKeyDispatchesProbe(t *testing.T) {
	detected := []config.Endpoint{
		{
			Name: "Ollama", URL: "http://localhost:11434", Type: "multi", Healthy: true,
			Models: []string{"nomic-embed-text"},
		},
	}
	ps := newProviderStep("Embed", "", detected, "embed", false, false)
	ps.cursor = 0
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.True(t, ps.pickerActive)

	// Press 't' — should set probeRunning=true and return a non-nil cmd.
	ps2, cmd := ps.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	assert.True(t, ps2.probeRunning)
	assert.NotNil(t, cmd, "probe command should be returned")
}

func TestProviderStep_CtrlTDispatchesProbe(t *testing.T) {
	// Free-text mode: Ctrl+T should dispatch probe even with focused input.
	ps := newProviderStep("LLM", "", nil, "llm", false, false)
	for i, c := range ps.choices {
		if c.label == "OpenAI" {
			ps.cursor = i
			break
		}
	}
	ps, _ = ps.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.False(t, ps.pickerActive)
	ps.modelIn.SetValue("gpt-4o-mini")

	ps2, cmd := ps.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	assert.True(t, ps2.probeRunning)
	assert.NotNil(t, cmd)
}

func TestProviderStep_ProbeResultRendered(t *testing.T) {
	ps := newProviderStep("LLM", "", nil, "llm", false, false)
	ps.serviceName = "llm"
	ps.phase = 2
	ps.selected.Endpoint = "http://x"
	ps.selected.Model = "m"

	// Inject success result.
	ok := config.ProbeResult{OK: true, Detail: "ok"}
	psOK, _ := ps.Update(probeResultMsg{step: "llm", result: ok})
	require.NotNil(t, psOK.probeResult)
	assert.True(t, psOK.probeResult.OK)
	assert.False(t, psOK.probeRunning)

	// Render must contain success indicator.
	view := psOK.View(80)
	assert.Contains(t, view, "Connected")

	// Inject failure result.
	fail := config.ProbeResult{OK: false, Detail: "boom"}
	psFail, _ := ps.Update(probeResultMsg{step: "llm", result: fail})
	view = psFail.View(80)
	assert.Contains(t, view, "Failed")
	assert.Contains(t, view, "boom")
}

func TestProviderStep_ProbeResultIgnoresOtherSteps(t *testing.T) {
	ps := newProviderStep("Embed", "", nil, "embed", false, false)
	ps.serviceName = "embed"

	ps, _ = ps.Update(probeResultMsg{step: "llm", result: config.ProbeResult{OK: true}})
	assert.Nil(t, ps.probeResult, "probe results from other steps must be ignored")
}

// ---------------------------------------------------------------------------
// #118: end-to-end probe via fake HTTP server (sanity check that tea.Cmd
// produced by the step actually hits the wire)
// ---------------------------------------------------------------------------

func TestProviderStep_ProbeEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"embedding": []float64{1, 2, 3}}},
		})
	}))
	defer srv.Close()

	ps := newProviderStep("Embed", "", nil, "embed", false, false)
	ps.serviceName = "embed"
	ps.phase = 2
	ps.pickerActive = true
	ps.pickerModels = []string{"x"}
	ps.selected.Endpoint = srv.URL
	ps.selected.Model = "x"

	ps2, cmd := ps.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	require.True(t, ps2.probeRunning)
	require.NotNil(t, cmd)

	// Execute the command synchronously to capture the message.
	msg := cmd()
	pr, ok := msg.(probeResultMsg)
	require.True(t, ok, "expected probeResultMsg, got %T", msg)
	require.Equal(t, "embed", pr.step)
	assert.True(t, pr.result.OK, "probe should succeed: %s", pr.result.Detail)
}

// ---------------------------------------------------------------------------
// #118: triplexStep Ctrl+T dispatch + NuExtract routing
// ---------------------------------------------------------------------------

func TestTriplexStep_CtrlTDispatchesTriplex(t *testing.T) {
	ts := newTriplexStep(nil)
	ts.endpointIn.SetValue("http://localhost:11434")
	ts.modelIn.SetValue(triplexModelName)

	ts2, cmd := ts.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	assert.True(t, ts2.probeRunning)
	require.NotNil(t, cmd)
}

func TestTriplexStep_CtrlTRoutesNuExtract(t *testing.T) {
	// Verify routing logic via end-to-end probe through a fake server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	ts := newTriplexStep(nil)
	ts.endpointIn.SetValue(srv.URL)
	ts.modelIn.SetValue("numind/NuExtract-2.0-2B")

	_, cmd := ts.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	require.NotNil(t, cmd)

	// Run the command and check the routed step.
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		pr, ok := msg.(probeResultMsg)
		require.True(t, ok)
		assert.Equal(t, "nuextract", pr.step, "NuExtract model id should route to nuextract probe")
		assert.True(t, pr.result.OK)
	case <-time.After(5 * time.Second):
		t.Fatal("probe timed out")
	}
}

func TestTriplexStep_ProbeResultRendered(t *testing.T) {
	ts := newTriplexStep(nil)
	res := config.ProbeResult{OK: true, Detail: "ok"}
	ts2, _ := ts.Update(probeResultMsg{step: "triplex", result: res})
	require.NotNil(t, ts2.probeResult)
	view := ts2.View(80)
	assert.Contains(t, view, "Connected")
}
