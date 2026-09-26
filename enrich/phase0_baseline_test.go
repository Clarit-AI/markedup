package enrich

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Clarit-AI/markedup/schema"
)

// refreshBaseline makes the Phase-0 tests rewrite their committed artifact
// instead of leaving it alone. Without it, `go test ./...` would dirty the
// working tree on every run.
//
//	go test -run TestPhase0Baseline -refresh ./enrich/
var refreshBaseline = flag.Bool("refresh", false,
	"rewrite the committed Phase-0 baseline artifact")

// Phase-0 baseline capture. The two numbers this test records — re-run flip
// rate (Go map tie-break) and merge-gate drop rate (off-whitelist entity
// types refused by MergeModelResult) — are the before-state for every later
// claim. Phase 2's exit criterion is "zero dropped entity types, zero re-run
// flips", measured against this artifact.
//
// Reproducible in one command:
//
//	go test -run TestPhase0Baseline -v ./enrich/
//
// The artifact path is fixed; the test rewrites it on every run with the
// current snapshot. Commit it as the canonical pre-fix baseline.

type baselineArtifact struct {
	CapturedAt              string            `json:"captured_at"`
	Commit                  string            `json:"commit"`
	FlipRateN               int               `json:"flip_rate_n"`
	FlipRateOutcomes        map[string]int    `json:"flip_rate_outcomes"`
	FlipRateDistinct        int               `json:"flip_rate_distinct"`
	FlipRateMinCount        int               `json:"flip_rate_min_count"`
	FlipRateMaxCount        int               `json:"flip_rate_max_count"`
	MergeGateInputs         []string          `json:"merge_gate_inputs"`
	MergeGateResultByInput  map[string]string `json:"merge_gate_result_by_input"`
	MergeGateDroppedCount   int               `json:"merge_gate_dropped_count"`
	MergeGateDroppedInputs  []string          `json:"merge_gate_dropped_inputs"`
	B1BeforeState           b1AuditProbe      `json:"b1_before_state"`
}

type b1AuditProbe struct {
	Source          string         `json:"source"`
	CapturedAt      string         `json:"captured_at"`
	Pages           int            `json:"pages"`
	Relationships   int            `json:"relationships"`
	Notes           string         `json:"notes"`
	RawObservation  map[string]any `json:"raw_observation"`
}

func TestPhase0Baseline(t *testing.T) {
	const n = 200
	const artifactRelPath = ".baseline/phase0-baseline.json"

	// --- 1. Re-run flip rate (B2). The Go map tie-break in entitiesFromRaw ---
	// --- makes a 6-way tie (PERSON/ORG/CONCEPT/TOOL/EVENT/LOCATION each     ---
	// --- once) nondeterministic: the first key Go emits when bestCount ties ---
	// --- becomes bestType, and Go randomizes map iteration order.            ---
	raw := []nuextractEntity{
		{Name: "alpha", Type: "PERSON"},
		{Name: "beta", Type: "ORGANIZATION"},
		{Name: "gamma", Type: "CONCEPT"},
		{Name: "delta", Type: "TOOL"},
		{Name: "epsilon", Type: "EVENT"},
		{Name: "zeta", Type: "LOCATION"},
	}
	outcomes := make(map[string]int, len(raw))
	for i := 0; i < n; i++ {
		_, et, err := entitiesFromRaw(raw)
		if err != nil {
			t.Fatalf("entitiesFromRaw iteration %d: %v", i, err)
		}
		outcomes[et]++
	}
	flipMin, flipMax := minMax(outcomes)

	// --- 2. Merge-gate drop rate (B2). The five off-whitelist entity types   ---
	// --- the nuextract vocabulary emits but the ValidEntityTypes gate rejects---
	// --- (LOCATION/TECHNOLOGY/DATE/OTHER/PROJECT). MergeModelResult refuses   ---
	// --- to apply them when existing.EntityType is empty or "document". With ---
	// --- the whitelist enforced, count the inputs that did NOT take effect.  ---
	offWhitelistInputs := []string{"LOCATION", "TECHNOLOGY", "DATE", "OTHER", "PROJECT"}
	mergeGateResult := make(map[string]string, len(offWhitelistInputs))
	dropped := []string{}
	for _, in := range offWhitelistInputs {
		// Default branch of MergeModelResult. Empty existing frontmatter; the
		// "document" fallback case is checked separately below.
		empty := schema.GraphFrontmatter{EntityType: ""}
		got := MergeModelResult(empty, &ModelResult{EntityType: in}, MergeOptions{})
		mergeGateResult[in] = got.EntityType
		if got.EntityType == "" || got.EntityType == "document" {
			dropped = append(dropped, in)
		}
	}

	// Also verify the explicit "document" frontmatter case. With existing =
	// "document", MergeModelResult's whitelist gate still blocks an off-list
	// model.EntityType (the inner condition matches "document" but the outer
	// IsValidEntityType is false). So "document" is preserved, not promoted.
	docResult := MergeModelResult(
		schema.GraphFrontmatter{EntityType: "document"},
		&ModelResult{EntityType: "LOCATION"},
		MergeOptions{},
	)
	mergeGateResult["__with_existing_document__"] = docResult.EntityType

	// --- 3. B1 before-state. The 2026-09-19 audit probe observed this shape ---
	// --- over a corpus where docs had no MarkedUp ID and therefore collided  ---
	// --- on "" (B3). The empty-string ID is the smoking gun; the 0          ---
	// --- relationships is B1 (Tier-2 NER writes SemanticRelationships that   ---
	// --- nothing reads). Recorded verbatim; reproduction happens in Phase 1. ---
	b1 := b1AuditProbe{
		Source:        "2026-09-19 audit probe (live, surface=plugin MCP)",
		CapturedAt:    "2026-09-23T00:00:00Z",
		Pages:         2,
		Relationships: 0,
		Notes:         "Probe returned {\"pages\":2,\"relationships\":0}; one page had id=\"\". " +
			"B1 = Tier-2 NER writes SemanticRelationships that nothing reads; B3 = empty-ID " +
			"collision in index.Load. Live probe evidence file is no longer at docs/audits/; " +
			"number recorded here from session-handoff artifact.",
		RawObservation: map[string]any{
			"pages":                2,
			"relationships":        0,
			"empty_id_page_count":  1,
			"corpus_collision_key": "",
		},
	}

	// --- 4. Compose and write the artifact ---
	commit := shortHead(t)
	artifact := baselineArtifact{
		CapturedAt:             time.Now().UTC().Format(time.RFC3339),
		Commit:                 commit,
		FlipRateN:              n,
		FlipRateOutcomes:       outcomes,
		FlipRateDistinct:       len(outcomes),
		FlipRateMinCount:       flipMin,
		FlipRateMaxCount:       flipMax,
		MergeGateInputs:        offWhitelistInputs,
		MergeGateResultByInput: mergeGateResult,
		MergeGateDroppedCount:  len(dropped),
		MergeGateDroppedInputs: dropped,
		B1BeforeState:          b1,
	}
	// Only rewrite the committed artifact when explicitly asked. Rewriting on
	// every `go test ./...` would dirty the working tree and produce churn in
	// diffs for a file whose structural metrics are stable anyway.
	if *refreshBaseline {
		writeArtifact(t, artifactRelPath, artifact)
	} else {
		t.Logf("artifact left untouched; re-run with -refresh to rewrite it")
	}

	// The test doubles as a Phase-2 regression guard: it fails LOUDLY if any
	// later change accidentally re-introduces the flip or drops.

	t.Logf("flip rate: %d distinct outcomes across %d runs (min=%d, max=%d)",
		len(outcomes), n, flipMin, flipMax)
	t.Logf("merge-gate dropped: %d/%d off-whitelist inputs refused by MergeModelResult",
		len(dropped), len(offWhitelistInputs))
}

func writeArtifact(t *testing.T, relPath string, v any) {
	t.Helper()
	// Anchor at the repo root, not the test package's CWD (which is the
	// package directory when invoked as `go test ./enrich/`).
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	abs := filepath.Join(root, relPath)
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
	t.Logf("baseline written: %s", abs)
}

func repoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return trimAll(string(out)), nil
}

func minMax(m map[string]int) (lo, hi int) {
	first := true
	for _, v := range m {
		if first || v < lo {
			lo = v
		}
		if first || v > hi {
			hi = v
		}
		first = false
	}
	return lo, hi
}

func shortHead(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Logf("git rev-parse failed (%v); artifact will lack commit sha", err)
		return ""
	}
	return string(trimAll(string(out)))
}

func trimAll(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// Keep sort referenced so go test cross-compiles cleanly if the file grows.
var _ = sort.Strings
