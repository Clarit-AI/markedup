package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/KHAEntertainment/markedup/index"
	"github.com/spf13/cobra"
)

var (
	exportCompact    bool
	exportEntityType string
	exportTag        string
	exportTemporal   bool
)

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export [path]",
		Short: "Export the knowledge graph",
		Long:  "Export the knowledge graph in various formats. Use --compact for a token-efficient summary suitable for LLM consumption.",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runExport,
	}

	cmd.Flags().BoolVar(&exportCompact, "compact", false, "export compact graph summary (no body text)")
	cmd.Flags().StringVar(&exportEntityType, "entity-type", "", "filter by entity type")
	cmd.Flags().StringVar(&exportTag, "tag", "", "filter by tag")
	cmd.Flags().BoolVar(&exportTemporal, "temporal", false, "include temporal metadata")

	return cmd
}

func runExport(cmd *cobra.Command, args []string) error {
	if !exportCompact {
		return fmt.Errorf("export requires --compact flag (other formats not yet supported)")
	}

	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	result, err := index.Load(context.Background(), path, index.WithIgnoreErrors(true))
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", path, err)
	}

	var opts []index.SummaryOption
	if exportEntityType != "" {
		opts = append(opts, index.WithEntityTypeFilter(exportEntityType))
	}
	if exportTag != "" {
		opts = append(opts, index.WithTagFilter(exportTag))
	}
	if exportTemporal {
		opts = append(opts, index.WithTemporal(true))
	}

	summary := result.Index.CompactGraphSummary(opts...)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(summary); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	return nil
}
