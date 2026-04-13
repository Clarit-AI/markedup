package schema

import (
	"fmt"
	"regexp"
)

// ValidationError describes a single validation failure for a Page.
type ValidationError struct {
	Field   string // Frontmatter field path, e.g. "id", "relationships[0].strength"
	Message string // Human-readable description of the problem
	Fix     string // Suggested fix
}

// Error implements the error interface for convenience.
func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationRule is a function that inspects a Page and returns any
// validation errors it finds. Rules are composed into a registry and
// executed sequentially by ValidatePage.
type ValidationRule func(*Page) []ValidationError

// defaultRules is the ordered registry of validation rules applied by
// ValidatePage.
var defaultRules = []ValidationRule{
	ruleRequiredFields,
	ruleConfidenceRange,
	ruleRelationships,
	ruleTemporalDates,
	ruleEntityNames,
	ruleDecayRate,
}

// ValidatePage runs every registered validation rule against p and returns
// the collected errors. An empty slice means the page is valid.
func ValidatePage(p *Page) []ValidationError {
	var errs []ValidationError
	for _, rule := range defaultRules {
		errs = append(errs, rule(p)...)
	}
	return errs
}

// ---------- individual rules ----------

// dateRe matches YYYY-MM-DD with basic range checks.
var dateRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$`)

func ruleRequiredFields(p *Page) []ValidationError {
	var errs []ValidationError
	if p.Frontmatter.ID == "" {
		errs = append(errs, ValidationError{
			Field:   "id",
			Message: "id is required",
			Fix:     "add a non-empty 'id' field to the frontmatter",
		})
	}
	if p.Frontmatter.Title == "" {
		errs = append(errs, ValidationError{
			Field:   "title",
			Message: "title is required",
			Fix:     "add a non-empty 'title' field to the frontmatter",
		})
	}
	if p.Frontmatter.EntityType == "" {
		errs = append(errs, ValidationError{
			Field:   "entity-type",
			Message: "entity-type is required",
			Fix:     "add a non-empty 'entity-type' field to the frontmatter",
		})
	}
	return errs
}

func ruleConfidenceRange(p *Page) []ValidationError {
	c := p.Frontmatter.Confidence
	if c < 0 || c > 1 {
		return []ValidationError{{
			Field:   "confidence",
			Message: fmt.Sprintf("confidence must be in [0, 1], got %g", c),
			Fix:     "set confidence to a value between 0.0 and 1.0",
		}}
	}
	return nil
}

func ruleRelationships(p *Page) []ValidationError {
	var errs []ValidationError
	for i, r := range p.Frontmatter.Relationships {
		prefix := fmt.Sprintf("relationships[%d]", i)
		if r.Target == "" {
			errs = append(errs, ValidationError{
				Field:   prefix + ".target",
				Message: "relationship target is required",
				Fix:     "set a non-empty 'target' for the relationship",
			})
		}
		if r.Type == "" {
			errs = append(errs, ValidationError{
				Field:   prefix + ".type",
				Message: "relationship type is required",
				Fix:     "set a non-empty 'type' for the relationship",
			})
		}
		if r.Strength < 0 || r.Strength > 1 {
			errs = append(errs, ValidationError{
				Field:   prefix + ".strength",
				Message: fmt.Sprintf("relationship strength must be in [0, 1], got %g", r.Strength),
				Fix:     "set strength to a value between 0.0 and 1.0",
			})
		}
	}
	return errs
}

func ruleTemporalDates(p *Page) []ValidationError {
	var errs []ValidationError
	check := func(field, value string) {
		if value != "" && !dateRe.MatchString(value) {
			errs = append(errs, ValidationError{
				Field:   "temporal." + field,
				Message: fmt.Sprintf("%s must be in YYYY-MM-DD format, got %q", field, value),
				Fix:     "use YYYY-MM-DD date format (e.g. 2024-01-15)",
			})
		}
	}
	t := p.Frontmatter.Temporal
	check("valid-from", t.ValidFrom)
	check("valid-until", t.ValidUntil)
	check("last-verified", t.LastVerified)
	return errs
}

func ruleEntityNames(p *Page) []ValidationError {
	var errs []ValidationError
	for i, e := range p.Frontmatter.Entities {
		if e.Name == "" {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("entities[%d].name", i),
				Message: "entity name must not be empty",
				Fix:     "set a non-empty 'name' for the entity",
			})
		}
	}
	return errs
}

func ruleDecayRate(p *Page) []ValidationError {
	if p.Frontmatter.Temporal.DecayRate < 0 {
		return []ValidationError{{
			Field:   "temporal.decay-rate",
			Message: fmt.Sprintf("decay-rate must be non-negative, got %g", p.Frontmatter.Temporal.DecayRate),
			Fix:     "set decay-rate to 0 or a positive value",
		}}
	}
	return nil
}
