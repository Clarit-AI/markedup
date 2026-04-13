package markdown

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHasFrontmatter(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{
			name: "with frontmatter",
			data: "---\nid: test\ntitle: Test\n---\nBody here.",
			want: true,
		},
		{
			name: "no frontmatter",
			data: "# Just a heading\n\nSome content.",
			want: false,
		},
		{
			name: "empty",
			data: "",
			want: false,
		},
		{
			name: "only delimiters",
			data: "---\n---\n",
			want: false, // regex requires at least one char between delimiters
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasFrontmatter([]byte(tt.data))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseBytesPermissive(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		wantID     string
		wantTitle  string
		wantBody   string
		wantHasFM  bool
		wantErr    bool
	}{
		{
			name:      "with full frontmatter",
			data:      "---\nid: test\ntitle: Test Page\n---\nBody content here.",
			wantID:    "test",
			wantTitle: "Test Page",
			wantBody:  "Body content here.",
			wantHasFM: true,
		},
		{
			name:     "no frontmatter",
			data:     "# My Document\n\nSome content with **bold** text.",
			wantBody: "# My Document\n\nSome content with **bold** text.",
		},
		{
			name:     "empty file",
			data:     "",
			wantBody: "",
		},
		{
			name:      "partial frontmatter",
			data:      "---\nid: partial\n---\nBody after partial frontmatter.",
			wantID:    "partial",
			wantBody:  "Body after partial frontmatter.",
			wantHasFM: true,
		},
		{
			name:    "invalid yaml in frontmatter",
			data:    "---\n: invalid: yaml: [broken\n---\nBody.",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := ParseBytesPermissive([]byte(tt.data))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, page.Frontmatter.ID)
			assert.Equal(t, tt.wantTitle, page.Frontmatter.Title)
			assert.Equal(t, tt.wantBody, page.Body)
		})
	}
}

func TestParseFilePermissive(t *testing.T) {
	t.Run("file without frontmatter", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "plain.md")
		content := "# Hello World\n\nThis is plain markdown."
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))

		page, err := ParseFilePermissive(path)
		require.NoError(t, err)
		assert.Equal(t, "", page.Frontmatter.ID)
		assert.Equal(t, content, page.Body)
		assert.Equal(t, path, page.SourcePath)
	})

	t.Run("file with frontmatter", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "enriched.md")
		content := "---\nid: enriched\ntitle: Enriched\n---\nBody text."
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))

		page, err := ParseFilePermissive(path)
		require.NoError(t, err)
		assert.Equal(t, "enriched", page.Frontmatter.ID)
		assert.Equal(t, "Body text.", page.Body)
		assert.Equal(t, path, page.SourcePath)
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, err := ParseFilePermissive("/nonexistent/file.md")
		require.Error(t, err)
	})
}
