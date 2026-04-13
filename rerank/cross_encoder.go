package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
)

// Format specifies the API request/response format for the reranker endpoint.
type Format int

const (
	// FormatJina uses the Jina reranker API format: POST /v1/rerank with
	// {"query": "...", "documents": ["..."], "model": "...", "top_n": N}.
	FormatJina Format = iota

	// FormatOpenAI uses an OpenAI-compatible reranker format with the same
	// request structure but potentially different response field names.
	FormatOpenAI
)

// Config holds the configuration for a CrossEncoderReranker.
type Config struct {
	// Endpoint is the base URL of the reranker API (e.g. "https://api.jina.ai").
	Endpoint string

	// Model is the reranker model name (e.g. "jina-reranker-v2-base-multilingual").
	Model string

	// APIKey is sent as "Bearer <APIKey>" in the Authorization header.
	// Mutually preferred over Token; if both set, APIKey takes precedence.
	APIKey string

	// Token is an alternative auth credential (e.g. future OAuth token).
	// Sent as "Bearer <Token>" if APIKey is empty.
	Token string

	// TopN limits the number of results returned. Zero means return all candidates.
	TopN int

	// Format selects the API request/response format. Defaults to FormatJina.
	Format Format

	// HTTPClient allows injecting a custom HTTP client for testing. If nil,
	// http.DefaultClient is used.
	HTTPClient *http.Client
}

// CrossEncoderReranker scores (query, document) pairs via an HTTP reranker API.
// It implements the Reranker interface.
type CrossEncoderReranker struct {
	cfg Config
}

// NewCrossEncoder creates a new CrossEncoderReranker with the given config.
func NewCrossEncoder(cfg Config) *CrossEncoderReranker {
	return &CrossEncoderReranker{cfg: cfg}
}

// Rerank sends each candidate's text to the configured reranker endpoint along
// with the query, and returns candidates re-ordered by cross-encoder score.
// Empty candidates returns an empty slice with no error.
func (r *CrossEncoderReranker) Rerank(ctx context.Context, query string, candidates []Candidate) ([]RankedResult, error) {
	if len(candidates) == 0 {
		return []RankedResult{}, nil
	}

	// Check context before making HTTP call.
	if err := ctx.Err(); err != nil {
		return nil, &APIError{
			Message: err.Error(),
			Err:     ErrContext,
		}
	}

	topN := r.cfg.TopN
	if topN <= 0 || topN > len(candidates) {
		topN = len(candidates)
	}

	scores, err := r.callAPI(ctx, query, candidates, topN)
	if err != nil {
		return nil, err
	}

	return buildRankedResults(candidates, scores, topN), nil
}

// jinaRequest is the request body for the Jina reranker API.
type jinaRequest struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	Model     string   `json:"model,omitempty"`
	TopN      int      `json:"top_n,omitempty"`
}

// jinaResponse is the response body from the Jina reranker API.
type jinaResponse struct {
	Results []jinaResult `json:"results"`
}

type jinaResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// openAIRerankResponse is the response body for OpenAI-compatible reranker APIs.
type openAIRerankResponse struct {
	Results []openAIRerankResult `json:"results"`
}

type openAIRerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// callAPI sends the rerank request to the configured endpoint and returns
// a map of candidate index -> score.
func (r *CrossEncoderReranker) callAPI(ctx context.Context, query string, candidates []Candidate, topN int) (map[int]float64, error) {
	documents := make([]string, len(candidates))
	for i, c := range candidates {
		documents[i] = c.Text
	}

	reqBody := jinaRequest{
		Query:     query,
		Documents: documents,
		Model:     r.cfg.Model,
		TopN:      topN,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("rerank: marshal request: %w", err)
	}

	endpoint := r.cfg.Endpoint + "/v1/rerank"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rerank: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Set auth header.
	if r.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.cfg.APIKey)
	} else if r.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.cfg.Token)
	}

	client := r.cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		// Check if the error is due to context cancellation.
		if ctx.Err() != nil {
			return nil, &APIError{
				Message: ctx.Err().Error(),
				Err:     ErrContext,
			}
		}
		return nil, fmt.Errorf("rerank: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("rerank: read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
			Err:        ErrAuth,
		}
	}

	if resp.StatusCode >= 500 {
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
			Err:        ErrServer,
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
		}
	}

	return r.parseResponse(respBody)
}

// parseResponse parses the API response based on the configured format.
func (r *CrossEncoderReranker) parseResponse(body []byte) (map[int]float64, error) {
	scores := make(map[int]float64)

	switch r.cfg.Format {
	case FormatJina:
		var resp jinaResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("rerank: unmarshal jina response: %w", err)
		}
		for _, res := range resp.Results {
			scores[res.Index] = res.RelevanceScore
		}

	case FormatOpenAI:
		var resp openAIRerankResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("rerank: unmarshal openai response: %w", err)
		}
		for _, res := range resp.Results {
			scores[res.Index] = res.RelevanceScore
		}

	default:
		return nil, fmt.Errorf("rerank: unsupported format: %d", r.cfg.Format)
	}

	return scores, nil
}

// buildRankedResults builds the final ranked results from candidates and their
// cross-encoder scores, sorted by score descending, limited to topN.
func buildRankedResults(candidates []Candidate, scores map[int]float64, topN int) []RankedResult {
	type indexedScore struct {
		idx   int
		score float64
	}

	scored := make([]indexedScore, 0, len(scores))
	for idx, score := range scores {
		if idx >= 0 && idx < len(candidates) {
			scored = append(scored, indexedScore{idx: idx, score: score})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].idx < scored[j].idx
	})

	if topN > 0 && len(scored) > topN {
		scored = scored[:topN]
	}

	results := make([]RankedResult, len(scored))
	for i, s := range scored {
		results[i] = RankedResult{
			Candidate: candidates[s.idx],
			Score:     s.score,
			Rank:      i + 1,
		}
	}

	return results
}
