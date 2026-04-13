package enrich

import (
	"testing"

	"github.com/KHAEntertainment/markedup/schema"
	"github.com/stretchr/testify/assert"
)

func TestMergeFrontmatter_EmptyExisting(t *testing.T) {
	existing := schema.GraphFrontmatter{}
	extracted := ExtractedFields{
		ID:         "test-doc",
		Title:      "Test Document",
		EntityType: "document",
		Confidence: 0.7,
		Tags:       []string{"go", "testing"},
		Relationships: []schema.Relationship{
			{Target: "other", Type: "related-to", Strength: 0.5},
		},
		Provenance: schema.Provenance{
			Sources:   []string{"https://example.com"},
			CreatedBy: "markedup-enrich-v1",
		},
	}

	result := MergeFrontmatter(existing, extracted, MergeOptions{})

	assert.Equal(t, "test-doc", result.ID)
	assert.Equal(t, "Test Document", result.Title)
	assert.Equal(t, "document", result.EntityType)
	assert.Equal(t, 0.7, result.Confidence)
	assert.Equal(t, []string{"go", "testing"}, result.Tags)
	assert.Len(t, result.Relationships, 1)
	assert.Equal(t, "https://example.com", result.Provenance.Sources[0])
	assert.Equal(t, "markedup-enrich-v1", result.Provenance.CreatedBy)
}

func TestMergeFrontmatter_PartialExisting(t *testing.T) {
	existing := schema.GraphFrontmatter{
		ID:         "my-id",
		Title:      "My Title",
		Tags:       []string{"existing-tag"},
		EntityType: "concept",
	}
	extracted := ExtractedFields{
		ID:         "extracted-id",
		Title:      "Extracted Title",
		EntityType: "document",
		Confidence: 0.7,
		Tags:       []string{"new-tag", "existing-tag"},
		Relationships: []schema.Relationship{
			{Target: "ref", Type: "related-to", Strength: 0.5},
		},
	}

	result := MergeFrontmatter(existing, extracted, MergeOptions{})

	// Scalars: existing values preserved
	assert.Equal(t, "my-id", result.ID)
	assert.Equal(t, "My Title", result.Title)
	assert.Equal(t, "concept", result.EntityType) // not overwritten
	// Confidence was 0, so filled
	assert.Equal(t, 0.7, result.Confidence)
	// Tags: union
	assert.Equal(t, []string{"existing-tag", "new-tag"}, result.Tags)
	// Relationships: appended
	assert.Len(t, result.Relationships, 1)
}

func TestMergeFrontmatter_CompleteExisting(t *testing.T) {
	existing := schema.GraphFrontmatter{
		ID:         "complete",
		Title:      "Complete Doc",
		EntityType: "concept",
		Confidence: 0.9,
		Tags:       []string{"tag1"},
		Relationships: []schema.Relationship{
			{Target: "existing-target", Type: "implements", Strength: 0.8},
		},
		Provenance: schema.Provenance{
			CreatedBy: "human",
		},
	}
	extracted := ExtractedFields{
		ID:         "overridden",
		Title:      "Overridden",
		EntityType: "document",
		Confidence: 0.7,
		Tags:       []string{"tag2"},
		Relationships: []schema.Relationship{
			{Target: "new-target", Type: "related-to", Strength: 0.5},
		},
		Provenance: schema.Provenance{
			CreatedBy: "markedup-enrich-v1",
		},
	}

	result := MergeFrontmatter(existing, extracted, MergeOptions{})

	// All scalars preserved
	assert.Equal(t, "complete", result.ID)
	assert.Equal(t, "Complete Doc", result.Title)
	assert.Equal(t, "concept", result.EntityType)
	assert.Equal(t, 0.9, result.Confidence)
	assert.Equal(t, "human", result.Provenance.CreatedBy)
	// Arrays: union
	assert.Equal(t, []string{"tag1", "tag2"}, result.Tags)
	assert.Len(t, result.Relationships, 2)
}

func TestMergeFrontmatter_Force(t *testing.T) {
	existing := schema.GraphFrontmatter{
		ID:         "old",
		Title:      "Old Title",
		EntityType: "concept",
		Confidence: 0.9,
		Tags:       []string{"old-tag"},
	}
	extracted := ExtractedFields{
		ID:         "new",
		Title:      "New Title",
		EntityType: "document",
		Confidence: 0.7,
		Tags:       []string{"new-tag"},
	}

	result := MergeFrontmatter(existing, extracted, MergeOptions{Force: true})

	assert.Equal(t, "new", result.ID)
	assert.Equal(t, "New Title", result.Title)
	assert.Equal(t, "document", result.EntityType)
	assert.Equal(t, 0.7, result.Confidence)
	assert.Equal(t, []string{"new-tag"}, result.Tags)
}

func TestMergeFrontmatter_ForceDoesNotBlank(t *testing.T) {
	existing := schema.GraphFrontmatter{
		ID:    "keep-this",
		Title: "Keep This",
	}
	extracted := ExtractedFields{} // all zero values

	result := MergeFrontmatter(existing, extracted, MergeOptions{Force: true})

	// Zero values should not overwrite existing
	assert.Equal(t, "keep-this", result.ID)
	assert.Equal(t, "Keep This", result.Title)
}

func TestMergeFrontmatter_Idempotent(t *testing.T) {
	existing := schema.GraphFrontmatter{
		Tags: []string{"existing"},
	}
	extracted := ExtractedFields{
		ID:         "doc",
		Title:      "Doc",
		EntityType: "document",
		Confidence: 0.7,
		Tags:       []string{"new-tag"},
	}

	first := MergeFrontmatter(existing, extracted, MergeOptions{})
	second := MergeFrontmatter(first, extracted, MergeOptions{})

	assert.Equal(t, first, second)
}

func TestMergeFrontmatter_TagUnionCaseInsensitive(t *testing.T) {
	existing := schema.GraphFrontmatter{
		Tags: []string{"Go"},
	}
	extracted := ExtractedFields{
		Tags: []string{"go", "python"},
	}

	result := MergeFrontmatter(existing, extracted, MergeOptions{})

	assert.Equal(t, []string{"Go", "python"}, result.Tags)
}

func TestMergeFrontmatter_RelationshipDedup(t *testing.T) {
	existing := schema.GraphFrontmatter{
		Relationships: []schema.Relationship{
			{Target: "target-a", Type: "implements", Strength: 0.8},
		},
	}
	extracted := ExtractedFields{
		Relationships: []schema.Relationship{
			{Target: "target-a", Type: "related-to", Strength: 0.5}, // same target, different type
			{Target: "target-b", Type: "related-to", Strength: 0.5},
		},
	}

	result := MergeFrontmatter(existing, extracted, MergeOptions{})

	// target-a should not be duplicated
	assert.Len(t, result.Relationships, 2)
	assert.Equal(t, "target-a", result.Relationships[0].Target)
	assert.Equal(t, "implements", result.Relationships[0].Type) // original preserved
	assert.Equal(t, "target-b", result.Relationships[1].Target)
}

func TestIsComplete(t *testing.T) {
	tests := []struct {
		name string
		fm   schema.GraphFrontmatter
		want bool
	}{
		{
			name: "complete",
			fm: schema.GraphFrontmatter{
				ID: "x", Title: "X", EntityType: "doc", Confidence: 0.7,
			},
			want: true,
		},
		{
			name: "missing id",
			fm:   schema.GraphFrontmatter{Title: "X", EntityType: "doc", Confidence: 0.7},
			want: false,
		},
		{
			name: "zero confidence",
			fm:   schema.GraphFrontmatter{ID: "x", Title: "X", EntityType: "doc"},
			want: false,
		},
		{
			name: "empty",
			fm:   schema.GraphFrontmatter{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsComplete(tt.fm))
		})
	}
}
