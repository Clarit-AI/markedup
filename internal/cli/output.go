package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/KHAEntertainment/markedup/index"
	"github.com/KHAEntertainment/markedup/schema"
)

// marshalJSON is a convenience wrapper around json.MarshalIndent.
func marshalJSON(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// OutputFormat selects between plain text and JSON output.
type OutputFormat int

const (
	FormatPlain OutputFormat = iota
	FormatJSON
)

func currentFormat() OutputFormat {
	if jsonOutput {
		return FormatJSON
	}
	return FormatPlain
}

// formatResults formats search results for display.
func formatResults(results []index.Result, format OutputFormat) string {
	if format == FormatJSON {
		type jsonResult struct {
			ID         string  `json:"id"`
			Title      string  `json:"title"`
			EntityType string  `json:"entity_type"`
			Summary    string  `json:"summary,omitempty"`
			Score      float64 `json:"score"`
			Matches    []struct {
				Field string `json:"field"`
				Value string `json:"value"`
			} `json:"matches"`
		}
		out := make([]jsonResult, len(results))
		for i, r := range results {
			jr := jsonResult{
				ID:         r.Page.Frontmatter.ID,
				Title:      r.Page.Frontmatter.Title,
				EntityType: r.Page.Frontmatter.EntityType,
				Summary:    r.Page.Frontmatter.Summary,
				Score:      r.Score,
			}
			for _, m := range r.Matches {
				jr.Matches = append(jr.Matches, struct {
					Field string `json:"field"`
					Value string `json:"value"`
				}{Field: m.Field, Value: m.Value})
			}
			out[i] = jr
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		return string(b)
	}

	if len(results) == 0 {
		return "No results found."
	}

	var b strings.Builder
	for i, r := range results {
		fm := r.Page.Frontmatter
		fmt.Fprintf(&b, "%d. %s", i+1, fm.Title)
		if fm.EntityType != "" {
			fmt.Fprintf(&b, " (%s)", fm.EntityType)
		}
		fmt.Fprintf(&b, "  [score: %.2f]\n", r.Score)
		fmt.Fprintf(&b, "   ID: %s\n", fm.ID)
		if fm.Summary != "" {
			fmt.Fprintf(&b, "   Summary: %s\n", fm.Summary)
		}
		if len(r.Matches) > 0 {
			fields := make([]string, len(r.Matches))
			for j, m := range r.Matches {
				fields[j] = m.Field
			}
			fmt.Fprintf(&b, "   Matched: %s\n", strings.Join(fields, ", "))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatValidationErrors formats validation errors grouped by file.
func formatValidationErrors(path string, errs []schema.ValidationError) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %s\n", path)
	for _, e := range errs {
		fmt.Fprintf(&b, "    - %s: %s\n", e.Field, e.Message)
		fmt.Fprintf(&b, "      Fix: %s\n", e.Fix)
	}
	return b.String()
}

// formatTraversal formats a traversal result for display.
func formatTraversal(result *index.TraversalResult, format OutputFormat) string {
	if format == FormatJSON {
		type jsonNode struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			EntityType string `json:"entity_type"`
			Depth      int    `json:"depth"`
		}
		type jsonEdge struct {
			From     string  `json:"from"`
			To       string  `json:"to"`
			Type     string  `json:"type"`
			Strength float64 `json:"strength"`
		}
		type jsonTraversal struct {
			Root     string     `json:"root"`
			MaxDepth int        `json:"max_depth"`
			Nodes    []jsonNode `json:"nodes"`
			Edges    []jsonEdge `json:"edges"`
		}

		jt := jsonTraversal{
			Root:     result.Root,
			MaxDepth: result.Depth,
		}
		for _, n := range result.Nodes {
			jt.Nodes = append(jt.Nodes, jsonNode{
				ID:         n.Page.Frontmatter.ID,
				Title:      n.Page.Frontmatter.Title,
				EntityType: n.Page.Frontmatter.EntityType,
				Depth:      n.Depth,
			})
		}
		for _, e := range result.Edges {
			jt.Edges = append(jt.Edges, jsonEdge{
				From:     e.From,
				To:       e.To,
				Type:     e.Relationship.Type,
				Strength: e.Relationship.Strength,
			})
		}
		b, _ := json.MarshalIndent(jt, "", "  ")
		return string(b)
	}

	// Plain text tree view.
	var b strings.Builder
	for _, n := range result.Nodes {
		indent := strings.Repeat("  ", n.Depth)
		fm := n.Page.Frontmatter
		if n.Depth == 0 {
			fmt.Fprintf(&b, "%s%s", indent, fm.Title)
		} else {
			fmt.Fprintf(&b, "%s- %s", indent, fm.Title)
		}
		if fm.EntityType != "" {
			fmt.Fprintf(&b, " (%s)", fm.EntityType)
		}
		fmt.Fprintf(&b, "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatStats formats index statistics for display.
func formatStats(idx *index.KnowledgeIndex, format OutputFormat) string {
	if format == FormatJSON {
		type jsonStats struct {
			Pages         int      `json:"pages"`
			Entities      int      `json:"entities"`
			Relationships int      `json:"relationships"`
			Tags          []string `json:"tags"`
		}
		s := jsonStats{
			Pages:         idx.Pages(),
			Entities:      idx.Entities(),
			Relationships: idx.Relationships(),
			Tags:          idx.Tags(),
		}
		b, _ := json.MarshalIndent(s, "", "  ")
		return string(b)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Pages:         %d\n", idx.Pages())
	fmt.Fprintf(&b, "Entities:      %d\n", idx.Entities())
	fmt.Fprintf(&b, "Relationships: %d\n", idx.Relationships())
	tags := idx.Tags()
	if len(tags) > 0 {
		fmt.Fprintf(&b, "Tags:          %s\n", strings.Join(tags, ", "))
	} else {
		fmt.Fprintf(&b, "Tags:          (none)\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatPage formats a single page for display.
func formatPage(page *schema.Page, format OutputFormat) string {
	if format == FormatJSON {
		type jsonPage struct {
			ID             string   `json:"id"`
			Title          string   `json:"title"`
			EntityType     string   `json:"entity_type"`
			Summary        string   `json:"summary,omitempty"`
			Confidence     float64  `json:"confidence"`
			Tags           []string `json:"tags"`
			Body           string   `json:"body"`
			SourcePath     string   `json:"source_path"`
			Entities       []schema.Entity       `json:"entities"`
			Relationships  []schema.Relationship `json:"relationships"`
		}
		jp := jsonPage{
			ID:            page.Frontmatter.ID,
			Title:         page.Frontmatter.Title,
			EntityType:    page.Frontmatter.EntityType,
			Summary:       page.Frontmatter.Summary,
			Confidence:    page.Frontmatter.Confidence,
			Tags:          page.Frontmatter.Tags,
			Body:          page.Body,
			SourcePath:    page.SourcePath,
			Entities:      page.Frontmatter.Entities,
			Relationships: page.Frontmatter.Relationships,
		}
		b, _ := json.MarshalIndent(jp, "", "  ")
		return string(b)
	}

	fm := page.Frontmatter
	var b strings.Builder
	fmt.Fprintf(&b, "ID:          %s\n", fm.ID)
	fmt.Fprintf(&b, "Title:       %s\n", fm.Title)
	fmt.Fprintf(&b, "Type:        %s\n", fm.EntityType)
	if fm.Summary != "" {
		fmt.Fprintf(&b, "Summary:     %s\n", fm.Summary)
	}
	fmt.Fprintf(&b, "Confidence:  %.2f\n", fm.Confidence)
	if len(fm.Tags) > 0 {
		fmt.Fprintf(&b, "Tags:        %s\n", strings.Join(fm.Tags, ", "))
	}
	if len(fm.Entities) > 0 {
		fmt.Fprintf(&b, "Entities:    %d\n", len(fm.Entities))
	}
	if len(fm.Relationships) > 0 {
		fmt.Fprintf(&b, "Relationships: %d\n", len(fm.Relationships))
	}
	if page.Body != "" {
		fmt.Fprintf(&b, "\n%s\n", page.Body)
	}
	return strings.TrimRight(b.String(), "\n")
}
