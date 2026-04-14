package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlobalPath(t *testing.T) {
	p := GlobalPath()
	assert.Contains(t, p, ".markedup.yaml")
}

func TestLocalPath(t *testing.T) {
	p := LocalPath("/some/kb")
	assert.Equal(t, "/some/kb/.markedup.yaml", p)
}

func TestExists(t *testing.T) {
	t.Run("no files", func(t *testing.T) {
		dir := t.TempDir()
		// Override GlobalPath by testing Exists with a kbDir that has no file,
		// and no global file at home. We can't easily override home, so we
		// just confirm local-only logic.
		assert.False(t, fileExists(filepath.Join(dir, ".markedup.yaml")))
	})

	t.Run("local exists", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, filepath.Join(dir, ".markedup.yaml"), "embed:\n  endpoint: http://local\n")
		assert.True(t, fileExists(filepath.Join(dir, ".markedup.yaml")))
	})
}

func TestLoad_NoFiles(t *testing.T) {
	dir := t.TempDir()
	// Point to a dir with no config; global may or may not exist but
	// we test that Load returns a valid zero Config without error.
	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.NotNil(t, cfg)
}

func TestLoad_GlobalOnly(t *testing.T) {
	globalDir := t.TempDir()
	kbDir := t.TempDir()

	globalPath := filepath.Join(globalDir, ".markedup.yaml")
	writeYAML(t, globalPath, `
embed:
  endpoint: http://global-embed
  model: global-model
llm:
  endpoint: http://global-llm
  model: global-llm-model
`)

	// We can't override os.UserHomeDir easily, so test readFile + merge directly.
	global, err := readFile(globalPath)
	require.NoError(t, err)

	local, err := readFile(LocalPath(kbDir))
	require.NoError(t, err)

	cfg := mergeConfigs(global, local)

	assert.Equal(t, "http://global-embed", cfg.Embed.Endpoint)
	assert.Equal(t, "global-model", cfg.Embed.Model)
	assert.Equal(t, "http://global-llm", cfg.LLM.Endpoint)
	assert.Equal(t, "global-llm-model", cfg.LLM.Model)
}

func TestLoad_LocalOnly(t *testing.T) {
	kbDir := t.TempDir()
	localPath := filepath.Join(kbDir, ".markedup.yaml")
	writeYAML(t, localPath, `
embed:
  endpoint: http://local-embed
  model: local-model
rerank:
  endpoint: http://local-rerank
  model: local-rerank-model
  format: openai
`)

	local, err := readFile(localPath)
	require.NoError(t, err)

	base := &Config{}
	cfg := mergeConfigs(base, local)

	assert.Equal(t, "http://local-embed", cfg.Embed.Endpoint)
	assert.Equal(t, "local-model", cfg.Embed.Model)
	assert.Equal(t, "http://local-rerank", cfg.Rerank.Endpoint)
	assert.Equal(t, "local-rerank-model", cfg.Rerank.Model)
	assert.Equal(t, "openai", cfg.Rerank.Format)
}

func TestLoad_MergeLocalOverridesGlobal(t *testing.T) {
	globalDir := t.TempDir()
	kbDir := t.TempDir()

	globalPath := filepath.Join(globalDir, ".markedup.yaml")
	writeYAML(t, globalPath, `
embed:
  endpoint: http://global-embed
  model: global-model
rerank:
  endpoint: http://global-rerank
  model: global-rerank
  format: jina
llm:
  endpoint: http://global-llm
  model: global-llm
`)

	localPath := filepath.Join(kbDir, ".markedup.yaml")
	writeYAML(t, localPath, `
embed:
  endpoint: http://local-embed
rerank:
  format: openai
`)

	global, err := readFile(globalPath)
	require.NoError(t, err)

	local, err := readFile(localPath)
	require.NoError(t, err)

	cfg := mergeConfigs(global, local)

	// Local endpoint overrides global.
	assert.Equal(t, "http://local-embed", cfg.Embed.Endpoint)
	// Global model preserved (local was zero).
	assert.Equal(t, "global-model", cfg.Embed.Model)
	// Rerank: endpoint from global, format overridden by local.
	assert.Equal(t, "http://global-rerank", cfg.Rerank.Endpoint)
	assert.Equal(t, "global-rerank", cfg.Rerank.Model)
	assert.Equal(t, "openai", cfg.Rerank.Format)
	// LLM: all from global.
	assert.Equal(t, "http://global-llm", cfg.LLM.Endpoint)
	assert.Equal(t, "global-llm", cfg.LLM.Model)
}

func TestApplyEnvOverrides(t *testing.T) {
	tests := []struct {
		name   string
		envKey string
		envVal string
		check  func(*Config) string
	}{
		{"embed endpoint", "MARKEDUP_EMBED_ENDPOINT", "http://env-embed", func(c *Config) string { return c.Embed.Endpoint }},
		{"embed model", "MARKEDUP_EMBED_MODEL", "env-model", func(c *Config) string { return c.Embed.Model }},
		{"embed api key", "MARKEDUP_EMBED_API_KEY", "env-key", func(c *Config) string { return c.Embed.APIKey }},
		{"rerank endpoint", "MARKEDUP_RERANK_ENDPOINT", "http://env-rerank", func(c *Config) string { return c.Rerank.Endpoint }},
		{"rerank model", "MARKEDUP_RERANK_MODEL", "env-rerank-model", func(c *Config) string { return c.Rerank.Model }},
		{"rerank api key", "MARKEDUP_RERANK_API_KEY", "env-rerank-key", func(c *Config) string { return c.Rerank.APIKey }},
		{"rerank format", "MARKEDUP_RERANK_FORMAT", "openai", func(c *Config) string { return c.Rerank.Format }},
		{"llm endpoint", "MARKEDUP_LLM_ENDPOINT", "http://env-llm", func(c *Config) string { return c.LLM.Endpoint }},
		{"llm model", "MARKEDUP_LLM_MODEL", "env-llm-model", func(c *Config) string { return c.LLM.Model }},
		{"llm api key", "MARKEDUP_LLM_API_KEY", "env-llm-key", func(c *Config) string { return c.LLM.APIKey }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envKey, tt.envVal)
			cfg := &Config{}
			applyEnvOverrides(cfg)
			assert.Equal(t, tt.envVal, tt.check(cfg))
		})
	}
}

func TestEnvOverridesWinOverFile(t *testing.T) {
	t.Setenv("MARKEDUP_EMBED_ENDPOINT", "http://env-wins")

	cfg := &Config{
		Embed: ServiceConfig{
			Endpoint: "http://file-value",
		},
	}
	applyEnvOverrides(cfg)

	assert.Equal(t, "http://env-wins", cfg.Embed.Endpoint)
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", ".markedup.yaml")

	cfg := &Config{
		Embed: ServiceConfig{
			Endpoint: "http://save-embed",
			Model:    "save-model",
			APIKey:   "secret-should-not-appear",
		},
		Rerank: RerankConfig{
			ServiceConfig: ServiceConfig{
				Endpoint: "http://save-rerank",
				Model:    "save-rerank-model",
				APIKey:   "another-secret",
			},
			Format: "jina",
		},
	}

	err := Save(cfg, path)
	require.NoError(t, err)

	// Read the raw file and verify no secrets.
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "http://save-embed")
	assert.Contains(t, content, "save-model")
	assert.NotContains(t, content, "secret-should-not-appear")
	assert.NotContains(t, content, "another-secret")
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".markedup.yaml")

	original := &Config{
		Embed: ServiceConfig{
			Endpoint: "http://rt-embed",
			Model:    "rt-model",
			APIKey:   "should-be-lost",
		},
		Rerank: RerankConfig{
			ServiceConfig: ServiceConfig{
				Endpoint: "http://rt-rerank",
				Model:    "rt-rerank-model",
			},
			Format: "openai",
		},
		LLM: ServiceConfig{
			Endpoint: "http://rt-llm",
			Model:    "rt-llm-model",
		},
		Triplex: ServiceConfig{
			Endpoint: "http://rt-triplex",
			Model:    "rt-triplex-model",
		},
	}

	err := Save(original, path)
	require.NoError(t, err)

	loaded, err := readFile(path)
	require.NoError(t, err)

	// Non-secret fields preserved.
	assert.Equal(t, original.Embed.Endpoint, loaded.Embed.Endpoint)
	assert.Equal(t, original.Embed.Model, loaded.Embed.Model)
	assert.Equal(t, original.Rerank.Endpoint, loaded.Rerank.Endpoint)
	assert.Equal(t, original.Rerank.Model, loaded.Rerank.Model)
	assert.Equal(t, original.Rerank.Format, loaded.Rerank.Format)
	assert.Equal(t, original.LLM.Endpoint, loaded.LLM.Endpoint)
	assert.Equal(t, original.LLM.Model, loaded.LLM.Model)
	assert.Equal(t, original.Triplex.Endpoint, loaded.Triplex.Endpoint)
	assert.Equal(t, original.Triplex.Model, loaded.Triplex.Model)

	// APIKey is lost (yaml:"-").
	assert.Empty(t, loaded.Embed.APIKey)
	assert.Empty(t, loaded.Rerank.APIKey)
}

func TestReadFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".markedup.yaml")
	// Use a YAML structure that will fail to unmarshal into Config
	// (embed must be a mapping, not a scalar list).
	writeYAML(t, path, "embed:\n  - not\n  - a\n  - mapping\n")

	_, err := readFile(path)
	assert.Error(t, err)
}

func TestMergeConfigs_BothZero(t *testing.T) {
	cfg := mergeConfigs(&Config{}, &Config{})
	assert.Equal(t, &Config{}, cfg)
}

// writeYAML is a test helper that writes content to a file.
func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
