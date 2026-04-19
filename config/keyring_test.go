package config

import (
	"path/filepath"
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
	assert.Equal(t, len("apikey-")+keyHashHexLen, len(a), "key name format: apikey-<16 hex chars>")
	assert.Equal(t, len("apikey-")+16, len(a), "expect 64-bit (16 hex char) hash truncation")
	assert.Empty(t, KeyNameForEndpoint(""), "empty endpoint -> empty key name")
}

// fakeRing is a minimal in-memory implementation of keyring.Keyring used by
// migration/hydration tests. It counts Get calls per key for assertions about
// read coalescing.
type fakeRing struct {
	items     map[string][]byte
	getCounts map[string]int
}

func newFakeRing() *fakeRing {
	return &fakeRing{
		items:     map[string][]byte{},
		getCounts: map[string]int{},
	}
}

func (f *fakeRing) Get(key string) (keyring.Item, error) {
	f.getCounts[key]++
	v, ok := f.items[key]
	if !ok {
		return keyring.Item{}, keyring.ErrKeyNotFound
	}
	return keyring.Item{Key: key, Data: v}, nil
}

func (f *fakeRing) GetMetadata(key string) (keyring.Metadata, error) {
	return keyring.Metadata{}, nil
}

func (f *fakeRing) Set(item keyring.Item) error {
	f.items[item.Key] = append([]byte(nil), item.Data...)
	return nil
}

func (f *fakeRing) Remove(key string) error {
	if _, ok := f.items[key]; !ok {
		return keyring.ErrKeyNotFound
	}
	delete(f.items, key)
	return nil
}

func (f *fakeRing) Keys() ([]string, error) {
	out := make([]string, 0, len(f.items))
	for k := range f.items {
		out = append(out, k)
	}
	return out, nil
}

// withFakeRing installs ring as the active keyring backend for the duration
// of the test, restoring the previous opener on cleanup.
func withFakeRing(t *testing.T, ring keyring.Keyring) {
	t.Helper()
	prev := openRingFn
	openRingFn = func() (keyring.Keyring, error) { return ring, nil }
	t.Cleanup(func() { openRingFn = prev })
}

func TestMigrateLegacyKeys_HappyPath(t *testing.T) {
	ring := newFakeRing()
	// Two legacy entries present.
	require.NoError(t, ring.Set(keyring.Item{Key: "embed-api-key", Data: []byte("sk-embed")}))
	require.NoError(t, ring.Set(keyring.Item{Key: "llm-api-key", Data: []byte("sk-llm")}))
	withFakeRing(t, ring)

	cfg := &Config{
		Embed: ServiceConfig{Endpoint: "https://api.openai.com/v1"},
		LLM:   ServiceConfig{Endpoint: "https://openrouter.ai/api/v1"},
	}

	res := MigrateLegacyKeys(cfg)

	assert.ElementsMatch(t, []string{"embed-api-key", "llm-api-key"}, res.Migrated)
	assert.Empty(t, res.Conflicts)
	assert.Empty(t, res.Skipped)

	// Legacy entries removed.
	_, err := ring.Get("embed-api-key")
	assert.ErrorIs(t, err, keyring.ErrKeyNotFound)
	_, err = ring.Get("llm-api-key")
	assert.ErrorIs(t, err, keyring.ErrKeyNotFound)

	// New entries written under endpoint-keyed names.
	embedName := KeyNameForEndpoint("https://api.openai.com/v1")
	llmName := KeyNameForEndpoint("https://openrouter.ai/api/v1")
	embedItem, err := ring.Get(embedName)
	require.NoError(t, err)
	assert.Equal(t, "sk-embed", string(embedItem.Data))
	llmItem, err := ring.Get(llmName)
	require.NoError(t, err)
	assert.Equal(t, "sk-llm", string(llmItem.Data))
}

func TestMigrateLegacyKeys_PreservesOnConflict(t *testing.T) {
	ring := newFakeRing()
	endpoint := "https://api.openai.com/v1"
	newName := KeyNameForEndpoint(endpoint)

	// Legacy entry holds OLD value; endpoint-keyed entry already holds a
	// DIFFERENT (newer, wizard-set) value.
	require.NoError(t, ring.Set(keyring.Item{Key: "embed-api-key", Data: []byte("sk-old")}))
	require.NoError(t, ring.Set(keyring.Item{Key: newName, Data: []byte("sk-new-from-wizard")}))
	withFakeRing(t, ring)

	cfg := &Config{Embed: ServiceConfig{Endpoint: endpoint}}

	res := MigrateLegacyKeys(cfg)

	assert.Empty(t, res.Migrated, "conflicting entry must not count as migrated")
	assert.Equal(t, []string{"embed-api-key"}, res.Conflicts, "conflict surfaced via result")

	// Legacy entry MUST still exist (no data loss).
	legacy, err := ring.Get("embed-api-key")
	require.NoError(t, err, "legacy entry must be preserved on conflict")
	assert.Equal(t, "sk-old", string(legacy.Data))

	// New entry untouched.
	newItem, err := ring.Get(newName)
	require.NoError(t, err)
	assert.Equal(t, "sk-new-from-wizard", string(newItem.Data), "endpoint-keyed value must not be overwritten")
}

func TestMigrateLegacyKeys_Idempotent(t *testing.T) {
	ring := newFakeRing()
	require.NoError(t, ring.Set(keyring.Item{Key: "embed-api-key", Data: []byte("sk-embed")}))
	withFakeRing(t, ring)

	cfg := &Config{Embed: ServiceConfig{Endpoint: "https://api.openai.com/v1"}}

	res1 := MigrateLegacyKeys(cfg)
	require.Equal(t, []string{"embed-api-key"}, res1.Migrated)

	// Snapshot ring state.
	keysAfter1, _ := ring.Keys()

	// Second run: legacy entries are gone, so nothing to migrate.
	res2 := MigrateLegacyKeys(cfg)
	assert.Empty(t, res2.Migrated, "second run must be a no-op")
	assert.Empty(t, res2.Conflicts)
	assert.Empty(t, res2.Skipped)

	keysAfter2, _ := ring.Keys()
	assert.ElementsMatch(t, keysAfter1, keysAfter2, "no extra writes on re-run")
}

func TestMigrateLegacyKeys_ConflictIdempotent(t *testing.T) {
	// Conflict case must also be stable across reruns: still no delete, still
	// reports conflict, still no overwrite.
	ring := newFakeRing()
	endpoint := "https://api.openai.com/v1"
	newName := KeyNameForEndpoint(endpoint)
	require.NoError(t, ring.Set(keyring.Item{Key: "embed-api-key", Data: []byte("sk-old")}))
	require.NoError(t, ring.Set(keyring.Item{Key: newName, Data: []byte("sk-new")}))
	withFakeRing(t, ring)

	cfg := &Config{Embed: ServiceConfig{Endpoint: endpoint}}

	res1 := MigrateLegacyKeys(cfg)
	res2 := MigrateLegacyKeys(cfg)

	assert.Equal(t, res1, res2, "conflict result must be stable across runs")
	legacy, err := ring.Get("embed-api-key")
	require.NoError(t, err)
	assert.Equal(t, "sk-old", string(legacy.Data))
}

func TestMigrateLegacyKeys_SplitsAcrossEndpoints(t *testing.T) {
	ring := newFakeRing()
	require.NoError(t, ring.Set(keyring.Item{Key: "embed-api-key", Data: []byte("sk-openai")}))
	require.NoError(t, ring.Set(keyring.Item{Key: "llm-api-key", Data: []byte("sk-openrouter")}))
	withFakeRing(t, ring)

	cfg := &Config{
		Embed: ServiceConfig{Endpoint: "https://api.openai.com/v1"},
		LLM:   ServiceConfig{Endpoint: "https://openrouter.ai/api/v1"},
	}

	res := MigrateLegacyKeys(cfg)
	assert.ElementsMatch(t, []string{"embed-api-key", "llm-api-key"}, res.Migrated)

	embedName := KeyNameForEndpoint("https://api.openai.com/v1")
	llmName := KeyNameForEndpoint("https://openrouter.ai/api/v1")
	require.NotEqual(t, embedName, llmName, "distinct endpoints must hash to distinct names")

	embedItem, err := ring.Get(embedName)
	require.NoError(t, err)
	assert.Equal(t, "sk-openai", string(embedItem.Data))

	llmItem, err := ring.Get(llmName)
	require.NoError(t, err)
	assert.Equal(t, "sk-openrouter", string(llmItem.Data))

	// Two distinct endpoint-keyed entries, not one collapsed.
	keys, _ := ring.Keys()
	assert.Contains(t, keys, embedName)
	assert.Contains(t, keys, llmName)
}

func TestMigrateLegacyKeys_SkippedWhenNoEndpoint(t *testing.T) {
	ring := newFakeRing()
	require.NoError(t, ring.Set(keyring.Item{Key: "embed-api-key", Data: []byte("sk-embed")}))
	withFakeRing(t, ring)

	// Endpoint not yet configured — migration should defer.
	cfg := &Config{}

	res := MigrateLegacyKeys(cfg)
	assert.Empty(t, res.Migrated)
	assert.Empty(t, res.Conflicts)
	assert.Equal(t, []string{"embed-api-key"}, res.Skipped)

	// Legacy entry still present.
	item, err := ring.Get("embed-api-key")
	require.NoError(t, err)
	assert.Equal(t, "sk-embed", string(item.Data))
}

func TestHydrateKeysFromKeyring_OneReadPerEndpoint(t *testing.T) {
	ring := newFakeRing()
	endpoint := "https://openrouter.ai/api/v1"
	keyName := KeyNameForEndpoint(endpoint)
	require.NoError(t, ring.Set(keyring.Item{Key: keyName, Data: []byte("sk-shared")}))
	withFakeRing(t, ring)

	cfg := &Config{
		Embed:   ServiceConfig{Endpoint: endpoint},
		LLM:     ServiceConfig{Endpoint: endpoint},
		Rerank:  RerankConfig{ServiceConfig: ServiceConfig{Endpoint: endpoint}},
		Triplex: ServiceConfig{Endpoint: "https://triplex.example/v1"},
	}

	hydrateKeysFromKeyring(cfg)

	// All three services pointing to the same endpoint should have been
	// hydrated from the SINGLE shared entry.
	assert.Equal(t, "sk-shared", cfg.Embed.APIKey)
	assert.Equal(t, "sk-shared", cfg.LLM.APIKey)
	assert.Equal(t, "sk-shared", cfg.Rerank.APIKey)

	// Exactly one Get call to the shared key (the per-call cache coalesces).
	assert.Equal(t, 1, ring.getCounts[keyName], "shared endpoint should be read exactly once across services")

	// Triplex (different endpoint) gets its own read; the entry doesn't
	// exist so APIKey stays empty, but the Get was attempted.
	triplexName := KeyNameForEndpoint("https://triplex.example/v1")
	assert.Equal(t, 1, ring.getCounts[triplexName])
	assert.Empty(t, cfg.Triplex.APIKey)
}

func TestLoad_MigrationRunsOncePerProcess(t *testing.T) {
	// Reset the package-level Once so this test can observe a fresh first run.
	resetMigrateOnceForTest()

	ring := newFakeRing()
	require.NoError(t, ring.Set(keyring.Item{Key: "embed-api-key", Data: []byte("sk-embed")}))
	withFakeRing(t, ring)

	kbDir := t.TempDir()
	writeYAML(t, filepath.Join(kbDir, ".markedup.yaml"), `
embed:
  endpoint: https://api.openai.com/v1
`)
	t.Setenv("HOME", t.TempDir())

	// First Load: migration runs, legacy entry is consumed.
	_, err := Load(kbDir)
	require.NoError(t, err)
	_, err = ring.Get("embed-api-key")
	assert.ErrorIs(t, err, keyring.ErrKeyNotFound, "first Load should have migrated the legacy entry")

	// Re-introduce the legacy entry. A second Load in the same process must
	// NOT re-run migration (sync.Once gating), so the entry should remain.
	require.NoError(t, ring.Set(keyring.Item{Key: "embed-api-key", Data: []byte("sk-embed-2")}))
	_, err = Load(kbDir)
	require.NoError(t, err)
	item, err := ring.Get("embed-api-key")
	require.NoError(t, err, "second Load must skip migration (sync.Once)")
	assert.Equal(t, "sk-embed-2", string(item.Data))
}

func TestHydrateKeysFromKeyring_DoesNotClobberExplicit(t *testing.T) {
	ring := newFakeRing()
	endpoint := "https://openrouter.ai/api/v1"
	keyName := KeyNameForEndpoint(endpoint)
	require.NoError(t, ring.Set(keyring.Item{Key: keyName, Data: []byte("sk-from-keyring")}))
	withFakeRing(t, ring)

	cfg := &Config{
		// Already populated (e.g. by env var or YAML).
		Embed: ServiceConfig{Endpoint: endpoint, APIKey: "sk-from-env"},
		// Empty — should be hydrated.
		LLM: ServiceConfig{Endpoint: endpoint},
	}

	hydrateKeysFromKeyring(cfg)

	assert.Equal(t, "sk-from-env", cfg.Embed.APIKey, "explicit key must not be clobbered")
	assert.Equal(t, "sk-from-keyring", cfg.LLM.APIKey, "empty key must be hydrated")

	// LLM triggered the only read; Embed was skipped because APIKey was set.
	assert.Equal(t, 1, ring.getCounts[keyName], "no read should occur for explicitly-populated services")
}
