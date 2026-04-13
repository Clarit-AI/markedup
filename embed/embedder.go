// Package embed provides interfaces and implementations for text embedding.
package embed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// Embedder produces vector embeddings from text.
type Embedder interface {
	// Embed returns one embedding vector per input text.
	Embed(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the dimensionality of the embedding vectors.
	Dimensions() int

	// Model returns the model identifier used for embedding.
	Model() string
}

// Config configures an OpenAI-compatible embedder.
type Config struct {
	// Endpoint is the base URL of the API (e.g. "http://localhost:11434").
	// The /v1/embeddings path is appended automatically.
	Endpoint string

	// ModelName is the embedding model to request (e.g. "text-embedding-3-small").
	ModelName string

	// APIKey is the bearer token for API authentication (optional for local backends).
	APIKey string

	// Token is reserved for future OAuth integration (e.g. OpenRouter OAuth).
	Token string

	// BatchSize controls how many texts are sent per request.
	// Defaults to DefaultBatchSize if zero or negative.
	BatchSize int

	// Dims is the expected dimensionality of the embedding vectors.
	Dims int

	// HTTPClient overrides the default HTTP client. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// DefaultBatchSize is the default number of texts per embedding request.
const DefaultBatchSize = 100

// ErrAuth indicates an authentication or authorization failure (HTTP 401/403).
var ErrAuth = errors.New("embed: authentication error")

// ErrServer indicates a server-side failure (HTTP 5xx).
var ErrServer = errors.New("embed: server error")

// AuthError wraps ErrAuth with the HTTP status code and response body.
type AuthError struct {
	StatusCode int
	Body       string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("embed: authentication error (HTTP %d): %s", e.StatusCode, e.Body)
}

func (e *AuthError) Unwrap() error { return ErrAuth }

// ServerError wraps ErrServer with the HTTP status code and response body.
type ServerError struct {
	StatusCode int
	Body       string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("embed: server error (HTTP %d): %s", e.StatusCode, e.Body)
}

func (e *ServerError) Unwrap() error { return ErrServer }

// RequestError represents an unexpected HTTP error that is neither auth nor server.
type RequestError struct {
	StatusCode int
	Body       string
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("embed: request error (HTTP %d): %s", e.StatusCode, e.Body)
}
