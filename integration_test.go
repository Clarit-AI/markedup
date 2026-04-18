//go:build integration

package markedup_test

import (
	"bytes"
	"context"
	"math"
	"testing"
	"time"

	"github.com/Clarit-AI/markedup/index"
	"github.com/Clarit-AI/markedup/markdown"
	"github.com/Clarit-AI/markedup/temporal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadValidDir(t *testing.T) {
	result, err := index.Load(context.Background(), "testdata/valid")
	require.NoError(t, err)
	assert.Equal(t, 6, result.Index.Pages(), "expected 6 valid pages")
}

func TestLoadInvalidDirWithIgnoreErrors(t *testing.T) {
	result, err := index.Load(context.Background(), "testdata/invalid",
		index.WithIgnoreErrors(true))
	require.NoError(t, err)

	// We expect warnings from all 3 files: broken-yaml (parse error),
	// missing-id (validation), bad-confidence (validation).
	warningCount := len(result.Warnings)
	assert.GreaterOrEqual(t, warningCount, 3,
		"expected at least 3 warning sets from invalid dir, got %d", warningCount)
}

func TestSearchAlice(t *testing.T) {
	result, err := index.Load(context.Background(), "testdata/valid")
	require.NoError(t, err)

	results := index.Search(result.Index, "alice")
	require.NotEmpty(t, results, "search for 'alice' should return results")
	assert.Equal(t, "alice", results[0].Page.Frontmatter.ID,
		"alice.md should be the top search result")
}

func TestTraverseAliceDepth2(t *testing.T) {
	result, err := index.Load(context.Background(), "testdata/valid")
	require.NoError(t, err)

	traversal, err := index.Traverse(result.Index, "alice", index.WithDepth(2))
	require.NoError(t, err)

	// Collect discovered node IDs (excluding the root).
	nodeIDs := make(map[string]bool)
	for _, node := range traversal.Nodes {
		nodeIDs[node.Page.Frontmatter.ID] = true
	}

	assert.True(t, nodeIDs["bob"],
		"traversal from alice at depth 2 should include bob (colleague)")
	assert.True(t, nodeIDs["concept-graph"],
		"traversal from alice at depth 2 should include concept-graph (studies)")
}

func TestStaleDocDecay(t *testing.T) {
	result, err := index.Load(context.Background(), "testdata/valid")
	require.NoError(t, err)

	stalePage, ok := result.Index.Get("stale-doc")
	require.True(t, ok, "stale-doc should be in the index")

	// stale-doc: confidence 0.9, decay-rate 0.05, last-verified 2023-04-12
	// At 365+ days since verification, effective = 0.9 * exp(-0.05 * days)
	lastVerified, err := time.Parse("2006-01-02", stalePage.Frontmatter.Temporal.LastVerified)
	require.NoError(t, err)

	now := time.Now()
	daysSince := now.Sub(lastVerified).Hours() / 24
	require.Greater(t, daysSince, float64(365),
		"stale-doc last-verified should be >365 days ago for this test")

	decayed := temporal.DecayedConfidenceAt(
		stalePage.Frontmatter.Confidence,
		stalePage.Frontmatter.Temporal.DecayRate,
		lastVerified,
		now,
	)

	// At ~365 days: 0.9 * exp(-0.05*365) ≈ 0.9 * exp(-18.25) ≈ very small
	// At ~1100 days (from 2023-04-12 to 2026-04-12): 0.9 * exp(-55) ≈ ~0
	assert.Less(t, decayed, 0.01,
		"stale-doc decayed confidence should be very low, got %g", decayed)

	// Verify it's close to the expected calculation
	expected := 0.9 * math.Exp(-0.05*daysSince)
	assert.InDelta(t, expected, decayed, 1e-10,
		"decayed confidence should match exponential formula")
}

func TestSerializeAliceContainsWikilinks(t *testing.T) {
	result, err := index.Load(context.Background(), "testdata/valid")
	require.NoError(t, err)

	alicePage, ok := result.Index.Get("alice")
	require.True(t, ok)

	var buf bytes.Buffer
	err = markdown.WritePage(&buf, alicePage)
	require.NoError(t, err)

	serialized := buf.String()
	assert.Contains(t, serialized, "## Related",
		"serialized alice.md should contain ## Related section")
	assert.Contains(t, serialized, "[[bob]]",
		"serialized alice.md should contain [[bob]] wikilink")
	assert.Contains(t, serialized, "[[concept-graph]]",
		"serialized alice.md should contain [[concept-graph]] wikilink")
}

func TestRoundTripParse(t *testing.T) {
	// Parse alice.md from disk.
	original, err := markdown.ParseFile("testdata/valid/alice.md")
	require.NoError(t, err)

	// Serialize to bytes.
	var buf bytes.Buffer
	err = markdown.WritePage(&buf, original)
	require.NoError(t, err)

	// Parse the serialized output.
	reparsed, err := markdown.ParseBytes(buf.Bytes())
	require.NoError(t, err)

	// Compare frontmatter fields.
	assert.Equal(t, original.Frontmatter.ID, reparsed.Frontmatter.ID)
	assert.Equal(t, original.Frontmatter.Title, reparsed.Frontmatter.Title)
	assert.Equal(t, original.Frontmatter.EntityType, reparsed.Frontmatter.EntityType)
	assert.InDelta(t, original.Frontmatter.Confidence, reparsed.Frontmatter.Confidence, 1e-10)
	assert.Equal(t, original.Frontmatter.Tags, reparsed.Frontmatter.Tags)
	assert.Equal(t, original.Frontmatter.Relationships, reparsed.Frontmatter.Relationships)
	assert.Equal(t, original.Frontmatter.Temporal, reparsed.Frontmatter.Temporal)
	assert.Equal(t, original.Frontmatter.SemanticHints, reparsed.Frontmatter.SemanticHints)
	assert.Equal(t, original.Frontmatter.PossibleQuestions, reparsed.Frontmatter.PossibleQuestions)
}

func TestTemporalFutureValid(t *testing.T) {
	result, err := index.Load(context.Background(), "testdata/temporal")
	require.NoError(t, err)

	futurePage, ok := result.Index.Get("future-valid")
	require.True(t, ok)

	validFrom, err := time.Parse("2006-01-02", futurePage.Frontmatter.Temporal.ValidFrom)
	require.NoError(t, err)

	now := time.Now()
	assert.True(t, now.Before(validFrom),
		"future-valid's valid-from should be in the future")

	effective := temporal.EffectiveConfidence(
		futurePage.Frontmatter.Confidence,
		futurePage.Frontmatter.Temporal.DecayRate,
		time.Time{}, // no last-verified needed for this check
		validFrom,
		time.Time{}, // no valid-until
		now,
	)
	assert.Equal(t, float64(0), effective,
		"future-valid should have 0 effective confidence before valid-from")
}

func TestTemporalExpired(t *testing.T) {
	result, err := index.Load(context.Background(), "testdata/temporal")
	require.NoError(t, err)

	expiredPage, ok := result.Index.Get("expired")
	require.True(t, ok)

	validFrom, err := time.Parse("2006-01-02", expiredPage.Frontmatter.Temporal.ValidFrom)
	require.NoError(t, err)
	validUntil, err := time.Parse("2006-01-02", expiredPage.Frontmatter.Temporal.ValidUntil)
	require.NoError(t, err)
	lastVerified, err := time.Parse("2006-01-02", expiredPage.Frontmatter.Temporal.LastVerified)
	require.NoError(t, err)

	now := time.Now()
	assert.True(t, now.After(validUntil),
		"expired's valid-until should be in the past")

	effective := temporal.EffectiveConfidence(
		expiredPage.Frontmatter.Confidence,
		expiredPage.Frontmatter.Temporal.DecayRate,
		lastVerified,
		validFrom,
		validUntil,
		now,
	)
	assert.Equal(t, float64(0), effective,
		"expired doc should have 0 effective confidence after valid-until")
}
