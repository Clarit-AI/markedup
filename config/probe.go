package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ProbeTimeout is the per-request deadline for Test Connection probes.
// Kept generous because cold-loaded local models can take several seconds
// to respond to the first request.
const ProbeTimeout = 10 * time.Second

// ProbeResult is returned by every Probe* function. OK reflects success
// (HTTP 200 + a structurally valid response). Detail provides a short
// human-readable summary suitable for inline display in the wizard
// (either an error reason or a confirmation snippet).
type ProbeResult struct {
	OK     bool
	Detail string
}

// ProbeEmbed sends a tiny embedding request to <endpoint>/v1/embeddings and
// verifies a non-empty vector is returned.
func ProbeEmbed(ctx context.Context, endpoint, model, apiKey string) ProbeResult {
	if endpoint == "" {
		return ProbeResult{OK: false, Detail: "no endpoint"}
	}
	body := map[string]any{
		"model": model,
		"input": "ping",
	}
	url := joinURL(endpoint, "/v1/embeddings")
	status, respBody, err := postJSON(ctx, url, apiKey, body)
	if err != nil {
		return ProbeResult{OK: false, Detail: err.Error()}
	}
	if status != http.StatusOK {
		return ProbeResult{OK: false, Detail: fmt.Sprintf("HTTP %d: %s", status, snippet(respBody))}
	}
	var resp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return ProbeResult{OK: false, Detail: "malformed response"}
	}
	if len(resp.Data) == 0 || len(resp.Data[0].Embedding) == 0 {
		return ProbeResult{OK: false, Detail: "no vector returned"}
	}
	return ProbeResult{OK: true, Detail: fmt.Sprintf("dim=%d", len(resp.Data[0].Embedding))}
}

// ProbeChat sends a tiny chat-completion to <endpoint>/v1/chat/completions
// and verifies the model returns a non-empty response. Used for LLM,
// Triplex, and NuExtract endpoints (all OpenAI-compatible chat).
func ProbeChat(ctx context.Context, endpoint, model, apiKey string) ProbeResult {
	if endpoint == "" {
		return ProbeResult{OK: false, Detail: "no endpoint"}
	}
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": "say ok"},
		},
		"max_tokens": 8,
	}
	url := joinURL(endpoint, "/v1/chat/completions")
	status, respBody, err := postJSON(ctx, url, apiKey, body)
	if err != nil {
		return ProbeResult{OK: false, Detail: err.Error()}
	}
	if status != http.StatusOK {
		return ProbeResult{OK: false, Detail: fmt.Sprintf("HTTP %d: %s", status, snippet(respBody))}
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return ProbeResult{OK: false, Detail: "malformed response"}
	}
	if len(resp.Choices) == 0 {
		return ProbeResult{OK: false, Detail: "no choices returned"}
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if content == "" {
		return ProbeResult{OK: false, Detail: "empty response"}
	}
	return ProbeResult{OK: true, Detail: snippet([]byte(content))}
}

// ProbeRerank sends a tiny rerank request to <endpoint>/v1/rerank and
// verifies a 200 response. The shape of rerank responses varies between
// providers, so we accept any 200 with a parseable JSON body.
func ProbeRerank(ctx context.Context, endpoint, model, apiKey string) ProbeResult {
	if endpoint == "" {
		return ProbeResult{OK: false, Detail: "no endpoint"}
	}
	body := map[string]any{
		"model":     model,
		"query":     "ping",
		"documents": []string{"pong", "other"},
		"top_n":     1,
	}
	url := joinURL(endpoint, "/v1/rerank")
	status, respBody, err := postJSON(ctx, url, apiKey, body)
	if err != nil {
		return ProbeResult{OK: false, Detail: err.Error()}
	}
	if status != http.StatusOK {
		return ProbeResult{OK: false, Detail: fmt.Sprintf("HTTP %d: %s", status, snippet(respBody))}
	}
	// Accept any structurally valid JSON object/array body.
	var any interface{}
	if err := json.Unmarshal(respBody, &any); err != nil {
		return ProbeResult{OK: false, Detail: "malformed response"}
	}
	return ProbeResult{OK: true, Detail: "ok"}
}

// ProbeService dispatches to the appropriate probe function based on
// service name. Recognized names: "embed", "llm", "triplex", "nuextract",
// "rerank". Unknown service names return a failed result.
func ProbeService(ctx context.Context, service, endpoint, model, apiKey string) ProbeResult {
	switch strings.ToLower(service) {
	case "embed":
		return ProbeEmbed(ctx, endpoint, model, apiKey)
	case "llm", "triplex", "nuextract":
		return ProbeChat(ctx, endpoint, model, apiKey)
	case "rerank":
		return ProbeRerank(ctx, endpoint, model, apiKey)
	default:
		return ProbeResult{OK: false, Detail: "unknown service: " + service}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// postJSON marshals body, posts it with optional Bearer auth, and reads the
// response. Per-call timeout is bounded by ProbeTimeout (the caller's ctx
// deadline still applies if it is shorter).
func postJSON(ctx context.Context, url, apiKey string, body any) (int, []byte, error) {
	pctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()

	buf, err := json.Marshal(body)
	if err != nil {
		return 0, nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(pctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, respBody, nil
}

// joinURL appends path to base, collapsing duplicate slashes at the boundary.
func joinURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

// snippet returns a short single-line preview of body content for inline
// display. Long bodies are truncated to 80 characters.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}
