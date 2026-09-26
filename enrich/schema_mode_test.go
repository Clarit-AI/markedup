package enrich

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B5b / #144. The old code hard-coded `SchemaMode: !isLocal`, which was wrong
// in both directions: vLLM (local) supports json_schema, and plenty of hosted
// endpoints do not. Either mismatch produced a hard HTTP 400 that killed the
// run outright.
//
// These tests pin the two properties that matter: the run must not die, and we
// must not spend money finding out.

// A well-formed chat response the fallback parser accepts.
func schemaModeOKBody() string {
	payload, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{
			"message": map[string]any{
				"role":    "assistant",
				"content": `{"entities":[],"relationships":[],"summary":"s","entity_type":"document"}`,
			},
		}},
	})
	return string(payload)
}

// The endpoint accepts json_schema and must not be degraded or re-probed.
func TestSchemaMode_SupportingEndpointKeepsStructuredOutput(t *testing.T) {
	ResetSchemaModeCache()
	t.Cleanup(ResetSchemaModeCache)

	var sawSchema atomic.Int32
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, ok := req["response_format"]; ok {
			sawSchema.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(schemaModeOKBody()))
	}))
	defer srv.Close()

	ex := NewLLMFallbackExtractor(LLMFallbackConfig{
		Endpoint:   srv.URL,
		Model:      "m",
		SchemaMode: ResolveSchemaMode(srv.URL, false),
	})
	require.True(t, ex.schemaMode, "non-local endpoint should start in schema mode")

	for i := 0; i < 3; i++ {
		_, err := ex.Extract(context.Background(), "body", nil, nil)
		require.NoError(t, err)
	}

	assert.Equal(t, int32(3), sawSchema.Load(), "structured output must be sent on every call")
	assert.Equal(t, int32(3), calls.Load(), "no extra probe requests")
}

// The endpoint rejects json_schema with a 400. The run must degrade and
// complete, not die — this is the exact failure the static guess caused.
func TestSchemaMode_RejectingEndpointDegradesAndCompletes(t *testing.T) {
	ResetSchemaModeCache()
	t.Cleanup(ResetSchemaModeCache)

	var sawSchema atomic.Int32
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, ok := req["response_format"]; ok {
			sawSchema.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"response_format json_schema is not supported by this model"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(schemaModeOKBody()))
	}))
	defer srv.Close()

	ex := NewLLMFallbackExtractor(LLMFallbackConfig{
		Endpoint:   srv.URL,
		Model:      "m",
		SchemaMode: ResolveSchemaMode(srv.URL, false),
	})

	res, err := ex.Extract(context.Background(), "body", nil, nil)
	require.NoError(t, err, "a schema-rejecting endpoint must degrade, not fail the run")
	require.NotNil(t, res)

	assert.False(t, ex.schemaMode, "extractor must have degraded for subsequent calls")

	// The answer must be remembered, so later calls skip the doomed first try.
	callsAfterFirst := calls.Load()
	for i := 0; i < 3; i++ {
		_, err := ex.Extract(context.Background(), "body", nil, nil)
		require.NoError(t, err)
	}
	assert.Equal(t, callsAfterFirst+3, calls.Load(),
		"after degrading, each call must cost exactly one request — the schema must not be retried")

	// A fresh extractor for the same endpoint must start from the learned
	// answer, not the heuristic.
	assert.False(t, ResolveSchemaMode(srv.URL, false),
		"the endpoint's demonstrated lack of support must be cached")

	// And the heuristic must be recovered once the endpoint starts working.
	ResetSchemaModeCache()
	assert.True(t, ResolveSchemaMode(srv.URL, false), "heuristic restored after a cache reset")
}

// A 400 that is NOT about the schema must not degrade structured output.
// Otherwise an unrelated bad request (bad model name, context overflow)
// silently costs the endpoint its schema support, and that wrong conclusion
// gets cached.
func TestSchemaMode_UnrelatedBadRequestDoesNotDegrade(t *testing.T) {
	ResetSchemaModeCache()
	t.Cleanup(ResetSchemaModeCache)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"maximum context length exceeded"}}`))
	}))
	defer srv.Close()

	ex := NewLLMFallbackExtractor(LLMFallbackConfig{
		Endpoint:   srv.URL,
		Model:      "m",
		SchemaMode: true,
	})
	_, err := ex.Extract(context.Background(), "body", nil, nil)
	require.Error(t, err, "a non-schema 400 is a real failure and must surface")
	assert.True(t, ex.schemaMode, "structured output must NOT be dropped for an unrelated 400")
}

// A network error must never be mistaken for schema rejection.
func TestIsSchemaRejection(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"schema named", errString("llm: model returned status 400: unsupported response_format json_schema"), true},
		{"json_schema named", errString("llm: model returned status 400: json_schema is not supported"), true},
		{"unrelated 400", errString("llm: model returned status 400: context length exceeded"), false},
		{"unrelated status", errString("llm: model returned status 500: response_format boom"), false},
		{"non-llm", errString("dial tcp: connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsSchemaRejection(tc.err))
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// MARKEDUP_LLM_SCHEMA_MODE forces the answer and must win over both the cache
// and the heuristic, so operators and offline runs can pin the behaviour.
func TestResolveSchemaMode_EnvOverride(t *testing.T) {
	ResetSchemaModeCache()
	t.Cleanup(ResetSchemaModeCache)

	for _, tc := range []struct {
		val  string
		want bool
	}{
		{"1", true}, {"true", true}, {"yes", true},
		{"0", false}, {"false", false}, {"no", false},
	} {
		t.Run(tc.val, func(t *testing.T) {
			require.NoError(t, os.Setenv("MARKEDUP_LLM_SCHEMA_MODE", tc.val))
			t.Cleanup(func() { _ = os.Unsetenv("MARKEDUP_LLM_SCHEMA_MODE") })
			assert.Equal(t, tc.want, ResolveSchemaMode("https://example.invalid", false))
			assert.Equal(t, tc.want, ResolveSchemaMode("https://example.invalid", true),
				"the override must beat the isLocal heuristic too")
		})
	}
}

// With no env override, the historical heuristic still holds — so endpoints
// that worked before this change keep working.
func TestResolveSchemaMode_HeuristicFallback(t *testing.T) {
	ResetSchemaModeCache()
	t.Cleanup(ResetSchemaModeCache)
	_ = os.Unsetenv("MARKEDUP_LLM_SCHEMA_MODE")

	assert.True(t, ResolveSchemaMode("https://cloud.example", false), "remote endpoints default to schema mode")
	assert.False(t, ResolveSchemaMode("http://localhost:11434", true), "local endpoints default to prompt-only")

	// Repeated calls must be stable.
	for i := 0; i < 5; i++ {
		assert.True(t, ResolveSchemaMode("https://cloud.example", false))
	}
}

// Distinct endpoints must not share a verdict.
func TestResolveSchemaMode_PerEndpoint(t *testing.T) {
	ResetSchemaModeCache()
	t.Cleanup(ResetSchemaModeCache)
	_ = os.Unsetenv("MARKEDUP_LLM_SCHEMA_MODE")

	assert.True(t, ResolveSchemaMode("https://a.example", false))
	assert.False(t, ResolveSchemaMode("https://b.example", true))
	assert.True(t, ResolveSchemaMode("https://a.example", false),
		"endpoint a's verdict must not have been polluted by endpoint b")
}

// Guard the cost property explicitly: resolving must perform no network I/O.
// A startup probe would mean a billable inference call before any real work.
func TestResolveSchemaMode_DoesNoNetworkIO(t *testing.T) {
	ResetSchemaModeCache()
	t.Cleanup(ResetSchemaModeCache)
	_ = os.Unsetenv("MARKEDUP_LLM_SCHEMA_MODE")

	// An unroutable endpoint would hang or error if touched. Resolve must
	// return instantly and without error — it has no error return precisely
	// because it cannot fail.
	start := make(chan bool, 1)
	go func() {
		_ = ResolveSchemaMode("http://127.0.0.1:1", false)
		start <- true
	}()
	select {
	case <-start:
	case <-time.After(2 * time.Second):
		t.Fatal("ResolveSchemaMode appears to perform network I/O")
	}
}
