// Package enrich provides automatic frontmatter extraction for markdown files.
// Tier 1 performs deterministic extraction from document structure (headings,
// wikilinks, hashtags, URLs). Tier 2 optionally uses a chat-completion model
// for richer knowledge graph extraction.
package enrich

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Clarit-AI/markedup/schema"
)

// ExtractedFields holds fields extracted from document structure.
type ExtractedFields struct {
	ID            string
	Title         string
	EntityType    string
	Confidence    float64
	Tags          []string
	Relationships []schema.Relationship
	Provenance    schema.Provenance
}

var (
	headingRe  = regexp.MustCompile(`(?m)^#\s+(.+)$`)
	hashtagRe  = regexp.MustCompile(`(?:^|\s)#([a-zA-Z][a-zA-Z0-9_-]*)`)
	wikilinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	urlRe      = regexp.MustCompile(`https?://[^\s\)>\]]+`)
	slugRe     = regexp.MustCompile(`[^a-z0-9]+`)
)

// ExtractFromDocument performs Tier 1 deterministic extraction.
// filePath is the original file path (used for ID, directory-based tags).
// body is the markdown body content.
// rootDir is the knowledge base root (for computing relative path segments).
func ExtractFromDocument(filePath, body, rootDir string) ExtractedFields {
	baseName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

	tags := extractHashtags(body)
	dirTags := directoryTags(filePath, rootDir)
	tags = append(tags, dirTags...)
	tags = dedup(tags)

	return ExtractedFields{
		ID:            slugify(baseName),
		Title:         extractTitle(body, baseName),
		EntityType:    "document",
		Confidence:    0.7,
		Tags:          tags,
		Relationships: extractWikilinks(body),
		Provenance: schema.Provenance{
			Sources:   extractURLs(body),
			CreatedBy: "markedup-enrich-v1",
		},
	}
}

// slugify converts a name to a URL-friendly slug.
func slugify(name string) string {
	s := strings.ToLower(name)
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "untitled"
	}
	return s
}

// extractTitle returns the first H1 heading, or a title derived from filename.
func extractTitle(body, baseName string) string {
	// Skip headings inside code fences.
	cleanBody := stripCodeFences(body)
	m := headingRe.FindStringSubmatch(cleanBody)
	if m != nil {
		return strings.TrimSpace(m[1])
	}
	// Fallback: title-case the filename.
	title := strings.ReplaceAll(baseName, "-", " ")
	title = strings.ReplaceAll(title, "_", " ")
	return strings.TrimSpace(title)
}

// extractHashtags extracts #hashtags from body, skipping code fences.
func extractHashtags(body string) []string {
	cleanBody := stripCodeFences(body)
	matches := hashtagRe.FindAllStringSubmatch(cleanBody, -1)
	if matches == nil {
		return nil
	}
	seen := make(map[string]bool)
	var tags []string
	for _, m := range matches {
		tag := strings.ToLower(m[1])
		if !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	return tags
}

// extractWikilinks detects [[target]] links and returns relationships.
func extractWikilinks(body string) []schema.Relationship {
	// Skip code fences and inline code.
	cleanBody := stripCode(body)
	matches := wikilinkRe.FindAllStringSubmatch(cleanBody, -1)
	if matches == nil {
		return nil
	}
	seen := make(map[string]bool)
	var rels []schema.Relationship
	for _, m := range matches {
		target := slugify(m[1])
		if !seen[target] {
			seen[target] = true
			rels = append(rels, schema.Relationship{
				Target:   target,
				Type:     "related-to",
				Strength: 0.5,
			})
		}
	}
	return rels
}

// extractURLs extracts HTTP/HTTPS URLs from body, deduplicated.
func extractURLs(body string) []string {
	matches := urlRe.FindAllString(body, -1)
	if matches == nil {
		return nil
	}
	seen := make(map[string]bool)
	var urls []string
	for _, u := range matches {
		// Trim trailing punctuation that's likely not part of the URL.
		u = strings.TrimRight(u, ".,;:!?")
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	return urls
}

// directoryTags returns path segments between rootDir and the file as tags.
func directoryTags(filePath, rootDir string) []string {
	rel, err := filepath.Rel(rootDir, filePath)
	if err != nil {
		return nil
	}
	dir := filepath.Dir(rel)
	if dir == "." {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(dir), "/")
	var tags []string
	for _, p := range parts {
		p = strings.ToLower(p)
		if p != "" && p != "." {
			tags = append(tags, p)
		}
	}
	return tags
}

// stripCodeFences removes content inside fenced code blocks (``` ... ```).
func stripCodeFences(body string) string {
	var result strings.Builder
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			result.WriteString(line)
			result.WriteByte('\n')
		}
	}
	return result.String()
}

// stripCode removes content inside fenced code blocks (``` ... ```) and
// inline code spans (`...`).
//
// Fences are handled first and unconditionally, since a fence is unambiguous.
// Inline spans are then removed by dropping the text between paired backticks.
//
// The unpaired case is the one that matters. A document with an odd number of
// backticks — a typo, an unbalanced delimiter — used to cause everything after
// the stray backtick to be discarded, silently deleting real wikilinks,
// hashtags and URLs from the rest of the file. Losing a link to a code span is
// the intended trade; losing the document is not. Per CommonMark an unmatched
// backtick is literal text, so the trailing segment is kept verbatim.
func stripCode(body string) string {
	noFences := stripCodeFences(body)

	parts := strings.Split(noFences, "`")
	if len(parts) <= 1 {
		return noFences
	}

	// An odd number of backticks leaves the final segment unterminated.
	unpairedTail := (len(parts)-1)%2 == 1

	var result strings.Builder
	for i, part := range parts {
		switch {
		case i%2 == 0:
			// Outside any span — always content.
			result.WriteString(part)
		case i == len(parts)-1 && unpairedTail:
			// The opening backtick was never closed. Per CommonMark it is
			// literal text, so the remainder is content too.
			result.WriteString(part)
		default:
			// Between a matched pair — this is code.
		}
	}
	return result.String()
}

// dedup returns a deduplicated slice preserving order.
func dedup(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, s := range ss {
		lower := strings.ToLower(s)
		if !seen[lower] {
			seen[lower] = true
			out = append(out, s)
		}
	}
	return out
}
