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
// ExtraBody is merged into the root of the marshalled JSON body. Collisions
// with core fields (Model/Messages) are ignored — core fields always win.
// Used for non-standard extensions like vLLM's chat_template_kwargs.
type Request struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Extra    map[string]any `json:"-"`
}

// marshalRequest marshals a Request to JSON. When Extra is empty, the output
// is byte-identical to json.Marshal(r). Otherwise Extra keys are merged at the
// root of the body; keys that collide with core struct fields are dropped.
func marshalRequest(r Request) ([]byte, error) {
	if len(r.Extra) == 0 {
		return json.Marshal(r)
	}
	core, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(core, &root); err != nil {
		return nil, err
	}
	for k, v := range r.Extra {
		if _, exists := root[k]; exists {
			continue
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("llm: marshal extra field %q: %w", k, err)
		}
		root[k] = raw
	}
	return json.Marshal(root)
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
	return c.ChatCompletionWith(ctx, Request{Messages: messages})
}

// ChatCompletionWith sends a chat completion request with an explicit Request,
// allowing callers to set Extra fields (e.g. chat_template_kwargs for NuExtract
// on vLLM/HF). Model is taken from the client Config unless overridden on req.
func (c *Client) ChatCompletionWith(ctx context.Context, req Request) (string, error) {
	if req.Model == "" {
		req.Model = c.cfg.Model
	}

	jsonBody, err := marshalRequest(req)
	if err != nil {
		return "", fmt.Errorf("llm: marshal request: %w", err)
	}

	endpoint := strings.TrimRight(c.cfg.Endpoint, "/") + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("llm: create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.httpClient.Do(httpReq)
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
