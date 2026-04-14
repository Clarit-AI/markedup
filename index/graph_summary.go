package index

import (
	"sort"

	"github.com/KHAEntertainment/markedup/schema"
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
	entityTypeSet := make(map[string]struct{})
	tagSet := make(map[string]struct{})
	totalRels := 0
	for _, p := range filtered {
		if p.Frontmatter.EntityType != "" {
			entityTypeSet[p.Frontmatter.EntityType] = struct{}{}
		}
		for _, t := range p.Frontmatter.Tags {
			tagSet[t] = struct{}{}
		}
		totalRels += len(p.Frontmatter.Relationships)
	}

	stats := SummaryStats{
		Pages:         len(filtered),
		Relationships: totalRels,
		EntityTypes:   sortedKeys(entityTypeSet),
		Tags:          sortedKeys(tagSet),
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
			for _, r := range fm.Relationships {
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
