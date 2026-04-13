// Package schema defines the core types for the markedup knowledge graph.
//
// All structs use kebab-case YAML tags to match the frontmatter format used
// in Obsidian-compatible markdown files.
package schema

// GraphFrontmatter represents the structured YAML frontmatter of a knowledge
// graph page. It captures identity, classification, relationships, temporal
// metadata, and provenance for a single node in the graph.
type GraphFrontmatter struct {
	ID                string         `yaml:"id"`
	Title             string         `yaml:"title"`
	EntityType        string         `yaml:"entity-type"`
	Confidence        float64        `yaml:"confidence"`
	Tags              []string       `yaml:"tags"`
	Entities          []Entity       `yaml:"entities"`
	Relationships     []Relationship `yaml:"relationships"`
	Temporal          TemporalInfo   `yaml:"temporal"`
	Provenance        Provenance     `yaml:"provenance"`
	SemanticHints     []string       `yaml:"semantic-hints"`
	PossibleQuestions []string       `yaml:"possible-questions"`
}

// Entity represents a named entity referenced within a page. Aliases allow
// fuzzy matching when the same concept appears under different names.
type Entity struct {
	Name    string   `yaml:"name"`
	Aliases []string `yaml:"aliases"`
	Role    string   `yaml:"role"`
}

// Relationship represents a directed edge from the containing page to a
// target page. Strength is a float64 in the range [0.0, 1.0].
type Relationship struct {
	Target   string  `yaml:"target"`
	Type     string  `yaml:"type"`
	Strength float64 `yaml:"strength"`
}

// TemporalInfo captures time-based metadata for confidence decay calculations.
// Date fields use string representation (e.g. "2024-01-15") to avoid timezone
// ambiguity. DecayRate controls how quickly confidence degrades over time.
type TemporalInfo struct {
	ValidFrom    string  `yaml:"valid-from"`
	ValidUntil   string  `yaml:"valid-until"`
	LastVerified string  `yaml:"last-verified"`
	DecayRate    float64 `yaml:"decay-rate"`
}

// Provenance tracks where the information in a page originated and who
// created it.
type Provenance struct {
	Sources   []string `yaml:"sources"`
	CreatedBy string   `yaml:"created-by"`
}

// Page holds a parsed markdown file: its structured frontmatter, the raw
// markdown body, and the filesystem path it was loaded from. Page is a
// runtime container and is not itself serialized to YAML.
type Page struct {
	Frontmatter GraphFrontmatter
	Body        string
	SourcePath  string
}
