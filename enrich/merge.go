package enrich

import (
	"strings"

	"github.com/Clarit-AI/markedup/schema"
)

// MergeOptions controls how fields are merged.
type MergeOptions struct {
	Force bool // If true, overwrite all fields. If false, only fill empty/zero fields.
}

// MergeFrontmatter merges extracted fields into existing frontmatter.
// Default mode: fill missing scalar fields, union/append arrays (deduplicated).
// Force mode: overwrite all scalars, replace all arrays (unless extracted is zero/empty).
// Returns a new GraphFrontmatter; existing is not mutated.
func MergeFrontmatter(existing schema.GraphFrontmatter, extracted ExtractedFields, opts MergeOptions) schema.GraphFrontmatter {
	result := existing

	if opts.Force {
		if extracted.ID != "" {
			result.ID = extracted.ID
		}
		if extracted.Title != "" {
			result.Title = extracted.Title
		}
		if extracted.EntityType != "" {
			result.EntityType = extracted.EntityType
		}
		if extracted.Confidence > 0 {
			result.Confidence = extracted.Confidence
		}
		if len(extracted.Tags) > 0 {
			result.Tags = dedupeStringsCaseInsensitive(extracted.Tags)
		}
		if len(extracted.Relationships) > 0 {
			result.Relationships = dedupeRelationships(extracted.Relationships)
		}
		if len(extracted.Provenance.Sources) > 0 {
			result.Provenance.Sources = dedupeStringsCaseInsensitive(extracted.Provenance.Sources)
		}
		if extracted.Provenance.CreatedBy != "" {
			result.Provenance.CreatedBy = extracted.Provenance.CreatedBy
		}
		return result
	}

	// Default mode: fill missing only, union arrays.
	if result.ID == "" {
		result.ID = extracted.ID
	}
	if result.Title == "" {
		result.Title = extracted.Title
	}
	if result.EntityType == "" {
		result.EntityType = extracted.EntityType
	}
	if result.Confidence == 0 {
		result.Confidence = extracted.Confidence
	}
	if result.Provenance.CreatedBy == "" {
		result.Provenance.CreatedBy = extracted.Provenance.CreatedBy
	}

	// Union tags (case-insensitive dedup).
	result.Tags = unionStrings(result.Tags, extracted.Tags)

	// Append relationships with new targets.
	result.Relationships = unionRelationships(result.Relationships, extracted.Relationships)

	// Append new provenance sources.
	result.Provenance.Sources = unionStrings(result.Provenance.Sources, extracted.Provenance.Sources)

	return result
}

// MergeSummary merges a generated summary into existing frontmatter.
// Default mode: fill if empty. Force mode: overwrite.
func MergeSummary(existing schema.GraphFrontmatter, summary string, opts MergeOptions) schema.GraphFrontmatter {
	result := existing
	if summary == "" {
		return result
	}
	if opts.Force || result.Summary == "" {
		result.Summary = summary
	}
	return result
}

// IsComplete reports whether the frontmatter has all required fields populated,
// meaning enrichment would be a no-op in default (non-force) mode for scalars.
func IsComplete(fm schema.GraphFrontmatter) bool {
	return fm.ID != "" && fm.Title != "" && fm.EntityType != "" && fm.Confidence > 0
}

// unionStrings returns the union of a and b, case-insensitive, preserving order.
func unionStrings(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var result []string
	for _, s := range a {
		lower := strings.ToLower(s)
		if !seen[lower] {
			seen[lower] = true
			result = append(result, s)
		}
	}
	for _, s := range b {
		lower := strings.ToLower(s)
		if !seen[lower] {
			seen[lower] = true
			result = append(result, s)
		}
	}
	return result
}

// unionRelationships appends relationships from b whose (Target, Type) key is
// not already present in a. Dedup is done on the composite (Target, Type) key
// so that two predicates between the same target pair are preserved, but an
// exact repeat is collapsed. Input a is also deduped against itself so a
// stale input with same-key duplicates gets cleaned up.
func unionRelationships(a, b []schema.Relationship) []schema.Relationship {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	seen := make(map[relKey]bool, len(a)+len(b))
	result := make([]schema.Relationship, 0, len(a)+len(b))
	for _, r := range a {
		k := relKey{Target: r.Target, Type: r.Type}
		if seen[k] {
			continue
		}
		seen[k] = true
		result = append(result, r)
	}
	for _, r := range b {
		k := relKey{Target: r.Target, Type: r.Type}
		if seen[k] {
			continue
		}
		seen[k] = true
		result = append(result, r)
	}
	return result
}

// relKey is the composite identity key used to dedupe relationships.
type relKey struct {
	Target string
	Type   string
}

// dedupeRelationships removes within-list duplicates keyed by (Target, Type),
// preserving first-seen order. Used in force-mode merges where the incoming
// slice replaces the existing one wholesale.
func dedupeRelationships(in []schema.Relationship) []schema.Relationship {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[relKey]bool, len(in))
	out := make([]schema.Relationship, 0, len(in))
	for _, r := range in {
		k := relKey{Target: r.Target, Type: r.Type}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out
}

// dedupeEntities removes within-list duplicates keyed by lowercased Name,
// preserving first-seen order. Used in force-mode merges.
func dedupeEntities(in []schema.Entity) []schema.Entity {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]schema.Entity, 0, len(in))
	for _, e := range in {
		k := strings.ToLower(e.Name)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	return out
}

// dedupeStringsCaseInsensitive removes case-insensitive duplicates from in,
// preserving first-seen order and the original casing of the first occurrence.
func dedupeStringsCaseInsensitive(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		k := strings.ToLower(s)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	return out
}
