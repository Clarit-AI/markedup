package markdown

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/KHAEntertainment/markedup/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWritePage_Basic(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:    "alice",
			Title: "Alice Person",
			Tags:  []string{"person"},
		},
		Body: "Some content here.",
	}

	var buf bytes.Buffer
	require.NoError(t, WritePage(&buf, page))

	output := buf.String()
	assert.Contains(t, output, "---\n")
	assert.Contains(t, output, "id: alice")
	assert.Contains(t, output, "# Alice Person")
	assert.Contains(t, output, "Some content here.")
}

func TestWritePage_WithRelationships(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:    "node-a",
			Title: "Node A",
			Relationships: []schema.Relationship{
				{Target: "node-b", Type: "relates-to", Strength: 0.8},
				{Target: "node-c", Type: "depends-on", Strength: 0.5},
			},
		},
		Body: "Body text.",
	}

	var buf bytes.Buffer
	require.NoError(t, WritePage(&buf, page))

	output := buf.String()
	assert.Contains(t, output, "## Related")
	assert.Contains(t, output, "- [[node-b]] (type: relates-to, strength: 0.8)")
	assert.Contains(t, output, "- [[node-c]] (type: depends-on, strength: 0.5)")
}

func TestWritePage_NilRelationships(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:    "lonely",
			Title: "Lonely Node",
		},
		Body: "No relationships.",
	}

	var buf bytes.Buffer
	require.NoError(t, WritePage(&buf, page))

	output := buf.String()
	assert.NotContains(t, output, "## Related")
}

func TestWritePage_EmptyBody(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:    "empty",
			Title: "Empty",
		},
	}

	var buf bytes.Buffer
	require.NoError(t, WritePage(&buf, page))

	output := buf.String()
	assert.Contains(t, output, "# Empty")
	// Should not have trailing blank lines before EOF for empty body
}

func TestWritePage_EmptyTitle(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID: "no-title",
		},
		Body: "Content without title.",
	}

	var buf bytes.Buffer
	require.NoError(t, WritePage(&buf, page))

	output := buf.String()
	assert.NotContains(t, output, "# \n")
	assert.Contains(t, output, "Content without title.")
}

func TestWriteFile_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test.md"

	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:    "atomic",
			Title: "Atomic Write",
		},
		Body: "Written atomically.",
	}

	require.NoError(t, WriteFile(path, page))

	// Verify file exists
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "id: atomic")

	// Verify temp file is cleaned up
	_, err = os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(err), "temp file should be cleaned up")
}

func TestRoundTrip_FrontmatterFields(t *testing.T) {
	input := `---
id: round-trip
title: Round Trip Test
entity-type: concept
confidence: 0.9
tags:
  - test
  - roundtrip
relationships:
  - target: other-node
    type: relates-to
    strength: 0.7
---

This is the body content.

Multiple paragraphs here.`

	// First parse
	page1, err := ParseBytes([]byte(input))
	require.NoError(t, err)

	// Serialize
	var buf bytes.Buffer
	require.NoError(t, WritePage(&buf, page1))

	// Second parse
	page2, err := ParseBytes(buf.Bytes())
	require.NoError(t, err)

	// Frontmatter fields must match
	assert.Equal(t, page1.Frontmatter.ID, page2.Frontmatter.ID)
	assert.Equal(t, page1.Frontmatter.Title, page2.Frontmatter.Title)
	assert.Equal(t, page1.Frontmatter.EntityType, page2.Frontmatter.EntityType)
	assert.InDelta(t, page1.Frontmatter.Confidence, page2.Frontmatter.Confidence, 0.001)
	assert.Equal(t, page1.Frontmatter.Tags, page2.Frontmatter.Tags)
	assert.Equal(t, page1.Frontmatter.Relationships, page2.Frontmatter.Relationships)
}

func TestRoundTrip_BodyPreserved(t *testing.T) {
	input := `---
id: body-test
title: Body Test
---

First paragraph.

Second paragraph with **bold** and _italic_.

- list item 1
- list item 2`

	page1, err := ParseBytes([]byte(input))
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, WritePage(&buf, page1))

	page2, err := ParseBytes(buf.Bytes())
	require.NoError(t, err)

	// Body should be preserved (trimmed whitespace OK per acceptance criteria)
	assert.Equal(t, strings.TrimSpace(page1.Body), strings.TrimSpace(page2.Body))
}

func TestRoundTrip_RelatedNotDuplicated(t *testing.T) {
	// Start with a page that has relationships
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:    "dup-test",
			Title: "Duplication Test",
			Relationships: []schema.Relationship{
				{Target: "other", Type: "relates-to", Strength: 0.5},
			},
		},
		Body: "Original body.",
	}

	// First serialize
	var buf1 bytes.Buffer
	require.NoError(t, WritePage(&buf1, page))

	// Parse the serialized output (body now contains ## Related)
	page2, err := ParseBytes(buf1.Bytes())
	require.NoError(t, err)

	// Second serialize — ## Related should not be duplicated
	var buf2 bytes.Buffer
	require.NoError(t, WritePage(&buf2, page2))

	output := buf2.String()
	count := strings.Count(output, "## Related")
	assert.Equal(t, 1, count, "## Related section should appear exactly once")
}

func TestRoundTrip_TripleParse(t *testing.T) {
	input := `---
id: triple
title: Triple Test
confidence: 0.85
relationships:
  - target: x
    type: links-to
    strength: 0.6
---

Body text.`

	// Parse -> Serialize -> Parse -> Serialize -> Parse
	page1, err := ParseBytes([]byte(input))
	require.NoError(t, err)

	var buf1 bytes.Buffer
	require.NoError(t, WritePage(&buf1, page1))

	page2, err := ParseBytes(buf1.Bytes())
	require.NoError(t, err)

	var buf2 bytes.Buffer
	require.NoError(t, WritePage(&buf2, page2))

	page3, err := ParseBytes(buf2.Bytes())
	require.NoError(t, err)

	// All frontmatter fields should be stable
	assert.Equal(t, page1.Frontmatter.ID, page3.Frontmatter.ID)
	assert.Equal(t, page1.Frontmatter.Title, page3.Frontmatter.Title)
	assert.InDelta(t, page1.Frontmatter.Confidence, page3.Frontmatter.Confidence, 0.001)
	assert.Equal(t, page1.Frontmatter.Relationships, page3.Frontmatter.Relationships)
}

func TestWriteFile_AtomicCleanupOnError(t *testing.T) {
	// Writing to a non-existent directory should fail and clean up
	path := "/nonexistent/dir/test.md"
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{ID: "fail"},
	}
	err := WriteFile(path, page)
	require.Error(t, err)
}
