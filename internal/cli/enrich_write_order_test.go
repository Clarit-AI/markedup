package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover B5 / #145: the write-ordering defect where Tier 1
// frontmatter was persisted to disk BEFORE the Tier 2 fallback outcome was
// known, so a crash in between left a document in a state strictly worse than
// untouched.
//
// The fix defers the write for any file queued for fallback until the outcome
// is settled. That creates a second obligation, which is what most of this file
// tests: once the outcome IS known and it is "Tier 2 failed", the Tier 1
// enrichment we legitimately produced must still land. Omitting that would
// trade one silent loss for another — the summary would report files left
// unrecovered while those files carried no enrichment at all.

// writeOrderTestEndpoint serves a WELL-FORMED llm.Response whose message
// content is not parseable as a NuExtract result.
//
// The distinction matters. A malformed HTTP body fails to unmarshal into
// llm.Response, which is a transport error and is NOT enqueued for fallback —
// only ErrParse is. So the response must be valid at the transport layer and
// garbage at the content layer, which is exactly the real-world failure this
// path exists for: the model answered, the answer was unusable.
func writeOrderTestEndpoint(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Valid llm.Response. The content is prose, so the NuExtract parser
		// raises ErrParse and the file is queued for the fallback batch.
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant",` +
			`"content":"I'm sorry, I cannot produce that output."}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// resetEnrichFlags restores the flag globals to defaults. The enrich command
// writes to package-level vars, so tests must reset them explicitly.
func resetEnrichFlags() {
	enrichDryRun = false
	enrichForce = false
	enrichSkipExist = false
	enrichModel = ""
	enrichEndpoint = ""
	enrichAPIKey = ""
	enrichTimeout = 30 * time.Second
	enrichFormat = ""
	enrichNuExtractMode = ""
	enrichNuExtractTransport = ""
	enrichApplyFallback = ""
	enrichAssumeNo = true
	enrichFallbackParallel = false
	enrichFallbackParallelSet = false
}

// The regression guard for the bug this file exists to prevent: a file whose
// Tier 2 extraction fails must STILL receive its Tier 1 frontmatter.
//
// Before the fix this passed for the wrong reason (the main loop wrote
// unconditionally). After deferring the write it would fail, because nothing
// downstream wrote the failure path. It is the guard that keeps both halves of
// the change honest.
func TestRunEnrich_FailedTier2StillPersistsTier1(t *testing.T) {
	dir := t.TempDir()
	srv := writeOrderTestEndpoint(t)

	plain := "# Crash Safety Doc\n\nBody with a #tag and a [[wikilink]].\n"
	filePath := filepath.Join(dir, "crash-safety.md")
	require.NoError(t, os.WriteFile(filePath, []byte(plain), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{
		dir,
		"--model", "test-model",
		"--format", "nuextract",
		"--endpoint", srv.URL,
		"--timeout", "10s",
	})
	resetEnrichFlags()
	// resetEnrichFlags must run before SetArgs-consuming Execute, but the
	// command reads the globals directly, so re-assert the ones SetArgs sets.
	enrichModel = "test-model"
	enrichEndpoint = srv.URL
	enrichTimeout = 10 * time.Second

	err := cmd.Execute()
	require.NoError(t, err)

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	content := string(data)

	// Tier 1 frontmatter must be present and correct.
	assert.Contains(t, content, "id: crash-safety", "Tier 1 frontmatter must be persisted even when Tier 2 fails")
	assert.Contains(t, content, "title: Crash Safety Doc")
	assert.Contains(t, content, "entity-type: document")
	assert.Contains(t, content, "wikilink")
}

// A queued file must be written with Tier-1 content and an EMPTY summary.
//
// The empty summary is load-bearing, not incidental. enrich.tier2Complete is
// (Summary != ""), so persisting a summary would make the file look
// Tier-2-complete; the next run would skip it as "already complete" and never
// retry the Tier 2 that just failed. That is issue #145's hazard reached by an
// ordinary successful run rather than a crash.
//
// Before this was pinned, a file whose summary call happened to succeed was
// written WITH the summary and became permanently stuck.
func TestRunEnrich_QueuedFileLeavesSummaryEmptySoNextRunRetries(t *testing.T) {
	dir := t.TempDir()
	srv := writeOrderTestEndpoint(t)

	filePath := filepath.Join(dir, "stuck.md")
	require.NoError(t, os.WriteFile(filePath,
		[]byte("# Stuck Doc\n\nBody with a #tag.\n"), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--model", "test-model", "--format", "nuextract",
		"--endpoint", srv.URL, "--timeout", "10s"})
	resetEnrichFlags()
	enrichModel = "test-model"
	enrichFormat = "nuextract"
	enrichEndpoint = srv.URL
	enrichTimeout = 10 * time.Second

	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	content := string(data)

	// Tier 1 content is present...
	assert.Contains(t, content, "id: stuck")
	assert.Contains(t, content, "entity-type: document")
	// ...and the summary is empty, so tier2Complete is false and a later run
	// will re-attempt Tier 2 instead of skipping the file forever.
	assert.Contains(t, content, `summary: ""`,
		"a queued file must not be persisted with a summary, or it looks Tier-2-complete and is never retried")
}

// Summary generation must be SKIPPED for queued files, not merely discarded.
//
// It used to run unconditionally, so every queued file paid for an LLM call
// whose output was thrown away on all five persistTier1 paths.
func TestRunEnrich_QueuedFileSkipsSummaryGeneration(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant",` +
			`"content":"unusable"}}]}`))
	}))
	defer srv.Close()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "doc.md"),
		[]byte("# Doc\n\nBody.\n"), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	// --nuextract-mode single makes one document cost exactly one request.
	// The default parallel mode issues a separate entities AND relations
	// request, which would make the call count ambiguous.
	cmd.SetArgs([]string{dir, "--model", "m", "--format", "nuextract",
		"--nuextract-mode", "single", "--endpoint", srv.URL, "--timeout", "10s"})
	resetEnrichFlags()
	enrichModel = "m"
	enrichFormat = "nuextract"
	enrichNuExtractMode = "single"
	enrichEndpoint = srv.URL
	enrichTimeout = 10 * time.Second

	require.NoError(t, cmd.Execute())

	// Exactly one call: the single extraction request, which is what produced
	// the parse error. A second would be the summary call we now skip.
	assert.Equal(t, int32(1), calls.Load(),
		"a file queued for fallback must not pay for a summary it will discard")
}

// A non-queued file still gets its summary — the skip must not leak.
func TestRunEnrich_NonQueuedFileStillGeneratesSummary(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	// This endpoint answers the entities call with valid JSON and the summary
	// call with a summary, so nothing is queued.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant",` +
			`"content":"{\"entities\":[],\"summary\":\"A real summary.\"}"}}]}`))
	}))
	defer srv.Close()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "doc.md"),
		[]byte("# Doc\n\nBody.\n"), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--model", "m", "--format", "nuextract",
		"--endpoint", srv.URL, "--timeout", "10s"})
	resetEnrichFlags()
	enrichModel = "m"
	enrichFormat = "nuextract"
	enrichEndpoint = srv.URL
	enrichTimeout = 10 * time.Second

	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(filepath.Join(dir, "doc.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "A real summary.",
		"a file that was never queued must keep its generated summary")
}

// Dry run with a file queued for fallback must not panic and must not write.
//
// This is the test the previous dry-run test could not be: it configured no
// model, so nothing was ever queued and the queue-reporting branch was
// unreachable. Configuring a model that returns a parse error reaches it, and
// it used to panic with "index out of range" — because the dry-run branch
// `continue`s before appending a results row, so a queued file's resultIdx
// pointed one past the end, or at a row belonging to a different file.
func TestRunEnrich_DryRunWithQueuedFileDoesNotPanicOrWrite(t *testing.T) {
	dir := t.TempDir()
	srv := writeOrderTestEndpoint(t)

	original := "# Dry Doc\n\nBody with a #tag and [[wikilink]].\n"
	queued := filepath.Join(dir, "queued.md")
	require.NoError(t, os.WriteFile(queued, []byte(original), 0644))
	// A second file, also queued (the endpoint always returns the same
	// unparseable content), so dry run must leave it untouched too. Note it
	// does NOT give the old indexing bug a row to land on: queued files get
	// no results row in dry run, so the pre-fix code panicked on the very
	// first queue entry. The silent variant — where a skipped file's row IS
	// overwritten and that file vanishes from the report — needs a file that
	// produces a row of its own; that is what
	// TestRunEnrich_DryRunQueuedFileLeavesOtherRowsIntact below covers.
	other := filepath.Join(dir, "other.md")
	require.NoError(t, os.WriteFile(other, []byte("# Other\n\nBody.\n"), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--dry-run", "--model", "m", "--format", "nuextract",
		"--endpoint", srv.URL, "--timeout", "10s"})
	resetEnrichFlags()
	enrichModel = "m"
	enrichFormat = "nuextract"
	enrichEndpoint = srv.URL
	enrichDryRun = true
	enrichTimeout = 10 * time.Second

	require.NotPanics(t, func() {
		require.NoError(t, cmd.Execute())
	}, "dry run with a queued file panicked — the pending row is indexed, not appended")

	// Nothing may be written, and the pending file must still be reported.
	// Each file is compared against its OWN original content.
	otherOriginal := "# Other\n\nBody.\n"
	for _, tc := range []struct{ path, want string }{
		{queued, original},
		{other, otherOriginal},
	} {
		data, err := os.ReadFile(tc.path)
		require.NoError(t, err)
		assert.Equal(t, tc.want, string(data),
			"dry run must not modify %s", filepath.Base(tc.path))
	}
	assert.Contains(t, buf.String(), "would be queued for LLM fallback",
		"the queued file must be reported")
}

// The silent variant of the dry-run indexing bug: not a panic, a clobber.
//
// When a file processed AFTER the queued one appends a results row of its own
// (here: skipped by --skip-existing), the queued file's resultIdx pointed at
// THAT row, so the pre-fix dry-run block overwrote it — the skipped file
// vanished from the JSON report and the command still exited 0. The fix must
// report BOTH files, each with its own status.
//
// Walk order matters and is lexical: a-queued.md is processed first, so it is
// enqueued with resultIdx 0 before b-skipped.md appends the only pre-existing
// row. (A queued file that sorted second would make the pre-fix code panic
// instead — that variant is the test above.)
func TestRunEnrich_DryRunQueuedFileLeavesOtherRowsIntact(t *testing.T) {
	dir := t.TempDir()
	srv := writeOrderTestEndpoint(t)

	queued := filepath.Join(dir, "a-queued.md")
	require.NoError(t, os.WriteFile(queued,
		[]byte("# Queued Doc\n\n#tag and [[wikilink]].\n"), 0644))
	skipped := filepath.Join(dir, "b-skipped.md")
	require.NoError(t, os.WriteFile(skipped,
		[]byte("---\nid: b-skipped\ntitle: Skipped\n---\n# Skipped\n\nBody.\n"), 0644))

	// The per-file rows are only printed with --json, a root-level flag the
	// detached enrich command does not carry; runEnrich reads the jsonOutput
	// package global, so set it directly.
	jsonOutput = true
	defer func() { jsonOutput = false }()

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{
		dir,
		"--dry-run",
		"--skip-existing",
		"--model", "test-model",
		"--format", "nuextract",
		"--endpoint", srv.URL,
		"--timeout", "10s",
	})
	resetEnrichFlags()
	enrichDryRun = true
	enrichSkipExist = true
	enrichModel = "test-model"
	enrichFormat = "nuextract"
	enrichEndpoint = srv.URL
	enrichTimeout = 10 * time.Second

	// Pre-fix this did NOT panic — that is the point: the failure was
	// silent, so it must be caught in the report, asserted below.
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "Dry-run: 1 files would be queued for LLM fallback.",
		"the queued file must be reported")

	// Both files must appear in the JSON report, each with its own status.
	idx := strings.Index(buf.String(), "{\n  \"enriched\"")
	require.GreaterOrEqual(t, idx, 0, "no JSON report found in output:\n%s", buf.String())
	var report struct {
		Files []enrichResult `json:"files"`
	}
	require.NoError(t, json.Unmarshal([]byte(buf.String()[idx:]), &report))
	require.Len(t, report.Files, 2,
		"both files must be reported; pre-fix the queued file's row overwrote the skipped file's row. Output:\n%s", buf.String())
	byPath := map[string]string{}
	for _, f := range report.Files {
		byPath[f.Path] = f.Status
	}
	assert.Equal(t, "fallback-pending-dry-run", byPath[queued])
	assert.Equal(t, "skipped", byPath[skipped])
}

// A dry run that queues nothing must still write nothing.
func TestRunEnrich_DryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	original := "# Untouched\n\n#tag and [[link]].\n"
	filePath := filepath.Join(dir, "untouched.md")
	require.NoError(t, os.WriteFile(filePath, []byte(original), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--dry-run"})
	resetEnrichFlags()
	enrichDryRun = true
	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, original, string(data), "dry-run must not modify the file")
}
