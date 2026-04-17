package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testServerPort extracts the port from an httptest.Server URL.
func testServerPort(t *testing.T, s *httptest.Server) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return port
}

// newOllamaServer returns a mock Ollama server.
func newOllamaServer(t *testing.T, models []string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "Ollama is running")
	})
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
		type model struct {
			Name string `json:"name"`
		}
		resp := struct {
			Models []model `json:"models"`
		}{}
		for _, m := range models {
			resp.Models = append(resp.Models, model{Name: m})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux)
}

// newOpenAIModelsServer returns a mock OpenAI-compatible /v1/models server.
func newOpenAIModelsServer(t *testing.T, models []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type model struct {
			ID string `json:"id"`
		}
		resp := struct {
			Data []model `json:"data"`
		}{}
		for _, m := range models {
			resp.Data = append(resp.Data, model{ID: m})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

// newTEIServer returns a mock TEI /info server.
func newTEIServer(t *testing.T, modelID, modelType string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			ModelID   string `json:"model_id"`
			ModelType string `json:"model_type"`
		}{ModelID: modelID, ModelType: modelType}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

// newLlamaCppServer returns a mock llama.cpp server with /health and /v1/models.
func newLlamaCppServer(t *testing.T, models []string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		type model struct {
			ID string `json:"id"`
		}
		resp := struct {
			Data []model `json:"data"`
		}{}
		for _, m := range models {
			resp.Data = append(resp.Data, model{ID: m})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux)
}

// overrideProbePort creates a copy of the default probes with the named
// service's port replaced.
func overrideProbePort(probes []probeSpec, name string, port int) []probeSpec {
	out := make([]probeSpec, len(probes))
	copy(out, probes)
	for i := range out {
		if out[i].Name == name {
			out[i].Port = port
		}
	}
	return out
}

func TestDetectLocal_AllHealthy(t *testing.T) {
	ollamaSrv := newOllamaServer(t, []string{"llama3", "mistral"})
	defer ollamaSrv.Close()

	lmSrv := newOpenAIModelsServer(t, []string{"deepseek-r1"})
	defer lmSrv.Close()

	teiSrv := newTEIServer(t, "BAAI/bge-small-en-v1.5", "Embedding")
	defer teiSrv.Close()

	vllmSrv := newOpenAIModelsServer(t, []string{"meta-llama/Llama-3-8b"})
	defer vllmSrv.Close()

	llamaCppSrv := newLlamaCppServer(t, []string{"ggml-model"})
	defer llamaCppSrv.Close()

	probes := defaultProbes
	probes = overrideProbePort(probes, "Ollama", testServerPort(t, ollamaSrv))
	probes = overrideProbePort(probes, "LM Studio", testServerPort(t, lmSrv))
	probes = overrideProbePort(probes, "TEI", testServerPort(t, teiSrv))
	probes = overrideProbePort(probes, "vLLM", testServerPort(t, vllmSrv))
	probes = overrideProbePort(probes, "llama.cpp", testServerPort(t, llamaCppSrv))

	eps := detectLocalWithProbes(context.Background(), probes)

	require.Len(t, eps, 5)

	// Sorted by name: LM Studio, Ollama, TEI, llama.cpp, vLLM
	assert.Equal(t, "LM Studio", eps[0].Name)
	assert.Equal(t, "multi", eps[0].Type)
	assert.True(t, eps[0].Healthy)
	assert.Equal(t, []string{"deepseek-r1"}, eps[0].Models)

	assert.Equal(t, "Ollama", eps[1].Name)
	assert.Equal(t, "multi", eps[1].Type)
	assert.True(t, eps[1].Healthy)
	assert.Equal(t, []string{"llama3", "mistral"}, eps[1].Models)

	assert.Equal(t, "TEI", eps[2].Name)
	assert.Equal(t, "embed", eps[2].Type)
	assert.True(t, eps[2].Healthy)
	assert.Equal(t, []string{"BAAI/bge-small-en-v1.5"}, eps[2].Models)

	assert.Equal(t, "llama.cpp", eps[3].Name)
	assert.Equal(t, "llm", eps[3].Type)
	assert.True(t, eps[3].Healthy)
	assert.Equal(t, []string{"ggml-model"}, eps[3].Models)

	assert.Equal(t, "vLLM", eps[4].Name)
	assert.Equal(t, "llm", eps[4].Type)
	assert.True(t, eps[4].Healthy)
	assert.Equal(t, []string{"meta-llama/Llama-3-8b"}, eps[4].Models)
}

func TestDetectLocal_AllUnreachable(t *testing.T) {
	// Use ports that are almost certainly not listening.
	probes := make([]probeSpec, len(defaultProbes))
	copy(probes, defaultProbes)
	for i := range probes {
		probes[i].Port = 59990 + i // unlikely to be in use
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eps := detectLocalWithProbes(ctx, probes)
	assert.Empty(t, eps)
}

func TestDetectLocal_Mixed(t *testing.T) {
	ollamaSrv := newOllamaServer(t, []string{"phi3"})
	defer ollamaSrv.Close()

	probes := make([]probeSpec, len(defaultProbes))
	copy(probes, defaultProbes)
	for i := range probes {
		if probes[i].Name == "Ollama" {
			probes[i].Port = testServerPort(t, ollamaSrv)
		} else {
			probes[i].Port = 59990 + i
		}
	}

	eps := detectLocalWithProbes(context.Background(), probes)
	require.Len(t, eps, 1)
	assert.Equal(t, "Ollama", eps[0].Name)
	assert.True(t, eps[0].Healthy)
	assert.Equal(t, []string{"phi3"}, eps[0].Models)
}

func TestDetectLocal_Timeout(t *testing.T) {
	// Server that never responds within the probe timeout.
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		fmt.Fprint(w, "Ollama is running")
	}))
	defer slowSrv.Close()

	probes := make([]probeSpec, len(defaultProbes))
	copy(probes, defaultProbes)
	for i := range probes {
		if probes[i].Name == "Ollama" {
			probes[i].Port = testServerPort(t, slowSrv)
		} else {
			probes[i].Port = 59990 + i
		}
	}

	start := time.Now()
	eps := detectLocalWithProbes(context.Background(), probes)
	elapsed := time.Since(start)

	assert.Empty(t, eps)
	// Should complete well within 3 seconds (2s timeout + margin).
	assert.Less(t, elapsed, 4*time.Second)
}

func TestDetectLocal_TEIReranker(t *testing.T) {
	teiSrv := newTEIServer(t, "cross-encoder/ms-marco", "CrossEncoder")
	defer teiSrv.Close()

	probes := make([]probeSpec, len(defaultProbes))
	copy(probes, defaultProbes)
	for i := range probes {
		if probes[i].Name == "TEI" {
			probes[i].Port = testServerPort(t, teiSrv)
		} else {
			probes[i].Port = 59990 + i
		}
	}

	eps := detectLocalWithProbes(context.Background(), probes)
	require.Len(t, eps, 1)
	assert.Equal(t, "TEI", eps[0].Name)
	assert.Equal(t, "rerank", eps[0].Type)
	assert.Equal(t, []string{"cross-encoder/ms-marco"}, eps[0].Models)
}

func TestDetectLocal_OllamaWrongBody(t *testing.T) {
	// Server returns 200 but wrong body — should not be detected.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Not the expected response")
	}))
	defer srv.Close()

	probes := make([]probeSpec, len(defaultProbes))
	copy(probes, defaultProbes)
	for i := range probes {
		if probes[i].Name == "Ollama" {
			probes[i].Port = testServerPort(t, srv)
		} else {
			probes[i].Port = 59990 + i
		}
	}

	eps := detectLocalWithProbes(context.Background(), probes)
	assert.Empty(t, eps)
}

func TestDetectLocal_ModelsParseFailure(t *testing.T) {
	// Ollama responds correctly for health but returns garbage for models.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "Ollama is running")
	})
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	probes := make([]probeSpec, len(defaultProbes))
	copy(probes, defaultProbes)
	for i := range probes {
		if probes[i].Name == "Ollama" {
			probes[i].Port = testServerPort(t, srv)
		} else {
			probes[i].Port = 59990 + i
		}
	}

	eps := detectLocalWithProbes(context.Background(), probes)
	require.Len(t, eps, 1)
	assert.Equal(t, "Ollama", eps[0].Name)
	assert.True(t, eps[0].Healthy)
	assert.Nil(t, eps[0].Models) // models parse failed, but endpoint still healthy
}

func TestDetectLocal_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	probes := make([]probeSpec, len(defaultProbes))
	copy(probes, defaultProbes)
	for i := range probes {
		probes[i].Port = 59990 + i
	}

	eps := detectLocalWithProbes(ctx, probes)
	assert.Empty(t, eps)
}

func TestParseOpenAIModels(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []string
	}{
		{
			name:   "valid",
			input:  `{"data":[{"id":"model-a"},{"id":"model-b"}]}`,
			expect: []string{"model-a", "model-b"},
		},
		{
			name:   "empty",
			input:  `{"data":[]}`,
			expect: []string{},
		},
		{
			name:   "invalid json",
			input:  `not json`,
			expect: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseOpenAIModels([]byte(tt.input))
			assert.Equal(t, tt.expect, result)
		})
	}
}

func TestLlamaCppHealthOkInBody(t *testing.T) {
	// llama.cpp returns non-200 status but body contains "ok".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"status":"ok"}`)
			return
		}
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"test-model"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	probes := make([]probeSpec, len(defaultProbes))
	copy(probes, defaultProbes)
	for i := range probes {
		if probes[i].Name == "llama.cpp" {
			probes[i].Port = testServerPort(t, srv)
		} else {
			probes[i].Port = 59990 + i
		}
	}

	eps := detectLocalWithProbes(context.Background(), probes)
	require.Len(t, eps, 1)
	assert.Equal(t, "llama.cpp", eps[0].Name)
	assert.True(t, eps[0].Healthy)
	assert.Equal(t, []string{"test-model"}, eps[0].Models)
}

func TestIsNuExtractModel(t *testing.T) {
	cases := map[string]bool{
		// Canonical matches
		"numind/NuExtract-2.0-8B":       true,
		"numind/nuextract-2.0-2b":       true,
		"NuExtract-2.0-4B":              true,
		"nuextract-2.0-8B-GGUF":         true,
		"someorg/nuextract-2.0-4b-gguf": true, // alt publisher
		// Rejections
		"triplex":              false,
		"llama3.1":             false,
		"numind/NuExtract-1.5": false,
		"nuextract-2.0":        false, // missing size suffix
		"nuextract-2.0foo":     false, // no dash separator
		"nuextract2.0-8b":      false, // missing hyphen before 2.0
		"":                     false,
	}
	for id, want := range cases {
		assert.Equal(t, want, IsNuExtractModel(id), "id=%q", id)
	}
}

func TestDetectLocal_NuExtractFormatNotTagged(t *testing.T) {
	srv := newOpenAIModelsServer(t, []string{"llama3.1", "mistral-7b"})
	defer srv.Close()
	probes := []probeSpec{{
		Name:         "LM Studio",
		Port:         testServerPort(t, srv),
		ProbePath:    "/v1/models",
		SuccessCheck: func(code int, _ []byte) bool { return code == http.StatusOK },
		Type:         "multi",
		ParseModels:  parseOpenAIModels,
	}}
	eps := detectLocalWithProbes(context.Background(), probes)
	require.Len(t, eps, 1)
	assert.Empty(t, eps[0].Formats, "non-NuExtract models should not tag Formats")
}

func TestDetectLocal_NuExtractTaggedOnce(t *testing.T) {
	srv := newOpenAIModelsServer(t, []string{
		"numind/NuExtract-2.0-8B",
		"numind/NuExtract-2.0-2B",
		"llama3.1",
	})
	defer srv.Close()
	probes := []probeSpec{{
		Name:         "LM Studio",
		Port:         testServerPort(t, srv),
		ProbePath:    "/v1/models",
		SuccessCheck: func(code int, _ []byte) bool { return code == http.StatusOK },
		Type:         "multi",
		ParseModels:  parseOpenAIModels,
	}}
	eps := detectLocalWithProbes(context.Background(), probes)
	require.Len(t, eps, 1)
	assert.Equal(t, []string{"nuextract"}, eps[0].Formats, "should tag nuextract exactly once")
}

func TestDetectLocal_NuExtractFormatTagged(t *testing.T) {
	srv := newOpenAIModelsServer(t, []string{"numind/NuExtract-2.0-8B"})
	defer srv.Close()

	probes := []probeSpec{{
		Name:         "LM Studio",
		Port:         testServerPort(t, srv),
		ProbePath:    "/v1/models",
		SuccessCheck: func(code int, _ []byte) bool { return code == http.StatusOK },
		Type:         "multi",
		ParseModels:  parseOpenAIModels,
	}}

	eps := detectLocalWithProbes(context.Background(), probes)
	require.Len(t, eps, 1)
	assert.Contains(t, eps[0].Formats, "nuextract")
}
