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

	"github.com/Clarit-AI/markedup/enrich"
	"github.com/Clarit-AI/markedup/markdown"
	"github.com/Clarit-AI/markedup/schema"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	enrichDryRun             bool
	enrichForce              bool
	enrichSkipExist          bool
	enrichModel              string
	enrichEndpoint           string
	enrichAPIKey             string
	enrichEntityTypes        string
	enrichPredicates         string
	enrichTimeout            time.Duration
	enrichFormat             string
	enrichNuExtractMode      string
	enrichNuExtractTransport string
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
	cmd.Flags().StringVar(&enrichFormat, "format", "", "Tier 2 extractor format: triplex, nuextract, generic (default: auto from config)")
	cmd.Flags().StringVar(&enrichNuExtractMode, "nuextract-mode", "", "NuExtract run mode: parallel (default) or single")
	cmd.Flags().StringVar(&enrichNuExtractTransport, "nuextract-transport", "", "NuExtract request transport: native (vLLM/HF) or manual (GGUF). Empty = auto-detect")

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

	// Resolve Tier 2 format: CLI flag > config.Format > legacy auto-detect.
	formatName := strings.ToLower(strings.TrimSpace(enrichFormat))
	if formatName == "" {
		formatName = strings.ToLower(strings.TrimSpace(appConfig.Format))
	}

	// Fill endpoint/model/apikey from the matching config block for the
	// resolved format. Falls back to LLM when the format-specific block is empty.
	switch formatName {
	case "nuextract":
		if enrichEndpoint == "" {
			enrichEndpoint = firstNonEmpty(appConfig.NuExtract.Endpoint, appConfig.LLM.Endpoint)
		}
		if enrichModel == "" {
			enrichModel = firstNonEmpty(appConfig.NuExtract.Model, appConfig.LLM.Model)
		}
		if enrichAPIKey == "" {
			enrichAPIKey = firstNonEmpty(appConfig.NuExtract.APIKey, appConfig.LLM.APIKey)
		}
	default:
		// triplex (explicit), generic, or empty — preserve legacy Triplex-first fallback.
		if enrichEndpoint == "" {
			enrichEndpoint = firstNonEmpty(appConfig.Triplex.Endpoint, appConfig.LLM.Endpoint)
		}
		if enrichModel == "" {
			enrichModel = firstNonEmpty(appConfig.Triplex.Model, appConfig.LLM.Model)
		}
		if enrichAPIKey == "" {
			enrichAPIKey = firstNonEmpty(appConfig.Triplex.APIKey, appConfig.LLM.APIKey)
		}
	}

	// Set up Tier 2 model extractor if --model is specified.
	var modelExtractor *enrich.ModelExtractor
	var entityTypes, predicates []string
	if enrichModel != "" {
		if enrichEndpoint == "" {
			return fmt.Errorf("--endpoint is required when --model is specified")
		}

		modelFormat := resolveModelFormat(formatName, enrichEndpoint)

		mc := enrich.ModelConfig{
			Endpoint: enrichEndpoint,
			Model:    enrichModel,
			APIKey:   enrichAPIKey,
			Format:   modelFormat,
		}
		if modelFormat == enrich.FormatNuExtract {
			mc.NuExtract = enrich.NuExtractOptions{
				Mode:        firstNonEmpty(enrichNuExtractMode, appConfig.NuExtract.Mode),
				Transport:   firstNonEmpty(enrichNuExtractTransport, appConfig.NuExtract.Transport),
				Predicates:  appConfig.NuExtract.Predicates,
				EntityTypes: appConfig.NuExtract.EntityTypes,
			}
		}

		modelExtractor = enrich.NewModelExtractor(mc)
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

// resolveModelFormat picks the enrich.ModelFormat based on (in order):
// explicit CLI/config format name, then legacy endpoint-origin Triplex
// match for backward compat, falling back to FormatGeneric.
func resolveModelFormat(formatName, endpoint string) enrich.ModelFormat {
	switch formatName {
	case "nuextract":
		return enrich.FormatNuExtract
	case "triplex":
		return enrich.FormatTriplex
	case "generic":
		return enrich.FormatGeneric
	}
	// Legacy auto-detect: endpoint matches the Triplex config block.
	if appConfig.Triplex.Endpoint != "" && endpoint == appConfig.Triplex.Endpoint {
		return enrich.FormatTriplex
	}
	// Auto-detect: endpoint matches the NuExtract config block.
	if appConfig.NuExtract.Endpoint != "" && endpoint == appConfig.NuExtract.Endpoint {
		return enrich.FormatNuExtract
	}
	return enrich.FormatGeneric
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
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
