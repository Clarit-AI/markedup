package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/KHAEntertainment/markedup/config"
	"github.com/KHAEntertainment/markedup/enrich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEnrichCmd_Flags(t *testing.T) {
	cmd := newEnrichCmd()
	flags := []string{"dry-run", "force", "skip-existing"}
	for _, name := range flags {
		f := cmd.Flags().Lookup(name)
		require.NotNilf(t, f, "flag --%s should exist", name)
	}
}

func TestRunEnrich_BasicEnrichment(t *testing.T) {
	dir := t.TempDir()

	// Write a plain markdown file without frontmatter.
	plain := "# My Document\n\nSome content with #golang tag.\n\nSee [[Other Page]] for details.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "my-doc.md"), []byte(plain), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})

	// Reset flags to defaults.
	enrichDryRun = false
	enrichForce = false
	enrichSkipExist = false

	err := cmd.Execute()
	require.NoError(t, err)

	// Check output
	assert.Contains(t, buf.String(), "Enriched 1 files")

	// Verify file was enriched.
	data, err := os.ReadFile(filepath.Join(dir, "my-doc.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "---")
	assert.Contains(t, content, "id: my-doc")
	assert.Contains(t, content, "title: My Document")
	assert.Contains(t, content, "entity-type: document")
	assert.Contains(t, content, "golang")
	assert.Contains(t, content, "other-page")
}

func TestRunEnrich_DryRun(t *testing.T) {
	dir := t.TempDir()

	plain := "# Test\n\nContent.\n"
	filePath := filepath.Join(dir, "test.md")
	require.NoError(t, os.WriteFile(filePath, []byte(plain), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--dry-run"})

	enrichDryRun = true
	enrichForce = false
	enrichSkipExist = false

	err := cmd.Execute()
	require.NoError(t, err)

	// Output should contain proposed YAML.
	assert.Contains(t, buf.String(), "id: test")
	assert.Contains(t, buf.String(), "title: Test")

	// File should NOT be modified.
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, plain, string(data))
}

func TestRunEnrich_SkipExisting(t *testing.T) {
	dir := t.TempDir()

	// File with frontmatter — should be skipped.
	withFM := "---\nid: existing\ntitle: Existing\nentity-type: concept\n---\nBody.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "existing.md"), []byte(withFM), 0644))

	// File without frontmatter — should be enriched.
	plain := "# New File\n\nContent.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.md"), []byte(plain), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--skip-existing"})

	enrichDryRun = false
	enrichForce = false
	enrichSkipExist = true

	err := cmd.Execute()
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "Enriched 1 files")
	assert.Contains(t, buf.String(), "1 skipped")

	// Verify existing file was not modified.
	data, err := os.ReadFile(filepath.Join(dir, "existing.md"))
	require.NoError(t, err)
	assert.Equal(t, withFM, string(data))
}

func TestRunEnrich_SkipComplete(t *testing.T) {
	dir := t.TempDir()

	// File with complete frontmatter — should be skipped.
	complete := "---\nid: complete\ntitle: Complete\nentity-type: concept\nconfidence: 0.9\n---\nBody.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "complete.md"), []byte(complete), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})

	enrichDryRun = false
	enrichForce = false
	enrichSkipExist = false

	err := cmd.Execute()
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "0 errors")
}

func TestRunEnrich_Force(t *testing.T) {
	dir := t.TempDir()

	existing := "---\nid: old-id\ntitle: Old Title\nentity-type: concept\nconfidence: 0.9\n---\nBody content.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "doc.md"), []byte(existing), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--force"})

	enrichDryRun = false
	enrichForce = true
	enrichSkipExist = false

	err := cmd.Execute()
	require.NoError(t, err)

	// Verify fields were overwritten.
	data, err := os.ReadFile(filepath.Join(dir, "doc.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "id: doc")      // from filename
	assert.Contains(t, content, "entity-type: document") // forced to default
}

func TestRunEnrich_SingleFile(t *testing.T) {
	dir := t.TempDir()

	plain := "# Single\n\nContent.\n"
	filePath := filepath.Join(dir, "single.md")
	require.NoError(t, os.WriteFile(filePath, []byte(plain), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{filePath})

	enrichDryRun = false
	enrichForce = false
	enrichSkipExist = false

	err := cmd.Execute()
	require.NoError(t, err)

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "id: single")
}

func TestEnrichCmd_FormatFlags(t *testing.T) {
	cmd := newEnrichCmd()
	for _, name := range []string{"format", "nuextract-mode", "nuextract-transport"} {
		require.NotNilf(t, cmd.Flags().Lookup(name), "flag --%s should exist", name)
	}
}

func TestResolveModelFormat(t *testing.T) {
	origCfg := appConfig
	defer func() { appConfig = origCfg }()

	// Explicit CLI/config names win regardless of endpoint.
	appConfig = &config.Config{
		Triplex: config.ServiceConfig{Endpoint: "http://triplex.local"},
	}
	assert.Equal(t, enrich.FormatNuExtract, resolveModelFormat("nuextract", "http://triplex.local"))
	assert.Equal(t, enrich.FormatTriplex, resolveModelFormat("triplex", "http://anything"))
	assert.Equal(t, enrich.FormatGeneric, resolveModelFormat("generic", "http://triplex.local"))

	// Legacy Triplex endpoint-origin auto-detect when name is empty.
	assert.Equal(t, enrich.FormatTriplex, resolveModelFormat("", "http://triplex.local"))

	// NuExtract endpoint-origin auto-detect when name is empty.
	appConfig = &config.Config{
		NuExtract: config.NuExtractConfig{
			ServiceConfig: config.ServiceConfig{Endpoint: "http://nuextract.cloud"},
		},
	}
	assert.Equal(t, enrich.FormatNuExtract, resolveModelFormat("", "http://nuextract.cloud"))

	// No match → generic.
	appConfig = &config.Config{}
	assert.Equal(t, enrich.FormatGeneric, resolveModelFormat("", "http://other"))
}

func TestFirstNonEmpty(t *testing.T) {
	assert.Equal(t, "a", firstNonEmpty("a", "b"))
	assert.Equal(t, "b", firstNonEmpty("", "b"))
	assert.Equal(t, "c", firstNonEmpty("", "", "c"))
	assert.Equal(t, "", firstNonEmpty("", "", ""))
	assert.Equal(t, "", firstNonEmpty())
}
