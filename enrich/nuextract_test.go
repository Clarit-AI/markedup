package enrich

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nuextractEntitiesResp(t *testing.T, entities []nuextractEntity) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"entities": entities})
	return string(b)
}

func nuextractRelsResp(t *testing.T, rels []nuextractRelation) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"relationships": rels})
	return string(b)
}

// wrapChatCompletion returns a full OpenAI chat-completion response body with
// the given content as the assistant message.
func wrapChatCompletion(content string) string {
	out, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"role": "assistant", "content": content}},
		},
	})
	return string(out)
}

func TestNuExtract_Parallel_HappyPath(t *testing.T) {
	var calls int32
	var gotEntitiesReq, gotRelsReq bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		atomic.AddInt32(&calls, 1)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))
		kw, _ := parsed["chat_template_kwargs"].(map[string]any)
		tmpl, _ := kw["template"].(string)

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(tmpl, `"entities"`) && !strings.Contains(tmpl, `"relationships"`):
			gotEntitiesReq = true
			w.Write([]byte(wrapChatCompletion(nuextractEntitiesResp(t, []nuextractEntity{
				{Name: "Alice", Type: "PERSON"},
				{Name: "Acme", Type: "ORGANIZATION"},
			}))))
		case strings.Contains(tmpl, `"relationships"`):
			gotRelsReq = true
			w.Write([]byte(wrapChatCompletion(nuextractRelsResp(t, []nuextractRelation{
				{Subject: "Alice", Predicate: "works_for", Object: "Acme"},
			}))))
		default:
			t.Fatalf("unexpected template: %s", tmpl)
		}
	}))
	defer srv.Close()

	m := NewModelExtractor(ModelConfig{
		Endpoint: srv.URL,
		Model:    "numind/NuExtract-2.0-8B",
		Format:   FormatNuExtract,
		NuExtract: NuExtractOptions{
			Mode:      "parallel",
			Transport: "native",
		},
	})

	res, err := m.Extract(context.Background(), "Alice works at Acme.", nil, nil)
	require.NoError(t, err)

	assert.True(t, gotEntitiesReq, "entities request fired")
	assert.True(t, gotRelsReq, "relations request fired")
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	assert.Len(t, res.Entities, 2)
	assert.Equal(t, "Alice", res.Entities[0].Name)
	require.Len(t, res.Relationships, 1)
	assert.Equal(t, "Acme", res.Relationships[0].Target)
	assert.Equal(t, "works_for", res.Relationships[0].Type)
	assert.InDelta(t, nuExtractStrength, res.Relationships[0].Strength, 0.0001)
}

func TestNuExtract_Single_HappyPath(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		combined, _ := json.Marshal(map[string]any{
			"entities":      []nuextractEntity{{Name: "X", Type: "CONCEPT"}},
			"relationships": []nuextractRelation{{Subject: "X", Predicate: "uses", Object: "Y"}},
		})
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(wrapChatCompletion(string(combined))))
	}))
	defer srv.Close()

	m := NewModelExtractor(ModelConfig{
		Endpoint:  srv.URL,
		Model:     "numind/NuExtract-2.0-2B",
		Format:    FormatNuExtract,
		NuExtract: NuExtractOptions{Mode: "single", Transport: "native"},
	})

	res, err := m.Extract(context.Background(), "body", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
	assert.Len(t, res.Entities, 1)
	assert.Len(t, res.Relationships, 1)
	assert.Equal(t, "concept", res.EntityType)
}

func TestNuExtract_ManualTransport_ClientRendersTemplate(t *testing.T) {
	var capturedContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		require.NoError(t, json.Unmarshal(body, &parsed))
		require.Len(t, parsed.Messages, 1)
		capturedContent = parsed.Messages[0].Content

		// chat_template_kwargs should NOT be in manual mode
		assert.NotContains(t, string(body), "chat_template_kwargs")

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(wrapChatCompletion(nuextractEntitiesResp(t, []nuextractEntity{{Name: "Z", Type: "OTHER"}}))))
	}))
	defer srv.Close()

	m := NewModelExtractor(ModelConfig{
		Endpoint:  srv.URL,
		Model:     "nuextract-2.0-2b",
		Format:    FormatNuExtract,
		NuExtract: NuExtractOptions{Mode: "single", Transport: "manual"},
	})

	_, err := m.Extract(context.Background(), "document", nil, nil)
	require.NoError(t, err)
	assert.Contains(t, capturedContent, "# Template:")
	assert.Contains(t, capturedContent, "# Context:\ndocument")
}

func TestNuExtract_MarkdownWrappedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := "```json\n" + nuextractEntitiesResp(t, []nuextractEntity{{Name: "W", Type: "EVENT"}}) + "\n```"
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(wrapChatCompletion(wrapped)))
	}))
	defer srv.Close()

	m := NewModelExtractor(ModelConfig{
		Endpoint:  srv.URL,
		Model:     "x",
		Format:    FormatNuExtract,
		NuExtract: NuExtractOptions{Mode: "single", Transport: "manual"},
	})
	res, err := m.Extract(context.Background(), "b", nil, nil)
	require.NoError(t, err)
	require.Len(t, res.Entities, 1)
	assert.Equal(t, "W", res.Entities[0].Name)
}

func TestNuExtract_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(wrapChatCompletion("not json at all")))
	}))
	defer srv.Close()

	m := NewModelExtractor(ModelConfig{
		Endpoint:  srv.URL,
		Model:     "x",
		Format:    FormatNuExtract,
		NuExtract: NuExtractOptions{Mode: "single", Transport: "manual"},
	})
	_, err := m.Extract(context.Background(), "b", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse nuextract")
}

func TestNuExtract_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("upstream exploded"))
	}))
	defer srv.Close()

	m := NewModelExtractor(ModelConfig{
		Endpoint:  srv.URL,
		Model:     "x",
		Format:    FormatNuExtract,
		NuExtract: NuExtractOptions{Mode: "parallel", Transport: "native"},
	})
	_, err := m.Extract(context.Background(), "b", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestNuExtract_Dedup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		combined, _ := json.Marshal(map[string]any{
			"entities": []nuextractEntity{
				{Name: "Alice", Type: "PERSON"},
				{Name: "alice", Type: "PERSON"}, // case-dup
				{Name: "Bob", Type: "PERSON"},
			},
			"relationships": []nuextractRelation{
				{Subject: "Alice", Predicate: "knows", Object: "Bob"},
				{Subject: "X", Predicate: "knows", Object: "bob"}, // dup via target+type
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(wrapChatCompletion(string(combined))))
	}))
	defer srv.Close()

	m := NewModelExtractor(ModelConfig{
		Endpoint:  srv.URL,
		Model:     "x",
		Format:    FormatNuExtract,
		NuExtract: NuExtractOptions{Mode: "single", Transport: "manual"},
	})
	res, err := m.Extract(context.Background(), "b", nil, nil)
	require.NoError(t, err)
	assert.Len(t, res.Entities, 2) // Alice and Bob
	assert.Len(t, res.Relationships, 1)
}

func TestAutodetectTransport(t *testing.T) {
	cases := map[string]string{
		// loopback / localhost
		"http://localhost:1234": "manual",
		"http://127.0.0.1:8080": "manual",
		"http://0.0.0.0:8080":   "manual",
		"http://[::1]:8080":     "manual",
		"http://[::1]":          "manual",
		// RFC1918 — all private ranges
		"http://192.168.1.5:11434": "manual",
		"http://10.0.1.5:8080":     "manual",
		"http://10.1.2.3:8080":     "manual",
		"http://10.42.0.1:8080":    "manual",
		"http://172.16.0.5:8080":   "manual",
		"http://172.31.255.254":    "manual",
		// link-local
		"http://169.254.1.1:8080": "manual",
		// local DNS
		"http://ollama.local:11434": "manual",
		"http://radxa.lan:8081":     "manual",
		"http://server.home/v1":     "manual",
		"http://gpu.internal/v1":    "manual",
		// public / cloud
		"https://api.openai.com/v1":  "native",
		"https://xyz.hf.space":       "native",
		"https://inference.endpoint": "native",
		"https://my-endpoint.us-east-1.aws.endpoints.huggingface.cloud/v1": "native",
		// 172.32 is NOT in the private range
		"http://172.32.0.1:8080": "native",
	}
	for url, want := range cases {
		assert.Equal(t, want, autodetectTransport(url), "url=%s", url)
	}
}

func TestDedupEntities_DifferentRolesPreserved(t *testing.T) {
	raw := []nuextractEntity{
		{Name: "Apple", Type: "ORGANIZATION"},
		{Name: "Apple", Type: "PRODUCT"},
		{Name: "apple", Type: "ORGANIZATION"},
	}
	entities, _, err := entitiesFromRaw(raw)
	require.NoError(t, err)
	out := dedupEntities(entities)
	require.Len(t, out, 2)
	roles := []string{out[0].Role, out[1].Role}
	assert.Contains(t, roles, "ORGANIZATION")
	assert.Contains(t, roles, "PRODUCT")
}

func TestNuExtract_DefaultsApplied_WhenExtractCalledWithNil(t *testing.T) {
	var capturedTemplate string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		if kw, ok := parsed["chat_template_kwargs"].(map[string]any); ok {
			capturedTemplate, _ = kw["template"].(string)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(wrapChatCompletion(`{"entities":[]}`)))
	}))
	defer srv.Close()

	m := NewModelExtractor(ModelConfig{
		Endpoint:  srv.URL,
		Model:     "numind/NuExtract-2.0-2B",
		Format:    FormatNuExtract,
		NuExtract: NuExtractOptions{Mode: "single", Transport: "native"},
	})
	_, err := m.Extract(context.Background(), "b", nil, nil)
	require.NoError(t, err)
	assert.Contains(t, capturedTemplate, "ORGANIZATION")
	assert.Contains(t, capturedTemplate, "PERSON")
}
