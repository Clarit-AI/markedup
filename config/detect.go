// Package config provides configuration management for markedup, including
// auto-detection of local model serving endpoints.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Endpoint describes a discovered local model-serving endpoint.
type Endpoint struct {
	Name    string   // "Ollama", "LM Studio", "TEI", "vLLM", "llama.cpp"
	URL     string   // e.g. "http://localhost:11434"
	Type    string   // "embed", "llm", "rerank", "multi"
	Healthy bool
	Models  []string // populated when the service exposes a models API
	// Formats lists Tier 2 extractors this endpoint can serve (e.g. "nuextract"
	// when a NuExtract-2.0 model id is present in Models).
	Formats []string
}

// IsNuExtractModel reports whether a model id names a NuExtract-2.0 model.
// Matches "nuextract-2.0-*" or "numind/nuextract-2.0-*" case-insensitively.
func IsNuExtractModel(id string) bool {
	lower := strings.ToLower(id)
	lower = strings.TrimPrefix(lower, "numind/")
	return strings.HasPrefix(lower, "nuextract-2.0") || strings.HasPrefix(lower, "nuextract2.0")
}

// probeSpec defines how to detect a single local service.
type probeSpec struct {
	Name         string
	Port         int
	ProbePath    string
	SuccessCheck func(statusCode int, body []byte) bool
	Type         string
	ModelsPath   string
	ParseModels  func(body []byte) []string
}

// defaultProbes is the canonical probe table. Tests override the Port field
// to point at httptest servers.
var defaultProbes = []probeSpec{
	{
		Name:      "Ollama",
		Port:      11434,
		ProbePath: "/",
		SuccessCheck: func(statusCode int, body []byte) bool {
			return statusCode == http.StatusOK && strings.Contains(string(body), "Ollama is running")
		},
		Type:       "multi",
		ModelsPath: "/api/tags",
		ParseModels: func(body []byte) []string {
			var resp struct {
				Models []struct {
					Name string `json:"name"`
				} `json:"models"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				return nil
			}
			names := make([]string, 0, len(resp.Models))
			for _, m := range resp.Models {
				names = append(names, m.Name)
			}
			return names
		},
	},
	{
		Name:      "LM Studio",
		Port:      1234,
		ProbePath: "/v1/models",
		SuccessCheck: func(statusCode int, _ []byte) bool {
			return statusCode == http.StatusOK
		},
		Type:       "multi",
		ModelsPath: "", // same as probe path
		ParseModels: func(body []byte) []string {
			return parseOpenAIModels(body)
		},
	},
	{
		Name:      "TEI",
		Port:      8091,
		ProbePath: "/info",
		SuccessCheck: func(statusCode int, _ []byte) bool {
			return statusCode == http.StatusOK
		},
		Type:       "embed",
		ModelsPath: "/info",
		ParseModels: func(body []byte) []string {
			var resp struct {
				ModelID   string `json:"model_id"`
				ModelType string `json:"model_type"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				return nil
			}
			if resp.ModelID != "" {
				return []string{resp.ModelID}
			}
			return nil
		},
	},
	{
		Name:      "vLLM",
		Port:      8000,
		ProbePath: "/v1/models",
		SuccessCheck: func(statusCode int, _ []byte) bool {
			return statusCode == http.StatusOK
		},
		Type:       "llm",
		ModelsPath: "", // same as probe path
		ParseModels: func(body []byte) []string {
			return parseOpenAIModels(body)
		},
	},
	{
		Name:      "llama.cpp",
		Port:      8080,
		ProbePath: "/health",
		SuccessCheck: func(statusCode int, body []byte) bool {
			return statusCode == http.StatusOK || strings.Contains(string(body), "ok")
		},
		Type:       "llm",
		ModelsPath: "/v1/models",
		ParseModels: func(body []byte) []string {
			return parseOpenAIModels(body)
		},
	},
}

// parseOpenAIModels parses the OpenAI-compatible GET /v1/models response.
func parseOpenAIModels(body []byte) []string {
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	names := make([]string, 0, len(resp.Data))
	for _, m := range resp.Data {
		names = append(names, m.ID)
	}
	return names
}

// probeTimeout is the per-probe deadline.
const probeTimeout = 2 * time.Second

// DetectLocal probes well-known localhost ports concurrently and returns all
// discovered endpoints sorted by name. It never returns an error; unreachable
// services are silently skipped.
func DetectLocal(ctx context.Context) []Endpoint {
	return detectLocalWithProbes(ctx, defaultProbes)
}

// detectLocalWithProbes is the internal implementation that accepts a probe
// table, enabling tests to override ports.
func detectLocalWithProbes(ctx context.Context, probes []probeSpec) []Endpoint {
	var (
		mu      sync.Mutex
		results []Endpoint
	)

	g, gctx := errgroup.WithContext(ctx)

	for _, p := range probes {
		p := p // capture loop variable
		g.Go(func() error {
			ep := probeEndpoint(gctx, p)
			if ep != nil {
				// Skip services that expose a models API and returned an explicitly
				// empty list ([]string{}) — e.g. LM Studio or Ollama with nothing
				// loaded. Surfacing an endpoint with no available models is confusing.
				// Note: nil models (parse failure) are not filtered so the endpoint
				// is still surfaced as healthy.
				if p.ParseModels != nil && ep.Models != nil && len(ep.Models) == 0 {
					return nil
				}
				mu.Lock()
				results = append(results, *ep)
				mu.Unlock()
			}
			return nil // never propagate errors
		})
	}

	_ = g.Wait()

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

	return results
}

// probeEndpoint checks a single service and optionally fetches its model list.
func probeEndpoint(ctx context.Context, p probeSpec) *Endpoint {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	baseURL := fmt.Sprintf("http://localhost:%d", p.Port)

	// Health probe.
	statusCode, body, err := httpGet(pctx, baseURL+p.ProbePath)
	if err != nil || !p.SuccessCheck(statusCode, body) {
		return nil
	}

	ep := &Endpoint{
		Name:    p.Name,
		URL:     baseURL,
		Type:    p.Type,
		Healthy: true,
	}

	// Determine the type for TEI based on model_type in the response.
	if p.Name == "TEI" {
		ep.Type = teiType(body)
	}

	// Fetch models if the service exposes a models API.
	if p.ParseModels != nil {
		modelsBody := body // reuse probe response if paths match
		if p.ModelsPath != "" && p.ModelsPath != p.ProbePath {
			_, modelsBody, err = httpGet(pctx, baseURL+p.ModelsPath)
			if err != nil {
				// Probe succeeded but models fetch failed — still healthy.
				return ep
			}
		}
		ep.Models = p.ParseModels(modelsBody)
	}

	for _, m := range ep.Models {
		if IsNuExtractModel(m) {
			ep.Formats = append(ep.Formats, "nuextract")
			break
		}
	}

	return ep
}

// teiType returns "rerank" or "embed" based on the TEI /info response.
func teiType(body []byte) string {
	var info struct {
		ModelType string `json:"model_type"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "embed" // default
	}
	lower := strings.ToLower(info.ModelType)
	if strings.Contains(lower, "rerank") || strings.Contains(lower, "cross") {
		return "rerank"
	}
	return "embed"
}

// httpGet performs a GET request and returns the status code and body.
func httpGet(ctx context.Context, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}
