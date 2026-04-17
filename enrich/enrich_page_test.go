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
	if len(delta.RelationshipsAdded) == 0 {
		t.Error("expected RelationshipsAdded from wikilink")
	}
}

func TestEnrichPage_AlreadyCompleteIsUnchanged(t *testing.T) {
	// Pre-populated frontmatter with matching content — Tier 1 extraction
	// will find the same tags/relationships (already present), so delta
	// should have no additions.
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
}

func TestEnrichPage_DoesNotMutateInput(t *testing.T) {
	original := schema.GraphFrontmatter{
		Tags: []string{"original"},
	}
	page := &schema.Page{
		Frontmatter: original,
		Body:        "# Title\n\n#newtag",
		SourcePath:  "/tmp/root/a.md",
	}

	EnrichPage(page, "/tmp/root/a.md", "/tmp/root", MergeOptions{})

	if !reflect.DeepEqual(page.Frontmatter.Tags, []string{"original"}) {
		t.Errorf("input frontmatter was mutated: %+v", page.Frontmatter.Tags)
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
	if enriched.Frontmatter.Summary != "A short summary." {
		t.Errorf("expected summary populated, got %q", enriched.Frontmatter.Summary)
	}
	if len(enriched.Frontmatter.Entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(enriched.Frontmatter.Entities))
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
	if enriched.Frontmatter.Summary != "summary only" {
		t.Errorf("expected summary populated")
	}
	if len(enriched.Frontmatter.Entities) != 0 {
		t.Errorf("expected no entities when model is nil")
	}
}
