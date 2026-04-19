package schema

import "strings"

// ValidEntityTypes is the canonical whitelist of document-level entity-type
// classifications recognized by markedup's enrichment pipeline.
//
// Note: schema validation intentionally remains permissive — any non-empty
// string passes ValidatePage (see validation.go), preserving backward
// compatibility with user-authored frontmatter. This whitelist gates only
// automated classification sources (e.g., Tier 2 NuExtract output) to prevent
// small/quantized models from inventing implausible types like "date" or
// "location" for unrelated documents. See issue #128.
//
// The canonical set covers: the default fallback (document), human-centric
// (person), work groupings (project, organization), knowledge artifacts
// (concept, paper), and tools/software (software, tool). event + place
// accommodate calendar-style and geographic notes. Extend this list when a
// genuine new category emerges, and keep the NuExtract prompt enum (issue
// #129) synchronized by consuming this same slice.
var ValidEntityTypes = []string{
	"document",
	"person",
	"project",
	"concept",
	"organization",
	"software",
	"event",
	"place",
	"tool",
	"paper",
}

// IsValidEntityType reports whether t (case-insensitive) is a member of the
// canonical ValidEntityTypes whitelist. Empty strings return false.
func IsValidEntityType(t string) bool {
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	for _, v := range ValidEntityTypes {
		if v == lower {
			return true
		}
	}
	return false
}
