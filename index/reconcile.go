package index

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Clarit-AI/markedup/schema"
)

// This file implements Phase 1 of the MarkedUp repair plan: the graph has to be
// traversable, not merely connected in theory.
//
// Three defects are fixed here, and all three are deterministic — no model is
// involved and none could be.
//
//   B1 — Tier 2 writes NER edges into Frontmatter.SemanticRelationships and
//        nothing ever read that field. Those edges are free-text entity names,
//        not document IDs, so they could not be dropped into adjacency as-is.
//        We reconcile them to IDs and fold them into traversal.
//
//   B3 — Pages arriving with an empty MarkedUp ID all collided on the "" key in
//        byID, so N pages silently overwrote each other and the graph reported
//        fewer pages than it held. We derive a stable ID from the source path.
//
//   B1b — A target that resolves to nothing used to be a LoadWarning that
//        scrolled past. We now count them and surface them in the graph summary
//        and via a dedicated accessor, because a review bucket nobody can see
//        is a slower silent drop.

// reconcileKey normalizes a free-text name into a lookup key. It is
// deliberately the same transform Tier 1 applies to a wikilink target
// (enrich.slugify), so `[[Neo4j]]`, `[[neo4j]]` and `[[Neo4j Graph]]` land in
// the same namespace as the IDs and titles they are meant to match.
func reconcileKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			// Collapse every run of non-alphanumerics into a single dash, and
			// drop leading/trailing ones, matching enrich.slugify.
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// deriveID produces a stable identifier for a page that has no MarkedUp ID.
// Stability matters: the ID must be identical across runs and across machines,
// or the cache invalidates itself every load and cross-references break.
//
// The source path is the only input guaranteed to exist and to be unique within
// a knowledge base, so it is the basis. On the (rare) collision, a short hash of
// the full path disambiguates — a function of the path alone, never of map
// iteration order, so the result does not depend on the order pages were
// parsed in.
func deriveID(p *schema.Page) string {
	base := p.SourcePath
	if base == "" {
		base = p.Frontmatter.Title
	}
	if base == "" {
		base = "untitled"
	}
	// Drop the extension but keep directories — flattening to a bare basename
	// would collide across directories (docs/api.md vs notes/api.md).
	ext := filepath.Ext(base)
	trimmed := strings.TrimSuffix(base, ext)
	slug := reconcileKey(trimmed)
	if slug == "" {
		slug = "untitled"
	}
	return slug
}

// disambiguationSuffix returns a short, deterministic, path-derived suffix.
func disambiguationSuffix(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])[:8]
}

// assignDerivedIDs gives every page an ID, in place, and reports which pages
// were given a derived one.
//
// Mutating the page is deliberate and load-bearing: if the index keyed a page
// under a derived ID while the page's own Frontmatter.ID stayed "", then Get()
// would hand back a page whose ID field disagrees with the key it was found
// under — the same class of identity defect this function exists to remove.
// The KnowledgeIndex already documents that callers must treat what it returns
// as read-only.
//
// A page that ALREADY has an ID is never touched, even if it collides with a
// derived one; explicit IDs always win over derived ones. The order is
// therefore: pass 1 reserves all explicit IDs, pass 2 fills the gaps.
func assignDerivedIDs(pages []*schema.Page) map[string]struct{} {
	derived := make(map[string]struct{})

	taken := make(map[string]struct{}, len(pages))
	for _, p := range pages {
		if id := p.Frontmatter.ID; id != "" {
			taken[id] = struct{}{}
		}
	}

	// Sort by source path so that "first one wins" on a residual collision is a
	// property of the corpus, not of the order files were walked in.
	ordered := make([]*schema.Page, len(pages))
	copy(ordered, pages)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].SourcePath != ordered[j].SourcePath {
			return ordered[i].SourcePath < ordered[j].SourcePath
		}
		return ordered[i].Frontmatter.ID < ordered[j].Frontmatter.ID
	})

	for _, p := range ordered {
		if p.Frontmatter.ID != "" {
			continue
		}
		id := deriveID(p)
		if _, exists := taken[id]; exists {
			// Disambiguate with a hash of the full path. A SHA-256 prefix
			// collision is not a real-world concern, but walk a counter anyway
			// so the function is total rather than probabilistic.
			base := id + "-" + disambiguationSuffix(p.SourcePath)
			id = base
			for n := 2; ; n++ {
				if _, exists := taken[id]; !exists {
					break
				}
				id = fmt.Sprintf("%s-%d", base, n)
			}
		}
		taken[id] = struct{}{}
		p.Frontmatter.ID = id
		derived[id] = struct{}{}
	}
	return derived
}

// targetResolver maps free-text names to canonical page IDs.
//
// It exists because the two relationship fields disagree about what a Target
// means. Relationships[].Target is a document ID (Tier 1 slugifies a wikilink
// into it). SemanticRelationships[].Target is a raw entity name from NER
// ("Alice", "Apache Tinkerpop") that was never an ID to begin with. Traversal
// needs both to land on the same key space.
type targetResolver struct {
	// byAlias maps a normalized alias to a canonical page ID.
	byAlias map[string]string
	// ids is the set of canonical page IDs.
	ids map[string]struct{}
	// ambiguous records aliases claimed by more than one page, so a resolution
	// that picked a winner can be audited.
	ambiguous map[string][]string
}

// newTargetResolver builds the alias table from the pages' IDs, titles, source
// filenames, and declared entities (names plus aliases).
//
// A document's declared entities are the point: an entity name on page P is
// exactly the thing a Tier 2 edge pointing at that name means.
func newTargetResolver(pages []*schema.Page) *targetResolver {
	r := &targetResolver{
		byAlias:   make(map[string]string, len(pages)*4),
		ids:       make(map[string]struct{}, len(pages)),
		ambiguous: make(map[string][]string),
	}

	// Collect every alias each page claims, so conflicts can be resolved
	// deterministically (lowest canonical ID wins) rather than by map order.
	claims := make(map[string][]string, len(pages)*4)
	add := func(pageID, alias string) {
		key := reconcileKey(alias)
		if key == "" {
			return
		}
		r.ids[pageID] = struct{}{}
		for _, existing := range claims[key] {
			if existing == pageID {
				return
			}
		}
		claims[key] = append(claims[key], pageID)
	}

	for _, p := range pages {
		id := p.Frontmatter.ID
		if id == "" {
			continue
		}
		add(id, id)
		add(id, p.Frontmatter.Title)
		if p.SourcePath != "" {
			base := filepath.Base(p.SourcePath)
			add(id, strings.TrimSuffix(base, filepath.Ext(base)))
		}
		for _, e := range p.Frontmatter.Entities {
			add(id, e.Name)
			for _, alias := range e.Aliases {
				add(id, alias)
			}
		}
	}

	for key, owners := range claims {
		sort.Strings(owners)
		r.byAlias[key] = owners[0]
		if len(owners) > 1 {
			r.ambiguous[key] = owners
		}
	}
	return r
}

// resolve maps a free-text target to a canonical page ID.
//
// It returns the resolved ID and whether the target matched anything at all.
// An exact ID hit is preferred over an alias hit, so an explicit identifier
// always beats a title that happens to collide.
func (r *targetResolver) resolve(target string) (string, bool) {
	if target == "" {
		return "", false
	}
	if _, ok := r.ids[target]; ok {
		return target, true
	}
	key := reconcileKey(target)
	if key == "" {
		return "", false
	}
	if id, ok := r.byAlias[key]; ok {
		return id, true
	}
	return "", false
}

// DanglingRef records one unresolvable edge: the target name, which page
// pointed at it, and whether it came from the wikilink or NER field.
type DanglingRef struct {
	Target   string `json:"target"`
	From     string `json:"from"`
	Semantic bool   `json:"semantic"`
}

// edgeSet is the reconciled edge list for one page plus the bookkeeping the
// summary needs to report B1's before/after honestly.
type edgeSet struct {
	// edges are post-reconciliation: targets are canonical IDs where they
	// resolved, and the original name where they did not. Traversal can use
	// these directly; unresolved ones simply find no page in byID.
	edges []schema.Relationship
	// semanticTotal counts edges sourced from SemanticRelationships.
	semanticTotal int
	// semanticResolved counts those that reconciled to a real page ID. This is
	// the number that was previously invisible to the graph entirely.
	semanticResolved int
	// dangling lists every edge that resolved to nothing.
	dangling []DanglingRef
}

// reconcileEdges folds a page's two relationship fields into one traversable
// edge list, resolving free-text targets to canonical IDs along the way.
//
// A target that does not resolve is RETAINED, not dropped. Silently discarding
// it would make the graph look healthier by making it wrong, and would destroy
// the evidence needed to tell a genuinely-unresolvable edge from a bug in the
// resolver.
func reconcileEdges(p *schema.Page, r *targetResolver) edgeSet {
	var out edgeSet

	appendEdge := func(rel schema.Relationship, semantic bool) {
		if rel.Target == "" {
			return
		}
		from := p.Frontmatter.ID
		resolved, ok := r.resolve(rel.Target)
		if !ok {
			out.dangling = append(out.dangling, DanglingRef{
				Target:   rel.Target,
				From:     from,
				Semantic: semantic,
			})
		} else {
			rel.Target = resolved
			if semantic {
				out.semanticResolved++
			}
		}
		if semantic {
			out.semanticTotal++
		}
		out.edges = append(out.edges, rel)
	}

	for _, rel := range p.Frontmatter.Relationships {
		appendEdge(rel, false)
	}
	for _, rel := range p.Frontmatter.SemanticRelationships {
		appendEdge(rel, true)
	}

	// A page can now reach the same target through both fields (a wikilink and
	// a NER edge, say). Adjacency is a set of edges; collapse exact duplicates
	// so traversal and the relationship count do not double-count them.
	out.edges = dedupeEdges(out.edges)
	sort.Slice(out.dangling, func(i, j int) bool {
		if out.dangling[i].Target != out.dangling[j].Target {
			return out.dangling[i].Target < out.dangling[j].Target
		}
		return out.dangling[i].From < out.dangling[j].From
	})
	return out
}

// dedupeEdges removes exact-duplicate (target, type, strength) triples,
// preserving first-seen order.
func dedupeEdges(edges []schema.Relationship) []schema.Relationship {
	if len(edges) < 2 {
		return edges
	}
	seen := make(map[schema.Relationship]struct{}, len(edges))
	out := edges[:0]
	for _, e := range edges {
		if _, dup := seen[e]; dup {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}
	return out
}
