package config

import (
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

const configFileName = ".markedup.yaml"

// migrateOnce gates the one-time legacy-key migration per process. markedup
// is a single-user CLI, so process-local serialization is sufficient;
// cross-process races between concurrently launched CLIs are possible but
// deemed acceptable — MigrateLegacyKeys is itself idempotent and safe to
// re-run, so the worst case is a duplicated read/no-op cycle, never data loss.
//
// Hydration is intentionally NOT gated by Once: each Load() call may receive
// a different cfg with different endpoints, so the keyring lookup must run
// every time.
var migrateOnce sync.Once

// resetMigrateOnceForTest re-arms migrateOnce so each test's Load() invocation
// re-runs migration against a freshly-stubbed keyring backend.
func resetMigrateOnceForTest() {
	migrateOnce = sync.Once{}
}

// GlobalPath returns the path to the global config file (~/.markedup.yaml).
func GlobalPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, configFileName)
}

// LocalPath returns the path to the local (per-KB) config file.
func LocalPath(kbDir string) string {
	return filepath.Join(kbDir, configFileName)
}

// Exists returns true if at least one config file (global or local) exists.
func Exists(kbDir string) bool {
	if fileExists(GlobalPath()) {
		return true
	}
	return fileExists(LocalPath(kbDir))
}

// Load reads the global and local config files, merges them (local overrides
// global), then applies environment variable overrides. A missing file is not
// an error — the cascade simply skips it.
func Load(kbDir string) (*Config, error) {
	global, err := readFile(GlobalPath())
	if err != nil {
		return nil, err
	}

	local, err := readFile(LocalPath(kbDir))
	if err != nil {
		return nil, err
	}

	cfg := mergeConfigs(global, local)
	applyEnvOverrides(cfg)

	// One-time-per-process migration of legacy per-service keyring entries
	// to the endpoint-keyed format (issue #117). MigrateLegacyKeys is itself
	// idempotent; the sync.Once just avoids redundant keyring reads when a
	// long-lived process calls Load() multiple times.
	migrateOnce.Do(func() {
		MigrateLegacyKeys(cfg)
	})

	// Hydrate any APIKey fields still empty (env vars / YAML did not provide
	// one) from the keyring, looked up by endpoint. Same endpoint = one read.
	// Runs every Load — different kbDirs may yield different endpoints.
	hydrateKeysFromKeyring(cfg)

	return cfg, nil
}

// Save writes cfg to path as YAML. Fields tagged yaml:"-" (APIKey) are
// automatically omitted by the YAML encoder.
func Save(cfg *Config, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}

// readFile reads and unmarshals a YAML config file. Returns a zero-value
// Config (not nil) if the file does not exist.
func readFile(path string) (*Config, error) {
	cfg := &Config{}
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// mergeConfigs returns a new Config where non-zero fields in override win
// over the corresponding fields in base.
func mergeConfigs(base, override *Config) *Config {
	out := &Config{}

	out.Embed = mergeService(base.Embed, override.Embed)
	out.LLM = mergeService(base.LLM, override.LLM)
	out.Triplex = mergeService(base.Triplex, override.Triplex)
	out.Rerank = mergeRerank(base.Rerank, override.Rerank)
	out.NuExtract = mergeNuExtract(base.NuExtract, override.NuExtract)
	out.Enrich = mergeEnrich(base.Enrich, override.Enrich)

	out.Format = base.Format
	if override.Format != "" {
		out.Format = override.Format
	}

	return out
}

func mergeEnrich(base, override EnrichConfig) EnrichConfig {
	out := EnrichConfig{Fallback: base.Fallback}
	// Pointer fields: non-nil override wins; nil = inherit base.
	if override.Fallback.Enabled != nil {
		out.Fallback.Enabled = override.Fallback.Enabled
	}
	if override.Fallback.MaxFiles != nil {
		out.Fallback.MaxFiles = override.Fallback.MaxFiles
	}
	if override.Fallback.Endpoint != "" {
		out.Fallback.Endpoint = override.Fallback.Endpoint
	}
	if override.Fallback.Model != "" {
		out.Fallback.Model = override.Fallback.Model
	}
	if override.Fallback.APIKey != "" {
		out.Fallback.APIKey = override.Fallback.APIKey
	}
	return out
}

func mergeNuExtract(base, override NuExtractConfig) NuExtractConfig {
	out := NuExtractConfig{
		ServiceConfig: mergeService(base.ServiceConfig, override.ServiceConfig),
		Mode:          base.Mode,
		Transport:     base.Transport,
		Predicates:    base.Predicates,
		EntityTypes:   base.EntityTypes,
	}
	if override.Mode != "" {
		out.Mode = override.Mode
	}
	if override.Transport != "" {
		out.Transport = override.Transport
	}
	// nil = absent from YAML (inherit base); non-nil (including []) = explicit override.
	// Lets `predicates: []` in local YAML clear an inherited list.
	if override.Predicates != nil {
		out.Predicates = override.Predicates
	}
	if override.EntityTypes != nil {
		out.EntityTypes = override.EntityTypes
	}
	return out
}

func mergeService(base, override ServiceConfig) ServiceConfig {
	out := base
	if override.Endpoint != "" {
		out.Endpoint = override.Endpoint
	}
	if override.Model != "" {
		out.Model = override.Model
	}
	if override.APIKey != "" {
		out.APIKey = override.APIKey
	}
	return out
}

func mergeRerank(base, override RerankConfig) RerankConfig {
	out := RerankConfig{
		ServiceConfig: mergeService(base.ServiceConfig, override.ServiceConfig),
	}
	out.Format = base.Format
	if override.Format != "" {
		out.Format = override.Format
	}
	return out
}

// applyEnvOverrides maps MARKEDUP_* environment variables to Config fields.
// A non-empty env var always wins.
func applyEnvOverrides(cfg *Config) {
	setFromEnv(&cfg.Embed.Endpoint, "MARKEDUP_EMBED_ENDPOINT")
	setFromEnv(&cfg.Embed.Model, "MARKEDUP_EMBED_MODEL")
	setFromEnv(&cfg.Embed.APIKey, "MARKEDUP_EMBED_API_KEY")

	setFromEnv(&cfg.Rerank.Endpoint, "MARKEDUP_RERANK_ENDPOINT")
	setFromEnv(&cfg.Rerank.Model, "MARKEDUP_RERANK_MODEL")
	setFromEnv(&cfg.Rerank.APIKey, "MARKEDUP_RERANK_API_KEY")
	setFromEnv(&cfg.Rerank.Format, "MARKEDUP_RERANK_FORMAT")

	setFromEnv(&cfg.LLM.Endpoint, "MARKEDUP_LLM_ENDPOINT")
	setFromEnv(&cfg.LLM.Model, "MARKEDUP_LLM_MODEL")
	setFromEnv(&cfg.LLM.APIKey, "MARKEDUP_LLM_API_KEY")

	setFromEnv(&cfg.Triplex.Endpoint, "MARKEDUP_TRIPLEX_ENDPOINT")
	setFromEnv(&cfg.Triplex.Model, "MARKEDUP_TRIPLEX_MODEL")
	setFromEnv(&cfg.Triplex.APIKey, "MARKEDUP_TRIPLEX_API_KEY")

	setFromEnv(&cfg.NuExtract.Endpoint, "MARKEDUP_NUEXTRACT_ENDPOINT")
	setFromEnv(&cfg.NuExtract.Model, "MARKEDUP_NUEXTRACT_MODEL")
	setFromEnv(&cfg.NuExtract.APIKey, "MARKEDUP_NUEXTRACT_API_KEY")
	setFromEnv(&cfg.NuExtract.Mode, "MARKEDUP_NUEXTRACT_MODE")
	setFromEnv(&cfg.NuExtract.Transport, "MARKEDUP_NUEXTRACT_TRANSPORT")

	setFromEnv(&cfg.Format, "MARKEDUP_FORMAT")

	// Enrich.Fallback env overrides (issue #140). Endpoint/Model/Key follow
	// the standard MARKEDUP_<SECTION>_<FIELD> shape.
	setFromEnv(&cfg.Enrich.Fallback.Endpoint, "MARKEDUP_ENRICH_FALLBACK_ENDPOINT")
	setFromEnv(&cfg.Enrich.Fallback.Model, "MARKEDUP_ENRICH_FALLBACK_MODEL")
	setFromEnv(&cfg.Enrich.Fallback.APIKey, "MARKEDUP_ENRICH_FALLBACK_API_KEY")
	if v := os.Getenv("MARKEDUP_ENRICH_FALLBACK_ENABLED"); v != "" {
		b := v == "1" || v == "true" || v == "TRUE" || v == "yes"
		cfg.Enrich.Fallback.Enabled = &b
	}
}

func setFromEnv(target *string, envKey string) {
	if v := os.Getenv(envKey); v != "" {
		*target = v
	}
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
