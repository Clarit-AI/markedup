package cli

import (
	"testing"

	"github.com/KHAEntertainment/markedup/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentHash_Deterministic(t *testing.T) {
	h1 := contentHash("hello world")
	h2 := contentHash("hello world")
	assert.Equal(t, h1, h2)
}

func TestContentHash_DifferentInputs(t *testing.T) {
	h1 := contentHash("hello")
	h2 := contentHash("world")
	assert.NotEqual(t, h1, h2)
}

func TestContentHash_EmptyString(t *testing.T) {
	h := contentHash("")
	assert.NotEmpty(t, h)
	assert.Len(t, h, 64) // SHA-256 hex = 64 chars
}

func TestPageText(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			Title: "Test Page",
		},
		Body: "This is the body content.",
	}

	text := pageText(page)
	assert.Equal(t, "Test Page\n\nThis is the body content.", text)
}

func TestPageText_EmptyBody(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			Title: "Title Only",
		},
		Body: "",
	}

	text := pageText(page)
	assert.Equal(t, "Title Only\n\n", text)
}

func TestNewEmbedCmd_Flags(t *testing.T) {
	cmd := newEmbedCmd()

	// Verify all flags exist.
	flags := []string{"endpoint", "model", "api-key", "batch-size", "dir", "status"}
	for _, name := range flags {
		f := cmd.Flags().Lookup(name)
		require.NotNilf(t, f, "flag --%s should exist", name)
	}
}

func TestNewEmbedCmd_Defaults(t *testing.T) {
	cmd := newEmbedCmd()

	f := cmd.Flags().Lookup("dir")
	require.NotNil(t, f)
	assert.Equal(t, ".", f.DefValue)

	f = cmd.Flags().Lookup("batch-size")
	require.NotNil(t, f)
	assert.Equal(t, "0", f.DefValue)
}

func TestRunEmbed_NoEndpoint(t *testing.T) {
	cmd := newEmbedCmd()
	// Set globals after newEmbedCmd (which resets them via flag defaults).
	embedEndpoint = ""
	embedModel = ""
	embedStatus = false

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no embedding endpoint configured")
	assert.Contains(t, err.Error(), "--endpoint")
}

func TestRunEmbed_NoModel(t *testing.T) {
	cmd := newEmbedCmd()
	// Set globals after newEmbedCmd (which resets them via flag defaults).
	embedEndpoint = "http://localhost:11434"
	embedModel = ""
	embedStatus = false

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no embedding model configured")
	assert.Contains(t, err.Error(), "--model")
}

func TestEmbedStatusJSON_Fields(t *testing.T) {
	s := embedStatusJSON{
		Total:      10,
		Embedded:   7,
		Pending:    3,
		Model:      "test-model",
		Dimensions: 768,
	}
	assert.Equal(t, 10, s.Total)
	assert.Equal(t, 7, s.Embedded)
	assert.Equal(t, 3, s.Pending)
	assert.Equal(t, "test-model", s.Model)
	assert.Equal(t, 768, s.Dimensions)
}
