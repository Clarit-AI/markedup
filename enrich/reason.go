package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ReasonConfig configures the GraphReasoner.
type ReasonConfig struct {
	Endpoint   string
	Model      string
	APIKey     string
	HTTPClient *http.Client
}

// ReasonResult is the parsed LLM response from a graph reasoning call.
type ReasonResult struct {
	Thinking          string   `json:"thinking"`
	PageIDs           []string `json:"page_ids"`
	RelationshipPaths []string `json:"relationship_paths"`
}

// GraphReasoner sends a graph navigation prompt to an LLM and returns
// the pages it selects for answering a query.
type GraphReasoner struct {
	cfg    ReasonConfig
	client *http.Client
}

// NewGraphReasoner creates a new GraphReasoner with the given config.
func NewGraphReasoner(cfg ReasonConfig) *GraphReasoner {
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &GraphReasoner{cfg: cfg, client: client}
}

// graphNavigationSystemPrompt is the system prompt used for all reasoning calls.
const graphNavigationSystemPrompt = `You are given a question and the structure of a knowledge graph.
Each node represents a markdown page with metadata. Edges represent typed relationships between pages.

Your task: identify which pages are most likely to contain or contribute to answering the question.
Consider relationship chains, entity types, temporal validity, and confidence scores.

Reply ONLY in JSON (no markdown fences):
{
  "thinking": "Your reasoning about which pages are relevant and why, considering the graph structure",
  "page_ids": ["id1", "id2", ...],
  "relationship_paths": ["id1 --studies--> id2"]
}`

// Reason takes a query and a compact graph JSON string, sends the navigation
// prompt to the LLM, and returns the reasoning result.
func (r *GraphReasoner) Reason(ctx context.Context, query, compactGraphJSON string) (*ReasonResult, error) {
	userMsg := fmt.Sprintf("Question: %s\n\nKnowledge graph structure:\n%s", query, compactGraphJSON)

	req := chatRequest{
		Model: r.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: graphNavigationSystemPrompt},
			{Role: "user", Content: userMsg},
		},
	}

	content, err := r.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	return parseReasonOutput(content)
}

// sendRequest sends a chat completion request and returns the first choice's content.
func (r *GraphReasoner) sendRequest(ctx context.Context, reqBody chatRequest) (string, error) {
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("enrich: marshal reason request: %w", err)
	}

	endpoint := strings.TrimRight(r.cfg.Endpoint, "/") + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("enrich: create reason request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if r.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+r.cfg.APIKey)
	}

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("enrich: reason request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("enrich: read reason response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("enrich: model returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("enrich: unmarshal reason response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("enrich: reason model returned no choices")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// parseReasonOutput parses the LLM's JSON response into a ReasonResult.
func parseReasonOutput(content string) (*ReasonResult, error) {
	content = strings.TrimSpace(content)
	// Strip markdown code fences if the model wrapped the output.
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) >= 3 {
			content = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	content = strings.TrimSpace(content)

	var result ReasonResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("parse reason output: %w", err)
	}

	return &result, nil
}
