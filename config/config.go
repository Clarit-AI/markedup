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
	// Format selects the Tier 2 extractor: "triplex", "nuextract", "generic", or "" (auto).
	Format string `yaml:"format,omitempty"`
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
