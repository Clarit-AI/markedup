// Package config provides configuration loading, merging, and persistence
// for the markedup CLI. Configuration cascades from global (~/.markedup.yaml)
// to local ({kbDir}/.markedup.yaml) to environment variables.
package config

// Config holds all service configurations for markedup.
type Config struct {
	Embed     ServiceConfig   `yaml:"embed"`
	Rerank    RerankConfig    `yaml:"rerank"`
	LLM       ServiceConfig   `yaml:"llm"`
	Triplex   ServiceConfig   `yaml:"triplex,omitempty"`
	NuExtract NuExtractConfig `yaml:"nuextract,omitempty"`
	Enrich    EnrichConfig    `yaml:"enrich,omitempty"`
	// Format selects the Tier 2 extractor: "triplex", "nuextract", "generic", or "" (auto).
	Format string `yaml:"format,omitempty"`
}

// EnrichConfig holds enrich-pipeline-wide settings. Currently a single
// nested Fallback block; left as a struct (not inlined) so future enrich
// settings — Parallel, Verbose, FailOnError — can land without churning the
// top-level config shape.
type EnrichConfig struct {
	Fallback FallbackConfig `yaml:"fallback,omitempty"`
}

// FallbackConfig configures the LLM fallback dispatcher (issue #140).
//
// Defaults are resolved at call-time, not parse-time: an empty Endpoint
// inherits cfg.LLM.Endpoint (the same env-var-backed reasoning endpoint the
// markedup_reason MCP tool uses), and Enabled / MaxFiles defaults depend on
// whether the resolved endpoint is local or cloud. See enrich.IsLocalEndpoint
// and the resolution path in internal/cli/enrich.go.
type FallbackConfig struct {
	// Enabled is a tri-state pointer: nil = "use the local/cloud default",
	// non-nil = explicit user choice. Local endpoints default-on; cloud
	// endpoints default-off (cost guard).
	Enabled *bool `yaml:"enabled,omitempty"`
	// Endpoint overrides cfg.LLM.Endpoint for fallback only. Empty = inherit.
	Endpoint string `yaml:"endpoint,omitempty"`
	// Model overrides cfg.LLM.Model for fallback only. Empty = inherit.
	Model string `yaml:"model,omitempty"`
	// APIKey overrides cfg.LLM.APIKey for fallback only.
	APIKey string `yaml:"-"` // NEVER written to YAML
	// MaxFiles caps the number of files dispatched per enrich run. Nil = use
	// the local/cloud default (50 cloud, unbounded local). Explicit 0 = unbounded.
	MaxFiles *int `yaml:"max_files,omitempty"`
	// Parallel enables concurrent fallback LLM calls (#141). Default false —
	// sequential execution remains the safe default. Opt-in for users with
	// capable hardware or cloud endpoints with high RPS.
	Parallel bool `yaml:"parallel,omitempty"`
	// ParallelWorkers caps the worker pool when Parallel=true. <=0 falls back
	// to runtime.NumCPU() at dispatch time.
	ParallelWorkers int `yaml:"parallel_workers,omitempty"`
}

// ServiceConfig holds endpoint, model, and authentication for a single service.
type ServiceConfig struct {
	Endpoint string `yaml:"endpoint"`
	Model    string `yaml:"model"`
	APIKey   string `yaml:"-"` // NEVER written to YAML
}

// RerankConfig extends ServiceConfig with a reranker-specific format field.
type RerankConfig struct {
	ServiceConfig `yaml:",inline"`
	Format        string `yaml:"format,omitempty"`
}

// NuExtractConfig configures the NuExtract-2.0 template-based extractor.
// Mode controls the two-pass runner: "parallel" (default) fires entities and
// relations calls simultaneously; "single" sends one combined template.
// Transport selects request shape: "native" uses chat_template_kwargs for
// vLLM/HF; "manual" renders the prompt client-side for GGUF runtimes.
// Empty Transport auto-detects from the endpoint URL.
type NuExtractConfig struct {
	ServiceConfig `yaml:",inline"`
	Mode          string   `yaml:"mode,omitempty"`
	Transport     string   `yaml:"transport,omitempty"`
	Predicates    []string `yaml:"predicates,omitempty"`
	EntityTypes   []string `yaml:"entity_types,omitempty"`
}
