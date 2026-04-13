package rerank

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestServer creates a mock reranker API server that returns the given
// results. If statusCode != 200, it returns an error response.
func newTestServer(t *testing.T, statusCode int, results []jinaResult) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request format.
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/rerank", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var req jinaRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)

		if statusCode != http.StatusOK {
			w.WriteHeader(statusCode)
			w.Write([]byte(`{"error": "test error"}`))
			return
		}

		resp := jinaResponse{Results: results}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestCrossEncoder_Rerank_HappyPath(t *testing.T) {
	// Server returns candidates re-ordered: index 2 highest, then 0, then 1.
	server := newTestServer(t, http.StatusOK, []jinaResult{
		{Index: 0, RelevanceScore: 0.5},
		{Index: 1, RelevanceScore: 0.2},
		{Index: 2, RelevanceScore: 0.9},
	})
	defer server.Close()

	reranker := NewCrossEncoder(Config{
		Endpoint:   server.URL,
		Model:      "test-model",
		HTTPClient: server.Client(),
	})

	candidates := []Candidate{
		{ID: "doc-a", Text: "Go programming language", OriginalScore: 10.0},
		{ID: "doc-b", Text: "Python programming", OriginalScore: 8.0},
		{ID: "doc-c", Text: "Go concurrency patterns", OriginalScore: 6.0},
	}

	results, err := reranker.Rerank(context.Background(), "go concurrency", candidates)
	require.NoError(t, err)
	require.Len(t, results, 3)

	// Verify re-ordering by cross-encoder score.
	assert.Equal(t, "doc-c", results[0].ID)
	assert.Equal(t, 0.9, results[0].Score)
	assert.Equal(t, 1, results[0].Rank)

	assert.Equal(t, "doc-a", results[1].ID)
	assert.Equal(t, 0.5, results[1].Score)
	assert.Equal(t, 2, results[1].Rank)

	assert.Equal(t, "doc-b", results[2].ID)
	assert.Equal(t, 0.2, results[2].Score)
	assert.Equal(t, 3, results[2].Rank)
}

func TestCrossEncoder_Rerank_EmptyCandidates(t *testing.T) {
	reranker := NewCrossEncoder(Config{
		Endpoint: "http://unused",
	})

	results, err := reranker.Rerank(context.Background(), "test query", []Candidate{})
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.NotNil(t, results) // returns empty slice, not nil
}

func TestCrossEncoder_Rerank_TopN(t *testing.T) {
	server := newTestServer(t, http.StatusOK, []jinaResult{
		{Index: 0, RelevanceScore: 0.9},
		{Index: 2, RelevanceScore: 0.7},
	})
	defer server.Close()

	reranker := NewCrossEncoder(Config{
		Endpoint:   server.URL,
		Model:      "test-model",
		TopN:       2,
		HTTPClient: server.Client(),
	})

	candidates := []Candidate{
		{ID: "doc-a", Text: "First document", OriginalScore: 10.0},
		{ID: "doc-b", Text: "Second document", OriginalScore: 8.0},
		{ID: "doc-c", Text: "Third document", OriginalScore: 6.0},
	}

	results, err := reranker.Rerank(context.Background(), "test", candidates)
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, "doc-a", results[0].ID)
	assert.Equal(t, "doc-c", results[1].ID)
}

func TestCrossEncoder_Rerank_ContextCanceled(t *testing.T) {
	// Use a server that delays so the context cancellation happens during the request.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	reranker := NewCrossEncoder(Config{
		Endpoint:   server.URL,
		Model:      "test-model",
		HTTPClient: server.Client(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	candidates := []Candidate{
		{ID: "doc-a", Text: "test", OriginalScore: 1.0},
	}

	_, err := reranker.Rerank(ctx, "query", candidates)
	require.Error(t, err)

	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	assert.True(t, errors.Is(apiErr, ErrContext))
}

func TestCrossEncoder_Rerank_Unauthorized(t *testing.T) {
	server := newTestServer(t, http.StatusUnauthorized, nil)
	defer server.Close()

	reranker := NewCrossEncoder(Config{
		Endpoint:   server.URL,
		Model:      "test-model",
		APIKey:     "bad-key",
		HTTPClient: server.Client(),
	})

	candidates := []Candidate{
		{ID: "doc-a", Text: "test", OriginalScore: 1.0},
	}

	_, err := reranker.Rerank(context.Background(), "query", candidates)
	require.Error(t, err)

	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	assert.True(t, errors.Is(apiErr, ErrAuth))
}

func TestCrossEncoder_Rerank_ServerError(t *testing.T) {
	server := newTestServer(t, http.StatusInternalServerError, nil)
	defer server.Close()

	reranker := NewCrossEncoder(Config{
		Endpoint:   server.URL,
		Model:      "test-model",
		HTTPClient: server.Client(),
	})

	candidates := []Candidate{
		{ID: "doc-a", Text: "test", OriginalScore: 1.0},
	}

	_, err := reranker.Rerank(context.Background(), "query", candidates)
	require.Error(t, err)

	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
	assert.True(t, errors.Is(apiErr, ErrServer))
}

func TestCrossEncoder_Rerank_AuthHeader_APIKey(t *testing.T) {
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		resp := jinaResponse{Results: []jinaResult{{Index: 0, RelevanceScore: 0.5}}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reranker := NewCrossEncoder(Config{
		Endpoint:   server.URL,
		Model:      "test-model",
		APIKey:     "my-api-key",
		HTTPClient: server.Client(),
	})

	candidates := []Candidate{{ID: "doc-a", Text: "test", OriginalScore: 1.0}}
	_, err := reranker.Rerank(context.Background(), "query", candidates)
	require.NoError(t, err)
	assert.Equal(t, "Bearer my-api-key", capturedAuth)
}

func TestCrossEncoder_Rerank_AuthHeader_Token(t *testing.T) {
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		resp := jinaResponse{Results: []jinaResult{{Index: 0, RelevanceScore: 0.5}}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reranker := NewCrossEncoder(Config{
		Endpoint:   server.URL,
		Model:      "test-model",
		Token:      "my-oauth-token",
		HTTPClient: server.Client(),
	})

	candidates := []Candidate{{ID: "doc-a", Text: "test", OriginalScore: 1.0}}
	_, err := reranker.Rerank(context.Background(), "query", candidates)
	require.NoError(t, err)
	assert.Equal(t, "Bearer my-oauth-token", capturedAuth)
}

func TestCrossEncoder_Rerank_APIKeyTakesPrecedence(t *testing.T) {
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		resp := jinaResponse{Results: []jinaResult{{Index: 0, RelevanceScore: 0.5}}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reranker := NewCrossEncoder(Config{
		Endpoint:   server.URL,
		Model:      "test-model",
		APIKey:     "the-api-key",
		Token:      "the-oauth-token",
		HTTPClient: server.Client(),
	})

	candidates := []Candidate{{ID: "doc-a", Text: "test", OriginalScore: 1.0}}
	_, err := reranker.Rerank(context.Background(), "query", candidates)
	require.NoError(t, err)
	assert.Equal(t, "Bearer the-api-key", capturedAuth)
}

func TestCrossEncoder_Rerank_OpenAIFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// OpenAI format uses the same request structure.
		var req jinaRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Return in OpenAI-compatible format (same structure).
		resp := openAIRerankResponse{
			Results: []openAIRerankResult{
				{Index: 0, RelevanceScore: 0.3},
				{Index: 1, RelevanceScore: 0.8},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reranker := NewCrossEncoder(Config{
		Endpoint:   server.URL,
		Model:      "test-model",
		Format:     FormatOpenAI,
		HTTPClient: server.Client(),
	})

	candidates := []Candidate{
		{ID: "doc-a", Text: "first", OriginalScore: 10.0},
		{ID: "doc-b", Text: "second", OriginalScore: 5.0},
	}

	results, err := reranker.Rerank(context.Background(), "query", candidates)
	require.NoError(t, err)
	require.Len(t, results, 2)

	assert.Equal(t, "doc-b", results[0].ID)
	assert.Equal(t, 0.8, results[0].Score)
	assert.Equal(t, 1, results[0].Rank)

	assert.Equal(t, "doc-a", results[1].ID)
	assert.Equal(t, 0.3, results[1].Score)
	assert.Equal(t, 2, results[1].Rank)
}

func TestCrossEncoder_Rerank_RequestBodyFormat(t *testing.T) {
	var capturedReq jinaRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedReq)
		resp := jinaResponse{Results: []jinaResult{{Index: 0, RelevanceScore: 0.5}}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reranker := NewCrossEncoder(Config{
		Endpoint:   server.URL,
		Model:      "jina-reranker-v2",
		TopN:       1,
		HTTPClient: server.Client(),
	})

	candidates := []Candidate{
		{ID: "doc-a", Text: "Hello world", OriginalScore: 1.0},
	}

	_, err := reranker.Rerank(context.Background(), "hello", candidates)
	require.NoError(t, err)

	assert.Equal(t, "hello", capturedReq.Query)
	assert.Equal(t, []string{"Hello world"}, capturedReq.Documents)
	assert.Equal(t, "jina-reranker-v2", capturedReq.Model)
	assert.Equal(t, 1, capturedReq.TopN)
}

// TestCrossEncoder_Rerank_InterfaceCompliance verifies CrossEncoderReranker
// satisfies the Reranker interface at compile time.
var _ Reranker = (*CrossEncoderReranker)(nil)
