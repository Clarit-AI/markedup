package markdown

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBytes(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantID    string
		wantTitle string
		wantBody  string
		wantTags  []string
		wantErr   bool
	}{
		{
			name: "valid frontmatter with body",
			input: `---
id: alice
title: Alice Person
entity-type: person
confidence: 0.95
tags:
  - person
  - engineer
---

Some body content here.

More paragraphs.`,
			wantID:    "alice",
			wantTitle: "Alice Person",
			wantBody:  "Some body content here.\n\nMore paragraphs.",
			wantTags:  []string{"person", "engineer"},
		},
		{
			name: "empty body",
			input: `---
id: empty
title: Empty Page
---
`,
			wantID:    "empty",
			wantTitle: "Empty Page",
			wantBody:  "",
		},
		{
			name:    "missing frontmatter",
			input:   "Just some text without frontmatter.",
			wantErr: true,
		},
		{
			name:    "incomplete frontmatter",
			input:   "---\nid: broken\n",
			wantErr: true,
		},
		{
			name: "special YAML characters in title",
			input: `---
id: special
title: "Title: with colons & special chars"
---

Body here.`,
			wantID:    "special",
			wantTitle: "Title: with colons & special chars",
			wantBody:  "Body here.",
		},
		{
			name: "relationships parsed",
			input: `---
id: node-a
title: Node A
relationships:
  - target: node-b
    type: relates-to
    strength: 0.8
  - target: node-c
    type: depends-on
    strength: 0.5
---

Content.`,
			wantID:    "node-a",
			wantTitle: "Node A",
			wantBody:  "Content.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := ParseBytes([]byte(tt.input))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, page.Frontmatter.ID)
			assert.Equal(t, tt.wantTitle, page.Frontmatter.Title)
			assert.Equal(t, tt.wantBody, page.Body)
			if tt.wantTags != nil {
				assert.Equal(t, tt.wantTags, page.Frontmatter.Tags)
			}
		})
	}
}

func TestParseBytes_Relationships(t *testing.T) {
	input := `---
id: node-a
title: Node A
relationships:
  - target: node-b
    type: relates-to
    strength: 0.8
---

Body.`
	page, err := ParseBytes([]byte(input))
	require.NoError(t, err)
	require.Len(t, page.Frontmatter.Relationships, 1)
	assert.Equal(t, "node-b", page.Frontmatter.Relationships[0].Target)
	assert.Equal(t, "relates-to", page.Frontmatter.Relationships[0].Type)
	assert.InDelta(t, 0.8, page.Frontmatter.Relationships[0].Strength, 0.001)
}

func TestParseReader(t *testing.T) {
	input := `---
id: reader-test
title: Reader Test
---

Body from reader.`
	page, err := ParseReader(strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, "reader-test", page.Frontmatter.ID)
	assert.Equal(t, "Body from reader.", page.Body)
}

func TestParseFile(t *testing.T) {
	// Write a temp file, parse it, verify SourcePath is set
	tmp := t.TempDir() + "/test.md"
	content := `---
id: file-test
title: File Test
---

File body.`
	require.NoError(t, writeTestFile(tmp, content))

	page, err := ParseFile(tmp)
	require.NoError(t, err)
	assert.Equal(t, "file-test", page.Frontmatter.ID)
	assert.Equal(t, "File body.", page.Body)
	assert.Equal(t, tmp, page.SourcePath)
}

func TestParseFile_NotFound(t *testing.T) {
	_, err := ParseFile("/nonexistent/path.md")
	require.Error(t, err)
}

func TestParseLongBody(t *testing.T) {
	longBody := strings.Repeat("This is a paragraph of text. ", 1000)
	input := "---\nid: long\ntitle: Long Body\n---\n\n" + longBody
	page, err := ParseBytes([]byte(input))
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSpace(longBody), page.Body)
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
