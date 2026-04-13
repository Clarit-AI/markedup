package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validPage returns a minimal page that passes all validation rules.
func validPage() *Page {
	return &Page{
		Frontmatter: GraphFrontmatter{
			ID:         "test-001",
			Title:      "Test Page",
			EntityType: "concept",
			Confidence: 0.9,
			Entities: []Entity{
				{Name: "Go", Role: "language"},
			},
			Relationships: []Relationship{
				{Target: "other-page", Type: "related-to", Strength: 0.8},
			},
			Temporal: TemporalInfo{
				ValidFrom:    "2024-01-01",
				ValidUntil:   "2025-12-31",
				LastVerified: "2024-06-15",
				DecayRate:    0.05,
			},
		},
	}
}

func TestValidatePage_ValidPage(t *testing.T) {
	errs := ValidatePage(validPage())
	assert.Empty(t, errs)
}

func TestValidatePage_RequiredFields(t *testing.T) {
	tests := []struct {
		name  string
		mutate func(*Page)
		field string
	}{
		{"missing id", func(p *Page) { p.Frontmatter.ID = "" }, "id"},
		{"missing title", func(p *Page) { p.Frontmatter.Title = "" }, "title"},
		{"missing entity-type", func(p *Page) { p.Frontmatter.EntityType = "" }, "entity-type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPage()
			tt.mutate(p)
			errs := ValidatePage(p)
			require.NotEmpty(t, errs)
			found := false
			for _, e := range errs {
				if e.Field == tt.field {
					found = true
					assert.NotEmpty(t, e.Fix, "Fix hint should be non-empty")
				}
			}
			assert.True(t, found, "expected error for field %q", tt.field)
		})
	}
}

func TestValidatePage_ConfidenceRange(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
		wantErr    bool
	}{
		{"exactly 0", 0, false},
		{"exactly 1", 1, false},
		{"mid range", 0.5, false},
		{"above 1", 1.5, true},
		{"below 0", -0.1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPage()
			p.Frontmatter.Confidence = tt.confidence
			errs := ValidatePage(p)
			if tt.wantErr {
				found := false
				for _, e := range errs {
					if e.Field == "confidence" {
						found = true
					}
				}
				assert.True(t, found, "expected confidence error")
			} else {
				for _, e := range errs {
					assert.NotEqual(t, "confidence", e.Field)
				}
			}
		})
	}
}

func TestValidatePage_RelationshipValidation(t *testing.T) {
	t.Run("missing target", func(t *testing.T) {
		p := validPage()
		p.Frontmatter.Relationships = []Relationship{{Type: "ref", Strength: 0.5}}
		errs := ValidatePage(p)
		require.NotEmpty(t, errs)
		assert.Equal(t, "relationships[0].target", errs[0].Field)
	})

	t.Run("missing type", func(t *testing.T) {
		p := validPage()
		p.Frontmatter.Relationships = []Relationship{{Target: "x", Strength: 0.5}}
		errs := ValidatePage(p)
		require.NotEmpty(t, errs)
		assert.Equal(t, "relationships[0].type", errs[0].Field)
	})

	t.Run("strength out of range", func(t *testing.T) {
		p := validPage()
		p.Frontmatter.Relationships = []Relationship{{Target: "x", Type: "ref", Strength: 1.5}}
		errs := ValidatePage(p)
		require.NotEmpty(t, errs)
		assert.Equal(t, "relationships[0].strength", errs[0].Field)
	})
}

func TestValidatePage_TemporalDates(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Page)
		wantErr bool
	}{
		{"valid dates", func(p *Page) {}, false},
		{"empty dates ok", func(p *Page) {
			p.Frontmatter.Temporal.ValidFrom = ""
			p.Frontmatter.Temporal.ValidUntil = ""
			p.Frontmatter.Temporal.LastVerified = ""
		}, false},
		{"bad valid-from", func(p *Page) { p.Frontmatter.Temporal.ValidFrom = "01-2024-15" }, true},
		{"bad valid-until", func(p *Page) { p.Frontmatter.Temporal.ValidUntil = "not-a-date" }, true},
		{"bad last-verified", func(p *Page) { p.Frontmatter.Temporal.LastVerified = "2024/01/15" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPage()
			tt.mutate(p)
			errs := ValidatePage(p)
			hasDateErr := false
			for _, e := range errs {
				if len(e.Field) > 9 && e.Field[:9] == "temporal." {
					hasDateErr = true
				}
			}
			assert.Equal(t, tt.wantErr, hasDateErr)
		})
	}
}

func TestValidatePage_EntityNames(t *testing.T) {
	p := validPage()
	p.Frontmatter.Entities = []Entity{{Name: "", Role: "thing"}}
	errs := ValidatePage(p)
	require.NotEmpty(t, errs)
	found := false
	for _, e := range errs {
		if e.Field == "entities[0].name" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestValidatePage_NegativeDecayRate(t *testing.T) {
	p := validPage()
	p.Frontmatter.Temporal.DecayRate = -0.01
	errs := ValidatePage(p)
	require.NotEmpty(t, errs)
	found := false
	for _, e := range errs {
		if e.Field == "temporal.decay-rate" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestValidatePage_ZeroDecayRate(t *testing.T) {
	p := validPage()
	p.Frontmatter.Temporal.DecayRate = 0
	errs := ValidatePage(p)
	// Should not have decay-rate error
	for _, e := range errs {
		assert.NotEqual(t, "temporal.decay-rate", e.Field)
	}
}
