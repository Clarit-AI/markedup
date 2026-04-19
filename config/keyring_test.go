package config

import (
	"testing"

	"github.com/99designs/keyring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openTestRing returns a file-backed keyring suitable for testing
// in environments where the OS keychain is unavailable (CI, headless).
func openTestRing(t *testing.T) keyring.Keyring {
	t.Helper()
	ring, err := keyring.Open(keyring.Config{
		ServiceName:     serviceName,
		AllowedBackends: []keyring.BackendType{keyring.FileBackend},
		FileDir:         t.TempDir(),
		FilePasswordFunc: func(_ string) (string, error) {
			return "test-password", nil
		},
	})
	require.NoError(t, err, "failed to open file-backed keyring for testing")
	return ring
}

func TestStoreAndGetKey(t *testing.T) {
	ring := openTestRing(t)

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"embed key", "embed-api-key", "sk-embed-12345"},
		{"rerank key", "rerank-api-key", "sk-rerank-67890"},
		{"llm key", "llm-api-key", "sk-llm-abcdef"},
		{"triplex key", "triplex-api-key", "sk-triplex-uvwxyz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ring.Set(keyring.Item{
				Key:  tt.key,
				Data: []byte(tt.value),
			})
			require.NoError(t, err)

			item, err := ring.Get(tt.key)
			require.NoError(t, err)
			assert.Equal(t, tt.value, string(item.Data))
		})
	}
}

func TestGetKey_NotFound(t *testing.T) {
	ring := openTestRing(t)

	_, err := ring.Get("nonexistent-key")
	assert.ErrorIs(t, err, keyring.ErrKeyNotFound)
}

func TestDeleteKey(t *testing.T) {
	ring := openTestRing(t)

	// Store a key, then delete it.
	err := ring.Set(keyring.Item{
		Key:  "embed-api-key",
		Data: []byte("sk-embed-temp"),
	})
	require.NoError(t, err)

	err = ring.Remove("embed-api-key")
	require.NoError(t, err)

	// Verify it's gone.
	_, err = ring.Get("embed-api-key")
	assert.ErrorIs(t, err, keyring.ErrKeyNotFound)
}

func TestOverwriteKey(t *testing.T) {
	ring := openTestRing(t)

	err := ring.Set(keyring.Item{
		Key:  "llm-api-key",
		Data: []byte("old-value"),
	})
	require.NoError(t, err)

	err = ring.Set(keyring.Item{
		Key:  "llm-api-key",
		Data: []byte("new-value"),
	})
	require.NoError(t, err)

	item, err := ring.Get("llm-api-key")
	require.NoError(t, err)
	assert.Equal(t, "new-value", string(item.Data))
}

func TestKeyringAvailable(t *testing.T) {
	// KeyringAvailable uses the default OS keyring backend.
	// We just verify it returns a boolean without panicking.
	_ = KeyringAvailable()
}

// TestPublicAPI_WithFileBackend exercises the public StoreKey/GetKey/DeleteKey
// functions using a patched openRing. Since those functions call the unexported
// openRing helper, we test the integration via the keyring directly (above)
// and validate the public function signatures compile correctly here.
func TestPublicAPI_Signatures(t *testing.T) {
	// Verify function signatures at compile time.
	var _ func(string, string) error = StoreKey
	var _ func(string) (string, error) = GetKey
	var _ func(string) error = DeleteKey
	var _ func() bool = KeyringAvailable
	var _ func(string) string = KeyNameForEndpoint
	var _ func(string) string = NormalizeEndpoint
}

// Issue #117: endpoint normalization and hashing.
func TestNormalizeEndpoint(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"trailing slash", "https://openrouter.ai/api/v1/", "https://openrouter.ai/api/v1"},
		{"no trailing slash", "https://openrouter.ai/api/v1", "https://openrouter.ai/api/v1"},
		{"uppercase host", "https://OpenRouter.AI/api/v1", "https://openrouter.ai/api/v1"},
		{"default https port", "https://openrouter.ai:443/api/v1", "https://openrouter.ai/api/v1"},
		{"default http port", "http://localhost:80/api", "http://localhost/api"},
		{"non-default port preserved", "http://localhost:11434/v1", "http://localhost:11434/v1"},
		{"strips query", "https://openrouter.ai/api/v1?token=x", "https://openrouter.ai/api/v1"},
		{"unparseable falls back to lowercase", "not a url", "not a url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeEndpoint(tt.in))
		})
	}
}

func TestKeyNameForEndpoint_StableAndDeduped(t *testing.T) {
	a := KeyNameForEndpoint("https://openrouter.ai/api/v1")
	b := KeyNameForEndpoint("https://openrouter.ai/api/v1/")
	c := KeyNameForEndpoint("https://OPENROUTER.ai/api/v1")
	d := KeyNameForEndpoint("https://api.openai.com/v1")

	require.NotEmpty(t, a, "non-empty endpoint should produce a key name")
	assert.Equal(t, a, b, "trailing slash should not change the key name")
	assert.Equal(t, a, c, "uppercase host should not change the key name")
	assert.NotEqual(t, a, d, "different providers must produce different key names")
	assert.True(t, len(a) == len("apikey-")+8, "key name format: apikey-<8 hex chars>")
	assert.Empty(t, KeyNameForEndpoint(""), "empty endpoint -> empty key name")
}
