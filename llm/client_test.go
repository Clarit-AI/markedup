package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_ChatCompletion(t *testing.T) {
	tests := []struct {
		name       string
		apiKey     string
		messages   []Message
		handler    http.HandlerFunc
		wantResult string
		wantErr    string
	}{
		{
			name:   "successful request with auth",
			apiKey: "test-key",
			messages: []Message{
				{Role: "system", Content: "You are helpful."},
				{Role: "user", Content: "Hello"},
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v1/chat/completions", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

				var req Request
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				assert.Equal(t, "test-model", req.Model)
				assert.Len(t, req.Messages, 2)
				assert.Equal(t, "system", req.Messages[0].Role)
				assert.Equal(t, "user", req.Messages[1].Role)

				resp := Response{
					Choices: []Choice{
						{Message: Message{Role: "assistant", Content: "Hi there!"}},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			},
			wantResult: "Hi there!",
		},
		{
			name:     "no auth header when key is empty",
			messages: []Message{{Role: "user", Content: "test"}},
			handler: func(w http.ResponseWriter, r *http.Request) {
				assert.Empty(t, r.Header.Get("Authorization"))

				resp := Response{
					Choices: []Choice{
						{Message: Message{Role: "assistant", Content: "ok"}},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			},
			wantResult: "ok",
		},
		{
			name:     "server returns error status",
			messages: []Message{{Role: "user", Content: "test"}},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("internal error"))
			},
			wantErr: "status 500",
		},
		{
			name:     "server returns no choices",
			messages: []Message{{Role: "user", Content: "test"}},
			handler: func(w http.ResponseWriter, r *http.Request) {
				resp := Response{Choices: []Choice{}}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			},
			wantErr: "no choices",
		},
		{
			name:     "server returns invalid json",
			messages: []Message{{Role: "user", Content: "test"}},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte("not json"))
			},
			wantErr: "unmarshal response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			client := NewClient(Config{
				Endpoint: server.URL,
				Model:    "test-model",
				APIKey:   tt.apiKey,
			})

			result, err := client.ChatCompletion(context.Background(), tt.messages)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestClient_EndpointTrailingSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		resp := Response{
			Choices: []Choice{
				{Message: Message{Role: "assistant", Content: "ok"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Endpoint with trailing slash should still work.
	client := NewClient(Config{
		Endpoint: server.URL + "/",
		Model:    "test",
	})

	result, err := client.ChatCompletion(context.Background(), []Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	assert.Equal(t, "ok", result)
}

func TestClient_ExtraBody_MergedAtRoot(t *testing.T) {
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))
		resp := Response{Choices: []Choice{{Message: Message{Role: "assistant", Content: "ok"}}}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(Config{Endpoint: server.URL, Model: "numind/NuExtract-2.0-8B"})

	_, err := client.ChatCompletionWith(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Extra: map[string]any{
			"chat_template_kwargs": map[string]any{"template": `{"x":"verbatim-string"}`},
			"temperature":          0,
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "numind/NuExtract-2.0-8B", capturedBody["model"])
	assert.NotNil(t, capturedBody["messages"])
	kw, ok := capturedBody["chat_template_kwargs"].(map[string]any)
	require.True(t, ok, "chat_template_kwargs should be at root")
	assert.Equal(t, `{"x":"verbatim-string"}`, kw["template"])
	assert.Equal(t, float64(0), capturedBody["temperature"])
}

func TestClient_ExtraBody_CoreFieldsWin(t *testing.T) {
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))
		resp := Response{Choices: []Choice{{Message: Message{Role: "assistant", Content: "ok"}}}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(Config{Endpoint: server.URL, Model: "real-model"})
	_, err := client.ChatCompletionWith(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Extra:    map[string]any{"model": "evil-override"},
	})
	require.NoError(t, err)
	assert.Equal(t, "real-model", capturedBody["model"])
}

func TestMarshalRequest_EmptyExtraIsIdentical(t *testing.T) {
	req := Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}}
	plain, err := json.Marshal(req)
	require.NoError(t, err)
	merged, err := marshalRequest(req)
	require.NoError(t, err)
	assert.Equal(t, plain, merged)
}

func TestClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This handler will never respond in time.
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewClient(Config{
		Endpoint: server.URL,
		Model:    "test",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := client.ChatCompletion(ctx, []Message{{Role: "user", Content: "hi"}})
	require.Error(t, err)
}
