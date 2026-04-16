package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KHAEntertainment/markedup/enrich"
	"github.com/KHAEntertainment/markedup/markdown"
	"github.com/KHAEntertainment/markedup/schema"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	enrichDryRun      bool
	enrichForce       bool
	enrichSkipExist   bool
	enrichModel       string
	enrichEndpoint    string
	enrichAPIKey      string
	enrichEntityTypes string
	enrichPredicates  string
	enrichTimeout     time.Duration
)

func newEnrichCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enrich [path]",
		Short: "Auto-generate frontmatter for markdown files",
		Long: `Scans markdown files and generates YAML frontmatter from document structure.

Extracts id from filename, title from headings, tags from #hashtags,
relationships from [[wikilinks]], and provenance URLs from the body.

Files with complete frontmatter are skipped unless --force is used.
Partial frontmatter is filled in without overwriting existing fields.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runEnrich,
	}

	cmd.Flags().BoolVar(&enrichDryRun, "dry-run", false, "show proposed frontmatter without modifying files")
	cmd.Flags().BoolVar(&enrichForce, "force", false, "overwrite existing frontmatter fields")
	cmd.Flags().BoolVar(&enrichSkipExist, "skip-existing", false, "only process files with no frontmatter at all")
	cmd.Flags().StringVar(&enrichModel, "model", "", "model name for Tier 2 extraction (e.g. triplex)")
	cmd.Flags().StringVar(&enrichEndpoint, "endpoint", "", "chat completion API endpoint (e.g. http://localhost:11434)")
	cmd.Flags().StringVar(&enrichAPIKey, "api-key", "", "API key for model endpoint (optional)")
	cmd.Flags().StringVar(&enrichEntityTypes, "entity-types", "", "comma-separated entity types for model extraction")
	cmd.Flags().StringVar(&enrichPredicates, "predicates", "", "comma-separated relationship predicates for model extraction")
	cmd.Flags().DurationVar(&enrichTimeout, "timeout", 5*time.Minute, "timeout per file for model extraction (e.g. 5m, 10m)")

	return cmd
}

// enrichResult tracks per-file enrichment status.
type enrichResult struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "enriched", "skipped", "error"
	Reason string `json:"reason,omitempty"`
}

func runEnrich(cmd *cobra.Command, args []string) error {
	path := "."
	if len(args) == 1 {
		path = args[0]
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	out := cmd.OutOrStdout()

	// Determine if target is a single file or directory.
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	var files []string
	rootDir := absPath
	if info.IsDir() {
		files, err = collectMarkdownFiles(absPath)
		if err != nil {
			return err
		}
	} else {
		rootDir = filepath.Dir(absPath)
		files = []string{absPath}
	}

	if len(files) == 0 {
		fmt.Fprintln(out, "No markdown files found.")
		return nil
	}

	// Fall back to config values when CLI flags are empty.
	// Use Triplex config if available, otherwise fall back to LLM config.
	if enrichEndpoint == "" {
		if appConfig.Triplex.Endpoint != "" {
			enrichEndpoint = appConfig.Triplex.Endpoint
		} else {
			enrichEndpoint = appConfig.LLM.Endpoint
		}
	}
	if enrichModel == "" {
		if appConfig.Triplex.Model != "" {
			enrichModel = appConfig.Triplex.Model
		} else {
			enrichModel = appConfig.LLM.Model
		}
	}
	if enrichAPIKey == "" {
		if appConfig.Triplex.APIKey != "" {
			enrichAPIKey = appConfig.Triplex.APIKey
		} else {
			enrichAPIKey = appConfig.LLM.APIKey
		}
	}

	// Set up Tier 2 model extractor if --model is specified.
	var modelExtractor *enrich.ModelExtractor
	var entityTypes, predicates []string
	if enrichModel != "" {
		if enrichEndpoint == "" {
			return fmt.Errorf("--endpoint is required when --model is specified")
		}

		// Detect Triplex format: use FormatTriplex when the model came from
		// the Triplex config section (not the LLM fallback).
		modelFormat := enrich.FormatGeneric
		if appConfig.Triplex.Endpoint != "" && enrichEndpoint == appConfig.Triplex.Endpoint {
			modelFormat = enrich.FormatTriplex
		}

		modelExtractor = enrich.NewModelExtractor(enrich.ModelConfig{
			Endpoint: enrichEndpoint,
			Model:    enrichModel,
			APIKey:   enrichAPIKey,
			Format:   modelFormat,
		})
		if enrichEntityTypes != "" {
			entityTypes = strings.Split(enrichEntityTypes, ",")
			for i := range entityTypes {
				entityTypes[i] = strings.TrimSpace(entityTypes[i])
			}
		}
		if enrichPredicates != "" {
			predicates = strings.Split(enrichPredicates, ",")
			for i := range predicates {
				predicates[i] = strings.TrimSpace(predicates[i])
			}
		}
	}

	opts := enrich.MergeOptions{Force: enrichForce}
	var results []enrichResult
	enriched, skipped, errors := 0, 0, 0

	for _, filePath := range files {
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			results = append(results, enrichResult{Path: filePath, Status: "error", Reason: readErr.Error()})
			errors++
			continue
		}

		hasFM := markdown.HasFrontmatter(data)

		// --skip-existing: only process files with no frontmatter at all.
		if enrichSkipExist && hasFM {
			results = append(results, enrichResult{Path: filePath, Status: "skipped", Reason: "has frontmatter"})
			skipped++
			continue
		}

		page, parseErr := markdown.ParseBytesPermissive(data)
		if parseErr != nil {
			results = append(results, enrichResult{Path: filePath, Status: "error", Reason: parseErr.Error()})
			errors++
			continue
		}

		// Skip if complete and not forcing.
		if enrich.IsComplete(page.Frontmatter) && !enrichForce {
			results = append(results, enrichResult{Path: filePath, Status: "skipped", Reason: "already complete"})
			skipped++
			continue
		}

		// Tier 1: Extract and merge.
		extracted := enrich.ExtractFromDocument(filePath, page.Body, rootDir)
		merged := enrich.MergeFrontmatter(page.Frontmatter, extracted, opts)

		// Tier 2: Model-assisted extraction (if --model specified).
		if modelExtractor != nil {
			relPath, _ := filepath.Rel(rootDir, filePath)
			if relPath == "" {
				relPath = filepath.Base(filePath)
			}
			fmt.Fprintf(out, "Model enriching %s...\n", relPath)

			ctx, cancel := context.WithTimeout(context.Background(), enrichTimeout)
			modelResult, modelErr := modelExtractor.Extract(ctx, page.Body, entityTypes, predicates)
			cancel()

			if modelErr != nil {
				fmt.Fprintf(out, "  Warning: model extraction failed for %s: %v\n", relPath, modelErr)
			} else {
				merged = enrich.MergeModelResult(merged, modelResult, opts)
			}

			// Generate summary (separate call for focused one-sentence output).
			if merged.Summary == "" || enrichForce {
				bodyPreview := enrich.BodyPreview(page.Body, 500)
				summaryCtx, summaryCancel := context.WithTimeout(context.Background(), enrichTimeout)
				summary, summaryErr := modelExtractor.GenerateSummary(summaryCtx, merged.Title, merged.EntityType, merged.Tags, bodyPreview)
				summaryCancel()

				if summaryErr != nil {
					fmt.Fprintf(out, "  Warning: summary generation failed for %s: %v\n", relPath, summaryErr)
				} else {
					merged = enrich.MergeSummary(merged, summary, opts)
				}
			}
		}

		if enrichDryRun {
			relPath, _ := filepath.Rel(rootDir, filePath)
			if relPath == "" {
				relPath = filepath.Base(filePath)
			}
			printDryRun(out, relPath, &merged)
			enriched++
			continue
		}

		// Write enriched frontmatter.
		content, writeErr := markdown.ReplaceFrontmatter(&merged, data)
		if writeErr != nil {
			results = append(results, enrichResult{Path: filePath, Status: "error", Reason: writeErr.Error()})
			errors++
			continue
		}

		if writeErr := markdown.WriteFrontmatterFile(filePath, content); writeErr != nil {
			results = append(results, enrichResult{Path: filePath, Status: "error", Reason: writeErr.Error()})
			errors++
			continue
		}

		results = append(results, enrichResult{Path: filePath, Status: "enriched"})
		enriched++
	}

	// Print summary.
	if jsonOutput {
		b, _ := json.MarshalIndent(struct {
			Enriched int            `json:"enriched"`
			Skipped  int            `json:"skipped"`
			Errors   int            `json:"errors"`
			Files    []enrichResult `json:"files"`
		}{
			Enriched: enriched,
			Skipped:  skipped,
			Errors:   errors,
			Files:    results,
		}, "", "  ")
		fmt.Fprintln(out, string(b))
	} else if !enrichDryRun {
		fmt.Fprintf(out, "Enriched %d files. %d skipped. %d errors.\n", enriched, skipped, errors)
	}

	return nil
}

// printDryRun outputs the proposed frontmatter YAML for a file.
func printDryRun(out io.Writer, relPath string, fm *schema.GraphFrontmatter) {
	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		fmt.Fprintf(out, "--- %s (error: %v) ---\n", relPath, err)
		return
	}
	fmt.Fprintf(out, "--- %s ---\n", relPath)
	fmt.Fprint(out, string(yamlBytes))
	fmt.Fprintln(out)
}

// collectMarkdownFiles walks a directory and returns .md file paths,
// skipping .knowledge/ directories.
func collectMarkdownFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".knowledge" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".md") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
