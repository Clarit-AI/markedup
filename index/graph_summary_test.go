package index

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadTestIndex(t *testing.T) *KnowledgeIndex {
	t.Helper()
	result, err := Load(context.Background(), "../testdata/valid")
	require.NoError(t, err)
	require.NotNil(t, result.Index)
	return result.Index
}

func TestCompactGraphSummary_Unfiltered(t *testing.T) {
	idx := loadTestIndex(t)
	summary := idx.CompactGraphSummary()

	assert.Equal(t, idx.Pages(), summary.Stats.Pages)
	assert.Greater(t, summary.Stats.Relationships, 0)
	assert.NotEmpty(t, summary.Stats.EntityTypes)
	assert.NotEmpty(t, summary.Stats.Tags)

	// All page IDs should be present.
	ids := make(map[string]bool)
	for _, n := range summary.Pages {
		ids[n.ID] = true
	}
	for _, p := range idx.All() {
		assert.True(t, ids[p.Frontmatter.ID], "missing page ID: %s", p.Frontmatter.ID)
	}

	// Compact nodes should NOT contain body, entities, or provenance — these
	// are structural guarantees of the SummaryNode type (no such fields exist).
	// We verify relationships are included by default.
	for _, n := range summary.Pages {
		if n.ID == "alice" {
			assert.NotEmpty(t, n.Relationships, "alice should have relationships by default")
		}
	}

	// Temporal fields should be empty by default.
	for _, n := range summary.Pages {
		assert.Empty(t, n.ValidFrom, "temporal should be excluded by default for %s", n.ID)
		assert.Empty(t, n.ValidUntil, "temporal should be excluded by default for %s", n.ID)
		assert.Empty(t, n.LastVerified, "temporal should be excluded by default for %s", n.ID)
	}
}

func TestCompactGraphSummary_EntityTypeFilter(t *testing.T) {
	idx := loadTestIndex(t)
	summary := idx.CompactGraphSummary(WithEntityTypeFilter("person"))

	for _, n := range summary.Pages {
		assert.Equal(t, "person", n.EntityType, "all pages should be person type")
	}
	// testdata/valid has alice and bob as person type.
	assert.Equal(t, 2, summary.Stats.Pages)
}

func TestCompactGraphSummary_TagFilter(t *testing.T) {
	idx := loadTestIndex(t)
	summary := idx.CompactGraphSummary(WithTagFilter("ai-researcher"))

	assert.Greater(t, summary.Stats.Pages, 0)
	for _, n := range summary.Pages {
		assert.Contains(t, n.Tags, "ai-researcher")
	}
	// Only alice has "ai-researcher" tag.
	assert.Equal(t, 1, summary.Stats.Pages)
	assert.Equal(t, "alice", summary.Pages[0].ID)
}

func TestCompactGraphSummary_NoRelationships(t *testing.T) {
	idx := loadTestIndex(t)
	summary := idx.CompactGraphSummary(WithRelationships(false))

	for _, n := range summary.Pages {
		assert.Empty(t, n.Relationships, "relationships should be nil/empty for %s", n.ID)
	}
}

func TestCompactGraphSummary_WithTemporal(t *testing.T) {
	idx := loadTestIndex(t)
	summary := idx.CompactGraphSummary(WithTemporal(true))

	// event-launch and stale-doc have temporal metadata.
	found := false
	for _, n := range summary.Pages {
		if n.ID == "event-launch" {
			assert.Equal(t, "2024-03-15", n.ValidFrom)
			assert.Equal(t, "2024-03-17", n.ValidUntil)
			assert.Equal(t, "2024-03-17", n.LastVerified)
			found = true
		}
	}
	assert.True(t, found, "event-launch should be in the summary")

	// alice has valid-from and last-verified but no valid-until.
	for _, n := range summary.Pages {
		if n.ID == "alice" {
			assert.Equal(t, "2023-01-01", n.ValidFrom)
			assert.Empty(t, n.ValidUntil)
			assert.Equal(t, "2026-04-01", n.LastVerified)
		}
	}
}

func TestCompactGraphSummary_MaxPages(t *testing.T) {
	idx := loadTestIndex(t)
	summary := idx.CompactGraphSummary(WithMaxPages(2))

	assert.Equal(t, 2, summary.Stats.Pages)
	assert.Len(t, summary.Pages, 2)
}

func TestCompactGraphSummary_CombinedFilters(t *testing.T) {
	idx := loadTestIndex(t)

	// Entity type + tag filter.
	summary := idx.CompactGraphSummary(
		WithEntityTypeFilter("person"),
		WithTagFilter("engineer"),
	)
	// Both alice and bob are person + engineer.
	assert.Equal(t, 2, summary.Stats.Pages)

	// Entity type + tag + max pages.
	summary = idx.CompactGraphSummary(
		WithEntityTypeFilter("person"),
		WithTagFilter("engineer"),
		WithMaxPages(1),
	)
	assert.Equal(t, 1, summary.Stats.Pages)
}

func TestCompactGraphSummary_EmptyIndex(t *testing.T) {
	idx := buildIndex(nil)
	summary := idx.CompactGraphSummary()

	assert.Equal(t, 0, summary.Stats.Pages)
	assert.Equal(t, 0, summary.Stats.Relationships)
	assert.Empty(t, summary.Stats.EntityTypes)
	assert.Empty(t, summary.Stats.Tags)
	assert.Empty(t, summary.Pages)
}

func TestCompactGraphSummary_NoMatchFilter(t *testing.T) {
	idx := loadTestIndex(t)
	summary := idx.CompactGraphSummary(WithEntityTypeFilter("nonexistent"))

	assert.Equal(t, 0, summary.Stats.Pages)
	assert.Empty(t, summary.Pages)
}

func TestCompactGraphSummary_StatsEntityTypes(t *testing.T) {
	idx := loadTestIndex(t)
	summary := idx.CompactGraphSummary()

	// testdata/valid has: person, concept, project, event.
	assert.Contains(t, summary.Stats.EntityTypes, "person")
	assert.Contains(t, summary.Stats.EntityTypes, "concept")
	assert.Contains(t, summary.Stats.EntityTypes, "project")
	assert.Contains(t, summary.Stats.EntityTypes, "event")
}
