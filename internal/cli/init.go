package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const sampleMarkdown = `---
id: example-page
title: Example Knowledge Page
entity-type: concept
confidence: 0.9
tags:
  - example
  - getting-started
entities:
  - name: Example Concept
    aliases:
      - sample concept
    role: primary
relationships:
  - target: another-page
    type: related-to
    strength: 0.8
temporal:
  valid-from: "2024-01-01"
  last-verified: "2024-01-15"
  decay-rate: 0.1
provenance:
  sources:
    - https://example.com
  created-by: markedup-init
semantic-hints:
  - example usage of markedup frontmatter
possible-questions:
  - What is markedup?
  - How do I create a knowledge page?
---

# Example Knowledge Page

This is a sample knowledge page created by ` + "`markedup init`" + `.

## Overview

Each markdown file in your knowledge base represents a node in a knowledge graph.
The YAML frontmatter captures structured metadata: identity, relationships,
temporal validity, and provenance.

## Next Steps

1. Add more .md files with frontmatter to this directory
2. Run ` + "`markedup check`" + ` to validate your pages
3. Run ` + "`markedup search . <query>`" + ` to search across pages
4. Run ` + "`markedup explore . <entity>`" + ` to traverse the graph
`

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize a new knowledge base directory",
		Long:  "Creates a new directory (if needed) with a sample markdown file demonstrating all frontmatter fields.",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runInit,
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	// Create directory if needed.
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}

	// Write sample file.
	samplePath := filepath.Join(path, "example.md")
	if err := os.WriteFile(samplePath, []byte(sampleMarkdown), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", samplePath, err)
	}

	if jsonOutput {
		fmt.Fprintf(cmd.OutOrStdout(), `{"status":"created","path":%q}`+"\n", path)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Knowledge base created at %s.\n", path)
		fmt.Fprintln(cmd.OutOrStdout(), "Add .md files, then run `markedup check`.")
	}

	return nil
}
