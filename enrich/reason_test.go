package enrich

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraphReasoner_Reason(t *testing.T) {
	responsePayload := map[string]interface{}{
		"thinking":           "Alice is a researcher, relevant to the query",
		"page_ids":           []string{"alice", "concept-graph"},
		"relationship_paths": []string{"alice --studies--> concept-graph"},
	}

	var capturedReq chatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)

		if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		respContent, _ := json.Marshal(responsePayload)
		resp := chatResponse{
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: string(respContent)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	reasoner := NewGraphReasoner(ReasonConfig{
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	query := "who does Alice work with?"
	graphJSON := `{"stats":{"pages":2},"pages":[{"id":"alice"},{"id":"concept-graph"}]}`

	result, err := reasoner.Reason(context.Background(), query, graphJSON)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify parsed result.
	assert.Len(t, result.PageIDs, 2)
	assert.Contains(t, result.PageIDs, "alice")
	assert.Contains(t, result.PageIDs, "concept-graph")
	assert.Len(t, result.RelationshipPaths, 1)
	assert.Equal(t, "alice --studies--> concept-graph", result.RelationshipPaths[0])
	assert.NotEmpty(t, result.Thinking)

	// Verify the request sent to the server.
	require.Len(t, capturedReq.Messages, 2)
	assert.True(t, strings.HasPrefix(capturedReq.Messages[0].Content, "You are given a question"),
		"system message should start with the graph navigation prompt preamble")
	assert.Contains(t, capturedReq.Messages[1].Content, query,
		"user message should contain the query")
	assert.Contains(t, capturedReq.Messages[1].Content, graphJSON,
		"user message should contain the graph JSON")
}

func TestGraphReasoner_Reason_MalformedOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: "not json"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	reasoner := NewGraphReasoner(ReasonConfig{
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	_, err := reasoner.Reason(context.Background(), "test query", "{}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse reason output",
		"error should mention parse reason output")
}

func TestGraphReasoner_Reason_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	reasoner := NewGraphReasoner(ReasonConfig{
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	_, err := reasoner.Reason(context.Background(), "test query", "{}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500",
		"error should mention status 500")
}
