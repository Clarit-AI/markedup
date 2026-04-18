package enrich

import (
	"reflect"
	"testing"

	"github.com/KHAEntertainment/markedup/schema"
)

func TestEnrichPage_EmptyFrontmatterGetsPopulated(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{},
		Body:        "# Hello World\n\nSome text with #example and [[other-page]].",
		SourcePath:  "/tmp/root/notes/hello.md",
	}

	enriched, delta := EnrichPage(page, "/tmp/root/notes/hello.md", "/tmp/root", MergeOptions{})

	if !delta.Changed {
		t.Fatal("expected delta.Changed=true for empty frontmatter")
	}
	if !delta.IDChanged {
		t.Error("expected IDChanged")
	}
	if !delta.TitleChanged {
		t.Error("expected TitleChanged")
	}
	if !delta.EntityTypeChanged {
		t.Error("expected EntityTypeChanged")
	}
	if !delta.ConfidenceChanged {
		t.Error("expected ConfidenceChanged")
	}
	if enriched.Frontmatter.Title != "Hello World" {
		t.Errorf("expected Title=Hello World, got %q", enriched.Frontmatter.Title)
	}
	if enriched.Frontmatter.ID != "hello" {
		t.Errorf("expected ID=hello, got %q", enriched.Frontmatter.ID)
	}
	if len(delta.TagsAdded) == 0 {
		t.Error("expected TagsAdded to include extracted tags")
	}
	if len(delta.TagsRemoved) != 0 {
		t.Errorf("expected no tags removed on empty -> populated, got %v", delta.TagsRemoved)
	}
	if len(delta.RelationshipsAdded) == 0 {
		t.Error("expected RelationshipsAdded from wikilink")
	}
}

func TestEnrichPage_AlreadyCompleteIsUnchanged(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:         "hello",
			Title:      "Hello World",
			EntityType: "document",
			Confidence: 0.9,
			Tags:       []string{"example"},
			Relationships: []schema.Relationship{
				{Target: "other-page", Type: "related-to", Strength: 0.5},
			},
			Provenance: schema.Provenance{CreatedBy: "manual"},
		},
		Body:       "# Hello World\n\nSome text with #example and [[other-page]].",
		SourcePath: "/tmp/root/notes/hello.md",
	}

	_, delta := EnrichPage(page, "/tmp/root/notes/hello.md", "/tmp/root", MergeOptions{})

	if delta.IDChanged {
		t.Error("ID should not change")
	}
	if delta.TitleChanged {
		t.Error("Title should not change")
	}
	if delta.ConfidenceChanged {
		t.Error("Confidence should not change in non-force mode")
	}
	if len(delta.RelationshipsAdded) != 0 {
		t.Errorf("expected no added relationships, got %+v", delta.RelationshipsAdded)
	}
	if len(delta.RelationshipsModified) != 0 {
		t.Errorf("expected no modified relationships, got %+v", delta.RelationshipsModified)
	}
}

// Force-mode replacement is the regression captured by Codex review point #1.
// Overwriting an existing tag set / relationship with different values MUST
// set delta.Changed=true, even though Tier 1 extraction would normally
// produce a superset.
func TestEnrichPage_ForceModeReplacementIsDetected(t *testing.T) {
	// Pre-populated page with tags and relationships that will NOT appear
	// in the body — so Tier 1 extraction produces a completely different
	// set, and force mode replaces the originals.
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:         "hello",
			Title:      "Hello",
			EntityType: "concept",
			Confidence: 0.9,
			Tags:       []string{"stale-tag", "obsolete"},
			Relationships: []schema.Relationship{
				{Target: "removed-page", Type: "related-to", Strength: 0.5},
			},
		},
		Body:       "# Hello\n\n#brandnew and [[new-target]]",
		SourcePath: "/tmp/root/hello.md",
	}

	_, delta := EnrichPage(page, "/tmp/root/hello.md", "/tmp/root", MergeOptions{Force: true})

	if !delta.Changed {
		t.Fatal("expected delta.Changed=true after force-mode replacement")
	}
	// Old tags should appear as Removed; new tags as Added.
	if len(delta.TagsRemoved) == 0 {
		t.Errorf("expected TagsRemoved to include stale-tag/obsolete, got %v", delta.TagsRemoved)
	}
	if len(delta.TagsAdded) == 0 {
		t.Errorf("expected TagsAdded to include brandnew, got %v", delta.TagsAdded)
	}
	if len(delta.RelationshipsRemoved) == 0 {
		t.Errorf("expected RelationshipsRemoved to include removed-page, got %v", delta.RelationshipsRemoved)
	}
	if len(delta.RelationshipsAdded) == 0 {
		t.Errorf("expected RelationshipsAdded to include new-target, got %v", delta.RelationshipsAdded)
	}
}

// Same Target, different Type/Strength should show up as Modified — the
// case that would otherwise slip past an "added only" delta.
func TestEnrichPage_RelationshipBodyChangeIsDetected(t *testing.T) {
	before := schema.GraphFrontmatter{
		ID:         "x",
		Title:      "X",
		EntityType: "concept",
		Confidence: 0.9,
		Relationships: []schema.Relationship{
			{Target: "y", Type: "old-predicate", Strength: 0.1},
		},
	}
	after := schema.GraphFrontmatter{
		ID:         "x",
		Title:      "X",
		EntityType: "concept",
		Confidence: 0.9,
		Relationships: []schema.Relationship{
			{Target: "y", Type: "new-predicate", Strength: 0.9},
		},
	}

	delta := computeDelta(before, after)

	if !delta.Changed {
		t.Fatal("expected delta.Changed=true when relationship body differs")
	}
	if len(delta.RelationshipsModified) != 1 {
		t.Fatalf("expected 1 modified relationship, got %d: %+v",
			len(delta.RelationshipsModified), delta.RelationshipsModified)
	}
	if delta.RelationshipsModified[0].Type != "new-predicate" {
		t.Errorf("expected modified rel to carry post-merge Type, got %q",
			delta.RelationshipsModified[0].Type)
	}
	if len(delta.RelationshipsAdded) != 0 || len(delta.RelationshipsRemoved) != 0 {
		t.Errorf("expected no add/remove for same-target change; got add=%v remove=%v",
			delta.RelationshipsAdded, delta.RelationshipsRemoved)
	}
}

// Entity body-only replacement (same Name, different Role/Aliases) must be
// detected via EntitiesModified. This mirrors the relationship body-change
// case and is the class of bug that slipped past EntitiesAdded/Removed alone.
func TestEnrichPage_EntityBodyChangeIsDetected(t *testing.T) {
	before := schema.GraphFrontmatter{
		ID:         "x",
		Title:      "X",
		EntityType: "document",
		Confidence: 0.9,
		Entities: []schema.Entity{
			{Name: "Alice", Role: "person", Aliases: []string{"A"}},
		},
	}
	after := schema.GraphFrontmatter{
		ID:         "x",
		Title:      "X",
		EntityType: "document",
		Confidence: 0.9,
		Entities: []schema.Entity{
			{Name: "Alice", Role: "author", Aliases: []string{"Al"}},
		},
	}

	delta := computeDelta(before, after)

	if !delta.Changed {
		t.Fatal("expected delta.Changed=true when entity body differs")
	}
	if len(delta.EntitiesModified) != 1 {
		t.Fatalf("expected 1 modified entity, got %d: %+v",
			len(delta.EntitiesModified), delta.EntitiesModified)
	}
	if delta.EntitiesModified[0].Role != "author" {
		t.Errorf("expected modified entity to carry post-merge Role, got %q",
			delta.EntitiesModified[0].Role)
	}
	if len(delta.EntitiesAdded) != 0 || len(delta.EntitiesRemoved) != 0 {
		t.Errorf("expected no add/remove for same-name change; got add=%v remove=%v",
			delta.EntitiesAdded, delta.EntitiesRemoved)
	}
}

// Deep-copy semantics: mutating the returned frontmatter must not affect
// the input. Addresses Codex review Must-Fix #2.
func TestEnrichPage_ReturnedPageIsDeepCopy(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			Tags:          []string{"original"},
			SemanticHints: []string{"hint1"},
			Relationships: []schema.Relationship{{Target: "x", Type: "r", Strength: 1}},
			Provenance:    schema.Provenance{Sources: []string{"http://a"}},
			Entities:      []schema.Entity{{Name: "Alice", Aliases: []string{"A"}}},
		},
		Body:       "# Title",
		SourcePath: "/tmp/root/a.md",
	}

	inputSnapshot := schema.GraphFrontmatter{
		Tags:          append([]string(nil), page.Frontmatter.Tags...),
		SemanticHints: append([]string(nil), page.Frontmatter.SemanticHints...),
		Relationships: append([]schema.Relationship(nil), page.Frontmatter.Relationships...),
		Provenance:    schema.Provenance{Sources: append([]string(nil), page.Frontmatter.Provenance.Sources...)},
		Entities: []schema.Entity{{
			Name:    "Alice",
			Aliases: append([]string(nil), page.Frontmatter.Entities[0].Aliases...),
		}},
	}

	enriched, _ := EnrichPage(page, "/tmp/root/a.md", "/tmp/root", MergeOptions{})

	// Mutate every slice on the returned frontmatter.
	if len(enriched.Frontmatter.Tags) > 0 {
		enriched.Frontmatter.Tags[0] = "MUTATED"
	}
	if len(enriched.Frontmatter.SemanticHints) > 0 {
		enriched.Frontmatter.SemanticHints[0] = "MUTATED"
	}
	if len(enriched.Frontmatter.Relationships) > 0 {
		enriched.Frontmatter.Relationships[0].Type = "MUTATED"
	}
	if len(enriched.Frontmatter.Provenance.Sources) > 0 {
		enriched.Frontmatter.Provenance.Sources[0] = "MUTATED"
	}
	if len(enriched.Frontmatter.Entities) > 0 && len(enriched.Frontmatter.Entities[0].Aliases) > 0 {
		enriched.Frontmatter.Entities[0].Aliases[0] = "MUTATED"
		enriched.Frontmatter.Entities[0].Name = "MUTATED"
	}

	// Input slice contents must be unchanged.
	if !reflect.DeepEqual(page.Frontmatter.Tags, inputSnapshot.Tags) {
		t.Errorf("Tags mutated: got %v, want %v", page.Frontmatter.Tags, inputSnapshot.Tags)
	}
	if !reflect.DeepEqual(page.Frontmatter.SemanticHints, inputSnapshot.SemanticHints) {
		t.Errorf("SemanticHints mutated: got %v, want %v", page.Frontmatter.SemanticHints, inputSnapshot.SemanticHints)
	}
	if !reflect.DeepEqual(page.Frontmatter.Relationships, inputSnapshot.Relationships) {
		t.Errorf("Relationships mutated: got %+v, want %+v", page.Frontmatter.Relationships, inputSnapshot.Relationships)
	}
	if !reflect.DeepEqual(page.Frontmatter.Provenance.Sources, inputSnapshot.Provenance.Sources) {
		t.Errorf("Sources mutated: got %v, want %v", page.Frontmatter.Provenance.Sources, inputSnapshot.Provenance.Sources)
	}
	if page.Frontmatter.Entities[0].Name != "Alice" {
		t.Errorf("Entity.Name mutated: got %q", page.Frontmatter.Entities[0].Name)
	}
	if !reflect.DeepEqual(page.Frontmatter.Entities[0].Aliases, inputSnapshot.Entities[0].Aliases) {
		t.Errorf("Entity.Aliases mutated: got %v, want %v",
			page.Frontmatter.Entities[0].Aliases, inputSnapshot.Entities[0].Aliases)
	}
}

func TestEnrichPage_PreservesBodyAndSourcePath(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{},
		Body:        "body content",
		SourcePath:  "/some/path.md",
	}

	enriched, _ := EnrichPage(page, "/some/path.md", "/some", MergeOptions{})

	if enriched.Body != page.Body {
		t.Errorf("Body was not preserved: got %q", enriched.Body)
	}
	if enriched.SourcePath != page.SourcePath {
		t.Errorf("SourcePath was not preserved: got %q", enriched.SourcePath)
	}
}

func TestEnrichPageWithModel_PopulatesEntitiesAndSummary(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:    "existing",
			Title: "Existing Title",
		},
		Body:       "body",
		SourcePath: "/p.md",
	}

	model := &ModelResult{
		Entities: []schema.Entity{
			{Name: "Alice", Role: "author"},
		},
		SemanticHints:     []string{"who wrote this"},
		PossibleQuestions: []string{"who is Alice?"},
		EntityType:        "person",
	}

	enriched, delta := EnrichPageWithModel(page, model, "A short summary.", MergeOptions{})

	if !delta.Changed {
		t.Fatal("expected delta.Changed=true")
	}
	if !delta.SummaryChanged {
		t.Error("expected SummaryChanged=true")
	}
	if len(delta.EntitiesAdded) != 1 || delta.EntitiesAdded[0].Name != "Alice" {
		t.Errorf("expected EntitiesAdded=[Alice], got %+v", delta.EntitiesAdded)
	}
	if len(delta.SemanticHintsAdded) != 1 {
		t.Errorf("expected SemanticHintsAdded=1, got %v", delta.SemanticHintsAdded)
	}
	if len(delta.QuestionsAdded) != 1 {
		t.Errorf("expected QuestionsAdded=1, got %v", delta.QuestionsAdded)
	}
	if enriched.Frontmatter.Summary != "A short summary." {
		t.Errorf("expected summary populated, got %q", enriched.Frontmatter.Summary)
	}
	if enriched.Frontmatter.EntityType != "person" {
		t.Errorf("expected EntityType=person, got %q", enriched.Frontmatter.EntityType)
	}
}

func TestEnrichPageWithModel_NilModelOnlyAppliesSummary(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{ID: "x", Title: "X"},
		Body:        "",
		SourcePath:  "/x.md",
	}

	enriched, delta := EnrichPageWithModel(page, nil, "summary only", MergeOptions{})

	if !delta.Changed {
		t.Fatal("expected delta.Changed=true")
	}
	if !delta.SummaryChanged {
		t.Error("expected SummaryChanged=true")
	}
	if enriched.Frontmatter.Summary != "summary only" {
		t.Errorf("expected summary populated")
	}
	if len(delta.EntitiesAdded) != 0 {
		t.Errorf("expected no entities when model is nil")
	}
}

// Regression: ensure the delta helpers use strings.ToLower (matching
// unionStrings) rather than ASCII-only folding.
func TestDiffStringsCaseInsensitive_MatchesUnionSemantics(t *testing.T) {
	// "Ångström" vs "ångström" — Unicode differs but should be treated as
	// equal under strings.ToLower.
	added, removed := diffStringsCaseInsensitive(
		[]string{"Ångström"},
		[]string{"ångström"},
	)
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("expected case-insensitive match under Unicode; got added=%v removed=%v",
			added, removed)
	}
}
