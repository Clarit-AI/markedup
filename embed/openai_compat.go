package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// embeddingRequest is the OpenAI-compatible /v1/embeddings request body.
type embeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// embeddingResponse is the OpenAI-compatible /v1/embeddings response body.
type embeddingResponse struct {
	Data []embeddingData `json:"data"`
}

// embeddingData holds a single embedding vector with its index.
type embeddingData struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// OpenAICompatibleEmbedder implements Embedder using any OpenAI-compatible
// /v1/embeddings endpoint (OpenAI, ollama, llama.cpp, OpenRouter, etc.).
type OpenAICompatibleEmbedder struct {
	endpoint   string // base URL without trailing slash
	model      string
	apiKey     string
	token      string
	batchSize  int
	dims       int
	httpClient *http.Client
}

// NewFromProvider is a convenience constructor for external callers that want
// to build an OpenAI-compatible embedder from flat provider settings (e.g.
// config inheritance from a host application like Plexium's assistiveAgent
// provider block). It is equivalent to NewOpenAICompatible with a Config
// populated from the given parameters and sensible defaults.
//
// endpoint is the base URL (without trailing /v1/embeddings); model is the
// embedding model identifier; apiKey is the bearer token (empty for local
// endpoints); dims is the expected embedding dimensionality.
func NewFromProvider(endpoint, model, apiKey string, dims int) *OpenAICompatibleEmbedder {
	return NewOpenAICompatible(Config{
		Endpoint:  endpoint,
		ModelName: model,
		APIKey:    apiKey,
		Dims:      dims,
	})
}

// NewOpenAICompatible creates an OpenAICompatibleEmbedder from the given config.
func NewOpenAICompatible(cfg Config) *OpenAICompatibleEmbedder {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	return &OpenAICompatibleEmbedder{
		endpoint:   endpoint,
		model:      cfg.ModelName,
		apiKey:     cfg.APIKey,
		token:      cfg.Token,
		batchSize:  batchSize,
		dims:       cfg.Dims,
		httpClient: client,
	}
}

// Embed sends texts in batches to the /v1/embeddings endpoint and returns
// the resulting embedding vectors. The returned slice is ordered to match
// the input texts.
func (e *OpenAICompatibleEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	result := make([][]float32, len(texts))

	for start := 0; start < len(texts); start += e.batchSize {
		end := start + e.batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]

		embeddings, err := e.sendBatch(ctx, batch)
		if err != nil {
			return nil, err
		}

		for i, emb := range embeddings {
			result[start+i] = emb
		}
	}

	return result, nil
}

// Dimensions returns the configured embedding dimensionality.
func (e *OpenAICompatibleEmbedder) Dimensions() int { return e.dims }

// Model returns the model identifier.
func (e *OpenAICompatibleEmbedder) Model() string { return e.model }

// sendBatch sends a single batch of texts to the embedding endpoint.
func (e *OpenAICompatibleEmbedder) sendBatch(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := embeddingRequest{
		Input: texts,
		Model: e.model,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("embed: marshal request: %w", err)
	}

	url := e.endpoint + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("embed: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Auth: prefer APIKey, fall back to Token (future OAuth).
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	} else if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("embed: read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &AuthError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	if resp.StatusCode >= 500 {
		return nil, &ServerError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &RequestError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var embResp embeddingResponse
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return nil, fmt.Errorf("embed: unmarshal response: %w", err)
	}

	if len(embResp.Data) != len(texts) {
		return nil, fmt.Errorf("embed: expected %d embeddings, got %d", len(texts), len(embResp.Data))
	}

	// Re-order by index to handle out-of-order responses.
	embeddings := make([][]float32, len(texts))
	for _, d := range embResp.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("embed: invalid embedding index %d for batch size %d", d.Index, len(texts))
		}
		embeddings[d.Index] = d.Embedding
	}

	return embeddings, nil
}
