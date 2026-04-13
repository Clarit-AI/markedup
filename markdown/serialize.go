package markdown

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/KHAEntertainment/markedup/schema"
	"gopkg.in/yaml.v3"
)

// relatedSectionRe matches an existing ## Related section and everything after it.
var relatedSectionRe = regexp.MustCompile(`(?m)^## Related\s*\n[\s\S]*$`)

// WritePage serializes a Page to the given writer in Obsidian-compatible
// markdown format: YAML frontmatter, H1 title, body, and auto-generated
// ## Related section with [[wikilinks]].
func WritePage(w io.Writer, p *schema.Page) error {
	// Marshal frontmatter
	yamlBytes, err := yaml.Marshal(&p.Frontmatter)
	if err != nil {
		return fmt.Errorf("markdown: marshal frontmatter: %w", err)
	}

	// Strip the body of any existing ## Related section to prevent duplication.
	body := stripRelatedSection(p.Body)

	// Build output
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(yamlBytes)
	b.WriteString("---\n\n")

	if p.Frontmatter.Title != "" {
		b.WriteString("# ")
		b.WriteString(p.Frontmatter.Title)
		b.WriteString("\n\n")
	}

	if body != "" {
		b.WriteString(body)
		b.WriteString("\n")
	}

	// Auto-generate ## Related from relationships
	if len(p.Frontmatter.Relationships) > 0 {
		if body != "" {
			b.WriteString("\n")
		}
		b.WriteString("## Related\n\n")
		for _, rel := range p.Frontmatter.Relationships {
			fmt.Fprintf(&b, "- [[%s]] (type: %s, strength: %g)\n", rel.Target, rel.Type, rel.Strength)
		}
	}

	_, err = io.WriteString(w, b.String())
	return err
}

// WriteFile atomically writes a Page to the given path. It writes to a
// temporary file first, then renames it into place.
func WriteFile(path string, p *schema.Page) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("markdown: create temp file %s: %w", tmp, err)
	}

	if err := WritePage(f, p); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("markdown: close temp file: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("markdown: rename %s -> %s: %w", tmp, path, err)
	}

	return nil
}

// stripRelatedSection removes an existing ## Related section from a body string.
func stripRelatedSection(body string) string {
	return strings.TrimSpace(relatedSectionRe.ReplaceAllString(body, ""))
}
