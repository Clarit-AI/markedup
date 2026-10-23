package cli

import (
	"context"
	"fmt"

	"github.com/Clarit-AI/markedup/config"
	"github.com/Clarit-AI/markedup/index"
	"github.com/Clarit-AI/markedup/internal/tui"
	"github.com/Clarit-AI/markedup/internal/tui/setup"
	"github.com/spf13/cobra"
)

var (
	hydrateKeys      = config.HydrateKeys
	runSetupWizard   = setup.Run
	runStartupSplash = tui.RunStartupSplash
	runTUIProgram    = tui.Run
)

func newTUICmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Launch the interactive TUI",
		Long:  "Opens an interactive terminal UI for searching, exploring, and viewing the knowledge graph.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(dir)
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "directory containing markdown files")

	return cmd
}

func runTUI(dir string) error {
	// First-run detection: launch setup wizard if no config exists.
	if !config.Exists(dir) {
		cancelled, err := runStartupSplash()
		if err != nil {
			return err
		}
		if cancelled {
			return nil
		}

		cfg, err := runSetupWizard()
		if err != nil {
			return err
		}
		if cfg != nil {
			if err := config.Save(cfg, config.GlobalPath()); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
			appConfig = cfg
		}
	}

	// tui exposes search/embed/llm interactions — populate API keys from the
	// keyring now (issue #153: lazy, opt-in hydration).
	_ = hydrateKeys(appConfig)

	result, err := index.Load(context.Background(), dir, index.WithIgnoreErrors(true))
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", dir, err)
	}

	return runTUIProgram(result.Index, dir, appConfig)
}
