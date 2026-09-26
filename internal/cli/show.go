package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Clarit-AI/markedup/index"
	"github.com/spf13/cobra"
)

// looksLikePath reports whether a single `markedup show` argument names a
// knowledge base rather than a page.
//
// An existing directory is decisive. Failing that, the shape of the string
// decides: page IDs are slugs and never contain a path separator, so a
// separator — or a trailing separator, which only a path would carry — means
// path. The filesystem check runs first so a knowledge base whose name happens
// to contain no separator is still recognized.
func looksLikePath(arg string) bool {
	if arg == "" {
		return false
	}
	if info, err := os.Stat(arg); err == nil && info.IsDir() {
		return true
	}
	return strings.ContainsRune(arg, '/') || strings.HasSuffix(arg, string(os.PathSeparator))
}

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [path] [id]",
		Short: "Show a page or index statistics",
		Long:  "Without an ID, shows index statistics. With an ID, dumps the page's frontmatter and body.",
		Args:  cobra.MaximumNArgs(2),
		RunE:  runShow,
	}
}

func runShow(cmd *cobra.Command, args []string) error {
	// Parse args: 0 args = stats for ".", 1 arg = could be path or id, 2 args = path + id.
	var path, id string
	switch len(args) {
	case 0:
		path = "."
	case 1:
		// A single argument is ambiguous: it may name a knowledge base or a
		// page. Decide by whether it exists on disk as a directory, and fall
		// back to the shape of the string.
		//
		// Previously this branch always set path="." and treated the argument
		// as an ID, so `markedup show /path/to/kb` looked up a page whose ID
		// was "/path/to/kb", found nothing, and exited 1 without explaining
		// itself. The comment above this branch described the intended
		// heuristic; the code never implemented it.
		if looksLikePath(args[0]) {
			path = args[0]
		} else {
			path = "."
			id = args[0]
		}
	case 2:
		path = args[0]
		id = args[1]
	}

	result, err := index.Load(context.Background(), path, index.WithIgnoreErrors(true))
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", path, err)
	}

	out := cmd.OutOrStdout()

	if id == "" {
		// Show stats.
		fmt.Fprintln(out, formatStats(result.Index, currentFormat()))
		if !jsonOutput {
			fmt.Fprintln(out, "\nUse `markedup show <id>` to view a specific page.")
		}
		return nil
	}

	// Show specific page.
	page, ok := result.Index.Get(id)
	if !ok {
		return fmt.Errorf("page %q not found in index", id)
	}

	fmt.Fprintln(out, formatPage(page, currentFormat()))
	if !jsonOutput {
		fmt.Fprintln(out, "\nUse `markedup explore . "+id+"` to see its graph neighborhood.")
	}

	return nil
}
