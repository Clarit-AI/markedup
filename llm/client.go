// Package llm provides a shared OpenAI-compatible chat completion HTTP client.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Config configures the chat completion client.
type Config struct {
	Endpoint   string       // Base URL (e.g. http://localhost:11434)
	Model      string       // Model name (e.g. "triplex")
	APIKey     string       // Optional API key for authentication
	HTTPClient *http.Client // Optional; defaults to http.DefaultClient
}

// Client is an OpenAI-compatible chat completion HTTP client.
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// NewClient creates a new chat completion client with the given config.
func NewClient(cfg Config) *Client {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{cfg: cfg, httpClient: hc}
}

// Message represents a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request is the OpenAI-compatible chat completion request.
type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// Response is the OpenAI-compatible chat completion response.
type Response struct {
	Choices []Choice `json:"choices"`
}

// Choice is a single completion choice in a chat response.
type Choice struct {
	Message Message `json:"message"`
}

// ChatCompletion sends a chat completion request and returns the first choice's content.
func (c *Client) ChatCompletion(ctx context.Context, messages []Message) (string, error) {
	reqBody := Request{
		Model:    c.cfg.Model,
		Messages: messages,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("llm: marshal request: %w", err)
	}

	endpoint := strings.TrimRight(c.cfg.Endpoint, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("llm: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: model returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp Response
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("llm: unmarshal response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("llm: model returned no choices")
	}

	return chatResp.Choices[0].Message.Content, nil
}
