// Package cli provides the cobra-based command-line interface for markedup.
package cli

import (
	"fmt"
	"os"

	"github.com/KHAEntertainment/markedup/config"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	jsonOutput bool
	depthFlag  int
	isTTY      bool
	noEnrich   bool
	appConfig  = &config.Config{} // safe zero-value default; overwritten by PersistentPreRun
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "markedup",
		Short:   "Knowledge graph toolkit for markdown files",
		Long:    "markedup builds an in-memory knowledge graph from Obsidian-compatible markdown files with YAML frontmatter.",
		Version: "0.1.0",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			isTTY = isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
			appConfig, _ = config.Load(".")

			if !config.Exists(".") && isTTY {
				fmt.Println("No configuration found. Run 'markedup setup' to configure, or set MARKEDUP_* env vars.")
				fmt.Println()
			}
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
