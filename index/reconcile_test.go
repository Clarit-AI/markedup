package index

import (
	"strings"
	"testing"

	"github.com/Clarit-AI/markedup/schema"
)

// These tests are the Phase 1 exit criterion from the MarkedUp repair plan:
// the graph must be traversable, and the previously-invisible Tier 2 edges must
// become visible without inventing documents that do not exist.

func reconcilePage(id, title, path string) *schema.Page {
	return &schema.Page{
		Frontmatter: schema.GraphFrontmatter{ID: id, Title: title},
		SourcePath:  path,
	}
}

// --- B1: Tier 2 semantic edges become traversable -------------------------

// The core defect: SemanticRelationships was written to disk by Tier 2 and read
// by nothing. An edge that resolves to a real document must appear in forward
// adjacency, be reachable in reverse, and be counted.
func TestSemanticRelationshipsAreTraversable(t *testing.T) {
	alice := reconcilePage("alice", "Alice", "docs/people/alice.md")
	bob := reconcilePage("bob", "Bob", "docs/people/bob.md")
	// A Tier 2 NER edge points at a free-text entity name, not a document ID.
	alice.Frontmatter.SemanticRelationships = []schema.Relationship{
		{Target: "Bob", Type: "works-with", Strength: 0.8},
	}

	idx := buildIndex([]*schema.Page{alice, bob})

	fwd := idx.ForwardRels("alice")
	if len(fwd) != 1 {
		t.Fatalf("expected alice to have 1 forward edge, got %d: %+v", len(fwd), fwd)
	}
	if fwd[0].Target != "bob" {
		t.Errorf("semantic target not reconciled to a document ID: got %q, want %q", fwd[0].Target, "bob")
	}
	if got := idx.ReverseRefs("bob"); len(got) != 1 || got[0] != "alice" {
		t.Errorf("reverse adjacency did not pick up the semantic edge: %+v", got)
	}
	if got := idx.Relationships(); got != 1 {
		t.Errorf("Relationships() = %d, want 1 — semantic edges must be counted", got)
	}

	total, resolved := idx.SemanticEdgeStats()
	if total != 1 || resolved != 1 {
		t.Errorf("SemanticEdgeStats() = (%d, %d), want (1, 1)", total, resolved)
	}
	if got := idx.DanglingTargets(); len(got) != 0 {
		t.Errorf("a resolvable edge should not be dangling, got %v", got)
	}
}

// An NER target that names an entity declared on another page must resolve via
// that entity's name and aliases — that is the whole point of keeping the
// alias table.
func TestSemanticTargetResolvesViaEntityNameAndAlias(t *testing.T) {
	neo := reconcilePage("neo4j", "Neo4j", "docs/graph/neo4j.md")
	neo.Frontmatter.Entities = []schema.Entity{
		{Name: "Neo4j", Aliases: []string{"neo4j graph database", "Neo 4j"}},
	}
	guide := reconcilePage("guide", "Graph Guide", "docs/guide.md")
	guide.Frontmatter.SemanticRelationships = []schema.Relationship{
		{Target: "Neo 4j", Type: "uses", Strength: 0.9},
	}

	idx := buildIndex([]*schema.Page{neo, guide})

	fwd := idx.ForwardRels("guide")
	if len(fwd) != 1 || fwd[0].Target != "neo4j" {
		t.Fatalf("alias did not resolve to a document ID: %+v", fwd)
	}
	if got := idx.DanglingTargets(); len(got) != 0 {
		t.Errorf("alias-resolved edge should not be dangling, got %v", got)
	}
}

// A target matching nothing must be RETAINED as an edge and REPORTED as
// dangling. Dropping it would make the graph look healthy by making it wrong.
func TestUnresolvableTargetIsCountedNotDropped(t *testing.T) {
	page := reconcilePage("page", "Page", "docs/page.md")
	page.Frontmatter.Relationships = []schema.Relationship{
		{Target: "does-not-exist", Type: "related-to", Strength: 0.5},
	}
	page.Frontmatter.SemanticRelationships = []schema.Relationship{
		{Target: "also-missing", Type: "mentions", Strength: 0.5},
	}

	idx := buildIndex([]*schema.Page{page})

	// The edge survives so the evidence is not destroyed...
	if got := idx.Relationships(); got != 2 {
		t.Errorf("Relationships() = %d, want 2 — unresolvable edges must be retained", got)
	}
	// ...and it is visible.
	got := idx.DanglingTargets()
	if len(got) != 2 || got[0] != "also-missing" || got[1] != "does-not-exist" {
		t.Errorf("DanglingTargets() = %v, want sorted [also-missing does-not-exist]", got)
	}
	refs := idx.DanglingRefs()
	if len(refs) != 2 {
		t.Fatalf("DanglingRefs() len = %d, want 2", len(refs))
	}
	// Provenance is preserved so a missing document can be told apart from a
	// resolver miss.
	for _, r := range refs {
		if r.Target == "also-missing" && !r.Semantic {
			t.Error("semantic dangling ref not marked as semantic")
		}
		if r.Target == "does-not-exist" && r.Semantic {
			t.Error("wikilink dangling ref wrongly marked as semantic")
		}
		if r.From != "page" {
			t.Errorf("dangling ref From = %q, want %q", r.From, "page")
		}
	}
	total, resolved := idx.SemanticEdgeStats()
	if total != 1 || resolved != 0 {
		t.Errorf("SemanticEdgeStats() = (%d, %d), want (1, 0)", total, resolved)
	}
}

// --- B3: the empty-ID collision -------------------------------------------

// The smoking gun from the 2026-09-19 audit probe was a page whose id was "".
// Two such pages collided on the "" key in byID and one silently disappeared.
func TestEmptyIDsDoNotCollide(t *testing.T) {
	a := reconcilePage("", "First", "docs/first.md")
	b := reconcilePage("", "Second", "docs/second.md")

	idx := buildIndex([]*schema.Page{a, b})

	if got := idx.Pages(); got != 2 {
		t.Fatalf("Pages() = %d, want 2 — empty-ID pages collided and one was lost", got)
	}
	if a.Frontmatter.ID == "" || b.Frontmatter.ID == "" {
		t.Fatal("page left with an empty ID after buildIndex")
	}
	if a.Frontmatter.ID == b.Frontmatter.ID {
		t.Fatalf("both pages still share ID %q", a.Frontmatter.ID)
	}
	// The page's own ID must agree with the key it is filed under, or Get()
	// hands back a page whose identity field contradicts its lookup.
	for _, p := range []*schema.Page{a, b} {
		found, ok := idx.Get(p.Frontmatter.ID)
		if !ok {
			t.Errorf("derived ID %q not retrievable from index", p.Frontmatter.ID)
			continue
		}
		if found.Frontmatter.ID != p.Frontmatter.ID {
			t.Errorf("Get(%q).Frontmatter.ID = %q", p.Frontmatter.ID, found.Frontmatter.ID)
		}
	}

	derived := idx.DerivedIDs()
	if len(derived) != 2 {
		t.Errorf("DerivedIDs() = %v, want 2 entries", derived)
	}
}

// The derived ID must be a function of the corpus, not of the order files
// happened to be walked in. A cache that invalidates every load, or
// cross-references that break between machines, is worse than the bug.
func TestDerivedIDsAreDeterministicAndOrderIndependent(t *testing.T) {
	build := func(order []int) []string {
		pages := []*schema.Page{
			reconcilePage("", "First", "docs/first.md"),
			reconcilePage("", "Second", "docs/second.md"),
			reconcilePage("", "Third", "docs/third.md"),
		}
		shuffled := make([]*schema.Page, 0, len(order))
		for _, i := range order {
			shuffled = append(shuffled, pages[i])
		}
		idx := buildIndex(shuffled)
		out := make([]string, 0, 3)
		for _, p := range shuffled {
			out = append(out, p.Frontmatter.ID)
		}
		_ = idx
		return out
	}

	first := build([]int{0, 1, 2})
	// Same corpus, different order.
	second := build([]int{2, 0, 1})
	third := build([]int{1, 2, 0})

	set := func(ids []string) map[string]bool {
		m := make(map[string]bool, len(ids))
		for _, id := range ids {
			m[id] = true
		}
		return m
	}
	a, b, c := set(first), set(second), set(third)

	for id := range a {
		if !b[id] || !c[id] {
			t.Fatalf("derived IDs depend on input order: %v vs %v vs %v", first, second, third)
		}
	}
	if len(a) != 3 {
		t.Fatalf("expected 3 distinct derived IDs, got %d: %v", len(a), a)
	}
}

// A page that HAS an ID must never have it overwritten by a derived one, even
// when the two collide.
func TestExplicitIDWinsOverDerived(t *testing.T) {
	explicit := reconcilePage("docs-first", "First", "docs/first.md")
	empty := reconcilePage("", "Something", "docs/first.md")

	idx := buildIndex([]*schema.Page{explicit, empty})

	if got, ok := idx.Get("docs-first"); !ok || got.Frontmatter.Title != "First" {
		t.Error("explicit ID was clobbered by a derived one")
	}
	if explicit.Frontmatter.ID != "docs-first" {
		t.Errorf("explicit page ID mutated to %q", explicit.Frontmatter.ID)
	}
	if empty.Frontmatter.ID == "docs-first" {
		t.Error("derived ID collided with an explicit ID")
	}
	if got := idx.Pages(); got != 2 {
		t.Errorf("Pages() = %d, want 2", got)
	}
}

// A page whose source path is bare (no directory) still needs a stable ID.
func TestDeriveIDHandlesBareAndMissingPaths(t *testing.T) {
	// With a root, the whole relative path is kept so same-named files in
	// different directories do not collide.
	cases := []struct{ name, path, want string }{
		{"bare filename", "/kb/notes.md", "notes"},
		{"nested", "/kb/a/b/c.md", "a-b-c"},
		{"no extension", "/kb/readme", "readme"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveID(&schema.Page{SourcePath: tc.path}, "/kb")
			if got != tc.want {
				t.Errorf("deriveID(%q, /kb) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
	// With no root, only the filename survives — still machine-independent.
	if got := deriveID(&schema.Page{SourcePath: "/a/b/notes.md"}, ""); got != "notes" {
		t.Errorf("rootless deriveID = %q, want %q", got, "notes")
	}
	// No path and no title must still yield something usable, not "".
	if got := deriveID(&schema.Page{}, ""); got == "" {
		t.Error("deriveID returned empty string for an empty page")
	}
}

// --- Visibility in the graph summary -------------------------------------

// "A review bucket nobody can see is just a slower silent drop." The summary is
// what the MCP tool and `export --compact` read, so the counts belong there.
func TestGraphSummarySurfacesDanglingAndSemanticCounts(t *testing.T) {
	good := reconcilePage("good", "Good", "docs/good.md")
	bad := reconcilePage("bad", "Bad", "docs/bad.md")
	bad.Frontmatter.Relationships = []schema.Relationship{
		{Target: "good", Type: "related-to", Strength: 0.5},
		{Target: "missing", Type: "related-to", Strength: 0.5},
	}
	bad.Frontmatter.SemanticRelationships = []schema.Relationship{
		{Target: "Good", Type: "mentions", Strength: 0.7},
	}

	idx := buildIndex([]*schema.Page{good, bad})
	sum := idx.CompactGraphSummary()

	if sum.Stats.Pages != 2 {
		t.Errorf("Stats.Pages = %d, want 2", sum.Stats.Pages)
	}
	// bad->good (wikilink) + bad->missing (wikilink) + bad->Good (NER) = 3
	if sum.Stats.Relationships != 3 {
		t.Errorf("Stats.Relationships = %d, want 3 — the summary must count reconciled edges, not just frontmatter ones", sum.Stats.Relationships)
	}
	if sum.Stats.DanglingTargets != 1 {
		t.Errorf("Stats.DanglingTargets = %d, want 1", sum.Stats.DanglingTargets)
	}
	if len(sum.Stats.DanglingTargetNames) != 1 || sum.Stats.DanglingTargetNames[0] != "missing" {
		t.Errorf("Stats.DanglingTargetNames = %v, want [missing]", sum.Stats.DanglingTargetNames)
	}
	if sum.Stats.SemanticEdges != 1 {
		t.Errorf("Stats.SemanticEdges = %d, want 1", sum.Stats.SemanticEdges)
	}
	if sum.Stats.SemanticEdgesResolved != 1 {
		t.Errorf("Stats.SemanticEdgesResolved = %d, want 1", sum.Stats.SemanticEdgesResolved)
	}

	// Per-node edges must also come from the reconciled set.
	var badNode *SummaryNode
	for i := range sum.Pages {
		if sum.Pages[i].ID == "bad" {
			badNode = &sum.Pages[i]
		}
	}
	if badNode == nil {
		t.Fatal("page 'bad' missing from summary")
	}
	if len(badNode.Relationships) != 3 {
		t.Errorf("bad has %d edges in summary, want 3: %+v", len(badNode.Relationships), badNode.Relationships)
	}
}

// Filtering must not mix scopes: a global dangling count alongside a filtered
// relationship count would let the summary claim edges it does not list.
func TestGraphSummaryDanglingRespectsFiltering(t *testing.T) {
	a := reconcilePage("a", "A", "docs/a.md")
	b := reconcilePage("b", "B", "docs/b.md")
	a.Frontmatter.Relationships = []schema.Relationship{{Target: "nowhere", Type: "related-to"}}

	idx := buildIndex([]*schema.Page{a, b})
	sum := idx.CompactGraphSummary(WithPageIDs([]string{"b"}))

	if sum.Stats.DanglingTargets != 0 {
		t.Errorf("DanglingTargets = %d, want 0 when page 'a' is filtered out", sum.Stats.DanglingTargets)
	}
	if sum.Stats.Relationships != 0 {
		t.Errorf("Relationships = %d, want 0 for the filtered set", sum.Stats.Relationships)
	}
}

// --- Determinism ----------------------------------------------------------

// Re-running the same corpus must produce byte-identical structure. A graph
// whose shape changes between runs is not usable as a cache or a diff base.
func TestIndexBuildIsDeterministic(t *testing.T) {
	buildCorpus := func() []*schema.Page {
		mk := func(id, title, path, semantic string) *schema.Page {
			p := reconcilePage(id, title, path)
			if semantic != "" {
				p.Frontmatter.SemanticRelationships = []schema.Relationship{
					{Target: semantic, Type: "mentions", Strength: 0.6},
				}
			}
			return p
		}
		return []*schema.Page{
			mk("", "Zeta", "docs/zeta.md", "Alpha"),
			mk("", "Alpha", "docs/alpha.md", "Nowhere"),
			mk("mid", "Mid", "docs/mid.md", "Zeta"),
			mk("", "Beta", "docs/beta.md", ""),
		}
	}

	var firstSummary *GraphSummary
	var firstDangling []string
	for run := 0; run < 25; run++ {
		idx := buildIndex(buildCorpus())
		sum := idx.CompactGraphSummary()
		dang := idx.DanglingTargets()
		if run == 0 {
			firstSummary, firstDangling = sum, dang
			continue
		}
		if len(dang) != len(firstDangling) {
			t.Fatalf("run %d: dangling set changed length: %v vs %v", run, dang, firstDangling)
		}
		for i := range dang {
			if dang[i] != firstDangling[i] {
				t.Fatalf("run %d: dangling set differs at %d: %q vs %q", run, i, dang[i], firstDangling[i])
			}
		}
		if sum.Stats.Relationships != firstSummary.Stats.Relationships {
			t.Fatalf("run %d: relationship count changed: %d vs %d",
				run, sum.Stats.Relationships, firstSummary.Stats.Relationships)
		}
		if sum.Stats.Pages != firstSummary.Stats.Pages {
			t.Fatalf("run %d: page count changed: %d vs %d", run, sum.Stats.Pages, firstSummary.Stats.Pages)
		}
	}
}

// The same document, checked out at two different absolute paths, must get
// the same ID. Absolute paths in a derived ID make the graph cache
// machine-dependent: it invalidates on every CI run and cross-references break
// when the knowledge base moves.
func TestDerivedIDsAreMachineIndependent(t *testing.T) {
	build := func(root, abs string) string {
		p := &schema.Page{SourcePath: abs}
		buildIndexAt([]*schema.Page{p}, root)
		return p.Frontmatter.ID
	}

	here := build("/Users/alice/kb", "/Users/alice/kb/notes/api.md")
	ci := build("/srv/ci/checkout", "/srv/ci/checkout/notes/api.md")
	unknown := build("", "/Users/alice/kb/notes/api.md")

	if here != ci {
		t.Errorf("same document at two roots got different IDs: %q vs %q", here, ci)
	}
	if strings.Contains(here, "users") || strings.Contains(here, "alice") ||
		strings.Contains(here, "tmp") || strings.Contains(here, "srv") {
		t.Errorf("derived ID %q leaks an absolute path segment", here)
	}
	// With no root we fall back to the bare filename, which is still stable.
	if unknown != "api" {
		t.Errorf("rootless deriveID = %q, want %q", unknown, "api")
	}
	if here != "notes-api" {
		t.Errorf("rooted deriveID = %q, want %q", here, "notes-api")
	}
}

// A path outside the declared root must not produce a ".."-prefixed ID.
func TestDerivedIDIgnoresPathsOutsideRoot(t *testing.T) {
	p := &schema.Page{SourcePath: "/elsewhere/notes/api.md"}
	buildIndexAt([]*schema.Page{p}, "/users/alice/kb")
	if p.Frontmatter.ID == "" {
		t.Fatal("out-of-root path produced an empty ID")
	}
	if strings.Contains(p.Frontmatter.ID, "..") {
		t.Errorf("derived ID %q leaks a parent-directory prefix", p.Frontmatter.ID)
	}
	if strings.Contains(p.Frontmatter.ID, "users") || strings.Contains(p.Frontmatter.ID, "alice") {
		t.Errorf("derived ID %q leaks an absolute path segment", p.Frontmatter.ID)
	}
}

// --- Regression guards ----------------------------------------------------

// The pre-fix shape of B1, stated as a failing condition: a Tier 2 edge that
// resolves must NOT be absent from traversal.
func TestNoPageIsUnreachableViaItsOwnSemanticEdge(t *testing.T) {
	// alpha declares an NER edge to "Beta", and beta is a real page. Before
	// reconciliation beta was unreachable from alpha entirely.
	alpha := reconcilePage("alpha", "Alpha", "docs/alpha.md")
	beta := reconcilePage("beta", "Beta", "docs/beta.md")
	alpha.Frontmatter.SemanticRelationships = []schema.Relationship{
		{Target: "Beta", Type: "knows", Strength: 0.5},
	}

	idx := buildIndex([]*schema.Page{alpha, beta})

	if got := idx.ReverseRefs("beta"); len(got) == 0 {
		t.Fatal("beta has no reverse references — the NER edge never reached traversal (B1 regression)")
	}
	// And traversal itself must find it.
	if !reachableFrom(idx, "alpha", "beta") {
		t.Error("Traverse-style reachability failed: alpha cannot reach beta via its NER edge (B1 regression)")
	}
}

// reachableFrom is a minimal stand-in for index.Traverse: a breadth-first walk
// of forward adjacency.
func reachableFrom(idx *KnowledgeIndex, from, to string) bool {
	if from == to {
		return true
	}
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, rel := range idx.ForwardRels(cur) {
			if rel.Target == to {
				return true
			}
			if !seen[rel.Target] {
				seen[rel.Target] = true
				queue = append(queue, rel.Target)
			}
		}
	}
	return false
}

// An alias claimed by two pages resolves deterministically, and the ambiguity
// is recorded so a surprising resolution can be explained rather than guessed.
func TestAmbiguousAliasResolvesDeterministicallyAndIsRecorded(t *testing.T) {
	a := reconcilePage("a", "Shared Title", "docs/a.md")
	b := reconcilePage("b", "Other", "docs/b.md")
	// Both pages claim the same title alias.
	b.Frontmatter.Entities = []schema.Entity{{Name: "Shared Title"}}

	src := reconcilePage("src", "Src", "docs/src.md")
	src.Frontmatter.Relationships = []schema.Relationship{
		{Target: "Shared Title", Type: "related-to", Strength: 0.5},
	}

	var firstWinner string
	for run := 0; run < 20; run++ {
		idx := buildIndex([]*schema.Page{a, b, src})
		amb := idx.AmbiguousAliases()
		if _, ok := amb["shared-title"]; !ok {
			t.Fatalf("run %d: ambiguous alias not recorded, got %v", run, amb)
		}
		fwd := idx.ForwardRels("src")
		if len(fwd) != 1 {
			t.Fatalf("run %d: expected 1 edge, got %d", run, len(fwd))
		}
		if run == 0 {
			firstWinner = fwd[0].Target
			continue
		}
		if fwd[0].Target != firstWinner {
			t.Fatalf("run %d: ambiguous alias resolved to %q, first run chose %q", run, fwd[0].Target, firstWinner)
		}
	}
}

// An exact ID hit must beat an alias hit, so an explicit identifier is never
// shadowed by a title that happens to collide.
func TestExactIDBeatsAlias(t *testing.T) {
	real := reconcilePage("neo4j", "Totally Different Title", "docs/real.md")
	other := reconcilePage("other", "Neo4j", "docs/other.md")
	src := reconcilePage("src", "Src", "docs/src.md")
	src.Frontmatter.Relationships = []schema.Relationship{
		{Target: "neo4j", Type: "related-to", Strength: 0.5},
	}

	idx := buildIndex([]*schema.Page{real, other, src})

	fwd := idx.ForwardRels("src")
	if len(fwd) != 1 || fwd[0].Target != "neo4j" {
		t.Fatalf("exact ID did not win: %+v", fwd)
	}
	// "other" claims "neo4j" as a title alias, but the exact ID must still win.
	if got := idx.ReverseRefs("neo4j"); len(got) != 1 || got[0] != "src" {
		t.Errorf("reverse refs for the exact ID = %v, want [src]", got)
	}
}
