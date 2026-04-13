package cli

import (
	"context"
	"fmt"

	"github.com/KHAEntertainment/markedup/index"
	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search [path] <query>",
		Short: "Search the knowledge base",
		Long:  "Loads the knowledge base and runs a scored search against all pages.",
		Args:  cobra.RangeArgs(1, 2),
		RunE:  runSearch,
	}
}

func runSearch(cmd *cobra.Command, args []string) error {
	var path, query string
	if len(args) == 1 {
		path = "."
		query = args[0]
	} else {
		path = args[0]
		query = args[1]
	}

	result, err := index.Load(context.Background(), path, index.WithIgnoreErrors(true))
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", path, err)
	}

	results := index.Search(result.Index, query)
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, formatResults(results, currentFormat()))

	if !jsonOutput && len(results) > 0 {
		fmt.Fprintln(out, "\nUse `markedup show <id>` to view a page.")
	}

	return nil
}
