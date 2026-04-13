// Package index provides the in-memory KnowledgeIndex for the markedup
// knowledge graph. It holds parsed pages and supports O(1) lookups by ID,
// tag-based filtering, and forward/reverse adjacency traversal.
package index

import (
	"sort"

	"github.com/KHAEntertainment/markedup/schema"
)

// KnowledgeIndex is the core read-only index over all parsed pages. It is
// built by Load and is safe for concurrent reads after construction (no
// internal mutation methods are exposed).
type KnowledgeIndex struct {
	byID       map[string]*schema.Page
	byTag      map[string][]*schema.Page
	adjacency  map[string][]schema.Relationship // forward: source ID → relationships
	reverseAdj map[string][]string              // reverse: target ID → source IDs
	allTags    []string                         // sorted, deduplicated
	sortedIDs  []string                         // sorted page IDs for deterministic All()
}

// Get returns the page with the given ID and true, or nil and false if the
// ID is not in the index.
func (idx *KnowledgeIndex) Get(id string) (*schema.Page, bool) {
	p, ok := idx.byID[id]
	return p, ok
}

// All returns every page in the index, sorted by ID for deterministic output.
func (idx *KnowledgeIndex) All() []*schema.Page {
	pages := make([]*schema.Page, 0, len(idx.sortedIDs))
	for _, id := range idx.sortedIDs {
		pages = append(pages, idx.byID[id])
	}
	return pages
}

// Pages returns the number of pages in the index.
func (idx *KnowledgeIndex) Pages() int {
	return len(idx.byID)
}

// Entities returns the count of unique entity names across all pages.
func (idx *KnowledgeIndex) Entities() int {
	seen := make(map[string]struct{})
	for _, p := range idx.byID {
		for _, e := range p.Frontmatter.Entities {
			seen[e.Name] = struct{}{}
		}
	}
	return len(seen)
}

// Relationships returns the total number of relationships across all pages.
func (idx *KnowledgeIndex) Relationships() int {
	total := 0
	for _, rels := range idx.adjacency {
		total += len(rels)
	}
	return total
}

// Tags returns all tags across all pages, sorted and deduplicated.
func (idx *KnowledgeIndex) Tags() []string {
	out := make([]string, len(idx.allTags))
	copy(out, idx.allTags)
	return out
}

// ByTag returns all pages that have the given tag. Returns nil if the tag
// is not found.
func (idx *KnowledgeIndex) ByTag(tag string) []*schema.Page {
	return idx.byTag[tag]
}

// ForwardRels returns the outgoing relationships from the page with the
// given ID. Returns nil if the ID is not in the index.
func (idx *KnowledgeIndex) ForwardRels(id string) []schema.Relationship {
	return idx.adjacency[id]
}

// ReverseRefs returns the IDs of pages that have a relationship targeting
// the given ID. Returns nil if no pages reference the target.
func (idx *KnowledgeIndex) ReverseRefs(id string) []string {
	return idx.reverseAdj[id]
}

// buildIndex constructs a KnowledgeIndex from a slice of parsed pages.
// This is called single-threaded after all concurrent parsing is complete.
func buildIndex(pages []*schema.Page) *KnowledgeIndex {
	idx := &KnowledgeIndex{
		byID:       make(map[string]*schema.Page, len(pages)),
		byTag:      make(map[string][]*schema.Page),
		adjacency:  make(map[string][]schema.Relationship),
		reverseAdj: make(map[string][]string),
	}

	tagSet := make(map[string]struct{})

	for _, p := range pages {
		id := p.Frontmatter.ID
		idx.byID[id] = p

		// Index tags.
		for _, tag := range p.Frontmatter.Tags {
			idx.byTag[tag] = append(idx.byTag[tag], p)
			tagSet[tag] = struct{}{}
		}

		// Build forward adjacency.
		if len(p.Frontmatter.Relationships) > 0 {
			idx.adjacency[id] = p.Frontmatter.Relationships
		}

		// Build reverse adjacency.
		for _, rel := range p.Frontmatter.Relationships {
			idx.reverseAdj[rel.Target] = append(idx.reverseAdj[rel.Target], id)
		}
	}

	// Sort and deduplicate tags.
	idx.allTags = make([]string, 0, len(tagSet))
	for tag := range tagSet {
		idx.allTags = append(idx.allTags, tag)
	}
	sort.Strings(idx.allTags)

	// Pre-compute sorted IDs for deterministic All().
	idx.sortedIDs = make([]string, 0, len(idx.byID))
	for id := range idx.byID {
		idx.sortedIDs = append(idx.sortedIDs, id)
	}
	sort.Strings(idx.sortedIDs)

	return idx
}
