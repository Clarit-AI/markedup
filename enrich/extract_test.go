package enrich

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"My File Name", "my-file-name"},
		{"already-slugged", "already-slugged"},
		{"file (1)", "file-1"},
		{"UPPER_CASE", "upper-case"},
		{"  spaces  ", "spaces"},
		{"---dashes---", "dashes"},
		{"file.name.here", "file-name-here"},
		{"", "untitled"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, slugify(tt.input))
		})
	}
}

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		baseName string
		want     string
	}{
		{
			name:     "h1 heading found",
			body:     "# My Document Title\n\nSome content.",
			baseName: "my-doc",
			want:     "My Document Title",
		},
		{
			name:     "no heading fallback to filename",
			body:     "Just plain text without headings.",
			baseName: "my-cool-doc",
			want:     "my cool doc",
		},
		{
			name:     "multiple headings takes first",
			body:     "# First Heading\n\n## Second\n\n# Third",
			baseName: "doc",
			want:     "First Heading",
		},
		{
			name:     "heading in code fence skipped",
			body:     "```\n# Not a heading\n```\n\n# Real Heading",
			baseName: "doc",
			want:     "Real Heading",
		},
		{
			name:     "underscore filename",
			body:     "No heading here.",
			baseName: "my_file_name",
			want:     "my file name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractTitle(tt.body, tt.baseName))
		})
	}
}

func TestExtractHashtags(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "basic tags",
			body: "Some text #python and #golang here.",
			want: []string{"python", "golang"},
		},
		{
			name: "duplicate tags",
			body: "#python is great, #Python is nice",
			want: []string{"python"},
		},
		{
			name: "tags in code fence skipped",
			body: "```\n#not-a-tag\n```\n#real-tag",
			want: []string{"real-tag"},
		},
		{
			name: "no tags",
			body: "No hashtags here.",
			want: nil,
		},
		{
			name: "tag with numbers",
			body: "#tag123 is valid",
			want: []string{"tag123"},
		},
		{
			name: "header not a tag",
			body: "# Heading\n\nSome text.",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractHashtags(tt.body))
		})
	}
}

func TestExtractWikilinks(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		targets []string
	}{
		{
			name:    "single link",
			body:    "See [[Other Page]] for details.",
			targets: []string{"other-page"},
		},
		{
			name:    "multiple links",
			body:    "See [[Page A]] and [[Page B]].",
			targets: []string{"page-a", "page-b"},
		},
		{
			name:    "duplicate links",
			body:    "[[Same]] and [[Same]] again.",
			targets: []string{"same"},
		},
		{
			name:    "no links",
			body:    "No wikilinks here.",
			targets: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rels := extractWikilinks(tt.body)
			if tt.targets == nil {
				assert.Nil(t, rels)
				return
			}
			assert.Len(t, rels, len(tt.targets))
			for i, r := range rels {
				assert.Equal(t, tt.targets[i], r.Target)
				assert.Equal(t, "related-to", r.Type)
				assert.Equal(t, 0.5, r.Strength)
			}
		})
	}
}

func TestExtractURLs(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "http and https",
			body: "Visit http://example.com and https://secure.example.com/path",
			want: []string{"http://example.com", "https://secure.example.com/path"},
		},
		{
			name: "url in parens",
			body: "See (https://example.com/page).",
			want: []string{"https://example.com/page"},
		},
		{
			name: "duplicate urls",
			body: "https://example.com and https://example.com again",
			want: []string{"https://example.com"},
		},
		{
			name: "no urls",
			body: "No links here.",
			want: nil,
		},
		{
			name: "url with trailing period",
			body: "Visit https://example.com.",
			want: []string{"https://example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractURLs(tt.body))
		})
	}
}

func TestDirectoryTags(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		rootDir  string
		want     []string
	}{
		{
			name:     "nested path",
			filePath: "/root/notes/research/paper.md",
			rootDir:  "/root",
			want:     []string{"notes", "research"},
		},
		{
			name:     "root level file",
			filePath: "/root/file.md",
			rootDir:  "/root",
			want:     nil,
		},
		{
			name:     "deeply nested",
			filePath: "/root/a/b/c/d.md",
			rootDir:  "/root",
			want:     []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, directoryTags(tt.filePath, tt.rootDir))
		})
	}
}

func TestExtractFromDocument_Integration(t *testing.T) {
	body := `# Research on Graph Databases

This document covers #graph-databases and #knowledge-graphs.

See [[Neo4j]] for implementation details and [[Apache TinkerPop]] for the standard.

References:
- https://neo4j.com/docs
- https://tinkerpop.apache.org

` + "```go\n#not-a-tag\n```"

	result := ExtractFromDocument("/kb/research/graph-dbs.md", body, "/kb")

	assert.Equal(t, "graph-dbs", result.ID)
	assert.Equal(t, "Research on Graph Databases", result.Title)
	assert.Equal(t, "document", result.EntityType)
	assert.Equal(t, 0.7, result.Confidence)
	assert.Contains(t, result.Tags, "graph-databases")
	assert.Contains(t, result.Tags, "knowledge-graphs")
	assert.Contains(t, result.Tags, "research")
	assert.NotContains(t, result.Tags, "not-a-tag")
	assert.Len(t, result.Relationships, 2)
	assert.Equal(t, "neo4j", result.Relationships[0].Target)
	assert.Equal(t, "apache-tinkerpop", result.Relationships[1].Target)
	assert.Contains(t, result.Provenance.Sources, "https://neo4j.com/docs")
	assert.Contains(t, result.Provenance.Sources, "https://tinkerpop.apache.org")
	assert.Equal(t, "markedup-enrich-v1", result.Provenance.CreatedBy)
}

func TestStripCodeFences(t *testing.T) {
	body := "Before\n```\nInside fence\n```\nAfter"
	result := stripCodeFences(body)
	assert.Contains(t, result, "Before")
	assert.Contains(t, result, "After")
	assert.NotContains(t, result, "Inside fence")
}
