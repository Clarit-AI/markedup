package index

import (
	"strings"
	"testing"

	"github.com/Clarit-AI/markedup/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testIndex builds a small KnowledgeIndex for search tests.
func testIndex() *KnowledgeIndex {
	pages := []*schema.Page{
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "alice",
				Title:      "Alice Johnson",
				EntityType: "person",
				Confidence: 0.95,
				Tags:       []string{"engineer", "golang"},
				Entities: []schema.Entity{
					{Name: "Alice Johnson", Aliases: []string{"AJ", "alice"}, Role: "protagonist"},
				},
				Relationships: []schema.Relationship{
					{Target: "bob", Type: "colleague", Strength: 0.8},
				},
				SemanticHints: []string{"software engineer at Acme"},
				Temporal: schema.TemporalInfo{
					LastVerified: "2026-04-01",
					DecayRate:    0.01,
				},
			},
			Body:       "Alice is a senior software engineer who works on distributed systems. She collaborates with Bob on the backend platform.",
			SourcePath: "alice.md",
		},
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "bob",
				Title:      "Bob Smith",
				EntityType: "person",
				Confidence: 0.8,
				Tags:       []string{"engineer", "python"},
				Entities: []schema.Entity{
					{Name: "Bob Smith", Aliases: []string{"bobby"}, Role: "support"},
				},
				Relationships: []schema.Relationship{
					{Target: "alice", Type: "colleague", Strength: 0.7},
				},
				SemanticHints: []string{"data engineer"},
			},
			Body:       "Bob specializes in data pipelines and machine learning infrastructure.",
			SourcePath: "bob.md",
		},
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "acme",
				Title:      "Acme Corp",
				EntityType: "organization",
				Confidence: 0.9,
				Tags:       []string{"company", "tech"},
				Entities: []schema.Entity{
					{Name: "Acme Corp", Aliases: []string{"Acme"}, Role: "employer"},
				},
				SemanticHints: []string{"technology company"},
			},
			Body:       "Acme Corp is a technology company founded in 2020.",
			SourcePath: "acme.md",
		},
	}
	return buildIndex(pages)
}

func TestSearchExactMatchHighestScore(t *testing.T) {
	idx := testIndex()
	results := Search(idx, "Alice Johnson")

	require.NotEmpty(t, results)
	assert.Equal(t, "alice", results[0].Page.Frontmatter.ID)

	// Exact match should have the highest score.
	for i := 1; i < len(results); i++ {
		assert.Greater(t, results[0].Score, results[i].Score,
			"exact match should score higher than result %d", i)
	}
}

func TestSearchPrefixLowerThanExact(t *testing.T) {
	idx := testIndex()

	exactResults := Search(idx, "alice")
	prefixResults := Search(idx, "alic")

	require.NotEmpty(t, exactResults)
	require.NotEmpty(t, prefixResults)

	// Find alice in both result sets.
	var exactScore, prefixScore float64
	for _, r := range exactResults {
		if r.Page.Frontmatter.ID == "alice" {
			exactScore = r.Score
		}
	}
	for _, r := range prefixResults {
		if r.Page.Frontmatter.ID == "alice" {
			prefixScore = r.Score
		}
	}

	assert.Greater(t, exactScore, prefixScore,
		"exact match score should be higher than prefix match score")
}

func TestSearchFuzzyLowestNonZero(t *testing.T) {
	idx := testIndex()

	// "alicx" is levenshtein distance 1 from "alice" — should fuzzy match.
	results := Search(idx, "alicx")

	require.NotEmpty(t, results)
	// Find alice page.
	var found bool
	for _, r := range results {
		if r.Page.Frontmatter.ID == "alice" {
			found = true
			assert.Greater(t, r.Score, 0.0, "fuzzy match should have non-zero score")
		}
	}
	assert.True(t, found, "alice should appear in fuzzy search results")
}

func TestMultiFieldMaxNotSum(t *testing.T) {
	// Create a page with many tags that match; score should use max, not sum.
	pages := []*schema.Page{
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "multi-tag",
				Title:      "Something",
				Confidence: 1.0,
				Tags:       []string{"go", "go-lang", "go-dev", "go-tools"},
			},
			Body: "some body",
		},
	}
	idx := buildIndex(pages)
	results := Search(idx, "go")

	require.Len(t, results, 1)
	// With max (not sum), only the best tag match contributes.
	// "go" exact match on tag = weightTag(5) * 1.0 = 5.
	// If it were sum, it would be higher (e.g. 5 + 5*0.8 + ...).
	// Also body may contribute "go-lang" etc. but tag weight is the key check.
	tagMatches := 0
	for _, m := range results[0].Matches {
		if m.Field == "tag" {
			tagMatches++
		}
	}
	assert.Equal(t, 1, tagMatches, "should have exactly 1 tag match (max, not sum)")
}

func TestTemporalDecayAffectsScore(t *testing.T) {
	// Page with recent verification should score higher than old verification.
	recentPage := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:         "recent",
			Title:      "Test Page",
			Confidence: 0.9,
			Temporal: schema.TemporalInfo{
				LastVerified: "2026-04-11",
				DecayRate:    0.1,
			},
		},
		Body: "test content",
	}
	oldPage := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:         "old",
			Title:      "Test Page",
			Confidence: 0.9,
			Temporal: schema.TemporalInfo{
				LastVerified: "2025-01-01",
				DecayRate:    0.1,
			},
		},
		Body: "test content",
	}

	idxRecent := buildIndex([]*schema.Page{recentPage})
	idxOld := buildIndex([]*schema.Page{oldPage})

	resultsRecent := Search(idxRecent, "test")
	resultsOld := Search(idxOld, "test")

	require.NotEmpty(t, resultsRecent)
	require.NotEmpty(t, resultsOld)

	assert.Greater(t, resultsRecent[0].Score, resultsOld[0].Score,
		"recently verified page should score higher due to temporal decay")
}

func TestWithLimitLimitsResults(t *testing.T) {
	idx := testIndex()
	results := Search(idx, "engineer", WithLimit(1))

	assert.LessOrEqual(t, len(results), 1)
}

func TestWithMinScoreFilters(t *testing.T) {
	idx := testIndex()
	results := Search(idx, "engineer")
	require.NotEmpty(t, results)

	// Set min score above all results — should return empty.
	results = Search(idx, "engineer", WithMinScore(9999))
	assert.Empty(t, results)
}

func TestEmptyQueryReturnsEmpty(t *testing.T) {
	idx := testIndex()
	results := Search(idx, "")
	assert.Empty(t, results)
}

func TestFormatForLLM(t *testing.T) {
	idx := testIndex()
	results := Search(idx, "alice")
	require.NotEmpty(t, results)

	var aliceResult Result
	for _, r := range results {
		if r.Page.Frontmatter.ID == "alice" {
			aliceResult = r
			break
		}
	}

	formatted := aliceResult.FormatForLLM()

	assert.Contains(t, formatted, "# Alice Johnson (person)")
	assert.Contains(t, formatted, "Confidence: 0.95")
	assert.Contains(t, formatted, "bob (colleague)")
	assert.Contains(t, formatted, "software engineer at Acme")
	assert.Contains(t, formatted, "Alice is a senior software engineer")
}

func TestSearchAliceReturnsAliceFirst(t *testing.T) {
	idx := testIndex()
	results := Search(idx, "alice")

	require.NotEmpty(t, results)
	assert.Equal(t, "alice", results[0].Page.Frontmatter.ID,
		"searching for 'alice' should return the alice page first")
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"kitten", "sitting", 3},
		{"alice", "alicx", 1},
		{"alice", "alice", 0},
		{"go", "goo", 1},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			got := levenshtein(tt.a, tt.b)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatForLLMBodyExcerpt(t *testing.T) {
	longBody := strings.Repeat("x", 600)
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:         "long",
			Title:      "Long Body Page",
			Confidence: 0.5,
		},
		Body: longBody,
	}
	result := Result{Page: page, Score: 1.0}
	formatted := result.FormatForLLM()

	// Body should be truncated to 500 chars.
	lines := strings.Split(formatted, "\n")
	// Find the body line (after the blank line).
	var bodyContent string
	pastBlank := false
	for _, line := range lines {
		if pastBlank && line != "" {
			bodyContent = line
			break
		}
		if line == "" {
			pastBlank = true
		}
	}
	assert.LessOrEqual(t, len(bodyContent), 500)
}

func TestFormatForLLMNoEntityType(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:         "no-type",
			Title:      "No Type",
			Confidence: 0.5,
		},
	}
	result := Result{Page: page, Score: 1.0}
	formatted := result.FormatForLLM()

	assert.Contains(t, formatted, "# No Type\n")
	assert.NotContains(t, formatted, "()")
}
