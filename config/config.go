// Package config provides configuration loading, merging, and persistence
// for the markedup CLI. Configuration cascades from global (~/.markedup.yaml)
// to local ({kbDir}/.markedup.yaml) to environment variables.
package config

// Config holds all service configurations for markedup.
type Config struct {
	Embed   ServiceConfig `yaml:"embed"`
	Rerank  RerankConfig  `yaml:"rerank"`
	LLM     ServiceConfig `yaml:"llm"`
	Triplex ServiceConfig `yaml:"triplex,omitempty"`
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
