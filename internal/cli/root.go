// Package cli provides the cobra-based command-line interface for markedup.
package cli

import (
	"os"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	jsonOutput bool
	depthFlag  int
	isTTY      bool
	noEnrich   bool
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "markedup",
		Short: "Knowledge graph toolkit for markdown files",
		Long:  "markedup builds an in-memory knowledge graph from Obsidian-compatible markdown files with YAML frontmatter.",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			isTTY = isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output in JSON format")
	root.PersistentFlags().IntVar(&depthFlag, "depth", 2, "traversal depth for explore command")
	root.PersistentFlags().BoolVar(&noEnrich, "no-enrich", false, "disable auto-enrichment of files without frontmatter")

	root.AddCommand(newInitCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newSearchCmd())
	root.AddCommand(newExploreCmd())
	root.AddCommand(newShowCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newTUICmd())
	root.AddCommand(newEmbedCmd())
	root.AddCommand(newEnrichCmd())
	root.AddCommand(newExportCmd())
	root.AddCommand(newSetupCmd())

	return root
}

// Execute runs the root command.
func Execute() error {
	return newRootCmd().Execute()
}
