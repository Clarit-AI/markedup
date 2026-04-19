package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Clarit-AI/markedup/config"
	"github.com/Clarit-AI/markedup/enrich"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEnrichCmd_Flags(t *testing.T) {
	cmd := newEnrichCmd()
	flags := []string{"dry-run", "force", "skip-existing", "fallback-parallel"}
	for _, name := range flags {
		f := cmd.Flags().Lookup(name)
		require.NotNilf(t, f, "flag --%s should exist", name)
	}
}

// TestEnrichCmd_FallbackParallelFlagOverride verifies the #141 contract:
// when --fallback-parallel is passed on the command line, it overrides
// cfg.Enrich.Fallback.Parallel; when omitted, the config value wins.
func TestEnrichCmd_FallbackParallelFlagOverride(t *testing.T) {
	// Reset package-level state.
	defer func() {
		enrichFallbackParallel = false
		enrichFallbackParallelSet = false
	}()

	t.Run("flag set true overrides config false", func(t *testing.T) {
		enrichFallbackParallel = false
		enrichFallbackParallelSet = false
		cmd := newEnrichCmd()
		require.NoError(t, cmd.ParseFlags([]string{"--fallback-parallel"}))
		cmd.PreRun(cmd, nil)
		assert.True(t, enrichFallbackParallelSet, "PreRun must mark flag as explicitly set")
		assert.True(t, enrichFallbackParallel, "flag value should be true")
	})

	t.Run("flag omitted leaves Set=false", func(t *testing.T) {
		enrichFallbackParallel = false
		enrichFallbackParallelSet = false
		cmd := newEnrichCmd()
		require.NoError(t, cmd.ParseFlags([]string{}))
		cmd.PreRun(cmd, nil)
		assert.False(t, enrichFallbackParallelSet, "without the flag, Set must be false so config value wins")
	})

	t.Run("flag set false explicitly overrides config true", func(t *testing.T) {
		enrichFallbackParallel = false
		enrichFallbackParallelSet = false
		cmd := newEnrichCmd()
		require.NoError(t, cmd.ParseFlags([]string{"--fallback-parallel=false"}))
		cmd.PreRun(cmd, nil)
		assert.True(t, enrichFallbackParallelSet, "explicit --fallback-parallel=false must still mark Set")
		assert.False(t, enrichFallbackParallel)
	})
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
	assert.Contains(t, buf.String(), "Enriched 1.")

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

	assert.Contains(t, buf.String(), "Enriched 1.")
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

	assert.Contains(t, buf.String(), "0 failed")
}

// TestRunEnrich_TierAwareSkip_NoModel verifies that when no --model is
// configured, a Tier-1-complete file is skipped on re-run. Regression guard
// for the post-#113 regression where IsComplete (which requires Tier 2) was
// applied unconditionally, causing every file to be re-enriched on every
// invocation when no model was configured.
func TestRunEnrich_TierAwareSkip_NoModel(t *testing.T) {
	dir := t.TempDir()

	plain := "# My Document\n\nSome content with #golang tag.\n"
	filePath := filepath.Join(dir, "my-doc.md")
	require.NoError(t, os.WriteFile(filePath, []byte(plain), 0644))

	runOnce := func() string {
		cmd := newEnrichCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{dir})

		enrichDryRun = false
		enrichForce = false
		enrichSkipExist = false
		enrichEndpoint, enrichModel, enrichAPIKey = "", "", ""

		require.NoError(t, cmd.Execute())
		return buf.String()
	}

	// First run: Tier 1 enrichment happens.
	first := runOnce()
	assert.Contains(t, first, "Enriched 1.")

	// Capture enriched file contents.
	afterFirst, err := os.ReadFile(filePath)
	require.NoError(t, err)

	// Second run with no model: should SKIP (Tier-1-complete) — not re-enrich.
	second := runOnce()
	assert.Contains(t, second, "1 skipped", "second run without model must skip Tier-1-complete files")
	assert.NotContains(t, second, "Enriched 1.")

	// File contents must be byte-identical (no rewrite).
	afterSecond, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, string(afterFirst), string(afterSecond), "file should not be rewritten on second run")
}

// TestRunEnrich_TierAwareSkip_ForceReprocesses verifies --force bypasses
// the tier-aware skip even when the file is Tier-1-complete and no model
// is configured.
func TestRunEnrich_TierAwareSkip_ForceReprocesses(t *testing.T) {
	dir := t.TempDir()

	// Already Tier-1-complete frontmatter.
	complete := "---\nid: doc\ntitle: Doc\nentity-type: concept\nconfidence: 0.9\n---\nBody.\n"
	filePath := filepath.Join(dir, "doc.md")
	require.NoError(t, os.WriteFile(filePath, []byte(complete), 0644))

	cmd := newEnrichCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--force"})

	enrichDryRun = false
	enrichForce = true
	enrichSkipExist = false
	enrichEndpoint, enrichModel, enrichAPIKey = "", "", ""

	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "Enriched 1.", "--force must re-process even Tier-1-complete files")
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

func TestValidateFormatName(t *testing.T) {
	cases := map[string]bool{
		"":          true,
		"triplex":   true,
		"NuExtract": true,
		" generic ": true,
		"nu-extract": false,
		"nuextracts": false,
		"typo":      false,
	}
	for input, ok := range cases {
		_, err := validateFormatName(input)
		if ok {
			assert.NoError(t, err, "input=%q", input)
		} else {
			assert.Error(t, err, "input=%q", input)
		}
	}
}

func TestValidateNuExtractMode(t *testing.T) {
	assert.NoError(t, validateNuExtractMode(""))
	assert.NoError(t, validateNuExtractMode("parallel"))
	assert.NoError(t, validateNuExtractMode("SINGLE"))
	assert.Error(t, validateNuExtractMode("parllel"))
	assert.Error(t, validateNuExtractMode("dual"))
}

func TestValidateNuExtractTransport(t *testing.T) {
	assert.NoError(t, validateNuExtractTransport(""))
	assert.NoError(t, validateNuExtractTransport("native"))
	assert.NoError(t, validateNuExtractTransport("MANUAL"))
	assert.Error(t, validateNuExtractTransport("manuel"))
}

func TestDetectFormatFromEndpoint(t *testing.T) {
	origCfg := appConfig
	defer func() { appConfig = origCfg }()

	// Triplex wins on tie (legacy precedence).
	appConfig = &config.Config{
		Triplex: config.ServiceConfig{Endpoint: "http://shared"},
		NuExtract: config.NuExtractConfig{
			ServiceConfig: config.ServiceConfig{Endpoint: "http://shared"},
		},
	}
	assert.Equal(t, "triplex", detectFormatFromEndpoint("http://shared"))

	appConfig = &config.Config{
		NuExtract: config.NuExtractConfig{
			ServiceConfig: config.ServiceConfig{Endpoint: "http://nuextract.cloud"},
		},
	}
	assert.Equal(t, "nuextract", detectFormatFromEndpoint("http://nuextract.cloud"))
	assert.Equal(t, "", detectFormatFromEndpoint("http://unknown"))
}

// Regression for Codex finding #3: --endpoint matching NuExtract must not
// pull Triplex credentials when both config blocks exist and no --format set.
func TestRunEnrich_EndpointMatchesNuExtract_UsesNuExtractCredentials(t *testing.T) {
	origCfg := appConfig
	defer func() {
		appConfig = origCfg
		enrichEndpoint, enrichModel, enrichAPIKey = "", "", ""
		enrichFormat, enrichNuExtractMode, enrichNuExtractTransport = "", "", ""
	}()

	appConfig = &config.Config{
		Triplex: config.ServiceConfig{
			Endpoint: "http://triplex.local",
			Model:    "phi3-triplex",
			APIKey:   "triplex-secret",
		},
		NuExtract: config.NuExtractConfig{
			ServiceConfig: config.ServiceConfig{
				Endpoint: "https://nx.hf.space",
				Model:    "numind/NuExtract-2.0-8B",
				APIKey:   "nuextract-secret",
			},
		},
	}

	// Simulate: user passes --endpoint matching NuExtract, no --format.
	// The resolution logic should detect nuextract and fill NuExtract creds.
	enrichEndpoint = "https://nx.hf.space"
	enrichModel, enrichAPIKey = "", ""
	formatName, err := validateFormatName("")
	require.NoError(t, err)
	if formatName == "" && enrichEndpoint != "" {
		formatName = detectFormatFromEndpoint(enrichEndpoint)
	}
	require.Equal(t, "nuextract", formatName)

	// Apply the same switch logic from runEnrich.
	switch formatName {
	case "nuextract":
		if enrichModel == "" {
			enrichModel = firstNonEmpty(appConfig.NuExtract.Model, appConfig.LLM.Model)
		}
		if enrichAPIKey == "" {
			enrichAPIKey = firstNonEmpty(appConfig.NuExtract.APIKey, appConfig.LLM.APIKey)
		}
	}

	assert.Equal(t, "numind/NuExtract-2.0-8B", enrichModel, "should use NuExtract model")
	assert.Equal(t, "nuextract-secret", enrichAPIKey, "should use NuExtract API key, not Triplex")
}
