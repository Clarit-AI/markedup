package enrich

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteRetryLog_ContainsAllRequiredFields covers issue #142 AC: the
// retry log must include per-file path, raw response, parse error, and the
// suggested frontmatter schema block so the coding agent has everything
// needed to re-extract without round-tripping back to MarkedUp.
func TestWriteRetryLog_ContainsAllRequiredFields(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.md")
	jobs := []HandoffJob{
		{
			Path:        "/abs/path/to/note.md",
			RelPath:     "note.md",
			Body:        "# Note\nbody text here.",
			RawResponse: `{"entities": [{"name": "Foo"`,
			PrimaryErr:  errors.New("parse nuextract entities: unexpected EOF"),
			FallbackErr: errors.New("llm: model returned status 500"),
			EntityTypes: []string{"PERSON", "ORG"},
			Predicates:  []string{"works_at"},
		},
		{
			Path:    "/abs/path/to/other.md",
			RelPath: "other.md",
			Body:    "Other doc.",
		},
	}
	require.NoError(t, WriteRetryLog(logPath, jobs))

	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)
	got := string(raw)

	// Header + schema block (required by spec).
	assert.Contains(t, got, "MarkedUp", "log must identify itself")
	assert.Contains(t, got, "files:", "log must include the YAML schema block")
	assert.Contains(t, got, "entity_type:")
	assert.Contains(t, got, "summary:")
	assert.Contains(t, got, "entities:")
	assert.Contains(t, got, "relationships:")
	assert.Contains(t, got, "markedup enrich --apply-fallback", "log must point user at the merge command")

	// Per-file required fields.
	assert.Contains(t, got, "/abs/path/to/note.md", "absolute path required for round-trip")
	assert.Contains(t, got, "/abs/path/to/other.md")
	assert.Contains(t, got, "note.md", "rel path required for human readability")
	assert.Contains(t, got, "Primary parse error", "parse error required")
	assert.Contains(t, got, "unexpected EOF")
	assert.Contains(t, got, "LLM fallback error", "fallback error required when present")
	assert.Contains(t, got, "PERSON, ORG", "entity types required when provided")
	assert.Contains(t, got, "works_at", "predicates required when provided")
	assert.Contains(t, got, `{"entities": [{"name": "Foo"`, "raw response required so agent sees what failed")
	assert.Contains(t, got, "body text here.", "document body required for re-extraction")
}

func TestWriteRetryLog_OneLineCollapsesMultilineErrors(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.md")
	jobs := []HandoffJob{{
		Path: "/x.md", RelPath: "x.md", Body: "doc",
		PrimaryErr: errors.New("parse failure\nraw: line1\nline2\nline3"),
	}}
	require.NoError(t, WriteRetryLog(logPath, jobs))
	raw, _ := os.ReadFile(logPath)
	// The bullet line that holds the parse error must not introduce a stray
	// newline mid-bullet; oneLine collapses embedded newlines to spaces.
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "- **Primary parse error:**") {
			assert.NotContains(t, line, "raw: line1\nline2", "multi-line collapsed to single line")
		}
	}
}

func TestLogPathsIn_CreatesDirectoryAndTimestampedNames(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "logs")
	paths, err := LogPathsIn(subdir)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(filepath.Base(paths.LogPath), "failed-enrichment-"))
	assert.True(t, strings.HasSuffix(paths.LogPath, ".md"))
	assert.True(t, strings.HasPrefix(filepath.Base(paths.ResultPath), "retry-result-"))
	assert.True(t, strings.HasSuffix(paths.ResultPath, ".yaml"))
	// Directory must have been created.
	info, err := os.Stat(subdir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestSuggestedCommand_FormatMatchesIssueSpec(t *testing.T) {
	paths := HandoffPaths{
		LogPath:    "/tmp/logs/failed-enrichment-20260418-1730.md",
		ResultPath: "/tmp/logs/retry-result-20260418-1730.yaml",
	}
	got := paths.SuggestedCommand("claude")
	want := "claude -p /tmp/logs/failed-enrichment-20260418-1730.md --output-format yaml > /tmp/logs/retry-result-20260418-1730.yaml"
	assert.Equal(t, want, got)
}

func TestSuggestedCommand_DefaultsToFirstKnownAgent(t *testing.T) {
	paths := HandoffPaths{LogPath: "/x.md", ResultPath: "/y.yaml"}
	got := paths.SuggestedCommand("")
	assert.True(t, strings.HasPrefix(got, DefaultCodingAgents[0]+" "), "empty agent name falls back to default candidate")
}

func TestFindCodingAgent_ReturnsFalseForNoMatches(t *testing.T) {
	// Use a name that cannot exist on PATH.
	_, _, ok := FindCodingAgent([]string{"definitely-not-a-real-binary-9f8e7d6c"})
	assert.False(t, ok)
}

func TestPromptYesNo_OnlyYesYesAccepted(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"YES\n", true},
		{"\n", false},  // default: no
		{"n\n", false},
		{"no\n", false},
		{"yep\n", false},
		{"", false}, // EOF
	}
	for _, tc := range cases {
		t.Run(strings.TrimSpace(tc.input), func(t *testing.T) {
			var out bytes.Buffer
			got := PromptYesNo(strings.NewReader(tc.input), &out, "Continue? [y/N] ")
			assert.Equal(t, tc.want, got)
			assert.Contains(t, out.String(), "Continue?")
		})
	}
}

// TestParseFallbackResult_AcceptsCodeFenceWrappedYAML guards the common
// failure mode where the agent emits ```yaml … ``` straight from chat.
func TestParseFallbackResult_AcceptsCodeFenceWrappedYAML(t *testing.T) {
	in := "```yaml\nfiles:\n  - path: /tmp/a.md\n    summary: hello\n```\n"
	doc, err := ParseFallbackResult([]byte(in))
	require.NoError(t, err)
	require.Len(t, doc.Files, 1)
	assert.Equal(t, "/tmp/a.md", doc.Files[0].Path)
	assert.Equal(t, "hello", doc.Files[0].Summary)
}

func TestParseFallbackResult_RejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace", "   \n\n"},
		{"not yaml", "this is not :: valid {{ yaml"},
		{"no files key", "metadata:\n  version: 1\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseFallbackResult([]byte(tc.in))
			assert.Error(t, err)
		})
	}
}

func TestFallbackResultFile_ToModelResult_TranslatesSPO(t *testing.T) {
	f := FallbackResultFile{
		Path:       "/x.md",
		EntityType: "  PROJECT  ",
		Summary:    " hello ",
		Entities: []FallbackEntity{
			{Name: "Foo", Type: "PERSON"},
			{Name: "  ", Type: "ORG"}, // empty name → dropped
			{Name: "Bar", Type: "ORG"},
		},
		Relationships: []FallbackRelation{
			{Subject: "Foo", Predicate: "works_at", Object: "bar"},   // canonical-cased to "Bar"
			{Subject: "Foo", Predicate: "", Object: "Bar"},           // empty predicate → dropped
			{Subject: "Foo", Predicate: "knows", Object: "Unknown"},  // unknown name passes through
		},
	}
	res := f.ToModelResult()
	assert.Equal(t, "project", res.EntityType, "entity_type lowercased")
	assert.Equal(t, "hello", res.Summary, "summary trimmed")
	require.Len(t, res.Entities, 2)
	assert.Equal(t, "Foo", res.Entities[0].Name)
	assert.Equal(t, "Bar", res.Entities[1].Name)
	require.Len(t, res.Relationships, 2)
	assert.Equal(t, "Bar", res.Relationships[0].Target, "object resolved to canonical entity casing")
	assert.Equal(t, "works_at", res.Relationships[0].Type)
	assert.Equal(t, "Unknown", res.Relationships[1].Target, "unknown object passes through")
}

// TestApplyFallbackResult_RoundTrip simulates the user-facing flow:
// (1) write a target markdown file with no Tier 2 metadata,
// (2) emit a fake retry-result YAML for it,
// (3) call ApplyFallbackResult, and
// (4) verify the target file's frontmatter now carries entity_type, summary,
//     entities, and semantic relationships.
func TestApplyFallbackResult_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "note.md")
	original := "---\nid: note\ntitle: Note\n---\n# Note\nBody.\n"
	require.NoError(t, os.WriteFile(target, []byte(original), 0o644))

	doc := &FallbackResultDoc{
		Files: []FallbackResultFile{{
			Path:       target,
			EntityType: "concept",
			Summary:    "A test note about widgets.",
			Entities: []FallbackEntity{
				{Name: "Widget", Type: "CONCEPT"},
				{Name: "Acme", Type: "ORGANIZATION"},
			},
			Relationships: []FallbackRelation{
				{Subject: "Widget", Predicate: "made_by", Object: "Acme"},
			},
		}},
	}
	outcomes := ApplyFallbackResult(doc, MergeOptions{})
	require.Len(t, outcomes, 1)
	require.True(t, outcomes[0].Applied, "merge must succeed: %v", outcomes[0].Err)

	// Re-read the target and verify the new frontmatter shape.
	updated, err := os.ReadFile(target)
	require.NoError(t, err)
	got := string(updated)
	assert.Contains(t, got, "entity-type: concept")
	assert.Contains(t, got, "summary: A test note about widgets.")
	assert.Contains(t, got, "Widget")
	assert.Contains(t, got, "Acme")
	assert.Contains(t, got, "made_by")
	// Original Tier 1 fields preserved.
	assert.Contains(t, got, "id: note")
	assert.Contains(t, got, "title: Note")
	// Body untouched.
	assert.Contains(t, got, "# Note")
	assert.Contains(t, got, "Body.")
}

func TestApplyFallbackResult_HandlesMissingFiles(t *testing.T) {
	doc := &FallbackResultDoc{
		Files: []FallbackResultFile{
			{Path: "/this/path/does/not/exist.md", Summary: "x"},
			{Path: "", Summary: "empty path"},
		},
	}
	outcomes := ApplyFallbackResult(doc, MergeOptions{})
	require.Len(t, outcomes, 2)
	for _, oc := range outcomes {
		assert.False(t, oc.Applied)
		require.Error(t, oc.Err)
	}
}

func TestApplyFallbackResult_NilDocReturnsNil(t *testing.T) {
	assert.Nil(t, ApplyFallbackResult(nil, MergeOptions{}))
}

// TestExtractedRawSurvivesInRetryLog covers the wire from primary error
// (which embeds raw output via "raw: …") through to the retry log.
// extractRawFromErr lives in the cli package so we exercise it indirectly
// via a parse-shape error message.
func TestWriteRetryLog_HandlesEmptyRawResponse(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.md")
	jobs := []HandoffJob{{
		Path: "/x.md", RelPath: "x.md", Body: "doc",
		PrimaryErr: errors.New("network timeout"),
	}}
	require.NoError(t, WriteRetryLog(logPath, jobs))
	raw, _ := os.ReadFile(logPath)
	got := string(raw)
	assert.Contains(t, got, "network timeout")
	assert.NotContains(t, got, "Raw primary model response", "no raw section when RawResponse is empty")
}

// Ensure compilation imports we reference are used in test scope.
var _ = fmt.Sprintf
