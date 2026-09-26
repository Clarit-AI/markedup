// Package index provides the in-memory KnowledgeIndex for the markedup
// knowledge graph. It holds parsed pages and supports O(1) lookups by ID,
// tag-based filtering, and forward/reverse adjacency traversal.
package index

import (
	"sort"

	"github.com/Clarit-AI/markedup/schema"
)

// KnowledgeIndex is the core read-only index over all parsed pages. It is
// built by Load (or Import) and is never mutated by its own methods.
//
// Concurrency: the read methods (Get, All, Pages, Entities, Relationships,
// Tags, ByTag, ForwardRels, ReverseRefs, Export, CompactGraphSummary) and
// the package-level APIs that take *KnowledgeIndex (Search, Traverse) are
// safe for concurrent use from multiple goroutines **provided callers do
// not mutate the values they return**. Get and All
// hand out *schema.Page pointers into the internal map; ByTag, ForwardRels,
// and ReverseRefs return the internal slices directly. Mutating any of
// these would be observable by other readers. Treat everything the index
// returns as read-only; clone first if you need to modify.
//
// The typical pattern for long-running hosts (daemons, MCP servers) is to
// hold a *KnowledgeIndex behind an atomic.Pointer or sync.RWMutex, rebuild
// a new index on the side via Load/Reload, then atomically swap the
// pointer. In-flight readers on the old index remain valid and cannot
// observe partial state.
type KnowledgeIndex struct {
	byID       map[string]*schema.Page
	byTag      map[string][]*schema.Page
	adjacency  map[string][]schema.Relationship // forward: source ID → relationships
	reverseAdj map[string][]string              // reverse: target ID → source IDs
	allTags    []string                         // sorted, deduplicated
	sortedIDs  []string                         // sorted page IDs for deterministic All()

	// Reconciliation state, populated by buildIndex. See reconcile.go for the
	// B1/B3 defects these address.

	// dangling lists every edge whose target resolved to no page, across both
	// the wikilink and NER relationship fields. Sorted by (target, from) so
	// output is deterministic.
	dangling []DanglingRef
	// danglingTargets is the deduplicated, sorted set of unresolved target
	// names — the count a caller most often wants.
	danglingTargets []string
	// derivedIDs holds pages whose MarkedUp ID was empty and was synthesized
	// from the source path (B3).
	derivedIDs map[string]struct{}
	// semanticEdges counts edges sourced from SemanticRelationships.
	semanticEdges int
	// semanticResolved counts those that reconciled to a real page ID. Before
	// this fix this number was structurally zero, because the field was never
	// read at all.
	semanticResolved int
	// ambiguousAliases records normalized aliases claimed by more than one
	// page. Surfaced so a surprising resolution can be explained.
	ambiguousAliases map[string][]string
	// resolver is retained so callers (notably the graph summary) can ask how
	// a given target was resolved without rebuilding the alias table.
	resolver *targetResolver
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

// IndexData is the exported, gob-serializable representation of a
// KnowledgeIndex. It is used by the cache package to persist and restore
// the index without re-parsing all markdown files.
type IndexData struct {
	Pages []*schema.Page
}

// Export converts a KnowledgeIndex into an IndexData suitable for gob
// encoding. The full page list is extracted in sorted-ID order.
func (idx *KnowledgeIndex) Export() *IndexData {
	return &IndexData{
		Pages: idx.All(),
	}
}

// Import reconstructs a KnowledgeIndex from an IndexData. This is the
// inverse of Export and is used by the cache package after gob decoding.
func Import(data *IndexData) *KnowledgeIndex {
	return buildIndex(data.Pages)
}

// buildIndex constructs a KnowledgeIndex from a slice of parsed pages.
// This is called single-threaded after all concurrent parsing is complete.
//
// buildIndex is the single funnel for both Load and Import, which is why the
// B1/B3 fixes live here rather than in either caller: one place, no path that
// can be forgotten.
//
// No knowledge-base root is assumed; see buildIndexAt for the variant that
// takes one. Callers that know the root should use it, because a derived ID
// built from an absolute path is machine-dependent.
func buildIndex(pages []*schema.Page) *KnowledgeIndex {
	return buildIndexAt(pages, "")
}

// buildIndexAt is buildIndex with an explicit knowledge-base root, used to make
// derived IDs relative rather than absolute.
func buildIndexAt(pages []*schema.Page, root string) *KnowledgeIndex {
	idx := &KnowledgeIndex{
		byID:             make(map[string]*schema.Page, len(pages)),
		byTag:            make(map[string][]*schema.Page),
		adjacency:        make(map[string][]schema.Relationship),
		reverseAdj:       make(map[string][]string),
		derivedIDs:       make(map[string]struct{}),
		ambiguousAliases: make(map[string][]string),
	}

	// B3: give every page an identity before anything is keyed on it. Without
	// this, every page lacking a MarkedUp ID collides on "" and all but one
	// silently vanish from the graph.
	idx.derivedIDs = assignDerivedIDs(pages, root)

	// The resolver needs the final ID set, so it runs after ID assignment.
	resolver := newTargetResolver(pages)
	idx.ambiguousAliases = resolver.ambiguous
	idx.resolver = resolver

	tagSet := make(map[string]struct{})
	danglingSet := make(map[string]struct{})

	for _, p := range pages {
		id := p.Frontmatter.ID
		idx.byID[id] = p

		// Index tags.
		for _, tag := range p.Frontmatter.Tags {
			idx.byTag[tag] = append(idx.byTag[tag], p)
			tagSet[tag] = struct{}{}
		}

		// B1: fold BOTH relationship fields into one traversable edge list,
		// resolving free-text NER targets to canonical document IDs. Before
		// this, only Frontmatter.Relationships reached adjacency, so every
		// Tier 2 edge was written to disk and then discarded by the reader.
		edges := reconcileEdges(p, resolver)
		idx.semanticEdges += edges.semanticTotal
		idx.semanticResolved += edges.semanticResolved

		if len(edges.edges) > 0 {
			idx.adjacency[id] = edges.edges
		}
		for _, rel := range edges.edges {
			idx.reverseAdj[rel.Target] = append(idx.reverseAdj[rel.Target], id)
		}
		for _, d := range edges.dangling {
			idx.dangling = append(idx.dangling, d)
			danglingSet[d.Target] = struct{}{}
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

	// B1b: make unresolvable targets countable rather than a warning that
	// scrolls past. Sorted so repeated runs produce identical output.
	idx.danglingTargets = make([]string, 0, len(danglingSet))
	for t := range danglingSet {
		idx.danglingTargets = append(idx.danglingTargets, t)
	}
	sort.Strings(idx.danglingTargets)
	sort.Slice(idx.dangling, func(i, j int) bool {
		if idx.dangling[i].Target != idx.dangling[j].Target {
			return idx.dangling[i].Target < idx.dangling[j].Target
		}
		return idx.dangling[i].From < idx.dangling[j].From
	})

	return idx
}

// DanglingTargets returns the sorted, deduplicated set of relationship targets
// that resolved to no page in the index. An empty result means every edge in
// the graph lands on a real document.
func (idx *KnowledgeIndex) DanglingTargets() []string {
	out := make([]string, len(idx.danglingTargets))
	copy(out, idx.danglingTargets)
	return out
}

// DanglingRefs returns every unresolvable edge with the page that declared it
// and whether it came from the NER field. Use this to tell a genuinely
// missing document apart from a resolver miss.
func (idx *KnowledgeIndex) DanglingRefs() []DanglingRef {
	out := make([]DanglingRef, len(idx.dangling))
	copy(out, idx.dangling)
	return out
}

// DerivedIDs returns the sorted IDs of pages whose MarkedUp ID was empty and
// was synthesized from the source path (B3). Non-empty means the corpus has
// documents MarkedUp could not identify on its own.
func (idx *KnowledgeIndex) DerivedIDs() []string {
	out := make([]string, 0, len(idx.derivedIDs))
	for id := range idx.derivedIDs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// SemanticEdgeStats reports how many edges originated in
// Frontmatter.SemanticRelationships (Tier 2 NER) and how many of those
// reconciled to a real page ID. SemanticTotal > 0 with SemanticRead == 0 is
// the exact shape of the B1 defect this package used to have.
func (idx *KnowledgeIndex) SemanticEdgeStats() (total, resolved int) {
	return idx.semanticEdges, idx.semanticResolved
}

// AmbiguousAliases returns aliases claimed by more than one page, mapped to
// the pages that claim them. Sorted for determinism.
func (idx *KnowledgeIndex) AmbiguousAliases() map[string][]string {
	out := make(map[string][]string, len(idx.ambiguousAliases))
	for k, v := range idx.ambiguousAliases {
		owners := make([]string, len(v))
		copy(owners, v)
		out[k] = owners
	}
	return out
}
