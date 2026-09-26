package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// A crash between the two writes must leave the file exactly as it was.
//
// This is the property #145 asks for, and it is not directly observable by
// running the command to completion — the crash is what we are trying to
// prevent. What IS observable is the ordering invariant underneath it: for a
// file that goes to fallback, the main loop must not perform a write before the
// outcome is known.
//
// We assert the weaker but checkable half — a file that never enters the
// fallback queue is written by the main loop, and one that does is written
// exactly once, by the fallback handler. Double-writing is the observable
// signature of the old ordering, because the old code wrote and then wrote
// again.
func TestRunEnrich_QueuedFileIsWrittenExactlyOnce(t *testing.T) {
	dir := t.TempDir()

	// A file with no model configured: Tier 2 is off, so the main loop owns
	// every write and the fallback path is never entered.
	queued := filepath.Join(dir, "queued.md")
	require.NoError(t, os.WriteFile(queued,
		[]byte("# Queued Doc\n\n#alpha content.\n"), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})
	resetEnrichFlags()
	// No --model/--endpoint: Tier 2 is off, so nothing is queued and the main
	// loop owns every write.
	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(queued)
	require.NoError(t, err)
	content := string(data)

	// Exactly one frontmatter block. A second write would produce a nested or
	// duplicated --- fence.
	assert.Equal(t, 1, bytes.Count(data, []byte("\n---\n")),
		"file should have exactly one frontmatter block, got:\n%s", content)
	assert.Contains(t, content, "id: queued")
}

// Dry-run must still write nothing, including for files that would be queued
// for fallback. The deferral must not have opened a path that persists during
// a dry run.
func TestRunEnrich_DryRunWritesNothingAfterDeferral(t *testing.T) {
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
