package index

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Clarit-AI/markedup/enrich"
	"github.com/Clarit-AI/markedup/schema"
)

var refreshBaseline = flag.Bool("refresh", false,
	"rewrite the committed Phase-0 corpus artifact")


// Phase-0 corpus probe. Where phase0_baseline_test.go measures the two B2 code
// paths in isolation (synthetic inputs, deterministic harness), this test runs
// the real Tier-1 pipeline over the D2 corpus — docs/*.md plus the testdata/
// fixtures — and records what the pipeline actually produces. It also performs
// the live B1 probe that Phase 0 step 3 asked for, rather than transcribing a
// snapshot from a handoff document.
//
// Reproducible in one command:
//
//	go test -run TestPhase0Corpus -v ./index/
//
// Nothing here writes to the corpus. Every document is read, enriched in
// memory, and indexed from memory.

type corpusArtifact struct {
	CapturedAt string `json:"captured_at"`
	Corpus     corpusReport `json:"corpus"`
	B1         b1Report     `json:"b1"`
}

type corpusReport struct {
	DocCount     int            `json:"doc_count"`
	SourceFiles  []string       `json:"source_files"`
	EntityTypes  map[string]int `json:"entity_types"`
	Tags         map[string]int `json:"tags"`
	RelationshipTargets map[string]int `json:"relationship_targets"`

	// Dangling is the B1 symptom in corpus terms: a relationship target that
	// matches no other page's ID. Phase 1's exit criterion is that this
	// count falls toward zero via resolution, not by deletion.
	DanglingTargetCount int      `json:"dangling_target_count"`
	DanglingTargets      []string `json:"dangling_targets"`

	// EmptyIDs is B3: pages whose MarkedUp ID is the empty string, which
	// collide on the "" key in buildIndex's byID map.
	EmptyIDCount int      `json:"empty_id_count"`
	EmptyIDPaths []string `json:"empty_id_paths"`
}

type b1Report struct {
	Pages            int `json:"pages"`
	Relationships    int `json:"relationships"`
	Entities         int `json:"entities"`
	GraphRels        int `json:"graph_relationships"`

	// The B1 mechanism, stated as a count: Tier-1 wikilinks that DO land in
	// Frontmatter.Relationships and are therefore traversable, versus the
	// SemanticRelationships the Tier-2 path writes that nothing reads.
	SemanticRelationshipsTotal int `json:"semantic_relationships_total"`
	SemanticRelationshipsRead  int `json:"semantic_relationships_read"`

	ReachablePages int      `json:"reachable_pages"`
	UnreachableIDs []string `json:"unreachable_page_ids"`
}

func TestPhase0Corpus(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}

	paths := corpusPaths(t, root)
	if len(paths) == 0 {
		t.Fatal("corpus is empty — nothing to baseline")
	}

	pages := make([]*schema.Page, 0, len(paths))
	for _, rel := range paths {
		abs := filepath.Join(root, rel)
		body, err := os.ReadFile(abs)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		page := &schema.Page{Body: string(body), SourcePath: rel}
		enriched, _ := enrich.EnrichPage(page, abs, root, enrich.MergeOptions{})
		pages = append(pages, enriched)
	}

	report := summarizeCorpus(pages)
	b1 := probeB1(pages)

	artifact := corpusArtifact{
		CapturedAt: nowUTC(),
		Corpus:     report,
		B1:         b1,
	}
	// Opt-in, same as the enrich baseline: only rewrite the committed
	// artifact when explicitly asked, so `go test ./...` does not dirty the
	// working tree.
	if *refreshBaseline {
		writeCorpusArtifact(t, root, artifact)
	} else {
		t.Logf("corpus artifact left untouched; re-run with -refresh to rewrite it")
	}

	t.Logf("corpus: %d docs, %d distinct entity types, %d tags", report.DocCount, len(report.EntityTypes), len(report.Tags))
	t.Logf("B1 live: pages=%d relationships=%d graph_rels=%d reachable=%d",
		b1.Pages, b1.Relationships, b1.GraphRels, b1.ReachablePages)
	t.Logf("dangling targets: %d, empty-ID pages: %d", report.DanglingTargetCount, report.EmptyIDCount)
}

// summarizeCorpus records what the Tier-1 pipeline produces over the corpus.
func summarizeCorpus(pages []*schema.Page) corpusReport {
	rep := corpusReport{
		DocCount:            len(pages),
		SourceFiles:         make([]string, 0, len(pages)),
		EntityTypes:         map[string]int{},
		Tags:                map[string]int{},
		RelationshipTargets: map[string]int{},
	}

	// A page's ID is its identity for dangling-target purposes. An empty ID
	// is tracked separately (B3) because it collides rather than dangles.
	ids := make(map[string]struct{}, len(pages))
	for _, p := range pages {
		if p.Frontmatter.ID != "" {
			ids[p.Frontmatter.ID] = struct{}{}
		}
	}

	dangling := map[string]int{}
	for _, p := range pages {
		rep.SourceFiles = append(rep.SourceFiles, p.SourcePath)
		rep.EntityTypes[p.Frontmatter.EntityType]++

		if p.Frontmatter.ID == "" {
			rep.EmptyIDCount++
			rep.EmptyIDPaths = append(rep.EmptyIDPaths, p.SourcePath)
		}

		for _, t := range p.Frontmatter.Tags {
			rep.Tags[t]++
		}
		for _, rel := range p.Frontmatter.Relationships {
			rep.RelationshipTargets[rel.Target]++
			if _, ok := ids[rel.Target]; !ok {
				dangling[rel.Target]++
			}
		}
	}

	rep.DanglingTargetCount = len(dangling)
	for t := range dangling {
		rep.DanglingTargets = append(rep.DanglingTargets, t)
	}
	sort.Strings(rep.DanglingTargets)
	sort.Strings(rep.SourceFiles)
	sort.Strings(rep.EmptyIDPaths)
	return rep
}

// probeB1 is the live equivalent of the 2026-09-19 audit probe. It builds the
// real index and reports what the graph can actually traverse, alongside the
// SemanticRelationships that are on disk but unread.
func probeB1(pages []*schema.Page) b1Report {
	idx := buildIndex(pages)

	rep := b1Report{
		Pages:         idx.Pages(),
		Relationships: idx.Relationships(),
		Entities:      idx.Entities(),
		GraphRels:     len(idx.adjacency),
	}

	// Reachability: a page is reachable if it has a forward edge or is the
	// target of someone else's edge.
	reachable := map[string]struct{}{}
	for id, rels := range idx.adjacency {
		if len(rels) > 0 {
			reachable[id] = struct{}{}
		}
		for _, r := range rels {
			reachable[r.Target] = struct{}{}
		}
	}
	rep.ReachablePages = len(reachable)
	for id := range idx.byID {
		if _, ok := reachable[id]; !ok {
			rep.UnreachableIDs = append(rep.UnreachableIDs, id)
		}
	}
	sort.Strings(rep.UnreachableIDs)

	// The B1 mechanism, quantified: SemanticRelationships exist on the pages
	// but graph_summary and index only ever read Frontmatter.Relationships.
	// We count the first (what is written) and the second (what is read).
	for _, p := range pages {
		rep.SemanticRelationshipsTotal += len(p.Frontmatter.SemanticRelationships)
		rep.SemanticRelationshipsRead += 0 // by construction: nothing reads it
	}
	return rep
}

// corpusPaths returns the D2 corpus: docs/*.md plus every testdata/ fixture.
func corpusPaths(t *testing.T, root string) []string {
	t.Helper()
	var out []string

	docs, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatalf("glob docs: %v", err)
	}
	for _, p := range docs {
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
	}

	td := filepath.Join(root, "testdata")
	err = filepath.Walk(td, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk testdata: %v", err)
	}
	sort.Strings(out)
	return out
}

func writeCorpusArtifact(t *testing.T, root string, v any) {
	t.Helper()
	abs := filepath.Join(root, ".baseline", "phase0-corpus.json")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("corpus artifact written: %s", abs)
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
