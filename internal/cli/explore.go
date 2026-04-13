package cli

import (
	"context"
	"fmt"

	"github.com/KHAEntertainment/markedup/index"
	"github.com/spf13/cobra"
)

func newExploreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explore [path] <entity>",
		Short: "Traverse the knowledge graph from an entity",
		Long:  "Loads the knowledge base and performs a graph traversal starting from the given entity ID.",
		Args:  cobra.RangeArgs(1, 2),
		RunE:  runExplore,
	}
}

func runExplore(cmd *cobra.Command, args []string) error {
	var path, entity string
	if len(args) == 1 {
		path = "."
		entity = args[0]
	} else {
		path = args[0]
		entity = args[1]
	}

	result, err := index.Load(context.Background(), path, index.WithIgnoreErrors(true))
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", path, err)
	}

	traversal, err := index.Traverse(result.Index, entity, index.WithDepth(depthFlag))
	if err != nil {
		return fmt.Errorf("traversal failed: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, formatTraversal(traversal, currentFormat()))

	return nil
}
