package enrich

import (
	"strings"

	"github.com/KHAEntertainment/markedup/schema"
)

// EnrichmentDelta describes what changed when enriching a page. It lets
// external callers (e.g. Plexium's MarkedUp plugin) capture the structured
// result of enrichment without having to diff the before/after frontmatter
// themselves.
//
// Semantics:
//   - Scalar *Changed fields are true iff the post-merge value differs from
//     the pre-merge value. This captures both default-mode fills and
//     force-mode overwrites uniformly.
//   - *Added slices hold items present after but absent before (matched
//     case-insensitively for string fields, by Target for relationships, by
//     Name for entities).
//   - *Removed slices hold items present before but absent after. These are
//     only populated in force mode (MergeOptions.Force=true); default mode
//     is union-only and cannot drop items.
//   - RelationshipsModified holds the post-merge value of relationships whose
//     Target is present both before and after but whose Type or Strength
//     differs. This catches force-mode per-target replacements that neither
//     Added nor Removed would surface.
//   - Changed is true iff any of the above is non-zero — the recommended
//     single-source signal for "should I persist this page?"
type EnrichmentDelta struct {
	// Scalar changes
	IDChanged         bool
	TitleChanged      bool
	EntityTypeChanged bool
	ConfidenceChanged bool
	CreatedByChanged  bool
	SummaryChanged    bool

	// Collection additions/removals. Added/Removed are symmetric; Modified
	// captures body changes where the identity key (Target for
	// relationships, Name for entities) is present on both sides.
	TagsAdded             []string
	TagsRemoved           []string
	RelationshipsAdded    []schema.Relationship
	RelationshipsRemoved  []schema.Relationship
	RelationshipsModified []schema.Relationship
	SourcesAdded          []string
	SourcesRemoved        []string
	EntitiesAdded         []schema.Entity
	EntitiesRemoved       []schema.Entity
	EntitiesModified      []schema.Entity
	SemanticHintsAdded    []string
	SemanticHintsRemoved  []string
	QuestionsAdded        []string
	QuestionsRemoved      []string

	// Changed is true iff any field above is non-zero. Callers that only
	// need a persist/skip signal can read this instead of inspecting
	// individual fields.
	Changed bool
}

// EnrichPage runs Tier 1 deterministic enrichment against the given page
// (using filePath and rootDir for ID/tag derivation), merges the extracted
// fields into the page's existing frontmatter, and returns the enriched
// page plus a structured delta describing what changed.
//
// The input page and the slices inside its Frontmatter are not mutated;
// the returned page's Frontmatter holds deep-copied slices. Body and
// SourcePath are preserved by value. Nothing is written to disk.
//
// This is the preferred entry point for external callers that want to
// capture enrichment results structurally (e.g. to populate a separate
// manifest or database) without frontmatter being written back to the
// markdown file on disk.
func EnrichPage(page *schema.Page, filePath, rootDir string, opts MergeOptions) (*schema.Page, EnrichmentDelta) {
	extracted := ExtractFromDocument(filePath, page.Body, rootDir)
	merged := MergeFrontmatter(page.Frontmatter, extracted, opts)
	delta := computeDelta(page.Frontmatter, merged)

	out := &schema.Page{
		Frontmatter: cloneFrontmatter(merged),
		Body:        page.Body,
		SourcePath:  page.SourcePath,
	}
	return out, delta
}

// EnrichPageWithModel is the Tier 2 variant: it merges a pre-computed
// ModelResult (from ModelExtractor.Extract and/or a generated summary) into
// the page's frontmatter and returns the enriched page plus delta.
//
// Ownership semantics match EnrichPage: input page and its frontmatter
// slices are not mutated, returned page holds deep-copied slices.
func EnrichPageWithModel(page *schema.Page, model *ModelResult, summary string, opts MergeOptions) (*schema.Page, EnrichmentDelta) {
	merged := page.Frontmatter
	if model != nil {
		merged = MergeModelResult(merged, model, opts)
	}
	if summary != "" {
		merged = MergeSummary(merged, summary, opts)
	}
	delta := computeDelta(page.Frontmatter, merged)

	out := &schema.Page{
		Frontmatter: cloneFrontmatter(merged),
		Body:        page.Body,
		SourcePath:  page.SourcePath,
	}
	return out, delta
}

// computeDelta produces an EnrichmentDelta by structurally comparing before
// and after frontmatter. It captures default-mode additions, force-mode
// replacements (add + remove on the same field), and relationship body
// changes against a shared Target.
func computeDelta(before, after schema.GraphFrontmatter) EnrichmentDelta {
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
	if before.Summary != after.Summary {
		d.SummaryChanged = true
	}

	d.TagsAdded, d.TagsRemoved = diffStringsCaseInsensitive(before.Tags, after.Tags)
	d.SourcesAdded, d.SourcesRemoved = diffStringsCaseInsensitive(before.Provenance.Sources, after.Provenance.Sources)
	d.SemanticHintsAdded, d.SemanticHintsRemoved = diffStringsCaseInsensitive(before.SemanticHints, after.SemanticHints)
	d.QuestionsAdded, d.QuestionsRemoved = diffStringsCaseInsensitive(before.PossibleQuestions, after.PossibleQuestions)

	d.RelationshipsAdded, d.RelationshipsRemoved, d.RelationshipsModified =
		diffRelationships(before.Relationships, after.Relationships)

	d.EntitiesAdded, d.EntitiesRemoved, d.EntitiesModified =
		diffEntities(before.Entities, after.Entities)

	d.Changed = d.IDChanged || d.TitleChanged || d.EntityTypeChanged ||
		d.ConfidenceChanged || d.CreatedByChanged || d.SummaryChanged ||
		len(d.TagsAdded) > 0 || len(d.TagsRemoved) > 0 ||
		len(d.SourcesAdded) > 0 || len(d.SourcesRemoved) > 0 ||
		len(d.SemanticHintsAdded) > 0 || len(d.SemanticHintsRemoved) > 0 ||
		len(d.QuestionsAdded) > 0 || len(d.QuestionsRemoved) > 0 ||
		len(d.RelationshipsAdded) > 0 || len(d.RelationshipsRemoved) > 0 ||
		len(d.RelationshipsModified) > 0 ||
		len(d.EntitiesAdded) > 0 || len(d.EntitiesRemoved) > 0 ||
		len(d.EntitiesModified) > 0

	return d
}

// diffStringsCaseInsensitive returns (added, removed) where items are
// compared via strings.ToLower, matching unionStrings in merge.go. Result
// order is preserved from the input slices.
func diffStringsCaseInsensitive(before, after []string) (added, removed []string) {
	beforeSet := make(map[string]struct{}, len(before))
	for _, s := range before {
		beforeSet[strings.ToLower(s)] = struct{}{}
	}
	afterSet := make(map[string]struct{}, len(after))
	for _, s := range after {
		afterSet[strings.ToLower(s)] = struct{}{}
	}
	for _, s := range after {
		if _, ok := beforeSet[strings.ToLower(s)]; !ok {
			added = append(added, s)
		}
	}
	for _, s := range before {
		if _, ok := afterSet[strings.ToLower(s)]; !ok {
			removed = append(removed, s)
		}
	}
	return added, removed
}

// diffRelationships categorizes the post-merge relationships into:
//   - added:    Target present in after but not before
//   - removed:  Target present in before but not after
//   - modified: Target in both but Type or Strength differs (post-merge value)
//
// This mirrors unionRelationships in merge.go (which dedupes by Target) and
// additionally detects the force-mode case where MergeFrontmatter replaces
// the entire slice.
//
// Invariant: Target is assumed unique within each input slice, matching
// the invariant unionRelationships relies on. If duplicates are present
// the later occurrence wins in the lookup maps; cardinality changes
// between duplicated targets may therefore be underreported. Enforcement
// of this invariant is upstream in the merge logic.
func diffRelationships(before, after []schema.Relationship) (added, removed, modified []schema.Relationship) {
	beforeByTarget := make(map[string]schema.Relationship, len(before))
	for _, r := range before {
		beforeByTarget[r.Target] = r
	}
	afterByTarget := make(map[string]schema.Relationship, len(after))
	for _, r := range after {
		afterByTarget[r.Target] = r
	}

	for _, r := range after {
		if prev, ok := beforeByTarget[r.Target]; !ok {
			added = append(added, r)
		} else if prev.Type != r.Type || prev.Strength != r.Strength {
			modified = append(modified, r)
		}
	}
	for _, r := range before {
		if _, ok := afterByTarget[r.Target]; !ok {
			removed = append(removed, r)
		}
	}
	return added, removed, modified
}

// diffEntities categorizes entities into (added, removed, modified),
// comparing identity by Name. Modified entities are those whose Name is
// present on both sides but whose Role or Aliases differ. This catches
// force-mode replacements like Alice{Role:"person"} -> Alice{Role:"author"}
// that Added/Removed alone would miss.
//
// Invariant: entity Name is assumed unique within each input slice.
// Duplicate Names follow the same "last wins" behavior as
// diffRelationships and rely on upstream merge logic for enforcement.
func diffEntities(before, after []schema.Entity) (added, removed, modified []schema.Entity) {
	beforeByName := make(map[string]schema.Entity, len(before))
	for _, e := range before {
		beforeByName[e.Name] = e
	}
	afterByName := make(map[string]schema.Entity, len(after))
	for _, e := range after {
		afterByName[e.Name] = e
	}

	for _, e := range after {
		if prev, ok := beforeByName[e.Name]; !ok {
			added = append(added, e)
		} else if !entityBodyEqual(prev, e) {
			modified = append(modified, e)
		}
	}
	for _, e := range before {
		if _, ok := afterByName[e.Name]; !ok {
			removed = append(removed, e)
		}
	}
	return added, removed, modified
}

// entityBodyEqual returns true iff two entities with the same Name also
// share identical Role and Aliases (order-sensitive).
func entityBodyEqual(a, b schema.Entity) bool {
	if a.Role != b.Role {
		return false
	}
	if len(a.Aliases) != len(b.Aliases) {
		return false
	}
	for i := range a.Aliases {
		if a.Aliases[i] != b.Aliases[i] {
			return false
		}
	}
	return true
}

// cloneFrontmatter returns a deep copy of the given GraphFrontmatter. All
// slice fields (including nested Entity.Aliases) get fresh backing arrays
// so the caller can safely mutate them without affecting the source.
func cloneFrontmatter(fm schema.GraphFrontmatter) schema.GraphFrontmatter {
	cp := fm
	cp.Tags = cloneStrings(fm.Tags)
	cp.Relationships = cloneRelationships(fm.Relationships)
	cp.SemanticHints = cloneStrings(fm.SemanticHints)
	cp.PossibleQuestions = cloneStrings(fm.PossibleQuestions)
	cp.Provenance.Sources = cloneStrings(fm.Provenance.Sources)
	if len(fm.Entities) > 0 {
		cp.Entities = make([]schema.Entity, len(fm.Entities))
		for i, e := range fm.Entities {
			cp.Entities[i] = e
			cp.Entities[i].Aliases = cloneStrings(e.Aliases)
		}
	} else {
		cp.Entities = nil
	}
	return cp
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func cloneRelationships(r []schema.Relationship) []schema.Relationship {
	if len(r) == 0 {
		return nil
	}
	out := make([]schema.Relationship, len(r))
	copy(out, r)
	return out
}
