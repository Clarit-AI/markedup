package enrich

import (
	"github.com/KHAEntertainment/markedup/schema"
)

// EnrichmentDelta describes what changed when enriching a page. It lets
// external callers (e.g. Plexium's MarkedUp plugin) capture the structured
// result of enrichment without having to diff the before/after frontmatter
// themselves.
//
// A field appears in the delta if it was empty/zero in the input frontmatter
// AND populated by the extracted fields. In Force mode, fields appear if the
// extracted value differed from the input value.
type EnrichmentDelta struct {
	// Scalar fields that were changed by the merge.
	IDChanged         bool
	TitleChanged      bool
	EntityTypeChanged bool
	ConfidenceChanged bool
	CreatedByChanged  bool

	// Collection fields. The slices hold only the newly-added items (not
	// the full post-merge list).
	TagsAdded          []string
	RelationshipsAdded []schema.Relationship
	SourcesAdded       []string

	// Changed reports whether any field in the delta is non-zero. Useful
	// for callers that want to skip writing unchanged pages.
	Changed bool
}

// EnrichPage runs Tier 1 deterministic enrichment against the given page
// (using filePath and rootDir for ID/tag derivation), merges the extracted
// fields into the page's existing frontmatter, and returns the enriched
// page plus a structured delta describing what changed.
//
// The input page is not mutated. The returned page is a copy with the
// merged frontmatter; its Body and SourcePath are preserved from the input.
// Nothing is written to disk — that is the caller's responsibility.
//
// This is the preferred entry point for external callers that want to
// capture enrichment results structurally (e.g. to populate a separate
// manifest or database) without the side effect of frontmatter being
// written back to the markdown file on disk.
func EnrichPage(page *schema.Page, filePath, rootDir string, opts MergeOptions) (*schema.Page, EnrichmentDelta) {
	extracted := ExtractFromDocument(filePath, page.Body, rootDir)
	merged := MergeFrontmatter(page.Frontmatter, extracted, opts)
	delta := computeDelta(page.Frontmatter, merged, extracted, opts)

	out := &schema.Page{
		Frontmatter: merged,
		Body:        page.Body,
		SourcePath:  page.SourcePath,
	}
	return out, delta
}

// EnrichPageWithModel is the Tier 2 variant: it merges a pre-computed
// ModelResult (from ModelExtractor.Extract and/or a generated summary) into
// the page's frontmatter and returns the enriched page plus delta.
//
// The input page is not mutated and nothing is written to disk.
func EnrichPageWithModel(page *schema.Page, model *ModelResult, summary string, opts MergeOptions) (*schema.Page, EnrichmentDelta) {
	merged := page.Frontmatter
	if model != nil {
		merged = MergeModelResult(merged, model, opts)
	}
	if summary != "" {
		merged = MergeSummary(merged, summary, opts)
	}
	delta := computeModelDelta(page.Frontmatter, merged)

	out := &schema.Page{
		Frontmatter: merged,
		Body:        page.Body,
		SourcePath:  page.SourcePath,
	}
	return out, delta
}

// computeDelta produces an EnrichmentDelta by comparing the pre-merge
// frontmatter against the post-merge frontmatter, using the extracted
// fields to identify which collection items are "new".
func computeDelta(before, after schema.GraphFrontmatter, extracted ExtractedFields, opts MergeOptions) EnrichmentDelta {
	d := EnrichmentDelta{}

	if before.ID != after.ID {
		d.IDChanged = true
	}
	if before.Title != after.Title {
		d.TitleChanged = true
	}
	if before.EntityType != after.EntityType {
		d.EntityTypeChanged = true
	}
	if before.Confidence != after.Confidence {
		d.ConfidenceChanged = true
	}
	if before.Provenance.CreatedBy != after.Provenance.CreatedBy {
		d.CreatedByChanged = true
	}

	d.TagsAdded = addedStrings(before.Tags, after.Tags)
	d.RelationshipsAdded = addedRelationships(before.Relationships, after.Relationships)
	d.SourcesAdded = addedStrings(before.Provenance.Sources, after.Provenance.Sources)

	// Suppress unused param warning; opts is accepted for future extension
	// (e.g. reporting overwritten-in-force-mode).
	_ = opts

	d.Changed = d.IDChanged || d.TitleChanged || d.EntityTypeChanged ||
		d.ConfidenceChanged || d.CreatedByChanged ||
		len(d.TagsAdded) > 0 || len(d.RelationshipsAdded) > 0 ||
		len(d.SourcesAdded) > 0
	return d
}

// computeModelDelta is the Tier 2 version — it additionally tracks entity
// and summary-field changes that model extraction can introduce.
func computeModelDelta(before, after schema.GraphFrontmatter) EnrichmentDelta {
	d := EnrichmentDelta{}

	if before.ID != after.ID {
		d.IDChanged = true
	}
	if before.Title != after.Title {
		d.TitleChanged = true
	}
	if before.EntityType != after.EntityType {
		d.EntityTypeChanged = true
	}
	if before.Confidence != after.Confidence {
		d.ConfidenceChanged = true
	}
	if before.Provenance.CreatedBy != after.Provenance.CreatedBy {
		d.CreatedByChanged = true
	}

	d.TagsAdded = addedStrings(before.Tags, after.Tags)
	d.RelationshipsAdded = addedRelationships(before.Relationships, after.Relationships)
	d.SourcesAdded = addedStrings(before.Provenance.Sources, after.Provenance.Sources)

	d.Changed = d.IDChanged || d.TitleChanged || d.EntityTypeChanged ||
		d.ConfidenceChanged || d.CreatedByChanged ||
		before.Summary != after.Summary ||
		len(d.TagsAdded) > 0 || len(d.RelationshipsAdded) > 0 ||
		len(d.SourcesAdded) > 0 ||
		!entitySlicesEqual(before.Entities, after.Entities) ||
		!stringSlicesEqual(before.SemanticHints, after.SemanticHints) ||
		!stringSlicesEqual(before.PossibleQuestions, after.PossibleQuestions)
	return d
}

// addedStrings returns elements in after that are not in before
// (case-insensitive match), preserving after's order.
func addedStrings(before, after []string) []string {
	if len(after) == 0 {
		return nil
	}
	beforeSet := make(map[string]struct{}, len(before))
	for _, s := range before {
		beforeSet[stringsFold(s)] = struct{}{}
	}
	var added []string
	for _, s := range after {
		if _, ok := beforeSet[stringsFold(s)]; !ok {
			added = append(added, s)
		}
	}
	return added
}

// addedRelationships returns relationships in after whose Target is not in
// before, preserving after's order.
func addedRelationships(before, after []schema.Relationship) []schema.Relationship {
	if len(after) == 0 {
		return nil
	}
	beforeSet := make(map[string]struct{}, len(before))
	for _, r := range before {
		beforeSet[r.Target] = struct{}{}
	}
	var added []schema.Relationship
	for _, r := range after {
		if _, ok := beforeSet[r.Target]; !ok {
			added = append(added, r)
		}
	}
	return added
}

// stringsFold is a minimal case-fold helper that matches the semantics used
// by unionStrings (lower-case equivalence).
func stringsFold(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

// stringSlicesEqual returns true iff a and b have the same elements in the
// same order.
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// entitySlicesEqual returns true iff a and b have the same entities in the
// same order (comparing Name, Aliases, and Role).
func entitySlicesEqual(a, b []schema.Entity) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Role != b[i].Role {
			return false
		}
		if !stringSlicesEqual(a[i].Aliases, b[i].Aliases) {
			return false
		}
	}
	return true
}
