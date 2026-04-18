package markdown

import (
	"fmt"
	"os"

	"github.com/Clarit-AI/markedup/schema"
	"gopkg.in/yaml.v3"
)

// HasFrontmatter reports whether data begins with YAML frontmatter delimiters.
func HasFrontmatter(data []byte) bool {
	return frontmatterRe.Match(data)
}

// ParseBytesPermissive parses markdown bytes into a Page. Unlike ParseBytes,
// it does not error when frontmatter is absent. Files without --- delimiters
// return a Page with zero-value Frontmatter and the full content as Body.
func ParseBytesPermissive(data []byte) (*schema.Page, error) {
	matches := frontmatterRe.FindSubmatch(data)
	if matches == nil {
		// No frontmatter — return full content as body.
		return &schema.Page{
			Body: string(data),
		}, nil
	}

	var fm schema.GraphFrontmatter
	if err := yaml.Unmarshal(matches[1], &fm); err != nil {
		return nil, fmt.Errorf("markdown: unmarshal frontmatter: %w", err)
	}

	body := string(matches[2])

	return &schema.Page{
		Frontmatter: fm,
		Body:        body,
	}, nil
}

// ParseFilePermissive reads a file and calls ParseBytesPermissive.
// The returned Page's SourcePath is set to the given path.
func ParseFilePermissive(path string) (*schema.Page, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("markdown: read file %s: %w", path, err)
	}
	page, err := ParseBytesPermissive(data)
	if err != nil {
		return nil, err
	}
	page.SourcePath = path
	return page, nil
}
