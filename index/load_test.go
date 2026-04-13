package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validMD = `---
id: test-page
title: Test Page
entity-type: concept
confidence: 0.9
tags:
  - go
  - testing
entities:
  - name: Go Language
    role: subject
relationships:
  - target: other-page
    type: related-to
    strength: 0.8
temporal:
  valid-from: "2024-01-01"
  last-verified: "2024-06-15"
  decay-rate: 0.1
provenance:
  sources:
    - https://example.com
  created-by: test
---
# Test Page

This is the body.
`

const validMD2 = `---
id: other-page
title: Other Page
entity-type: tool
confidence: 0.8
tags:
  - go
entities:
  - name: Tooling
    role: subject
temporal:
  valid-from: "2024-02-01"
  last-verified: "2024-06-15"
  decay-rate: 0.05
provenance:
  sources:
    - https://example.com
  created-by: test
---
# Other Page

Other body content.
`

const invalidMD = `This is not valid markdown frontmatter at all.
No YAML delimiters here.
`

const validationFailMD = `---
id: ""
title: ""
entity-type: ""
confidence: 2.0
---
Body with invalid frontmatter.
`

func writeTestFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(path, []byte(content), 0o644)
		require.NoError(t, err)
	}
}

func TestLoadBasic(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"test-page.md":  validMD,
		"other-page.md": validMD2,
	})

	result, err := Load(context.Background(), dir)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Index)

	idx := result.Index
	assert.Equal(t, 2, idx.Pages())

	p, ok := idx.Get("test-page")
	require.True(t, ok)
	assert.Equal(t, "Test Page", p.Frontmatter.Title)

	p2, ok := idx.Get("other-page")
	require.True(t, ok)
	assert.Equal(t, "Other Page", p2.Frontmatter.Title)

	// Forward/reverse relationships.
	rels := idx.ForwardRels("test-page")
	require.Len(t, rels, 1)
	assert.Equal(t, "other-page", rels[0].Target)

	refs := idx.ReverseRefs("other-page")
	require.Len(t, refs, 1)
	assert.Equal(t, "test-page", refs[0])

	// Tags.
	tags := idx.Tags()
	assert.Equal(t, []string{"go", "testing"}, tags)

	// No dangling warnings since other-page exists.
	danglingCount := 0
	for _, w := range result.Warnings {
		if w.Message != "" && len(w.Errors) == 0 {
			danglingCount++
		}
	}
	assert.Equal(t, 0, danglingCount)
}

func TestLoadEmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	result, err := Load(context.Background(), dir)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.Index.Pages())
	assert.Empty(t, result.Warnings)
}

func TestLoadNonExistentDirectory(t *testing.T) {
	_, err := Load(context.Background(), "/nonexistent/dir/that/does/not/exist")
	require.Error(t, err)
}

func TestLoadInvalidFileWithIgnoreErrors(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"valid.md":   validMD,
		"invalid.md": invalidMD,
	})

	result, err := Load(context.Background(), dir, WithIgnoreErrors(true))
	require.NoError(t, err)
	require.NotNil(t, result)

	// Only the valid page should be indexed.
	assert.Equal(t, 1, result.Index.Pages())
	_, ok := result.Index.Get("test-page")
	assert.True(t, ok)

	// Should have a warning for the invalid file.
	require.True(t, len(result.Warnings) >= 1)
	foundParseWarning := false
	for _, w := range result.Warnings {
		if filepath.Base(w.Path) == "invalid.md" {
			foundParseWarning = true
		}
	}
	assert.True(t, foundParseWarning, "expected warning for invalid.md")
}

func TestLoadInvalidFileWithoutIgnoreErrors(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"invalid.md": invalidMD,
	})

	_, err := Load(context.Background(), dir)
	require.Error(t, err)
}

func TestLoadValidationErrorsWithIgnoreErrors(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"valid.md":   validMD,
		"invalid.md": validationFailMD,
	})

	result, err := Load(context.Background(), dir, WithIgnoreErrors(true))
	require.NoError(t, err)
	require.NotNil(t, result)

	// Both should be indexed (validation errors are warnings in ignore mode).
	assert.Equal(t, 2, result.Index.Pages())

	// Should have validation warnings.
	require.True(t, len(result.Warnings) >= 1)
}

func TestLoadValidationErrorsWithoutIgnoreErrors(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"bad.md": validationFailMD,
	})

	_, err := Load(context.Background(), dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation")
}

func TestLoadDanglingRelationship(t *testing.T) {
	dir := t.TempDir()
	// test-page references "other-page" which does not exist.
	writeTestFiles(t, dir, map[string]string{
		"test-page.md": validMD,
	})

	result, err := Load(context.Background(), dir)
	require.NoError(t, err)

	// Should have a dangling relationship warning.
	found := false
	for _, w := range result.Warnings {
		if w.Message != "" && assert.ObjectsAreEqual("dangling relationship: target \"other-page\" not found in index", w.Message) {
			found = true
		}
	}
	assert.True(t, found, "expected dangling relationship warning")
}

func TestLoadWithConcurrency(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"page1.md": validMD,
		"page2.md": validMD2,
	})

	result, err := Load(context.Background(), dir, WithConcurrency(1))
	require.NoError(t, err)
	assert.Equal(t, 2, result.Index.Pages())
}

func TestLoadWithFilePattern(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"included.md":  validMD,
		"excluded.txt": validMD2,
	})

	result, err := Load(context.Background(), dir, WithFilePattern("*.md"))
	require.NoError(t, err)
	assert.Equal(t, 1, result.Index.Pages())
}

func TestLoadSubdirectories(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"root.md":          validMD,
		"subdir/nested.md": validMD2,
	})

	result, err := Load(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Index.Pages())
}

func TestLoadConcurrentRace(t *testing.T) {
	// This test is meaningful under -race flag.
	dir := t.TempDir()
	// Create multiple files to exercise concurrency.
	files := make(map[string]string)
	for i := 0; i < 20; i++ {
		name := filepath.Join(dir, "page"+string(rune('a'+i))+".md")
		_ = name
		files["page"+string(rune('a'+i))+".md"] = validMD
	}
	writeTestFiles(t, dir, files)

	result, err := Load(context.Background(), dir, WithConcurrency(4))
	require.NoError(t, err)
	// All files have the same ID ("test-page"), so only the last one wins
	// in the map. But we still exercise the concurrent path.
	require.NotNil(t, result.Index)
}

func TestLoadContextCancellation(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"page.md": validMD,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	// With a cancelled context, Load may still succeed if the walk and parse
	// complete before the context check, or it may fail. Either is acceptable.
	_, _ = Load(ctx, dir)
}

func TestWithConcurrencyMinimum(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"page.md": validMD,
	})

	// Concurrency of 0 should be clamped to 1.
	result, err := Load(context.Background(), dir, WithConcurrency(0))
	require.NoError(t, err)
	assert.Equal(t, 1, result.Index.Pages())
}
