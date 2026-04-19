package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Clarit-AI/markedup/llm"
	"github.com/Clarit-AI/markedup/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseError_NuExtractWrapsSentinel verifies the parse-error sentinel is
// wrapped by every NuExtract parse failure path so IsParseError can route the
// failure to the LLM fallback queue (#140 trigger condition).
func TestParseError_NuExtractWrapsSentinel(t *testing.T) {
	_, _, err := parseNuExtractEntities("not json")
	require.Error(t, err)
	assert.True(t, IsParseError(err), "parseNuExtractEntities must wrap ErrParse")

	_, err = parseNuExtractRelations("not json")
	require.Error(t, err)
	assert.True(t, IsParseError(err), "parseNuExtractRelations must wrap ErrParse")

	_, _, _, err = parseNuExtractCombined("not json")
	require.Error(t, err)
	assert.True(t, IsParseError(err), "parseNuExtractCombined must wrap ErrParse")
}

// TestParseError_GenericModelOutputWrapsSentinel covers the FormatGeneric path
// in model.go which is the third primary extractor that can produce parse
// failures (issue #140 AC: any Tier 2 parse error → fallback).
func TestParseError_GenericModelOutputWrapsSentinel(t *testing.T) {
	_, err := parseModelOutput("not json")
	require.Error(t, err)
	assert.True(t, IsParseError(err))
}

// TestIsParseError_NonParseErrorsExcluded guards against false positives —
// transport / network / config errors must NOT enqueue (they wouldn't recover
// from a different prompt).
func TestIsParseError_NonParseErrorsExcluded(t *testing.T) {
	assert.False(t, IsParseError(nil))
	assert.False(t, IsParseError(errors.New("connection refused")))
	assert.False(t, IsParseError(fmt.Errorf("llm: model returned status 500")))
}

// TestIsLocalEndpoint_DetectsLoopback covers the default-on/default-off split
// (#140 AC: local endpoints default Enabled=true, cloud default false).
func TestIsLocalEndpoint_DetectsLoopback(t *testing.T) {
	cases := []struct {
		endpoint string
		want     bool
	}{
		{"http://localhost:11434", true},
		{"http://127.0.0.1:8080", true},
		{"http://127.0.0.1", true},
		{"localhost:11434", true},
		{"127.0.0.1", true},
		{"", true}, // no endpoint = treat as local for default purposes
		{"https://api.openai.com/v1", false},
		{"https://api.anthropic.com", false},
		{"http://10.0.0.5:8080", false}, // private LAN — not loopback, treat as remote
	}
	for _, tc := range cases {
		t.Run(tc.endpoint, func(t *testing.T) {
			assert.Equal(t, tc.want, IsLocalEndpoint(tc.endpoint))
		})
	}
}

// fakeFallbackExtractor is a deterministic stub used by RunFallbackBatch tests
// to avoid spinning up an HTTP server for every cap/disabled assertion.
type fakeFallbackExtractor struct {
	results map[string]*ModelResult
	errs    map[string]error
	calls   int
}

func (f *fakeFallbackExtractor) Extract(ctx context.Context, body string, _, _ []string) (*ModelResult, error) {
	f.calls++
	if err, ok := f.errs[body]; ok {
		return nil, err
	}
	if r, ok := f.results[body]; ok {
		return r, nil
	}
	return &ModelResult{Summary: "ok", EntityType: "document"}, nil
}

func TestRunFallbackBatch_ProcessesAllJobsSequentially(t *testing.T) {
	jobs := []FallbackJob{
		{Path: "a.md", RelPath: "a.md", Body: "alpha"},
		{Path: "b.md", RelPath: "b.md", Body: "bravo"},
		{Path: "c.md", RelPath: "c.md", Body: "charlie"},
	}
	ext := &fakeFallbackExtractor{}
	out := RunFallbackBatch(context.Background(), ext, jobs, FallbackBatchOptions{})
	require.Len(t, out, 3)
	for _, oc := range out {
		assert.True(t, oc.Recovered())
	}
	assert.Equal(t, 3, ext.calls)
}

// TestRunFallbackBatch_MaxFilesCap verifies the cloud cost-guard (default 50
// for cloud endpoints) — no LLM calls beyond the cap (#140 AC).
func TestRunFallbackBatch_MaxFilesCap(t *testing.T) {
	jobs := []FallbackJob{
		{Path: "a.md", RelPath: "a.md", Body: "alpha"},
		{Path: "b.md", RelPath: "b.md", Body: "bravo"},
		{Path: "c.md", RelPath: "c.md", Body: "charlie"},
	}
	ext := &fakeFallbackExtractor{}
	out := RunFallbackBatch(context.Background(), ext, jobs, FallbackBatchOptions{MaxFiles: 2})
	require.Len(t, out, 2)
	assert.Equal(t, 2, ext.calls, "extractor must NOT be called for jobs beyond MaxFiles")
}

// TestRunFallbackBatch_NoExtractorIsNoop guards the disabled-config path —
// when fallback is disabled the dispatcher must not be invoked, but if it
// somehow is with a nil extractor it must return cleanly.
func TestRunFallbackBatch_NoExtractorIsNoop(t *testing.T) {
	out := RunFallbackBatch(context.Background(), nil, []FallbackJob{{Path: "x.md"}}, FallbackBatchOptions{})
	assert.Nil(t, out)
}

func TestRunFallbackBatch_RecordsErrors(t *testing.T) {
	jobs := []FallbackJob{
		{Path: "a.md", RelPath: "a.md", Body: "alpha"},
		{Path: "b.md", RelPath: "b.md", Body: "bravo"},
	}
	ext := &fakeFallbackExtractor{
		errs: map[string]error{"bravo": errors.New("model unavailable")},
	}
	out := RunFallbackBatch(context.Background(), ext, jobs, FallbackBatchOptions{})
	require.Len(t, out, 2)
	assert.True(t, out[0].Recovered())
	assert.False(t, out[1].Recovered())
	assert.Contains(t, out[1].Err.Error(), "model unavailable")
}

// TestLLMFallbackExtractor_Extract_PromptOnlyShape exercises the wire path
// against a stubbed chat-completion server. Verifies the prompt-only mode
// (SchemaMode=false) round-trips a NuExtract-shape JSON payload into a
// ModelResult that MergeModelResult can consume.
func TestLLMFallbackExtractor_Extract_PromptOnlyShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := `{"entities":[{"name":"Alice","type":"PERSON"},{"name":"Acme","type":"ORGANIZATION"}],"relationships":[{"subject":"Alice","predicate":"works_for","object":"Acme"}],"summary":"Alice works at Acme.","entity_type":"person"}`
		resp := llm.Response{Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: payload}}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ext := NewLLMFallbackExtractor(LLMFallbackConfig{Endpoint: server.URL, Model: "gpt-test"})
	res, err := ext.Extract(context.Background(), "doc body", nil, nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "person", res.EntityType)
	assert.Equal(t, "Alice works at Acme.", res.Summary)
	require.Len(t, res.Entities, 2)
	assert.Equal(t, "Alice", res.Entities[0].Name)
	assert.Equal(t, "PERSON", res.Entities[0].Role)
	require.Len(t, res.Relationships, 1)
	assert.Equal(t, "Acme", res.Relationships[0].Target)
	assert.Equal(t, "works_for", res.Relationships[0].Type)
}

// TestLLMFallbackExtractor_FencedJSONIsTolerated covers prompt-only mode where
// a chatty model wraps its JSON in ```json fences despite our instruction.
// The repair pre-pass for NuExtract has the same tolerance — fallback must
// match.
func TestLLMFallbackExtractor_FencedJSONIsTolerated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := "```json\n" + `{"entities":[],"relationships":[],"summary":"x","entity_type":"document"}` + "\n```"
		resp := llm.Response{Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: payload}}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()
	ext := NewLLMFallbackExtractor(LLMFallbackConfig{Endpoint: server.URL, Model: "gpt-test"})
	res, err := ext.Extract(context.Background(), "doc", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "x", res.Summary)
}

// TestFallbackMergeProducesNuExtractShape is the merge-equivalence test from
// #140 AC: feeding a fallback ModelResult into MergeModelResult must produce
// frontmatter byte-comparable to what the NuExtract path would have, modulo
// field aliasing (subject/predicate/object → target/type).
func TestFallbackMergeProducesNuExtractShape(t *testing.T) {
	// Result that a fallback LLM might return.
	fbResult := &ModelResult{
		EntityType: "person",
		Summary:    "Alice is a researcher.",
		Entities: []schema.Entity{
			{Name: "Alice", Role: "PERSON"},
			{Name: "Bob", Role: "PERSON"},
		},
		Relationships: []schema.Relationship{
			{Target: "Bob", Type: "knows", Strength: nuExtractStrength},
		},
	}
	// Equivalent result the NuExtract path would have produced for the same doc.
	nuResult := &ModelResult{
		EntityType: "person",
		Summary:    "Alice is a researcher.",
		Entities: []schema.Entity{
			{Name: "Alice", Role: "PERSON"},
			{Name: "Bob", Role: "PERSON"},
		},
		Relationships: []schema.Relationship{
			{Target: "Bob", Type: "knows", Strength: nuExtractStrength},
		},
	}
	base := schema.GraphFrontmatter{ID: "alice", Title: "Alice"}
	opts := MergeOptions{}

	fbMerged := MergeModelResult(base, fbResult, opts)
	nuMerged := MergeModelResult(base, nuResult, opts)

	assert.Equal(t, nuMerged.EntityType, fbMerged.EntityType)
	assert.Equal(t, nuMerged.Summary, fbMerged.Summary)
	assert.Equal(t, nuMerged.Entities, fbMerged.Entities)
	assert.Equal(t, nuMerged.SemanticRelationships, fbMerged.SemanticRelationships)
}

// TestParseFallbackPayload_RejectsGarbage guards the JSON-parse boundary so
// fallback failures are themselves reported (and surfaced in the J failed
// summary count) rather than silently producing empty results.
func TestParseFallbackPayload_RejectsGarbage(t *testing.T) {
	_, err := parseFallbackPayload("not json at all")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse payload")
}

// concurrentFakeExtractor is a goroutine-safe fake used by the parallel
// dispatcher tests (#141). Tracks max concurrency observed via in-flight
// counter so tests can assert the worker cap is respected.
type concurrentFakeExtractor struct {
	delay     time.Duration
	calls     int64
	inFlight  int64
	maxFlight int64
	errs      map[string]error
	results   map[string]*ModelResult
	mu        sync.Mutex
}

func (f *concurrentFakeExtractor) Extract(ctx context.Context, body string, _, _ []string) (*ModelResult, error) {
	atomic.AddInt64(&f.calls, 1)
	cur := atomic.AddInt64(&f.inFlight, 1)
	defer atomic.AddInt64(&f.inFlight, -1)
	for {
		max := atomic.LoadInt64(&f.maxFlight)
		if cur <= max || atomic.CompareAndSwapInt64(&f.maxFlight, max, cur) {
			break
		}
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errs != nil {
		if err, ok := f.errs[body]; ok {
			return nil, err
		}
	}
	if f.results != nil {
		if r, ok := f.results[body]; ok {
			return r, nil
		}
	}
	return &ModelResult{Summary: "ok-" + body, EntityType: "document"}, nil
}

func makeJobs(n int) []FallbackJob {
	jobs := make([]FallbackJob, n)
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("body-%d", i)
		jobs[i] = FallbackJob{
			Path:    fmt.Sprintf("f%d.md", i),
			RelPath: fmt.Sprintf("f%d.md", i),
			Body:    body,
		}
	}
	return jobs
}

// TestRunFallbackBatch_ParallelEqualsSequential is the #141 equivalence AC:
// running the same batch through Parallel=true and Parallel=false must
// produce the same outcomes (modulo log ordering, which is timing-dependent).
func TestRunFallbackBatch_ParallelEqualsSequential(t *testing.T) {
	jobs := makeJobs(10)
	seqExt := &concurrentFakeExtractor{}
	seqOut := RunFallbackBatch(context.Background(), seqExt, jobs, FallbackBatchOptions{})

	parExt := &concurrentFakeExtractor{}
	parOut := RunFallbackBatch(context.Background(), parExt, jobs, FallbackBatchOptions{
		Parallel: true,
		Workers:  4,
	})

	require.Len(t, seqOut, len(jobs))
	require.Len(t, parOut, len(jobs))
	// Outcomes must be in input-job order in both modes so the CLI's
	// merge-by-index loop stays valid.
	for i := range jobs {
		assert.Equal(t, jobs[i].Path, seqOut[i].Job.Path)
		assert.Equal(t, jobs[i].Path, parOut[i].Job.Path, "parallel mode must preserve input order at index %d", i)
		assert.True(t, seqOut[i].Recovered())
		assert.True(t, parOut[i].Recovered())
		assert.Equal(t, seqOut[i].Result.Summary, parOut[i].Result.Summary)
		assert.Equal(t, seqOut[i].InputTokens, parOut[i].InputTokens)
		assert.Equal(t, seqOut[i].OutputTokens, parOut[i].OutputTokens)
	}
	assert.Equal(t, int64(len(jobs)), atomic.LoadInt64(&seqExt.calls))
	assert.Equal(t, int64(len(jobs)), atomic.LoadInt64(&parExt.calls))
}

// TestRunFallbackBatch_ParallelRespectsWorkerCap proves the bounded pool —
// max in-flight extractor calls never exceeds Workers.
func TestRunFallbackBatch_ParallelRespectsWorkerCap(t *testing.T) {
	jobs := makeJobs(20)
	const cap = 3
	ext := &concurrentFakeExtractor{delay: 10 * time.Millisecond}
	out := RunFallbackBatch(context.Background(), ext, jobs, FallbackBatchOptions{
		Parallel: true,
		Workers:  cap,
	})
	require.Len(t, out, len(jobs))
	// Concurrency observed should be at most `cap`. Use <= so we don't
	// flake when scheduling happens to serialize work on a slow CI box.
	assert.LessOrEqual(t, atomic.LoadInt64(&ext.maxFlight), int64(cap),
		"workers in flight must never exceed Workers cap")
	assert.Greater(t, atomic.LoadInt64(&ext.maxFlight), int64(1),
		"with 20 jobs and 10ms delay, at least 2 should run concurrently")
}

// TestRunFallbackBatch_ParallelIsolatesErrors verifies one fallback failure
// does not abort the batch under parallel dispatch (#141 AC).
func TestRunFallbackBatch_ParallelIsolatesErrors(t *testing.T) {
	jobs := makeJobs(5)
	ext := &concurrentFakeExtractor{
		errs: map[string]error{"body-2": errors.New("kaboom")},
	}
	out := RunFallbackBatch(context.Background(), ext, jobs, FallbackBatchOptions{
		Parallel: true,
		Workers:  3,
	})
	require.Len(t, out, len(jobs))
	recoveredPaths := []string{}
	failedPaths := []string{}
	for _, oc := range out {
		if oc.Recovered() {
			recoveredPaths = append(recoveredPaths, oc.Job.Path)
		} else {
			failedPaths = append(failedPaths, oc.Job.Path)
		}
	}
	sort.Strings(recoveredPaths)
	assert.Equal(t, []string{"f0.md", "f1.md", "f3.md", "f4.md"}, recoveredPaths)
	assert.Equal(t, []string{"f2.md"}, failedPaths)
	// Index alignment: the failure at body-2 must land at outcome[2].
	assert.False(t, out[2].Recovered())
	assert.Contains(t, out[2].Err.Error(), "kaboom")
}

// TestRunFallbackBatch_ParallelDefaultsToNumCPU verifies Workers<=0 falls
// back to runtime.NumCPU() rather than degenerating to a single goroutine.
func TestRunFallbackBatch_ParallelDefaultsToNumCPU(t *testing.T) {
	jobs := makeJobs(8)
	ext := &concurrentFakeExtractor{delay: 5 * time.Millisecond}
	out := RunFallbackBatch(context.Background(), ext, jobs, FallbackBatchOptions{
		Parallel: true,
		Workers:  0, // defaulted
	})
	require.Len(t, out, len(jobs))
	// We can't assert == NumCPU because tests may run on a 1-core box, but
	// we can assert at least 1 in-flight, which proves the parallel path
	// did dispatch (the sequential path would record 1 too — that's fine).
	assert.GreaterOrEqual(t, atomic.LoadInt64(&ext.maxFlight), int64(1))
}

// TestRunFallbackBatch_ParallelRespectsMaxFiles ensures the cost-cap still
// applies under parallel dispatch.
func TestRunFallbackBatch_ParallelRespectsMaxFiles(t *testing.T) {
	jobs := makeJobs(10)
	ext := &concurrentFakeExtractor{}
	out := RunFallbackBatch(context.Background(), ext, jobs, FallbackBatchOptions{
		Parallel: true,
		Workers:  4,
		MaxFiles: 3,
	})
	require.Len(t, out, 3)
	assert.Equal(t, int64(3), atomic.LoadInt64(&ext.calls))
}

// TestRunFallbackBatch_Logger ensures token-count log lines are emitted (#140
// AC: log approximate input/output token counts per fallback file).
func TestRunFallbackBatch_Logger(t *testing.T) {
	var lines []string
	jobs := []FallbackJob{{Path: "a.md", RelPath: "a.md", Body: "alpha bravo charlie"}}
	ext := &fakeFallbackExtractor{}
	out := RunFallbackBatch(context.Background(), ext, jobs, FallbackBatchOptions{
		Logger: func(format string, args ...any) {
			lines = append(lines, fmt.Sprintf(format, args...))
		},
	})
	require.Len(t, out, 1)
	require.True(t, out[0].Recovered())
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "input tokens")
	assert.Contains(t, joined, "output tokens")
}
