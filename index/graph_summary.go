package index

import (
	"sort"

	"github.com/Clarit-AI/markedup/schema"
)

// GraphSummary is a compact, token-efficient representation of the knowledge
// graph. It intentionally excludes body text, entities, provenance, and
// enrichment metadata — those are available via per-page retrieval.
type GraphSummary struct {
	Stats SummaryStats  `json:"stats"`
	Pages []SummaryNode `json:"pages"`
}

// SummaryStats provides aggregate counts for the graph.
type SummaryStats struct {
	Pages         int      `json:"pages"`
	Relationships int      `json:"relationships"`
	EntityTypes   []string `json:"entity_types"`
	Tags          []string `json:"tags"`

	// DanglingTargets counts edges in the filtered set whose target resolved to
	// no page, and DanglingTargetNames lists them (sorted, deduplicated).
	//
	// This is the B1 exit signal. A graph with DanglingTargets > 0 has edges
	// pointing at documents that do not exist; one with 0 has every edge
	// landing on a real page. Reporting it here — rather than only as a load
	// warning — is what makes the condition countable by any consumer of the
	// summary, including the MCP markedup_get_structure tool and export.
	DanglingTargets     int      `json:"dangling_targets"`
	DanglingTargetNames []string `json:"dangling_target_names,omitempty"`

	// SemanticEdges counts edges sourced from Frontmatter.SemanticRelationships
	// (Tier 2 NER) and SemanticEdgesResolved counts those that reconciled to a
	// real page ID. Before reconciliation these edges were written to disk and
	// never read, so SemanticEdges was structurally unreachable and
	// SemanticEdgesResolved was always 0.
	SemanticEdges         int `json:"semantic_edges"`
	SemanticEdgesResolved int `json:"semantic_edges_resolved"`

	// DerivedIDs counts pages whose MarkedUp ID was empty and was synthesized
	// from the source path (B3). Before the fix these all collided on the ""
	// key and all but one disappeared from the graph.
	DerivedIDs int `json:"derived_ids"`
}

// SummaryNode is a compact representation of a single page in the graph.
type SummaryNode struct {
	ID            string                `json:"id"`
	Title         string                `json:"title"`
	Summary       string                `json:"summary,omitempty"`
	EntityType    string                `json:"entity_type"`
	Tags          []string              `json:"tags"`
	Confidence    float64               `json:"confidence"`
	Relationships []SummaryRelationship `json:"relationships,omitempty"`

	// Temporal fields are only included when WithTemporal(true) is set.
	ValidFrom    string `json:"valid_from,omitempty"`
	ValidUntil   string `json:"valid_until,omitempty"`
	LastVerified string `json:"last_verified,omitempty"`
}

// SummaryRelationship is a compact edge representation.
type SummaryRelationship struct {
	Target   string  `json:"target"`
	Type     string  `json:"type"`
	Strength float64 `json:"strength"`
}

// summaryConfig holds the options for CompactGraphSummary.
type summaryConfig struct {
	entityTypeFilter string
	tagFilter        string
	pageIDFilter     []string
	includeRels      bool
	includeTemporal  bool
	maxPages         int
}

// SummaryOption configures the behavior of CompactGraphSummary.
type SummaryOption func(*summaryConfig)

// WithEntityTypeFilter restricts the summary to pages matching the given
// entity type (case-sensitive match against the page's entity-type field).
func WithEntityTypeFilter(et string) SummaryOption {
	return func(c *summaryConfig) { c.entityTypeFilter = et }
}

// WithTagFilter restricts the summary to pages that contain the given tag.
func WithTagFilter(tag string) SummaryOption {
	return func(c *summaryConfig) { c.tagFilter = tag }
}

// WithRelationships controls whether relationship edges are included in
// each SummaryNode. Default is true.
func WithRelationships(include bool) SummaryOption {
	return func(c *summaryConfig) { c.includeRels = include }
}

// WithTemporal controls whether temporal metadata (valid-from, valid-until,
// last-verified) is included in each SummaryNode. Default is false.
func WithTemporal(include bool) SummaryOption {
	return func(c *summaryConfig) { c.includeTemporal = include }
}

// WithMaxPages limits the number of pages in the summary. A value of 0 or
// negative means no limit. Pages are returned in sorted-ID order; the limit
// is applied after filtering.
func WithMaxPages(n int) SummaryOption {
	return func(c *summaryConfig) { c.maxPages = n }
}

// WithPageIDs restricts the summary to pages whose ID is in the given slice.
// An empty or nil slice means no restriction. Uses a map for O(1) lookup.
func WithPageIDs(ids []string) SummaryOption {
	return func(c *summaryConfig) { c.pageIDFilter = ids }
}

// CompactGraphSummary builds a token-efficient summary of the knowledge
// graph. It is used by both the MCP markedup_get_structure tool and the
// CLI export --compact command.
func (idx *KnowledgeIndex) CompactGraphSummary(opts ...SummaryOption) *GraphSummary {
	cfg := summaryConfig{
		includeRels: true,
	}
	for _, o := range opts {
		o(&cfg)
	}

	// Build page-ID filter set for O(1) lookup.
	var pageIDSet map[string]struct{}
	if len(cfg.pageIDFilter) > 0 {
		pageIDSet = make(map[string]struct{}, len(cfg.pageIDFilter))
		for _, id := range cfg.pageIDFilter {
			pageIDSet[id] = struct{}{}
		}
	}

	// Collect matching pages (deterministic order via sortedIDs).
	var filtered []*schema.Page
	for _, id := range idx.sortedIDs {
		p := idx.byID[id]
		if pageIDSet != nil {
			if _, ok := pageIDSet[id]; !ok {
				continue
			}
		}
		if cfg.entityTypeFilter != "" && p.Frontmatter.EntityType != cfg.entityTypeFilter {
			continue
		}
		if cfg.tagFilter != "" && !hasTag(p.Frontmatter.Tags, cfg.tagFilter) {
			continue
		}
		filtered = append(filtered, p)
	}

	// Apply max-pages limit.
	if cfg.maxPages > 0 && len(filtered) > cfg.maxPages {
		filtered = filtered[:cfg.maxPages]
	}

	// Build stats from filtered set.
	//
	// Relationships are counted from the index's reconciled adjacency rather
	// than from Frontmatter.Relationships. The distinction is the whole point
	// of B1: the frontmatter field holds only wikilink edges and silently omits
	// every Tier 2 NER edge, so counting it here reported a graph that was
	// smaller than the one traversal actually walks.
	entityTypeSet := make(map[string]struct{})
	tagSet := make(map[string]struct{})
	totalRels := 0
	semanticEdges := 0
	semanticResolved := 0
	derivedIDs := 0
	danglingSet := make(map[string]struct{})

	for _, p := range filtered {
		if p.Frontmatter.EntityType != "" {
			entityTypeSet[p.Frontmatter.EntityType] = struct{}{}
		}
		for _, t := range p.Frontmatter.Tags {
			tagSet[t] = struct{}{}
		}
		if _, wasDerived := idx.derivedIDs[p.Frontmatter.ID]; wasDerived {
			derivedIDs++
		}
		totalRels += len(idx.adjacency[p.Frontmatter.ID])
		semanticEdges += len(p.Frontmatter.SemanticRelationships)
	}

	// Dangling targets are scoped to the filtered set so the numbers agree with
	// the Relationships count above — mixing a filtered count with a global one
	// would let the summary claim more edges than it lists.
	danglingByFrom := make(map[string][]DanglingRef, len(idx.dangling))
	for _, d := range idx.dangling {
		danglingByFrom[d.From] = append(danglingByFrom[d.From], d)
	}
	for _, p := range filtered {
		for _, d := range danglingByFrom[p.Frontmatter.ID] {
			danglingSet[d.Target] = struct{}{}
		}
	}
	// Count semantic edges that reconciled to a real page, for pages in the
	// filtered set only.
	for _, p := range filtered {
		for _, rel := range p.Frontmatter.SemanticRelationships {
			if _, ok := idx.resolver.resolve(rel.Target); ok {
				semanticResolved++
			}
		}
	}

	stats := SummaryStats{
		Pages:                 len(filtered),
		Relationships:         totalRels,
		EntityTypes:           sortedKeys(entityTypeSet),
		Tags:                  sortedKeys(tagSet),
		DanglingTargets:       len(danglingSet),
		DanglingTargetNames:   sortedKeys(danglingSet),
		SemanticEdges:         semanticEdges,
		SemanticEdgesResolved: semanticResolved,
		DerivedIDs:            derivedIDs,
	}

	// Build compact nodes.
	nodes := make([]SummaryNode, 0, len(filtered))
	for _, p := range filtered {
		fm := p.Frontmatter
		node := SummaryNode{
			ID:         fm.ID,
			Title:      fm.Title,
			Summary:    fm.Summary,
			EntityType: fm.EntityType,
			Tags:       fm.Tags,
			Confidence: fm.Confidence,
		}

		if cfg.includeRels {
			for _, r := range idx.adjacency[fm.ID] {
				node.Relationships = append(node.Relationships, SummaryRelationship{
					Target:   r.Target,
					Type:     r.Type,
					Strength: r.Strength,
				})
			}
		}

		if cfg.includeTemporal {
			node.ValidFrom = fm.Temporal.ValidFrom
			node.ValidUntil = fm.Temporal.ValidUntil
			node.LastVerified = fm.Temporal.LastVerified
		}

		nodes = append(nodes, node)
	}

	return &GraphSummary{
		Stats: stats,
		Pages: nodes,
	}
}

// hasTag checks whether the given tag is present in the slice.
func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

// sortedKeys returns the keys of a string set in sorted order.
func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
