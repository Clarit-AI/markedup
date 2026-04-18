package markdown

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Clarit-AI/markedup/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrependFrontmatter(t *testing.T) {
	fm := &schema.GraphFrontmatter{
		ID:         "test-doc",
		Title:      "Test Document",
		EntityType: "document",
		Confidence: 0.7,
	}
	body := "# Hello\n\nSome content here.\n"

	result, err := PrependFrontmatter(fm, body)
	require.NoError(t, err)

	// Should start with ---
	assert.True(t, bytes.HasPrefix(result, []byte("---\n")))

	// Should contain the YAML fields
	assert.Contains(t, string(result), "id: test-doc")
	assert.Contains(t, string(result), "title: Test Document")

	// Should end with the original body exactly
	assert.True(t, bytes.HasSuffix(result, []byte(body)))
}

func TestReplaceFrontmatter_ExistingFrontmatter(t *testing.T) {
	original := []byte("---\nid: old-id\ntitle: Old Title\n---\n# Heading\n\nBody with **special** chars.\n\n  indented line\n")
	fm := &schema.GraphFrontmatter{
		ID:         "new-id",
		Title:      "New Title",
		EntityType: "concept",
		Confidence: 0.9,
	}

	result, err := ReplaceFrontmatter(fm, original)
	require.NoError(t, err)

	// New frontmatter should be present
	assert.Contains(t, string(result), "id: new-id")
	assert.Contains(t, string(result), "title: New Title")
	assert.NotContains(t, string(result), "id: old-id")

	// Body must be byte-for-byte identical.
	loc := frontmatterRe.FindSubmatchIndex(original)
	require.NotNil(t, loc)
	originalBody := original[loc[4]:]

	locNew := frontmatterRe.FindSubmatchIndex(result)
	require.NotNil(t, locNew)
	newBody := result[locNew[4]:]

	assert.True(t, bytes.Equal(originalBody, newBody),
		"body was modified: original=%q, got=%q", originalBody, newBody)
}

func TestReplaceFrontmatter_NoFrontmatter(t *testing.T) {
	original := []byte("# Plain Document\n\nNo frontmatter here.\n")
	fm := &schema.GraphFrontmatter{
		ID:         "plain-doc",
		Title:      "Plain Document",
		EntityType: "document",
		Confidence: 0.7,
	}

	result, err := ReplaceFrontmatter(fm, original)
	require.NoError(t, err)

	// Should have frontmatter now
	assert.True(t, HasFrontmatter(result))
	assert.Contains(t, string(result), "id: plain-doc")

	// Body should be preserved
	assert.True(t, bytes.HasSuffix(result, original),
		"original body not preserved at end of result")
}

func TestReplaceFrontmatter_BodyPreservedByteForByte(t *testing.T) {
	// Use a body with trailing whitespace, tabs, special chars
	body := "# Title  \n\n\tindented with tab\n   spaces   \n```code```\n\n"
	original := []byte("---\nid: x\n---\n" + body)
	fm := &schema.GraphFrontmatter{
		ID:         "replaced",
		Title:      "Replaced",
		EntityType: "document",
	}

	result, err := ReplaceFrontmatter(fm, original)
	require.NoError(t, err)

	// Extract body from result
	loc := frontmatterRe.FindSubmatchIndex(result)
	require.NotNil(t, loc)
	resultBody := result[loc[4]:]

	assert.True(t, bytes.Equal([]byte(body), resultBody),
		"body mismatch:\nexpected: %q\ngot:      %q", body, resultBody)
}

// TestReplaceFrontmatter_StripsStackedLeadingBlocks guards the issue #108
// invariant: when a file pathologically contains multiple consecutive
// leading `---...---` blocks (from a manual edit or upstream tool bug),
// ReplaceFrontmatter must strip ALL of them and emit exactly one.
func TestReplaceFrontmatter_StripsStackedLeadingBlocks(t *testing.T) {
	original := []byte("---\nid: first\n---\n\n---\nid: stale\nrelationships:\n  - target: ghost\n    type: old\n    strength: 0.1\n---\n\n# Real Body\n\nContent.\n")
	fm := &schema.GraphFrontmatter{
		ID:         "merged",
		Title:      "Merged",
		EntityType: "concept",
		Confidence: 0.9,
	}

	result, err := ReplaceFrontmatter(fm, original)
	require.NoError(t, err)

	// Exactly one `^relationships:` would show up if present; here fm has
	// none so we expect zero. The critical assertion is that the stale
	// leading block with `relationships:` was stripped entirely.
	assert.NotContains(t, string(result), "id: stale",
		"stale stacked frontmatter block must be stripped")
	assert.NotContains(t, string(result), "target: ghost",
		"stale frontmatter body must be stripped")
	assert.Contains(t, string(result), "id: merged")
	assert.Contains(t, string(result), "# Real Body")

	// Idempotency: running ReplaceFrontmatter again should be a no-op on
	// block count.
	result2, err := ReplaceFrontmatter(fm, result)
	require.NoError(t, err)
	assert.Equal(t, result, result2, "ReplaceFrontmatter must be idempotent")

	// Count leading `---` pairs. A well-formed single-FM file has exactly
	// two `---` lines at the top, separated by YAML.
	assert.Equal(t, 2, bytes.Count(result, []byte("\n---\n"))+boolToInt(bytes.HasPrefix(result, []byte("---\n"))),
		"result must contain exactly one leading frontmatter block")
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestWriteFrontmatterFile_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	content := []byte("---\nid: test\n---\nBody.\n")
	err := WriteFrontmatterFile(path, content)
	require.NoError(t, err)

	// File should exist with correct content
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, data)

	// Temp file should not exist
	_, err = os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(err))
}
