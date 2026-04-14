//go:build localmodel

package markedup_test

import (
	"context"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KHAEntertainment/markedup/embed"
	"github.com/KHAEntertainment/markedup/enrich"
	"github.com/KHAEntertainment/markedup/markdown"
	"github.com/KHAEntertainment/markedup/rerank"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipIfNoEndpoint skips the test if the env var is unset or the endpoint is
// unreachable. Returns the endpoint URL if reachable.
func skipIfNoEndpoint(t *testing.T, envVar string) string {
	t.Helper()
	endpoint := os.Getenv(envVar)
	if endpoint == "" {
		t.Skipf("skipping: %s not set", envVar)
	}
	// Quick reachability check via GET /v1/models.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/models", nil)
	if err != nil {
		t.Skipf("skipping: %s endpoint %s — could not build request: %v", envVar, endpoint, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode >= 500 {
		t.Skipf("skipping: %s endpoint %s unreachable (%v)", envVar, endpoint, err)
	}
	resp.Body.Close()
	return endpoint
}

// skipIfNoEndpointNoProbe skips if the env var is unset but does NOT probe for
// reachability. Used for the reranker which may not serve GET /v1/models.
func skipIfNoEndpointNoProbe(t *testing.T, envVar string) string {
	t.Helper()
	endpoint := os.Getenv(envVar)
	if endpoint == "" {
		t.Skipf("skipping: %s not set", envVar)
	}
	return endpoint
}

// TestEmbedLocalModel tests the OpenAI-compatible embedder against a local
// embedding server. Set MARKEDUP_EMBED_ENDPOINT and MARKEDUP_EMBED_MODEL.
func TestEmbedLocalModel(t *testing.T) {
	endpoint := skipIfNoEndpoint(t, "MARKEDUP_EMBED_ENDPOINT")

	model := os.Getenv("MARKEDUP_EMBED_MODEL")
	if model == "" {
		t.Skip("skipping: MARKEDUP_EMBED_MODEL not set")
	}

	embedder := embed.NewOpenAICompatible(embed.Config{
		Endpoint:  endpoint,
		ModelName: model,
		Dims:      0, // no dimensionality validation for E2E
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	texts := []string{"The knowledge graph connects concepts through semantic relationships."}
	vecs, err := embedder.Embed(ctx, texts)
	require.NoError(t, err, "Embed() must not error")
	require.Len(t, vecs, 1, "expected exactly 1 embedding vector")
	require.NotEmpty(t, vecs[0], "embedding vector must not be empty")

	// Validate all values are finite (not NaN or Inf).
	for i, v := range vecs[0] {
		assert.False(t, math.IsNaN(float64(v)), "embedding[%d] is NaN", i)
		assert.False(t, math.IsInf(float64(v), 0), "embedding[%d] is Inf", i)
	}

	t.Logf("embed OK: model=%s dims=%d", model, len(vecs[0]))
}

// TestEnrichSummaryLocalModel tests GenerateSummary against a local LLM.
// Set MARKEDUP_LLM_ENDPOINT and MARKEDUP_LLM_MODEL.
func TestEnrichSummaryLocalModel(t *testing.T) {
	endpoint := skipIfNoEndpoint(t, "MARKEDUP_LLM_ENDPOINT")

	model := os.Getenv("MARKEDUP_LLM_MODEL")
	if model == "" {
		t.Skip("skipping: MARKEDUP_LLM_MODEL not set")
	}

	extractor := enrich.NewModelExtractor(enrich.ModelConfig{
		Endpoint: endpoint,
		Model:    model,
	})

	page, err := markdown.ParseFile("testdata/valid/alice.md")
	require.NoError(t, err, "failed to parse alice.md")

	fm := page.Frontmatter
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	summary, err := extractor.GenerateSummary(
		ctx,
		fm.Title,
		fm.EntityType,
		fm.Tags,
		enrich.BodyPreview(page.Body, 500),
	)
	require.NoError(t, err, "GenerateSummary() must not error")
	assert.Greater(t, len(summary), 10, "summary must be more than 10 characters, got: %q", summary)

	t.Logf("summary OK: model=%s len=%d summary=%q", model, len(summary), summary)
}

// TestEnrichExtractLLM tests Extract() against a local general-purpose LLM.
// Small models (e.g. granite-4-h-tiny) often fail to emit valid JSON, so JSON
// parse failures are treated as warnings rather than hard failures.
// Set MARKEDUP_LLM_ENDPOINT and MARKEDUP_LLM_MODEL.
func TestEnrichExtractLLM(t *testing.T) {
	endpoint := skipIfNoEndpoint(t, "MARKEDUP_LLM_ENDPOINT")

	model := os.Getenv("MARKEDUP_LLM_MODEL")
	if model == "" {
		t.Skip("skipping: MARKEDUP_LLM_MODEL not set")
	}

	extractor := enrich.NewModelExtractor(enrich.ModelConfig{
		Endpoint: endpoint,
		Model:    model,
	})

	page, err := markdown.ParseFile("testdata/valid/alice.md")
	require.NoError(t, err, "failed to parse alice.md")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := extractor.Extract(ctx, page.Body, nil, nil)
	if err != nil {
		errStr := err.Error()
		// Lenient: JSON parse failures are expected for small models.
		if strings.Contains(errStr, "parse model output") {
			t.Logf("model did not return valid JSON (expected for small models): %v", err)
			return
		}
		// Hard-fail on network/timeout errors.
		require.NoError(t, err, "Extract() failed with a non-parse error")
	}

	t.Logf("extract OK: model=%s entity_type=%q entities=%d hints=%d",
		model, result.EntityType, len(result.Entities), len(result.SemanticHints))
}

// TestEnrichExtractTriplex tests Extract() against the Triplex model which is
// specifically fine-tuned for structured JSON knowledge extraction.
// Set MARKEDUP_TRIPLEX_ENDPOINT and MARKEDUP_TRIPLEX_MODEL.
func TestEnrichExtractTriplex(t *testing.T) {
	endpoint := skipIfNoEndpoint(t, "MARKEDUP_TRIPLEX_ENDPOINT")

	model := os.Getenv("MARKEDUP_TRIPLEX_MODEL")
	if model == "" {
		t.Skip("skipping: MARKEDUP_TRIPLEX_MODEL not set")
	}

	extractor := enrich.NewModelExtractor(enrich.ModelConfig{
		Endpoint: endpoint,
		Model:    model,
		Format:   enrich.FormatTriplex,
	})

	page, err := markdown.ParseFile("testdata/valid/alice.md")
	require.NoError(t, err, "failed to parse alice.md")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := extractor.Extract(ctx, page.Body, nil, nil)
	require.NoError(t, err, "Triplex Extract() must not error — model should return valid JSON")
	require.NotNil(t, result)

	assert.NotEmpty(t, result.EntityType, "Triplex must produce a non-empty entity_type")

	hasEntities := len(result.Entities) > 0
	hasHints := len(result.SemanticHints) > 0
	assert.True(t, hasEntities || hasHints,
		"Triplex must produce at least one entity or semantic hint")

	t.Logf("triplex OK: model=%s entity_type=%q entities=%d hints=%d",
		model, result.EntityType, len(result.Entities), len(result.SemanticHints))
}

// TestRerankLocalModel tests the cross-encoder reranker against a local rerank
// server (e.g. infinity-emb with a jina-reranker model).
// Set MARKEDUP_RERANK_ENDPOINT and MARKEDUP_RERANK_MODEL.
func TestRerankLocalModel(t *testing.T) {
	// Use no-probe variant since rerankers typically don't serve GET /v1/models.
	endpoint := skipIfNoEndpointNoProbe(t, "MARKEDUP_RERANK_ENDPOINT")

	model := os.Getenv("MARKEDUP_RERANK_MODEL")
	if model == "" {
		t.Skip("skipping: MARKEDUP_RERANK_MODEL not set")
	}

	reranker := rerank.NewCrossEncoder(rerank.Config{
		Endpoint: endpoint,
		Model:    model,
		Format:   rerank.FormatJina,
	})

	candidates := []rerank.Candidate{
		{
			ID:            "alice",
			Text:          "Alice Chen is a senior AI researcher specializing in knowledge graph construction.",
			OriginalScore: 0.8,
		},
		{
			ID:            "bob",
			Text:          "Bob Martinez is a software engineer working on distributed systems.",
			OriginalScore: 0.6,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, err := reranker.Rerank(ctx, "knowledge graph", candidates)
	require.NoError(t, err, "Rerank() must not error")
	require.NotEmpty(t, results, "Rerank() must return at least one result")

	t.Logf("rerank OK: model=%s results=%d top=%q score=%.4f",
		model, len(results), results[0].ID, results[0].Score)
}

// TestSearchSemanticLocalModel: see embed command for full pipeline test.
// The embedding E2E is covered by TestEmbedLocalModel; full semantic search
// pipeline testing requires the embed CLI command or a loaded index with
// pre-computed vectors.
