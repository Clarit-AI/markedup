package enrich

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Clarit-AI/markedup/llm"
	"github.com/Clarit-AI/markedup/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelExtractor_Extract(t *testing.T) {
	modelResult := ModelResult{
		Entities: []schema.Entity{
			{Name: "Go", Role: "programming language", Aliases: []string{"Golang"}},
		},
		Relationships: []schema.Relationship{
			{Target: "concurrency", Type: "implements", Strength: 0.8},
		},
		EntityType:        "concept",
		SemanticHints:     []string{"programming language", "systems programming"},
		PossibleQuestions: []string{"What is Go?", "How does Go handle concurrency?"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request.
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		var req llm.Request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "triplex", req.Model)
		assert.Len(t, req.Messages, 2)
		assert.Equal(t, "system", req.Messages[0].Role)
		assert.Equal(t, "user", req.Messages[1].Role)

		resultJSON, _ := json.Marshal(modelResult)
		resp := llm.Response{
			Choices: []llm.Choice{
				{Message: llm.Message{Role: "assistant", Content: string(resultJSON)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extractor := NewModelExtractor(ModelConfig{
		Endpoint: server.URL,
		Model:    "triplex",
		APIKey:   "test-key",
	})

	result, err := extractor.Extract(context.Background(), "Go is a programming language.", nil, nil)
	require.NoError(t, err)

	assert.Equal(t, "concept", result.EntityType)
	assert.Len(t, result.Entities, 1)
	assert.Equal(t, "Go", result.Entities[0].Name)
	assert.Len(t, result.Relationships, 1)
	assert.Equal(t, "concurrency", result.Relationships[0].Target)
	assert.Len(t, result.SemanticHints, 2)
	assert.Len(t, result.PossibleQuestions, 2)
}

func TestModelExtractor_CodeFencedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := ModelResult{
			EntityType:    "document",
			SemanticHints: []string{"test"},
		}
		resultJSON, _ := json.Marshal(result)
		// Wrap in code fences like some models do.
		fenced := "```json\n" + string(resultJSON) + "\n```"

		resp := llm.Response{
			Choices: []llm.Choice{
				{Message: llm.Message{Role: "assistant", Content: fenced}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extractor := NewModelExtractor(ModelConfig{
		Endpoint: server.URL,
		Model:    "test",
	})

	result, err := extractor.Extract(context.Background(), "test content", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "document", result.EntityType)
}

func TestModelExtractor_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	extractor := NewModelExtractor(ModelConfig{
		Endpoint: server.URL,
		Model:    "test",
	})

	_, err := extractor.Extract(context.Background(), "test", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestModelExtractor_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := llm.Response{
			Choices: []llm.Choice{
				{Message: llm.Message{Role: "assistant", Content: "not valid json at all"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extractor := NewModelExtractor(ModelConfig{
		Endpoint: server.URL,
		Model:    "test",
	})

	_, err := extractor.Extract(context.Background(), "test", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse model output")
}

func TestModelExtractor_NoChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := llm.Response{Choices: []llm.Choice{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extractor := NewModelExtractor(ModelConfig{
		Endpoint: server.URL,
		Model:    "test",
	})

	_, err := extractor.Extract(context.Background(), "test", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no choices")
}

func TestGenerateSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)

		var req llm.Request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "test-model", req.Model)
		assert.Len(t, req.Messages, 2)
		assert.Contains(t, req.Messages[1].Content, "Alice Chen")

		resp := llm.Response{
			Choices: []llm.Choice{
				{Message: llm.Message{Role: "assistant", Content: "AI researcher specializing in knowledge graphs"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extractor := NewModelExtractor(ModelConfig{
		Endpoint: server.URL,
		Model:    "test-model",
	})

	summary, err := extractor.GenerateSummary(
		context.Background(),
		"Alice Chen",
		"person",
		[]string{"ai", "researcher"},
		"Alice is a senior AI researcher...",
	)
	require.NoError(t, err)
	assert.Equal(t, "AI researcher specializing in knowledge graphs", summary)
}

func TestGenerateSummary_StripsQuotes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := llm.Response{
			Choices: []llm.Choice{
				{Message: llm.Message{Role: "assistant", Content: `"AI researcher specializing in knowledge graphs"`}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extractor := NewModelExtractor(ModelConfig{Endpoint: server.URL, Model: "test"})
	summary, err := extractor.GenerateSummary(context.Background(), "Alice", "person", nil, "body")
	require.NoError(t, err)
	assert.Equal(t, "AI researcher specializing in knowledge graphs", summary)
}

func TestGenerateSummary_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error"))
	}))
	defer server.Close()

	extractor := NewModelExtractor(ModelConfig{Endpoint: server.URL, Model: "test"})
	_, err := extractor.GenerateSummary(context.Background(), "Test", "doc", nil, "body")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestModelExtractor_Extract_TriplexFormat(t *testing.T) {
	triplexPayload := `{"entities_and_triples": ["[1], PERSON:Alice Chen", "[2], ORGANIZATION:LucidityLabs", "[3], CONCEPT:knowledge graphs", "(1, WORKS_FOR, 2)", "(1, RESEARCHES, 3)"]}`

	var capturedReq llm.Request

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedReq))

		resp := llm.Response{
			Choices: []llm.Choice{
				{Message: llm.Message{Role: "assistant", Content: triplexPayload}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extractor := NewModelExtractor(ModelConfig{
		Endpoint: server.URL,
		Model:    "triplex",
		Format:   FormatTriplex,
	})

	result, err := extractor.Extract(context.Background(), "Alice Chen works at LucidityLabs researching knowledge graphs.", nil, nil)
	require.NoError(t, err)

	// Verify request has NO system message — only a single user message.
	assert.Len(t, capturedReq.Messages, 1)
	assert.Equal(t, "user", capturedReq.Messages[0].Role)
	assert.True(t, strings.HasPrefix(capturedReq.Messages[0].Content, "Perform Named Entity Recognition"),
		"user message should start with the NER preamble")

	// Verify parsed result.
	require.Len(t, result.Entities, 3)
	require.Len(t, result.Relationships, 2)

	// Check Alice Chen entity exists.
	names := make([]string, 0, len(result.Entities))
	for _, e := range result.Entities {
		names = append(names, e.Name)
	}
	assert.Contains(t, names, "Alice Chen")

	// Check WORKS_FOR relationship exists.
	relTypes := make([]string, 0, len(result.Relationships))
	for _, r := range result.Relationships {
		relTypes = append(relTypes, r.Type)
	}
	assert.Contains(t, relTypes, "WORKS_FOR")
}

func TestModelExtractor_TriplexEntityOnly(t *testing.T) {
	triplexPayload := `{"entities_and_triples": ["[1], PERSON:Bob", "[2], CONCEPT:graphs"]}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := llm.Response{
			Choices: []llm.Choice{
				{Message: llm.Message{Role: "assistant", Content: triplexPayload}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extractor := NewModelExtractor(ModelConfig{
		Endpoint: server.URL,
		Model:    "triplex",
		Format:   FormatTriplex,
	})

	result, err := extractor.Extract(context.Background(), "Bob studies graphs.", nil, nil)
	require.NoError(t, err)
	assert.Len(t, result.Entities, 2)
	assert.Empty(t, result.Relationships)
}

func TestModelExtractor_TriplexMalformedOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := llm.Response{
			Choices: []llm.Choice{
				{Message: llm.Message{Role: "assistant", Content: "not json at all"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extractor := NewModelExtractor(ModelConfig{
		Endpoint: server.URL,
		Model:    "triplex",
		Format:   FormatTriplex,
	})

	_, err := extractor.Extract(context.Background(), "some text", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse triplex output")
}

func TestBodyPreview(t *testing.T) {
	short := "a few words only"
	assert.Equal(t, short, BodyPreview(short, 500))

	long := strings.Repeat("word ", 600)
	preview := BodyPreview(long, 500)
	words := strings.Fields(preview)
	assert.Len(t, words, 500)
}

func TestMergeModelResult_Default(t *testing.T) {
	existing := schema.GraphFrontmatter{
		EntityType: "document",
		Entities: []schema.Entity{
			{Name: "Go", Role: "language"},
		},
	}
	model := &ModelResult{
		EntityType: "concept", // should NOT override since existing has entity type
		Entities: []schema.Entity{
			{Name: "Go", Role: "language"},   // duplicate — skip
			{Name: "Rust", Role: "language"}, // new — add
		},
		Relationships: []schema.Relationship{
			{Target: "concurrency", Type: "implements", Strength: 0.8},
		},
		SemanticHints:     []string{"systems programming"},
		PossibleQuestions: []string{"What is Go?"},
	}

	result := MergeModelResult(existing, model, MergeOptions{})

	assert.Equal(t, "document", result.EntityType) // preserved
	assert.Len(t, result.Entities, 2)              // Go + Rust
	// Tier 2 relationships route into SemanticRelationships (issue #109).
	assert.Empty(t, result.Relationships, "Tier 2 must not write to Relationships")
	assert.Len(t, result.SemanticRelationships, 1)
	assert.Equal(t, []string{"systems programming"}, result.SemanticHints)
	assert.Equal(t, []string{"What is Go?"}, result.PossibleQuestions)
}

func TestMergeModelResult_SummaryDefault(t *testing.T) {
	existing := schema.GraphFrontmatter{ID: "test"}
	model := &ModelResult{Summary: "A test entity"}
	result := MergeModelResult(existing, model, MergeOptions{})
	assert.Equal(t, "A test entity", result.Summary)
}

func TestMergeModelResult_SummaryPreserved(t *testing.T) {
	existing := schema.GraphFrontmatter{ID: "test", Summary: "Original"}
	model := &ModelResult{Summary: "New"}
	result := MergeModelResult(existing, model, MergeOptions{})
	assert.Equal(t, "Original", result.Summary)
}

func TestMergeModelResult_SummaryForce(t *testing.T) {
	existing := schema.GraphFrontmatter{ID: "test", Summary: "Original"}
	model := &ModelResult{Summary: "New"}
	result := MergeModelResult(existing, model, MergeOptions{Force: true})
	assert.Equal(t, "New", result.Summary)
}

func TestMergeModelResult_Force(t *testing.T) {
	existing := schema.GraphFrontmatter{
		EntityType:    "document",
		SemanticHints: []string{"old hint"},
	}
	model := &ModelResult{
		EntityType:    "concept",
		SemanticHints: []string{"new hint"},
	}

	result := MergeModelResult(existing, model, MergeOptions{Force: true})

	assert.Equal(t, "concept", result.EntityType)
	assert.Equal(t, []string{"new hint"}, result.SemanticHints)
}

func TestParseModelOutput(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name:    "plain json",
			content: `{"entity_type": "concept", "semantic_hints": ["test"]}`,
		},
		{
			name:    "code fenced json",
			content: "```json\n{\"entity_type\": \"concept\"}\n```",
		},
		{
			name:    "invalid json",
			content: "not json",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseModelOutput(tt.content)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, result)
		})
	}
}
