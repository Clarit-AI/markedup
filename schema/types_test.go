package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// yamlRoundTrip marshals v to YAML and unmarshals back into a new T.
// This is the fundamental operation under test: marshal→unmarshal should
// produce a struct equal to the input (modulo nil→empty-slice coercion
// by yaml.v3).
func yamlRoundTrip[T any](t *testing.T, v T) T {
	t.Helper()
	data, err := yaml.Marshal(v)
	require.NoError(t, err, "marshal should not fail")

	var got T
	err = yaml.Unmarshal(data, &got)
	require.NoError(t, err, "unmarshal should not fail")
	return got
}

func TestGraphFrontmatter_YAMLRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		give GraphFrontmatter
		want GraphFrontmatter // expected after round-trip (nil slices become empty)
	}{
		{
			name: "fully populated frontmatter",
			give: GraphFrontmatter{
				ID:         "page-001",
				Title:      "Knowledge Graph Basics",
				EntityType: "concept",
				Confidence: 0.95,
				Tags:       []string{"graph", "knowledge", "basics"},
				Entities: []Entity{
					{Name: "Graph Theory", Aliases: []string{"graph-theory", "GT"}, Role: "subject"},
					{Name: "Node", Aliases: []string{}, Role: "component"},
				},
				Relationships: []Relationship{
					{Target: "page-002", Type: "related-to", Strength: 0.8},
					{Target: "page-003", Type: "depends-on", Strength: 1.0},
				},
				Temporal: TemporalInfo{
					ValidFrom:    "2024-01-01",
					ValidUntil:   "2025-12-31",
					LastVerified: "2024-06-15",
					DecayRate:    0.1,
				},
				Provenance: Provenance{
					Sources:   []string{"https://example.com/graphs", "textbook-ch3"},
					CreatedBy: "agent-alpha",
				},
				Summary:           "Foundational concepts for building and understanding knowledge graphs",
				SemanticHints:     []string{"foundational", "introductory"},
				PossibleQuestions: []string{"What is a knowledge graph?", "How do nodes relate?"},
			},
			want: GraphFrontmatter{
				ID:         "page-001",
				Title:      "Knowledge Graph Basics",
				EntityType: "concept",
				Confidence: 0.95,
				Tags:       []string{"graph", "knowledge", "basics"},
				Entities: []Entity{
					{Name: "Graph Theory", Aliases: []string{"graph-theory", "GT"}, Role: "subject"},
					{Name: "Node", Aliases: []string{}, Role: "component"},
				},
				Relationships: []Relationship{
					{Target: "page-002", Type: "related-to", Strength: 0.8},
					{Target: "page-003", Type: "depends-on", Strength: 1.0},
				},
				Temporal: TemporalInfo{
					ValidFrom:    "2024-01-01",
					ValidUntil:   "2025-12-31",
					LastVerified: "2024-06-15",
					DecayRate:    0.1,
				},
				Provenance: Provenance{
					Sources:   []string{"https://example.com/graphs", "textbook-ch3"},
					CreatedBy: "agent-alpha",
				},
				Summary:           "Foundational concepts for building and understanding knowledge graphs",
				SemanticHints:     []string{"foundational", "introductory"},
				PossibleQuestions: []string{"What is a knowledge graph?", "How do nodes relate?"},
			},
		},
		{
			name: "zero-value frontmatter round-trips with empty slices",
			give: GraphFrontmatter{},
			want: GraphFrontmatter{
				Tags:              []string{},
				Entities:          []Entity{},
				Relationships:     []Relationship{},
				Provenance:        Provenance{Sources: []string{}},
				SemanticHints:     []string{},
				PossibleQuestions: []string{},
			},
		},
		{
			name: "confidence at boundary zero",
			give: GraphFrontmatter{
				ID:         "edge-zero",
				Confidence: 0.0,
				Relationships: []Relationship{
					{Target: "t", Type: "x", Strength: 0.0},
				},
				Temporal: TemporalInfo{DecayRate: 0.0},
			},
			want: GraphFrontmatter{
				ID:         "edge-zero",
				Confidence: 0.0,
				Tags:              []string{},
				Entities:          []Entity{},
				Relationships: []Relationship{
					{Target: "t", Type: "x", Strength: 0.0},
				},
				Temporal:          TemporalInfo{DecayRate: 0.0},
				Provenance:        Provenance{Sources: []string{}},
				SemanticHints:     []string{},
				PossibleQuestions: []string{},
			},
		},
		{
			name: "confidence at boundary one",
			give: GraphFrontmatter{
				ID:         "edge-one",
				Confidence: 1.0,
				Relationships: []Relationship{
					{Target: "t", Type: "x", Strength: 1.0},
				},
				Temporal: TemporalInfo{DecayRate: 1.0},
			},
			want: GraphFrontmatter{
				ID:         "edge-one",
				Confidence: 1.0,
				Tags:              []string{},
				Entities:          []Entity{},
				Relationships: []Relationship{
					{Target: "t", Type: "x", Strength: 1.0},
				},
				Temporal:          TemporalInfo{DecayRate: 1.0},
				Provenance:        Provenance{Sources: []string{}},
				SemanticHints:     []string{},
				PossibleQuestions: []string{},
			},
		},
		{
			name: "empty slices are stable through round-trip",
			give: GraphFrontmatter{
				ID:                "empty-slices",
				Tags:              []string{},
				Entities:          []Entity{},
				Relationships:     []Relationship{},
				SemanticHints:     []string{},
				PossibleQuestions: []string{},
				Provenance:        Provenance{Sources: []string{}},
			},
			want: GraphFrontmatter{
				ID:                "empty-slices",
				Tags:              []string{},
				Entities:          []Entity{},
				Relationships:     []Relationship{},
				SemanticHints:     []string{},
				PossibleQuestions: []string{},
				Provenance:        Provenance{Sources: []string{}},
			},
		},
		{
			name: "summary field round-trip",
			give: GraphFrontmatter{
				ID:      "summary-test",
				Summary: "AI researcher specializing in knowledge graphs",
			},
			want: GraphFrontmatter{
				ID:                "summary-test",
				Summary:           "AI researcher specializing in knowledge graphs",
				Tags:              []string{},
				Entities:          []Entity{},
				Relationships:     []Relationship{},
				Provenance:        Provenance{Sources: []string{}},
				SemanticHints:     []string{},
				PossibleQuestions: []string{},
			},
		},
		{
			name: "missing summary field is empty string",
			give: GraphFrontmatter{
				ID: "no-summary",
			},
			want: GraphFrontmatter{
				ID:                "no-summary",
				Tags:              []string{},
				Entities:          []Entity{},
				Relationships:     []Relationship{},
				Provenance:        Provenance{Sources: []string{}},
				SemanticHints:     []string{},
				PossibleQuestions: []string{},
			},
		},
		{
			name: "entity aliases round-trip",
			give: GraphFrontmatter{
				ID: "alias-test",
				Entities: []Entity{
					{Name: "Solo", Aliases: []string{}, Role: "primary"},
					{Name: "Multi", Aliases: []string{"a", "b"}, Role: "secondary"},
				},
			},
			want: GraphFrontmatter{
				ID:   "alias-test",
				Tags: []string{},
				Entities: []Entity{
					{Name: "Solo", Aliases: []string{}, Role: "primary"},
					{Name: "Multi", Aliases: []string{"a", "b"}, Role: "secondary"},
				},
				Relationships:     []Relationship{},
				Provenance:        Provenance{Sources: []string{}},
				SemanticHints:     []string{},
				PossibleQuestions: []string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := yamlRoundTrip(t, tt.give)
			require.Equal(t, tt.want, got, "round-trip should produce expected struct")
		})
	}
}

func TestGraphFrontmatter_YAMLIdempotent(t *testing.T) {
	// Verify that a second round-trip produces the same result as the first,
	// proving the serialization is stable/idempotent.
	original := GraphFrontmatter{
		ID:         "idempotent-test",
		Title:      "Stability Check",
		EntityType: "test",
		Confidence: 0.5,
		Tags:       []string{"stable"},
		Entities: []Entity{
			{Name: "A", Aliases: []string{"a1"}, Role: "r"},
		},
		Relationships: []Relationship{
			{Target: "b", Type: "links", Strength: 0.9},
		},
		Temporal: TemporalInfo{
			ValidFrom:    "2024-01-01",
			ValidUntil:   "2024-12-31",
			LastVerified: "2024-06-01",
			DecayRate:    0.05,
		},
		Provenance: Provenance{
			Sources:   []string{"test"},
			CreatedBy: "tester",
		},
		Summary:           "A test entity for stability verification",
		SemanticHints:     []string{"hint"},
		PossibleQuestions: []string{"question?"},
	}

	first := yamlRoundTrip(t, original)
	second := yamlRoundTrip(t, first)
	require.Equal(t, first, second, "second round-trip should be identical to first")
}

func TestEntity_YAMLRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		give Entity
		want Entity
	}{
		{
			name: "full entity",
			give: Entity{Name: "Go", Aliases: []string{"golang", "Go language"}, Role: "language"},
			want: Entity{Name: "Go", Aliases: []string{"golang", "Go language"}, Role: "language"},
		},
		{
			name: "entity with nil aliases becomes empty",
			give: Entity{Name: "Rust", Role: "language"},
			want: Entity{Name: "Rust", Aliases: []string{}, Role: "language"},
		},
		{
			name: "zero value entity",
			give: Entity{},
			want: Entity{Aliases: []string{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := yamlRoundTrip(t, tt.give)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRelationship_YAMLRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		give Relationship
	}{
		{
			name: "normal relationship",
			give: Relationship{Target: "page-99", Type: "cites", Strength: 0.75},
		},
		{
			name: "zero strength",
			give: Relationship{Target: "t", Type: "weak", Strength: 0.0},
		},
		{
			name: "max strength",
			give: Relationship{Target: "t", Type: "strong", Strength: 1.0},
		},
		{
			name: "zero value",
			give: Relationship{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := yamlRoundTrip(t, tt.give)
			// Relationship has no slice fields, so round-trip is exact.
			require.Equal(t, tt.give, got)
		})
	}
}

func TestTemporalInfo_YAMLRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		give TemporalInfo
	}{
		{
			name: "full temporal",
			give: TemporalInfo{
				ValidFrom:    "2024-01-01",
				ValidUntil:   "2025-12-31",
				LastVerified: "2024-06-15",
				DecayRate:    0.05,
			},
		},
		{
			name: "zero value",
			give: TemporalInfo{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := yamlRoundTrip(t, tt.give)
			// TemporalInfo has no slice fields, so round-trip is exact.
			require.Equal(t, tt.give, got)
		})
	}
}

func TestProvenance_YAMLRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		give Provenance
		want Provenance
	}{
		{
			name: "full provenance",
			give: Provenance{Sources: []string{"src1", "src2"}, CreatedBy: "bot"},
			want: Provenance{Sources: []string{"src1", "src2"}, CreatedBy: "bot"},
		},
		{
			name: "nil sources becomes empty",
			give: Provenance{CreatedBy: "human"},
			want: Provenance{Sources: []string{}, CreatedBy: "human"},
		},
		{
			name: "zero value",
			give: Provenance{},
			want: Provenance{Sources: []string{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := yamlRoundTrip(t, tt.give)
			require.Equal(t, tt.want, got)
		})
	}
}
