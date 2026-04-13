package markdown

import (
	"fmt"
	"os"
	"strings"

	"github.com/KHAEntertainment/markedup/schema"
	"gopkg.in/yaml.v3"
)

// PrependFrontmatter serializes fm as YAML and prepends it to body.
// The body is preserved exactly as-is (no trimming, no H1 injection).
// Returns the full file content as []byte.
func PrependFrontmatter(fm *schema.GraphFrontmatter, body string) ([]byte, error) {
	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("markdown: marshal frontmatter: %w", err)
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(yamlBytes)
	b.WriteString("---\n")
	b.WriteString(body)

	return []byte(b.String()), nil
}

// ReplaceFrontmatter replaces existing YAML frontmatter in data with the
// serialized fm, preserving the body after the closing --- delimiter exactly.
// If data has no frontmatter, behaves like PrependFrontmatter.
func ReplaceFrontmatter(fm *schema.GraphFrontmatter, data []byte) ([]byte, error) {
	loc := frontmatterRe.FindSubmatchIndex(data)
	if loc == nil {
		// No existing frontmatter — prepend.
		return PrependFrontmatter(fm, string(data))
	}

	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("markdown: marshal frontmatter: %w", err)
	}

	// loc[4]:loc[5] is the body capture group (group 2).
	// Everything from loc[4] onward is the original body content.
	bodyStart := loc[4]
	originalBody := data[bodyStart:]

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(yamlBytes)
	b.WriteString("---\n")
	b.Write(originalBody)

	return []byte(b.String()), nil
}

// WriteFrontmatterFile atomically writes content to path using a
// temp-file-then-rename pattern.
func WriteFrontmatterFile(path string, content []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		return fmt.Errorf("markdown: write temp file %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("markdown: rename %s -> %s: %w", tmp, path, err)
	}

	return nil
}
