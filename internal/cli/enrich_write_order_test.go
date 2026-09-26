package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

	// Tier 1 frontmatter must be present and correct. The relationship
	// assertion targets the frontmatter field specifically: the body always
	// contains the literal "wikilink" (bodies are preserved verbatim), so
	// asserting on the bare word would pass even with no frontmatter at all.
	assert.Contains(t, content, "id: crash-safety", "Tier 1 frontmatter must be persisted even when Tier 2 fails")
	assert.Contains(t, content, "title: Crash Safety Doc")
	assert.Contains(t, content, "entity-type: document")
	assert.Contains(t, content, "target: wikilink",
		"the [[wikilink]] relationship must be in the frontmatter, not just the preserved body")
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

// The behavioral half of "the next run retries": a file whose Tier 2 failed
// must be REPROCESSED by the next run, not skipped as already complete.
//
// The empty-summary test above pins the mechanism (persistTier1 writes
// preTier2, so the persisted file carries no summary and tier2Complete —
// (Summary != "") — is false). This test runs the command a second time and
// asserts the consequence end to end. Against the pre-repair code the file
// was persisted WITH its summary, the next run then saw it as already
// complete, and the failed Tier 2 was never retried: issue #145's hazard
// reached by an ordinary run, no crash required. The summary skip is what
// keeps this retry loop alive, so this test guards it behaviorally.
//
// Deliberately does NOT re-assert the persisted summary here: that is the
// empty-summary test's job, and keeping the two apart means a "persisted with
// summary" regression trips the retry assertion below, not a duplicate.
func TestRunEnrich_FailedFileIsRetriedOnNextRun(t *testing.T) {
	dir := t.TempDir()
	// Always unparseable: both runs fail Tier 2 the same way, so the only
	// variable across the two runs is whether the file is processed at all.
	srv := writeOrderTestEndpoint(t)

	filePath := filepath.Join(dir, "retry.md")
	require.NoError(t, os.WriteFile(filePath, []byte("# Retry Doc\n\nBody with a #tag.\n"), 0644))

	run := func() string {
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
		return buf.String()
	}

	// First run: Tier 2 fails, the file is queued, and Tier 1 is persisted.
	firstOut := run()
	assert.Contains(t, firstOut, "1 files left unrecovered")
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "id: retry",
		"the first run must persist Tier 1 for the failed file")

	// Second run: the file must be reprocessed — Tier 2 attempted again,
	// queued again, reported failed again — not skipped as already complete.
	secondOut := run()
	assert.Contains(t, secondOut, "Model enriching retry.md",
		"the second run must retry Tier 2 for the failed file, not skip it as already complete")
	assert.Contains(t, secondOut, "1 files left unrecovered",
		"the retried file must be queued and reported again")
	assert.Contains(t, secondOut, "0 skipped. 0 recovered via LLM fallback. 1 failed.",
		"a retried file must be counted failed again, not skipped")

	// And the retry loop must leave the file in the same good state.
	data, err = os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "id: retry",
		"the retried-and-failed file must keep its Tier 1 frontmatter")
}

// A persistTier1 write failure must be SURFACED, not swallowed.
//
// persistTier1 is the run's last chance to leave a failed-Tier-2 file better
// than it found it. It used to assign the write error to `_`, so a full disk
// or a read-only directory produced a "1 failed." line whose reason was the
// Tier-2 parse error — with no hint that the Tier-1 write the run implicitly
// promised never happened. The user could not tell whether Tier 1 landed.
//
// This forces exactly that failure — a read-only directory makes
// WriteFrontmatterFile's temp-file creation fail — and pins three things:
// the Warning line is printed, the file really is untouched (the warning must
// not lie in the other direction either), and the run still reports the
// fallback failure.
func TestRunEnrich_PersistTier1WarnsWhenWriteFails(t *testing.T) {
	// Directory permission bits are the failure lever; they do not map
	// cleanly on Windows, and root ignores them on Unix.
	if runtime.GOOS == "windows" {
		t.Skip("directory write permission bits are not enforced the same way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permission bits")
	}

	dir := t.TempDir()
	original := "# RO Doc\n\nBody with a #tag and [[wikilink]].\n"
	filePath := filepath.Join(dir, "ro.md")
	require.NoError(t, os.WriteFile(filePath, []byte(original), 0644))
	require.NoError(t, os.Chmod(dir, 0555))
	// Registered after TempDir's own cleanup, so LIFO restores writability
	// before TempDir tries to remove the directory.
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	srv := writeOrderTestEndpoint(t)

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

	// The warning must be printed — this is the assertion that fails if a
	// refactor ever goes back to swallowing the error.
	assert.Contains(t, buf.String(), "Warning: could not persist Tier 1 frontmatter for ro.md",
		"a failed Tier-1 write must be surfaced, not silently swallowed")

	// The warning must correspond to reality: nothing landed.
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, original, string(data),
		"the file must be untouched when the Tier-1 write failed")

	// And the fallback failure is still reported.
	assert.Contains(t, buf.String(), "1 files left unrecovered")
	assert.Contains(t, buf.String(), "1 failed.")
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
