// Package rerank provides interfaces and implementations for reranking search
// candidates using cross-encoder models. Reranking is applied after initial
// search scoring to refine result ordering using more expensive but more
// accurate models.
package rerank

import (
	"context"
	"errors"
	"fmt"
)

// Candidate represents a single search result candidate to be reranked.
type Candidate struct {
	ID            string
	Text          string
	OriginalScore float64
}

// RankedResult holds a reranked candidate with its cross-encoder score and
// final rank position (1-based).
type RankedResult struct {
	Candidate
	Score float64
	Rank  int
}

// Reranker reorders candidates by scoring each (query, document) pair using
// an external model. Implementations must be safe for concurrent use.
type Reranker interface {
	Rerank(ctx context.Context, query string, candidates []Candidate) ([]RankedResult, error)
}

// Typed errors returned by reranker implementations.
var (
	// ErrAuth is returned when the reranker API rejects authentication (401/403).
	ErrAuth = errors.New("rerank: authentication failed")

	// ErrServer is returned when the reranker API returns a server error (5xx).
	ErrServer = errors.New("rerank: server error")

	// ErrContext is returned when the context is canceled or deadline exceeded.
	ErrContext = errors.New("rerank: context error")
)

// APIError wraps a reranker API error with the HTTP status code.
type APIError struct {
	StatusCode int
	Message    string
	Err        error
}

func (e *APIError) Error() string {
	return fmt.Sprintf("rerank: HTTP %d: %s", e.StatusCode, e.Message)
}

func (e *APIError) Unwrap() error {
	return e.Err
}
