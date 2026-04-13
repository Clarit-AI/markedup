package cli

import (
	"context"
	"fmt"

	"github.com/KHAEntertainment/markedup/index"
	"github.com/spf13/cobra"
)

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
		// Heuristic: if arg looks like a path (contains / or ends with /), treat as path.
		// Otherwise treat as id and use current dir.
		path = "."
		id = args[0]
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
