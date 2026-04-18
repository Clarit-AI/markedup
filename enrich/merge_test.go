package enrich

import (
	"testing"

	"github.com/Clarit-AI/markedup/schema"
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
			// Same target as existing but a different type: kept as a
			// distinct edge. Dedup key is (Target, Type), not Target alone.
			{Target: "target-a", Type: "related-to", Strength: 0.5},
			// Exact duplicate of the existing edge: collapsed.
			{Target: "target-a", Type: "implements", Strength: 0.8},
			{Target: "target-b", Type: "related-to", Strength: 0.5},
		},
	}

	result := MergeFrontmatter(existing, extracted, MergeOptions{})

	// (target-a, implements) dedupes against existing; (target-a, related-to)
	// is a new edge; (target-b, related-to) is new.
	assert.Len(t, result.Relationships, 3)
	assert.Equal(t, "target-a", result.Relationships[0].Target)
	assert.Equal(t, "implements", result.Relationships[0].Type) // original preserved
	assert.Equal(t, "target-a", result.Relationships[1].Target)
	assert.Equal(t, "related-to", result.Relationships[1].Type)
	assert.Equal(t, "target-b", result.Relationships[2].Target)
}

func TestMergeSummary_EmptyExisting(t *testing.T) {
	existing := schema.GraphFrontmatter{ID: "test"}
	result := MergeSummary(existing, "A test entity", MergeOptions{})
	assert.Equal(t, "A test entity", result.Summary)
}

func TestMergeSummary_ExistingPreserved(t *testing.T) {
	existing := schema.GraphFrontmatter{
		ID:      "test",
		Summary: "Original summary",
	}
	result := MergeSummary(existing, "New summary", MergeOptions{})
	assert.Equal(t, "Original summary", result.Summary)
}

func TestMergeSummary_ForceOverwrite(t *testing.T) {
	existing := schema.GraphFrontmatter{
		ID:      "test",
		Summary: "Original summary",
	}
	result := MergeSummary(existing, "New summary", MergeOptions{Force: true})
	assert.Equal(t, "New summary", result.Summary)
}

func TestMergeSummary_EmptySummaryNoOp(t *testing.T) {
	existing := schema.GraphFrontmatter{
		ID:      "test",
		Summary: "Keep this",
	}
	result := MergeSummary(existing, "", MergeOptions{Force: true})
	assert.Equal(t, "Keep this", result.Summary)
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

// TestMergeFrontmatter_ForceDedupesInput guards the force-mode path against
// a model (or deterministic extractor) returning within-list duplicates of
// the same (Target, Type) edge — the observable symptom behind issue #108.
func TestMergeFrontmatter_ForceDedupesInput(t *testing.T) {
	existing := schema.GraphFrontmatter{
		ID:         "prev",
		Title:      "Prev",
		EntityType: "concept",
		Confidence: 0.9,
		Relationships: []schema.Relationship{
			{Target: "old", Type: "related-to", Strength: 0.5},
		},
	}
	extracted := ExtractedFields{
		Relationships: []schema.Relationship{
			{Target: "foo", Type: "related-to", Strength: 0.5},
			{Target: "foo", Type: "related-to", Strength: 0.9}, // exact-key dup
			{Target: "bar", Type: "implements", Strength: 0.7},
		},
		Tags:       []string{"a", "A", "b"}, // case-insensitive dup
		Provenance: schema.Provenance{Sources: []string{"s1", "s1"}},
	}

	result := MergeFrontmatter(existing, extracted, MergeOptions{Force: true})

	assert.Len(t, result.Relationships, 2, "duplicate (foo, related-to) must collapse")
	assert.Equal(t, "foo", result.Relationships[0].Target)
	assert.Equal(t, 0.5, result.Relationships[0].Strength, "first-seen wins on exact dup")
	assert.Equal(t, "bar", result.Relationships[1].Target)
	assert.Equal(t, []string{"a", "b"}, result.Tags)
	assert.Equal(t, []string{"s1"}, result.Provenance.Sources)
}

// TestMergeFrontmatter_ForceDedupesEntities guards AC #4 from issue #108:
// entities carried through existing frontmatter into a force-mode merge must
// be deduped by lowercased Name. Mirrors TestMergeFrontmatter_ForceDedupesInput
// on the entities side — ExtractedFields has no Entities field, so the stale
// duplicates come in via existing.
func TestMergeFrontmatter_ForceDedupesEntities(t *testing.T) {
	existing := schema.GraphFrontmatter{
		ID:         "prev",
		Title:      "Prev",
		EntityType: "concept",
		Confidence: 0.9,
		Entities: []schema.Entity{
			{Name: "Alice", Role: "person"},
			{Name: "alice", Role: "person"}, // case-insensitive dup
			{Name: "Bob", Role: "person"},
			{Name: "Bob", Role: "person"}, // exact dup
		},
	}
	extracted := ExtractedFields{
		ID:    "new",
		Title: "New",
	}

	result := MergeFrontmatter(existing, extracted, MergeOptions{Force: true})

	assert.Len(t, result.Entities, 2, "case-insensitive and exact entity dupes must collapse")
	assert.Equal(t, "Alice", result.Entities[0].Name, "first-seen wins")
	assert.Equal(t, "Bob", result.Entities[1].Name)
}

// TestMergeFrontmatter_DefaultDedupesEntities covers the non-force path: if
// existing frontmatter already carries entity duplicates (e.g. from a prior
// buggy write), a default merge must clean them up rather than preserve them.
func TestMergeFrontmatter_DefaultDedupesEntities(t *testing.T) {
	existing := schema.GraphFrontmatter{
		Entities: []schema.Entity{
			{Name: "Alice"},
			{Name: "ALICE"},
			{Name: "Bob"},
		},
	}
	extracted := ExtractedFields{ID: "p", Title: "P"}

	result := MergeFrontmatter(existing, extracted, MergeOptions{})

	assert.Len(t, result.Entities, 2)
	assert.Equal(t, "Alice", result.Entities[0].Name)
	assert.Equal(t, "Bob", result.Entities[1].Name)
}

// TestMergeFrontmatter_IdempotentAcrossModes asserts the issue #108
// single-block invariant: repeated merges of the same extracted payload
// never grow the relationships / entities / tags lists, in either default
// or force mode.
func TestMergeFrontmatter_IdempotentAcrossModes(t *testing.T) {
	extracted := ExtractedFields{
		ID:         "p",
		Title:      "P",
		EntityType: "concept",
		Confidence: 0.9,
		Tags:       []string{"go"},
		Relationships: []schema.Relationship{
			{Target: "foo", Type: "related-to", Strength: 0.5},
			{Target: "bar", Type: "implements", Strength: 0.7},
		},
	}

	// Two passes in default (non-force) mode.
	r1 := MergeFrontmatter(schema.GraphFrontmatter{}, extracted, MergeOptions{})
	r2 := MergeFrontmatter(r1, extracted, MergeOptions{})
	assert.Equal(t, r1.Relationships, r2.Relationships, "non-force merge must be idempotent")
	assert.Equal(t, r1.Tags, r2.Tags)

	// Two passes in force mode, input seeded with within-list dups.
	dupExtracted := extracted
	dupExtracted.Relationships = append(dupExtracted.Relationships,
		schema.Relationship{Target: "foo", Type: "related-to", Strength: 0.5})
	f1 := MergeFrontmatter(schema.GraphFrontmatter{}, dupExtracted, MergeOptions{Force: true})
	f2 := MergeFrontmatter(f1, dupExtracted, MergeOptions{Force: true})
	assert.Len(t, f1.Relationships, 2)
	assert.Equal(t, f1.Relationships, f2.Relationships, "force merge must be idempotent after dedup")
}

// TestMergeModelResult_ForceDedupesInput mirrors the above guard on the
// Tier 2 MergeModelResult force branch, which was the other path that could
// write within-list duplicates into frontmatter under issue #108.
func TestMergeModelResult_ForceDedupesInput(t *testing.T) {
	existing := schema.GraphFrontmatter{}
	model := &ModelResult{
		Entities: []schema.Entity{
			{Name: "Alice", Role: "person"},
			{Name: "alice", Role: "person"}, // case-insensitive dup
			{Name: "Bob", Role: "person"},
		},
		Relationships: []schema.Relationship{
			{Target: "x", Type: "related-to", Strength: 0.5},
			{Target: "x", Type: "related-to", Strength: 0.5}, // exact dup
		},
		SemanticHints:     []string{"h1", "h1"},
		PossibleQuestions: []string{"q1", "Q1"},
	}

	result := MergeModelResult(existing, model, MergeOptions{Force: true})

	assert.Len(t, result.Entities, 2)
	assert.Equal(t, "Alice", result.Entities[0].Name)
	assert.Equal(t, "Bob", result.Entities[1].Name)
	// Tier 2 relationships route into SemanticRelationships (issue #109).
	assert.Empty(t, result.Relationships, "Tier 2 must not write to Relationships")
	assert.Len(t, result.SemanticRelationships, 1)
	assert.Len(t, result.SemanticHints, 1)
	assert.Len(t, result.PossibleQuestions, 1)
}

// TestMergeFrontmatter_Tier1_RoutesToRelationships guards the Tier 1 path: wikilink-
// derived ExtractedFields.Relationships must remain in Relationships and must
// NOT bleed into SemanticRelationships. Issue #109.
func TestMergeFrontmatter_Tier1_RoutesToRelationships(t *testing.T) {
	existing := schema.GraphFrontmatter{}
	extracted := ExtractedFields{
		Relationships: []schema.Relationship{
			{Target: "other-doc", Type: "related-to", Strength: 0.5},
		},
	}

	// Both default and force branches must route identically.
	for _, force := range []bool{false, true} {
		result := MergeFrontmatter(existing, extracted, MergeOptions{Force: force})
		assert.Len(t, result.Relationships, 1, "force=%v", force)
		assert.Equal(t, "other-doc", result.Relationships[0].Target)
		assert.Empty(t, result.SemanticRelationships, "Tier 1 must not populate SemanticRelationships; force=%v", force)
	}
}

// TestMergeModelResult_Tier2_PreservesExistingRelationships ensures Tier 2
// running after Tier 1 does NOT clobber wikilink Relationships. Issue #109.
func TestMergeModelResult_Tier2_PreservesExistingRelationships(t *testing.T) {
	existing := schema.GraphFrontmatter{
		Relationships: []schema.Relationship{
			{Target: "doc-a", Type: "related-to", Strength: 0.5},
		},
	}
	model := &ModelResult{
		Relationships: []schema.Relationship{
			{Target: "wikilinks", Type: "mentions", Strength: 0.8},
		},
	}

	for _, force := range []bool{false, true} {
		result := MergeModelResult(existing, model, MergeOptions{Force: force})
		assert.Len(t, result.Relationships, 1, "wikilink edge preserved; force=%v", force)
		assert.Equal(t, "doc-a", result.Relationships[0].Target)
		assert.Len(t, result.SemanticRelationships, 1, "NER edge routed; force=%v", force)
		assert.Equal(t, "wikilinks", result.SemanticRelationships[0].Target)
	}
}

// TestMergeModelResult_SemanticRelationships_Idempotent ensures running Tier 2
// merge twice produces stable counts on SemanticRelationships (mirror of the
// #108 dedup invariant, but on the new field). Issue #109.
func TestMergeModelResult_SemanticRelationships_Idempotent(t *testing.T) {
	existing := schema.GraphFrontmatter{}
	model := &ModelResult{
		Relationships: []schema.Relationship{
			{Target: "alice", Type: "mentions", Strength: 0.8},
			{Target: "bob", Type: "mentions", Strength: 0.8},
		},
	}

	for _, force := range []bool{false, true} {
		pass1 := MergeModelResult(existing, model, MergeOptions{Force: force})
		pass2 := MergeModelResult(pass1, model, MergeOptions{Force: force})
		assert.Len(t, pass1.SemanticRelationships, 2, "force=%v", force)
		assert.Len(t, pass2.SemanticRelationships, 2, "idempotent; force=%v", force)
	}
}
