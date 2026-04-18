package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Clarit-AI/markedup/index"
	"github.com/Clarit-AI/markedup/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTestPages creates test pages backed by real files in dir.
func makeTestPages(t *testing.T, dir string) []*schema.Page {
	t.Helper()
	files := map[string]string{
		"page-a.md": "---\nid: page-a\ntitle: Page A\n---\nBody A",
		"page-b.md": "---\nid: page-b\ntitle: Page B\n---\nBody B",
	}
	var pages []*schema.Page
	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		pages = append(pages, &schema.Page{
			Frontmatter: schema.GraphFrontmatter{
				ID:         name[:len(name)-3],
				Title:      "Page " + string(name[5]),
				EntityType: "concept",
				Confidence: 0.9,
				Tags:       []string{"test"},
				Entities: []schema.Entity{
					{Name: "Test Entity", Role: "subject"},
				},
				Relationships: nil,
			},
			Body:       "Body " + string(name[5]),
			SourcePath: path,
		})
	}
	return pages
}

func buildTestIndex(pages []*schema.Page) *index.KnowledgeIndex {
	data := &index.IndexData{Pages: pages}
	return index.Import(data)
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pages := makeTestPages(t, dir)
	idx := buildTestIndex(pages)

	gc := &GraphCache{}

	// Save.
	err := gc.Save(idx, dir)
	require.NoError(t, err)

	// Verify files exist.
	assert.FileExists(t, filepath.Join(dir, ".knowledge", "graph", "index.gob"))
	assert.FileExists(t, filepath.Join(dir, ".knowledge", "graph", "checksums"))

	// Load.
	loaded, err := gc.Load(dir)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Deep equal: same number of pages.
	assert.Equal(t, idx.Pages(), loaded.Pages())

	// Check all pages match.
	for _, p := range idx.All() {
		lp, ok := loaded.Get(p.Frontmatter.ID)
		require.True(t, ok, "page %s not found in loaded index", p.Frontmatter.ID)
		assert.Equal(t, p.Frontmatter, lp.Frontmatter)
		assert.Equal(t, p.Body, lp.Body)
		assert.Equal(t, p.SourcePath, lp.SourcePath)
	}

	// Tags should match.
	assert.Equal(t, idx.Tags(), loaded.Tags())
}

func TestSaveNilIndex(t *testing.T) {
	gc := &GraphCache{}
	err := gc.Save(nil, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil index")
}

func TestLoadNoCacheFound(t *testing.T) {
	gc := &GraphCache{}
	_, err := gc.Load(t.TempDir())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoCacheFound)
}

func TestStaleNoChanges(t *testing.T) {
	dir := t.TempDir()
	pages := makeTestPages(t, dir)
	idx := buildTestIndex(pages)

	gc := &GraphCache{}
	require.NoError(t, gc.Save(idx, dir))

	// All files unchanged.
	var files []string
	for _, p := range pages {
		files = append(files, p.SourcePath)
	}

	changed, added, removed, err := gc.Stale(dir, files)
	require.NoError(t, err)
	assert.Empty(t, changed)
	assert.Empty(t, added)
	assert.Empty(t, removed)
}

func TestStaleDetectsChanged(t *testing.T) {
	dir := t.TempDir()
	pages := makeTestPages(t, dir)
	idx := buildTestIndex(pages)

	gc := &GraphCache{}
	require.NoError(t, gc.Save(idx, dir))

	// Modify one file.
	modPath := pages[0].SourcePath
	require.NoError(t, os.WriteFile(modPath, []byte("modified content"), 0o644))

	var files []string
	for _, p := range pages {
		files = append(files, p.SourcePath)
	}

	changed, added, removed, err := gc.Stale(dir, files)
	require.NoError(t, err)
	assert.Len(t, changed, 1)
	assert.Contains(t, changed, modPath)
	assert.Empty(t, added)
	assert.Empty(t, removed)
}

func TestStaleDetectsAdded(t *testing.T) {
	dir := t.TempDir()
	pages := makeTestPages(t, dir)
	idx := buildTestIndex(pages)

	gc := &GraphCache{}
	require.NoError(t, gc.Save(idx, dir))

	// Add a new file.
	newPath := filepath.Join(dir, "page-c.md")
	require.NoError(t, os.WriteFile(newPath, []byte("new page"), 0o644))

	files := []string{newPath}
	for _, p := range pages {
		files = append(files, p.SourcePath)
	}

	changed, added, removed, err := gc.Stale(dir, files)
	require.NoError(t, err)
	assert.Empty(t, changed)
	assert.Len(t, added, 1)
	assert.Contains(t, added, newPath)
	assert.Empty(t, removed)
}

func TestStaleDetectsRemoved(t *testing.T) {
	dir := t.TempDir()
	pages := makeTestPages(t, dir)
	idx := buildTestIndex(pages)

	gc := &GraphCache{}
	require.NoError(t, gc.Save(idx, dir))

	// Only provide one of the original files (simulate one removed).
	files := []string{pages[0].SourcePath}

	changed, added, removed, err := gc.Stale(dir, files)
	require.NoError(t, err)
	assert.Empty(t, changed)
	assert.Empty(t, added)
	assert.Len(t, removed, 1)
	assert.Contains(t, removed, pages[1].SourcePath)
}

func TestStaleNoCacheFound(t *testing.T) {
	gc := &GraphCache{}
	_, _, _, err := gc.Stale(t.TempDir(), []string{"/nonexistent"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoCacheFound)
}

func TestEnsureGitignore(t *testing.T) {
	dir := t.TempDir()
	giPath := filepath.Join(dir, ".gitignore")

	// First call creates .gitignore with entry.
	ensureGitignore(dir)
	data, err := os.ReadFile(giPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), ".knowledge/")

	// Second call is idempotent.
	ensureGitignore(dir)
	data2, err := os.ReadFile(giPath)
	require.NoError(t, err)
	// Should not duplicate.
	count := 0
	for _, line := range filepath.SplitList(string(data2)) {
		_ = line
	}
	// Simple count of occurrences.
	content := string(data2)
	for i := 0; i < len(content); {
		idx := len(content)
		for j := i; j < len(content)-len(".knowledge/"); j++ {
			if content[j:j+len(".knowledge/")] == ".knowledge/" {
				idx = j
				break
			}
		}
		if idx == len(content) {
			break
		}
		count++
		i = idx + len(".knowledge/")
	}
	assert.Equal(t, 1, count, ".knowledge/ should appear exactly once in .gitignore")
}

func TestEnsureGitignoreExistingFile(t *testing.T) {
	dir := t.TempDir()
	giPath := filepath.Join(dir, ".gitignore")

	// Create .gitignore with existing content (no trailing newline).
	require.NoError(t, os.WriteFile(giPath, []byte("node_modules/"), 0o644))

	ensureGitignore(dir)
	data, err := os.ReadFile(giPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), ".knowledge/")
	assert.Contains(t, string(data), "node_modules/")
}

func TestRoundTripWithRelationships(t *testing.T) {
	dir := t.TempDir()

	// Create real files.
	pathA := filepath.Join(dir, "a.md")
	pathB := filepath.Join(dir, "b.md")
	require.NoError(t, os.WriteFile(pathA, []byte("content a"), 0o644))
	require.NoError(t, os.WriteFile(pathB, []byte("content b"), 0o644))

	pages := []*schema.Page{
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "a",
				Title:      "A",
				EntityType: "concept",
				Confidence: 0.9,
				Tags:       []string{"go", "test"},
				Entities: []schema.Entity{
					{Name: "Go", Aliases: []string{"golang"}, Role: "subject"},
				},
				Relationships: []schema.Relationship{
					{Target: "b", Type: "related-to", Strength: 0.8},
				},
				Temporal: schema.TemporalInfo{
					ValidFrom:    "2024-01-01",
					ValidUntil:   "2025-01-01",
					LastVerified: "2024-06-15",
					DecayRate:    0.1,
				},
				Provenance: schema.Provenance{
					Sources:   []string{"https://example.com"},
					CreatedBy: "test",
				},
				SemanticHints:     []string{"programming language"},
				PossibleQuestions: []string{"What is Go?"},
			},
			Body:       "Body A",
			SourcePath: pathA,
		},
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "b",
				Title:      "B",
				EntityType: "tool",
				Confidence: 0.7,
				Tags:       []string{"test"},
				Relationships: []schema.Relationship{
					{Target: "a", Type: "depends-on", Strength: 0.6},
				},
			},
			Body:       "Body B",
			SourcePath: pathB,
		},
	}

	idx := buildTestIndex(pages)
	gc := &GraphCache{}

	require.NoError(t, gc.Save(idx, dir))
	loaded, err := gc.Load(dir)
	require.NoError(t, err)

	// Verify relationships are preserved.
	assert.Equal(t, idx.Relationships(), loaded.Relationships())

	// Verify forward rels.
	fwdA := loaded.ForwardRels("a")
	require.Len(t, fwdA, 1)
	assert.Equal(t, "b", fwdA[0].Target)
	assert.Equal(t, 0.8, fwdA[0].Strength)

	// Verify reverse refs.
	revA := loaded.ReverseRefs("a")
	require.Len(t, revA, 1)
	assert.Equal(t, "b", revA[0])

	// Verify entities with aliases.
	pa, ok := loaded.Get("a")
	require.True(t, ok)
	require.Len(t, pa.Frontmatter.Entities, 1)
	assert.Equal(t, []string{"golang"}, pa.Frontmatter.Entities[0].Aliases)

	// Verify temporal info.
	assert.Equal(t, "2024-01-01", pa.Frontmatter.Temporal.ValidFrom)
	assert.Equal(t, 0.1, pa.Frontmatter.Temporal.DecayRate)

	// Verify provenance.
	assert.Equal(t, []string{"https://example.com"}, pa.Frontmatter.Provenance.Sources)

	// Verify semantic hints and possible questions.
	assert.Equal(t, []string{"programming language"}, pa.Frontmatter.SemanticHints)
	assert.Equal(t, []string{"What is Go?"}, pa.Frontmatter.PossibleQuestions)
}

func TestRoundTripEmptyIndex(t *testing.T) {
	dir := t.TempDir()
	idx := buildTestIndex(nil)

	gc := &GraphCache{}
	require.NoError(t, gc.Save(idx, dir))

	loaded, err := gc.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, 0, loaded.Pages())
}
