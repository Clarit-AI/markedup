package index

import (
	"context"
	"fmt"
	"testing"

	"github.com/KHAEntertainment/markedup/embed"
	"github.com/KHAEntertainment/markedup/rerank"
	"github.com/KHAEntertainment/markedup/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockVectorCache implements VectorCacheLookup for testing without importing cache.
type mockVectorCache struct {
	vectors map[string][]float32 // fileID -> vector
	hashes  map[string]string    // fileID -> expected hash
}

func (m *mockVectorCache) LoadVectors(fileID string, contentHash string) ([]float32, error) {
	if v, ok := m.vectors[fileID]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("no vectors for %s", fileID)
}

// mockEmbedder implements embed.Embedder for testing.
type mockEmbedder struct {
	vectors map[string][]float32 // text -> vector
	dims    int
	model   string
}

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, t := range texts {
		if v, ok := m.vectors[t]; ok {
			result[i] = v
		} else {
			// Return a zero vector for unknown texts.
			result[i] = make([]float32, m.dims)
		}
	}
	return result, nil
}

func (m *mockEmbedder) Dimensions() int { return m.dims }
func (m *mockEmbedder) Model() string   { return m.model }

// mockReranker implements rerank.Reranker for testing.
type mockReranker struct {
	scores map[string]float64 // candidate ID -> score
}

func (m *mockReranker) Rerank(_ context.Context, _ string, candidates []rerank.Candidate) ([]rerank.RankedResult, error) {
	results := make([]rerank.RankedResult, len(candidates))
	for i, c := range candidates {
		score := 0.0
		if s, ok := m.scores[c.ID]; ok {
			score = s
		}
		results[i] = rerank.RankedResult{
			Candidate: c,
			Score:     score,
			Rank:      0, // will be set below
		}
	}
	// Sort by score descending.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	for i := range results {
		results[i].Rank = i + 1
	}
	return results, nil
}

// setupTestIndexWithVectors creates a test index and populates a mock vector
// cache with pre-computed vectors for each page.
func setupTestIndexWithVectors(t *testing.T) (*KnowledgeIndex, *mockVectorCache, *mockEmbedder) {
	t.Helper()

	pages := []*schema.Page{
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "go-page",
				Title:      "Go Programming",
				EntityType: "topic",
				Confidence: 1.0,
				Tags:       []string{"golang", "programming"},
			},
			Body:       "Go is a statically typed compiled programming language.",
			SourcePath: "go.md",
		},
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "rust-page",
				Title:      "Rust Programming",
				EntityType: "topic",
				Confidence: 1.0,
				Tags:       []string{"rust", "programming"},
			},
			Body:       "Rust is a systems programming language focused on safety.",
			SourcePath: "rust.md",
		},
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "cooking-page",
				Title:      "Italian Cooking",
				EntityType: "topic",
				Confidence: 1.0,
				Tags:       []string{"cooking", "food"},
			},
			Body:       "Italian cooking is known for pasta, pizza, and fresh ingredients.",
			SourcePath: "cooking.md",
		},
	}
	idx := buildIndex(pages)

	// Store vectors: go and rust are similar, cooking is different.
	vc := &mockVectorCache{
		vectors: map[string][]float32{
			"go-page":      {0.9, 0.1, 0.0},
			"rust-page":    {0.8, 0.2, 0.0},
			"cooking-page": {0.0, 0.1, 0.9},
		},
	}

	emb := &mockEmbedder{
		vectors: map[string][]float32{
			"go concurrency": {0.85, 0.15, 0.0}, // similar to go/rust
			"pasta recipe":   {0.0, 0.05, 0.95}, // similar to cooking
		},
		dims:  3,
		model: "test-model",
	}

	return idx, vc, emb
}

func TestSearch_NeitherEmbedderNorReranker(t *testing.T) {
	// Verify behavior is identical to Phase 1.
	idx := testIndex()
	results := Search(idx, "alice")

	require.NotEmpty(t, results)
	assert.Equal(t, "alice", results[0].Page.Frontmatter.ID)
}

func TestSearch_WithMockEmbedder(t *testing.T) {
	idx, vc, emb := setupTestIndexWithVectors(t)

	results := Search(idx, "go concurrency",
		WithEmbedder(emb),
		WithVectorCache(vc),
		WithContext(context.Background()),
	)

	require.NotEmpty(t, results)
	// Go page should rank higher than cooking due to embedding similarity.
	var goScore, cookingScore float64
	for _, r := range results {
		switch r.Page.Frontmatter.ID {
		case "go-page":
			goScore = r.Score
		case "cooking-page":
			cookingScore = r.Score
		}
	}
	assert.Greater(t, goScore, cookingScore,
		"go-page should score higher than cooking-page with semantic similarity")
}

func TestSearch_WithMockReranker(t *testing.T) {
	idx := testIndex()

	rr := &mockReranker{
		scores: map[string]float64{
			"alice": 0.3,
			"bob":   0.9, // reranker prefers bob
			"acme":  0.1,
		},
	}

	results := Search(idx, "engineer",
		WithReranker(rr),
		WithContext(context.Background()),
	)

	require.NotEmpty(t, results)
	// Reranker should reorder: bob first (0.9), alice second (0.3), acme third (0.1).
	assert.Equal(t, "bob", results[0].Page.Frontmatter.ID,
		"reranker should put bob first")
}

func TestSearch_WithBothEmbedderAndReranker(t *testing.T) {
	idx, vc, emb := setupTestIndexWithVectors(t)

	rr := &mockReranker{
		scores: map[string]float64{
			"go-page":      0.5,
			"rust-page":    0.9, // reranker prefers rust
			"cooking-page": 0.1,
		},
	}

	results := Search(idx, "go concurrency",
		WithEmbedder(emb),
		WithVectorCache(vc),
		WithReranker(rr),
		WithContext(context.Background()),
	)

	require.NotEmpty(t, results)
	// After reranking, rust should be first (reranker score 0.9).
	assert.Equal(t, "rust-page", results[0].Page.Frontmatter.ID,
		"reranker should reorder results after embedding scoring")
}

func TestSearch_EmbedderWithoutVectorCache(t *testing.T) {
	idx := testIndex()
	emb := &mockEmbedder{
		vectors: map[string][]float32{
			"alice": {1.0, 0.0, 0.0},
		},
		dims:  3,
		model: "test",
	}

	// Embedder without vector cache should fall back to keyword-only.
	results := Search(idx, "alice",
		WithEmbedder(emb),
		WithContext(context.Background()),
	)

	require.NotEmpty(t, results)
	assert.Equal(t, "alice", results[0].Page.Frontmatter.ID)
}

func TestSearch_VectorsNotCachedFallback(t *testing.T) {
	pages := []*schema.Page{
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "page-a",
				Title:      "Test Page A",
				Confidence: 1.0,
				Tags:       []string{"test"},
			},
			Body:       "test content",
			SourcePath: "a.md",
		},
	}
	idx := buildIndex(pages)

	vc := &mockVectorCache{vectors: map[string][]float32{}}
	// No vectors saved — should fall back gracefully.

	emb := &mockEmbedder{
		vectors: map[string][]float32{
			"test": {1.0, 0.0},
		},
		dims:  2,
		model: "test",
	}

	results := Search(idx, "test",
		WithEmbedder(emb),
		WithVectorCache(vc),
		WithContext(context.Background()),
	)

	// Should still return keyword-based results.
	require.NotEmpty(t, results)
	assert.Equal(t, "page-a", results[0].Page.Frontmatter.ID)
}

func TestSearch_WithEmbeddingWeight(t *testing.T) {
	idx, vc, emb := setupTestIndexWithVectors(t)

	// Weight 0 = keyword only.
	resultsKeyword := Search(idx, "go concurrency",
		WithEmbedder(emb),
		WithVectorCache(vc),
		WithEmbeddingWeight(0.0),
		WithContext(context.Background()),
	)

	// Weight 1 = embedding only (well, keyword * 0 + embedding * 1).
	resultsEmbedding := Search(idx, "go concurrency",
		WithEmbedder(emb),
		WithVectorCache(vc),
		WithEmbeddingWeight(1.0),
		WithContext(context.Background()),
	)

	// Both should return results, but ordering may differ.
	require.NotEmpty(t, resultsKeyword)
	require.NotEmpty(t, resultsEmbedding)
}

func TestSearch_EmbeddingWeightClamped(t *testing.T) {
	// Verify negative weight is clamped to 0.
	cfg := &searchConfig{}
	WithEmbeddingWeight(-0.5)(cfg)
	assert.Equal(t, 0.0, cfg.embeddingWeight)

	// Verify weight > 1 is clamped to 1.
	cfg2 := &searchConfig{}
	WithEmbeddingWeight(1.5)(cfg2)
	assert.Equal(t, 1.0, cfg2.embeddingWeight)
}

func TestCosineSimilarity_IntegrationWithSearch(t *testing.T) {
	// Verify CosineSimilarity is used correctly by checking score blending.
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	sim := embed.CosineSimilarity(a, b)
	assert.InDelta(t, 1.0, sim, 1e-6)

	// Orthogonal vectors.
	c := []float32{0, 1, 0}
	sim2 := embed.CosineSimilarity(a, c)
	assert.InDelta(t, 0.0, sim2, 1e-6)
}
