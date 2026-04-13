package embed

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestServer creates an httptest.Server that responds to /v1/embeddings.
// The handler function receives the decoded request and returns a status code
// and response body bytes.
func newTestServer(t *testing.T, handler func(req embeddingRequest) (int, []byte)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/embeddings", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var req embeddingRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)

		code, body := handler(req)
		w.WriteHeader(code)
		_, _ = w.Write(body)
	}))
}

// makeEmbeddingResponse builds a valid JSON embedding response for the given
// number of inputs, each with the specified dimensionality.
func makeEmbeddingResponse(n, dims int) []byte {
	resp := embeddingResponse{Data: make([]embeddingData, n)}
	for i := 0; i < n; i++ {
		vec := make([]float32, dims)
		for d := 0; d < dims; d++ {
			vec[d] = float32(i*dims + d)
		}
		resp.Data[i] = embeddingData{Index: i, Embedding: vec}
	}
	b, _ := json.Marshal(resp)
	return b
}

func TestEmbed_HappyPath(t *testing.T) {
	dims := 3
	srv := newTestServer(t, func(req embeddingRequest) (int, []byte) {
		return http.StatusOK, makeEmbeddingResponse(len(req.Input), dims)
	})
	defer srv.Close()

	emb := NewOpenAICompatible(Config{
		Endpoint:  srv.URL,
		ModelName: "test-model",
		Dims:      dims,
	})

	vecs, err := emb.Embed(context.Background(), []string{"hello world"})
	require.NoError(t, err)
	require.Len(t, vecs, 1)
	assert.Len(t, vecs[0], dims)
	assert.Equal(t, "test-model", emb.Model())
	assert.Equal(t, dims, emb.Dimensions())
}

func TestEmbed_EmptyInput(t *testing.T) {
	emb := NewOpenAICompatible(Config{
		Endpoint:  "http://unused",
		ModelName: "m",
	})
	vecs, err := emb.Embed(context.Background(), []string{})
	require.NoError(t, err)
	assert.Nil(t, vecs)
}

func TestEmbed_NilInput(t *testing.T) {
	emb := NewOpenAICompatible(Config{
		Endpoint:  "http://unused",
		ModelName: "m",
	})
	vecs, err := emb.Embed(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, vecs)
}

func TestEmbed_CanceledContext(t *testing.T) {
	srv := newTestServer(t, func(req embeddingRequest) (int, []byte) {
		return http.StatusOK, makeEmbeddingResponse(1, 3)
	})
	defer srv.Close()

	emb := NewOpenAICompatible(Config{
		Endpoint:  srv.URL,
		ModelName: "m",
		Dims:      3,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := emb.Embed(ctx, []string{"hello"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got: %v", err)
}

func TestEmbed_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	emb := NewOpenAICompatible(Config{
		Endpoint:  srv.URL,
		ModelName: "m",
		APIKey:    "bad-key",
	})

	_, err := emb.Embed(context.Background(), []string{"hello"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAuth), "expected ErrAuth, got: %v", err)

	var authErr *AuthError
	require.True(t, errors.As(err, &authErr))
	assert.Equal(t, http.StatusUnauthorized, authErr.StatusCode)
	assert.Contains(t, authErr.Body, "invalid api key")
}

func TestEmbed_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal"}`))
	}))
	defer srv.Close()

	emb := NewOpenAICompatible(Config{
		Endpoint:  srv.URL,
		ModelName: "m",
	})

	_, err := emb.Embed(context.Background(), []string{"hello"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrServer), "expected ErrServer, got: %v", err)

	var srvErr *ServerError
	require.True(t, errors.As(err, &srvErr))
	assert.Equal(t, http.StatusInternalServerError, srvErr.StatusCode)
}

func TestEmbed_Batching(t *testing.T) {
	dims := 2
	var batchCount atomic.Int32

	srv := newTestServer(t, func(req embeddingRequest) (int, []byte) {
		batchCount.Add(1)
		return http.StatusOK, makeEmbeddingResponse(len(req.Input), dims)
	})
	defer srv.Close()

	emb := NewOpenAICompatible(Config{
		Endpoint:  srv.URL,
		ModelName: "m",
		BatchSize: 3,
		Dims:      dims,
	})

	// 7 texts with batch size 3 → 3 batches (3+3+1)
	texts := make([]string, 7)
	for i := range texts {
		texts[i] = "text"
	}

	vecs, err := emb.Embed(context.Background(), texts)
	require.NoError(t, err)
	require.Len(t, vecs, 7)
	assert.Equal(t, int32(3), batchCount.Load(), "expected 3 batch requests")

	// Verify each vector has correct dimensions.
	for i, v := range vecs {
		assert.Len(t, v, dims, "vector %d has wrong dimensions", i)
	}
}

func TestEmbed_AuthHeader(t *testing.T) {
	tests := []struct {
		name     string
		apiKey   string
		token    string
		wantAuth string
	}{
		{"api key", "sk-123", "", "Bearer sk-123"},
		{"token", "", "oauth-tok", "Bearer oauth-tok"},
		{"api key takes precedence", "sk-123", "oauth-tok", "Bearer sk-123"},
		{"no auth", "", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got := r.Header.Get("Authorization")
				assert.Equal(t, tc.wantAuth, got)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(makeEmbeddingResponse(1, 2))
			}))
			defer srv.Close()

			emb := NewOpenAICompatible(Config{
				Endpoint:  srv.URL,
				ModelName: "m",
				APIKey:    tc.apiKey,
				Token:     tc.token,
				Dims:      2,
			})

			_, err := emb.Embed(context.Background(), []string{"hi"})
			require.NoError(t, err)
		})
	}
}

func TestEmbed_DefaultBatchSize(t *testing.T) {
	emb := NewOpenAICompatible(Config{
		Endpoint:  "http://unused",
		ModelName: "m",
	})
	assert.Equal(t, DefaultBatchSize, emb.batchSize)
}

func TestEmbed_ForbiddenIsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer srv.Close()

	emb := NewOpenAICompatible(Config{
		Endpoint:  srv.URL,
		ModelName: "m",
	})

	_, err := emb.Embed(context.Background(), []string{"hello"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAuth))
}

func TestEmbed_UnexpectedStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	emb := NewOpenAICompatible(Config{
		Endpoint:  srv.URL,
		ModelName: "m",
	})

	_, err := emb.Embed(context.Background(), []string{"hello"})
	require.Error(t, err)

	var reqErr *RequestError
	require.True(t, errors.As(err, &reqErr))
	assert.Equal(t, http.StatusTooManyRequests, reqErr.StatusCode)
}
