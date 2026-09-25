package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Clarit-AI/markedup/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("MARKEDUP_DISABLE_KEYRING", "1")
	os.Exit(m.Run())
}

// fakeDeps builds a setupDeps with sensible defaults for testing.
// stdin is the newline-separated user input to feed.
func fakeDeps(stdin string) (*setupDeps, *bytes.Buffer) {
	out := &bytes.Buffer{}
	var savedCfg *config.Config
	var savedPath string
	storedKeys := map[string]string{}

	d := &setupDeps{
		Stdin:  strings.NewReader(stdin),
		Stdout: out,
		DetectLocal: func(_ context.Context) []config.Endpoint {
			return nil // no local servers by default
		},
		Save: func(cfg *config.Config, path string) error {
			savedCfg = cfg
			savedPath = path
			_ = savedCfg
			_ = savedPath
			return nil
		},
		StoreKey: func(name, value string) error {
			storedKeys[name] = value
			return nil
		},
		KeyringAvail: func() bool { return true },
		GlobalPath:   func() string { return "/tmp/test-markedup.yaml" },
		ReadPassword: func(fd int) ([]byte, error) { return nil, nil },
		StdinFd:      func() int { return 0 },
		IsTerminal:   func(fd int) bool { return false }, // force scanner fallback
	}
	return d, out
}

func TestSetup_SkipAll(t *testing.T) {
	// User picks "Skip" for all three services, then declines save.
	// With 0 detected + 4 cloud + 1 skip = 5 options, skip = 5.
	input := "5\n5\n5\nn\n"
	d, out := fakeDeps(input)

	err := runSetup(context.Background(), d)
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "Scanning for local model servers...")
	assert.Contains(t, output, "No local servers detected.")
	assert.Contains(t, output, "Setup cancelled.")
}

func TestSetup_CloudProvider_SavesConfig(t *testing.T) {
	// User picks OpenRouter (1) for embed with model "text-embedding-3",
	// skips LLM and reranker, confirms save.
	input := strings.Join([]string{
		"1",                 // Embed: OpenRouter
		"text-embedding-3",  // model name
		"sk-test-embed-key", // API key
		"5",                 // LLM: Skip
		"5",                 // Rerank: Skip
		"y",                 // Save
	}, "\n") + "\n"

	var savedCfg *config.Config
	var savedPath string
	storedKeys := map[string]string{}

	d, out := fakeDeps(input)
	d.Save = func(cfg *config.Config, path string) error {
		savedCfg = cfg
		savedPath = path
		return nil
	}
	d.StoreKey = func(name, value string) error {
		storedKeys[name] = value
		return nil
	}

	err := runSetup(context.Background(), d)
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "Configuration saved to /tmp/test-markedup.yaml")
	assert.Contains(t, output, "Setup complete!")

	require.NotNil(t, savedCfg)
	assert.Equal(t, "https://openrouter.ai/api", savedCfg.Embed.Endpoint)
	assert.Equal(t, "text-embedding-3", savedCfg.Embed.Model)
	assert.Equal(t, "/tmp/test-markedup.yaml", savedPath)
	// Issue #117: keys are stored under the endpoint hash, not the service name.
	expectedKeyName := config.KeyNameForEndpoint("https://openrouter.ai/api")
	assert.Equal(t, "sk-test-embed-key", storedKeys[expectedKeyName])
}

func TestSetup_DetectedLocal(t *testing.T) {
	// Simulate a detected Ollama instance. User picks it for embed,
	// selects model by number, skips rest, saves.
	input := strings.Join([]string{
		"1", // Embed: Ollama (local)
		"1", // model by number: nomic-embed-text
		"6", // LLM: Skip (1 detected + 4 cloud + 1 skip = 6)
		"6", // Rerank: Skip
		"y", // Save
	}, "\n") + "\n"

	var savedCfg *config.Config

	d, out := fakeDeps(input)
	d.DetectLocal = func(_ context.Context) []config.Endpoint {
		return []config.Endpoint{
			{
				Name:    "Ollama",
				URL:     "http://localhost:11434",
				Type:    "multi",
				Healthy: true,
				Models:  []string{"nomic-embed-text", "llama3"},
			},
		}
	}
	d.Save = func(cfg *config.Config, path string) error {
		savedCfg = cfg
		return nil
	}

	err := runSetup(context.Background(), d)
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "Ollama")
	assert.Contains(t, output, "http://localhost:11434")

	require.NotNil(t, savedCfg)
	assert.Equal(t, "http://localhost:11434", savedCfg.Embed.Endpoint)
	assert.Equal(t, "nomic-embed-text", savedCfg.Embed.Model)
}

func TestSetup_CustomEndpoint(t *testing.T) {
	// User picks Custom for embed, provides endpoint, model, and key.
	// Custom is option 4 (no detected servers).
	input := strings.Join([]string{
		"4",                     // Embed: Custom
		"http://my-server:9000", // endpoint
		"my-embed-model",        // model
		"my-secret-key",         // API key
		"5",                     // LLM: Skip
		"5",                     // Rerank: Skip
		"y",                     // Save
	}, "\n") + "\n"

	var savedCfg *config.Config

	d, _ := fakeDeps(input)
	d.Save = func(cfg *config.Config, path string) error {
		savedCfg = cfg
		return nil
	}

	err := runSetup(context.Background(), d)
	require.NoError(t, err)

	require.NotNil(t, savedCfg)
	assert.Equal(t, "http://my-server:9000", savedCfg.Embed.Endpoint)
	assert.Equal(t, "my-embed-model", savedCfg.Embed.Model)
}

func TestSetup_KeyringUnavailable(t *testing.T) {
	// When keyring is not available, should print env var instructions.
	input := strings.Join([]string{
		"1", // Embed: OpenRouter
		"text-embedding-3",
		"sk-key",
		"5", // LLM: Skip
		"5", // Rerank: Skip
		"y", // Save
	}, "\n") + "\n"

	d, out := fakeDeps(input)
	d.KeyringAvail = func() bool { return false }

	err := runSetup(context.Background(), d)
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "Keyring not available")
	assert.Contains(t, output, "MARKEDUP_EMBED_API_KEY")
}

func TestSetup_CommandRegistered(t *testing.T) {
	root := newRootCmd()
	found := false
	for _, cmd := range root.Commands() {
		if cmd.Use == "setup" {
			found = true
			break
		}
	}
	assert.True(t, found, "setup command should be registered in root")
}

func TestSetup_InvalidInput_Reprompts(t *testing.T) {
	// Send invalid input first, then valid.
	input := strings.Join([]string{
		"abc", // invalid
		"99",  // out of range
		"5",   // valid: Skip
		"5",   // LLM: Skip
		"5",   // Rerank: Skip
		"n",   // Don't save
	}, "\n") + "\n"

	d, out := fakeDeps(input)

	err := runSetup(context.Background(), d)
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "Please enter a number between")
}
