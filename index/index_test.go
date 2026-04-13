package index

import (
	"testing"

	"github.com/KHAEntertainment/markedup/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPages() []*schema.Page {
	return []*schema.Page{
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "page-a",
				Title:      "Page A",
				EntityType: "concept",
				Confidence: 0.9,
				Tags:       []string{"go", "testing"},
				Entities: []schema.Entity{
					{Name: "Go Language", Role: "subject"},
					{Name: "Testing", Role: "topic"},
				},
				Relationships: []schema.Relationship{
					{Target: "page-b", Type: "related-to", Strength: 0.8},
				},
			},
			Body:       "Page A body",
			SourcePath: "/tmp/page-a.md",
		},
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "page-b",
				Title:      "Page B",
				EntityType: "tool",
				Confidence: 0.7,
				Tags:       []string{"go", "concurrency"},
				Entities: []schema.Entity{
					{Name: "Go Language", Role: "subject"},
					{Name: "Goroutines", Role: "topic"},
				},
				Relationships: []schema.Relationship{
					{Target: "page-a", Type: "depends-on", Strength: 0.6},
					{Target: "page-c", Type: "extends", Strength: 0.5},
				},
			},
			Body:       "Page B body",
			SourcePath: "/tmp/page-b.md",
		},
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "page-c",
				Title:      "Page C",
				EntityType: "concept",
				Confidence: 0.8,
				Tags:       []string{"testing"},
				Entities: []schema.Entity{
					{Name: "Benchmarks", Role: "topic"},
				},
			},
			Body:       "Page C body",
			SourcePath: "/tmp/page-c.md",
		},
	}
}

func TestBuildIndex(t *testing.T) {
	idx := buildIndex(testPages())
	require.NotNil(t, idx)

	t.Run("Pages", func(t *testing.T) {
		assert.Equal(t, 3, idx.Pages())
	})

	t.Run("Get existing", func(t *testing.T) {
		p, ok := idx.Get("page-a")
		require.True(t, ok)
		assert.Equal(t, "Page A", p.Frontmatter.Title)
	})

	t.Run("Get non-existent", func(t *testing.T) {
		p, ok := idx.Get("page-z")
		assert.False(t, ok)
		assert.Nil(t, p)
	})

	t.Run("All sorted by ID", func(t *testing.T) {
		all := idx.All()
		require.Len(t, all, 3)
		assert.Equal(t, "page-a", all[0].Frontmatter.ID)
		assert.Equal(t, "page-b", all[1].Frontmatter.ID)
		assert.Equal(t, "page-c", all[2].Frontmatter.ID)
	})

	t.Run("Entities unique count", func(t *testing.T) {
		// Go Language (deduped), Testing, Goroutines, Benchmarks = 4
		assert.Equal(t, 4, idx.Entities())
	})

	t.Run("Relationships total", func(t *testing.T) {
		// page-a has 1, page-b has 2, page-c has 0 = 3
		assert.Equal(t, 3, idx.Relationships())
	})

	t.Run("Tags sorted and deduplicated", func(t *testing.T) {
		tags := idx.Tags()
		assert.Equal(t, []string{"concurrency", "go", "testing"}, tags)
	})

	t.Run("ByTag", func(t *testing.T) {
		goPages := idx.ByTag("go")
		require.Len(t, goPages, 2)

		testingPages := idx.ByTag("testing")
		require.Len(t, testingPages, 2)

		assert.Nil(t, idx.ByTag("nonexistent"))
	})

	t.Run("ForwardRels", func(t *testing.T) {
		rels := idx.ForwardRels("page-b")
		require.Len(t, rels, 2)
		assert.Equal(t, "page-a", rels[0].Target)
		assert.Equal(t, "page-c", rels[1].Target)

		assert.Nil(t, idx.ForwardRels("page-c"))
		assert.Nil(t, idx.ForwardRels("nonexistent"))
	})

	t.Run("ReverseRefs", func(t *testing.T) {
		// page-a is targeted by page-b
		refs := idx.ReverseRefs("page-a")
		require.Len(t, refs, 1)
		assert.Equal(t, "page-b", refs[0])

		// page-b is targeted by page-a
		refs = idx.ReverseRefs("page-b")
		require.Len(t, refs, 1)
		assert.Equal(t, "page-a", refs[0])

		// page-c is targeted by page-b
		refs = idx.ReverseRefs("page-c")
		require.Len(t, refs, 1)
		assert.Equal(t, "page-b", refs[0])

		assert.Nil(t, idx.ReverseRefs("nonexistent"))
	})
}

func TestBuildIndexEmpty(t *testing.T) {
	idx := buildIndex(nil)
	require.NotNil(t, idx)
	assert.Equal(t, 0, idx.Pages())
	assert.Empty(t, idx.All())
	assert.Equal(t, 0, idx.Entities())
	assert.Equal(t, 0, idx.Relationships())
	assert.Empty(t, idx.Tags())
}

func TestBuildIndexSinglePage(t *testing.T) {
	pages := []*schema.Page{
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "solo",
				Title:      "Solo Page",
				EntityType: "note",
				Confidence: 1.0,
				Tags:       []string{"alpha", "beta"},
			},
			Body: "solo body",
		},
	}
	idx := buildIndex(pages)
	assert.Equal(t, 1, idx.Pages())
	assert.Equal(t, []string{"alpha", "beta"}, idx.Tags())
}

func TestTagsReturnsCopy(t *testing.T) {
	idx := buildIndex(testPages())
	tags1 := idx.Tags()
	tags2 := idx.Tags()
	// Modifying one should not affect the other.
	tags1[0] = "MODIFIED"
	assert.NotEqual(t, tags1[0], tags2[0])
}
