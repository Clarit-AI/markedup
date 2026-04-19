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
	enrichFallbackParallel   bool
	enrichFallbackParallelSet bool
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
	cmd.Flags().BoolVar(&enrichFallbackParallel, "fallback-parallel", false, "run LLM fallback retries concurrently (#141). Overrides cfg.Enrich.Fallback.Parallel when set.")

	// PreRun captures whether the user explicitly passed --fallback-parallel
	// so we can distinguish "flag default" from "flag set to false" — the
	// override semantics in the resolution block below depend on it.
	cmd.PreRun = func(c *cobra.Command, _ []string) {
		enrichFallbackParallelSet = c.Flags().Changed("fallback-parallel")
	}

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

	// Validate format, mode, transport flags at the CLI boundary.
	formatName, err := validateFormatName(firstNonEmpty(enrichFormat, appConfig.Format))
	if err != nil {
		return err
	}
	if err := validateNuExtractMode(enrichNuExtractMode); err != nil {
		return err
	}
	if err := validateNuExtractTransport(enrichNuExtractTransport); err != nil {
		return err
	}

	// Resolve format BEFORE filling credentials so the credential fallback
	// reads from the matching config block. --endpoint that matches
	// cfg.NuExtract.Endpoint must NOT end up with Triplex credentials.
	if enrichEndpoint != "" && formatName == "" {
		// Endpoint-origin auto-detect.
		formatName = detectFormatFromEndpoint(enrichEndpoint)
	}

	// Fill endpoint/model/apikey from the config block matching the resolved
	// format. "" (unresolved) falls back to the legacy Triplex-first path.
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
	case "triplex":
		if enrichEndpoint == "" {
			enrichEndpoint = firstNonEmpty(appConfig.Triplex.Endpoint, appConfig.LLM.Endpoint)
		}
		if enrichModel == "" {
			enrichModel = firstNonEmpty(appConfig.Triplex.Model, appConfig.LLM.Model)
		}
		if enrichAPIKey == "" {
			enrichAPIKey = firstNonEmpty(appConfig.Triplex.APIKey, appConfig.LLM.APIKey)
		}
	default:
		// "generic" or "" (nothing resolved) — legacy Triplex-first fallback.
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

	// Phase 1 LLM fallback queue (#140). Files whose Tier 2 extraction failed
	// with a parse error get a job appended here; the batch runs after the
	// main loop completes and re-merges recovered results into the captured
	// post-Tier-1 frontmatter so the final write is identical to a successful
	// primary run.
	type fallbackPending struct {
		job       enrich.FallbackJob
		preTier2  schema.GraphFrontmatter
		fileBytes []byte // for re-replacing frontmatter after recovery
		resultIdx int    // index in results slice to update on outcome
	}
	var fallbackQueue []fallbackPending

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

		// Skip if complete for all configured tiers and not forcing. When no
		// model is configured, Tier 2 fields are unreachable, so we must only
		// require Tier 1 completeness — otherwise idempotent re-runs would
		// re-process every file on every invocation.
		var complete bool
		if modelExtractor != nil {
			complete = enrich.IsComplete(page.Frontmatter)
		} else {
			complete = enrich.IsCompleteForTier(page.Frontmatter, enrich.Tier1)
		}
		if complete && !enrichForce {
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
				// Enqueue parse-error failures for the LLM fallback batch
				// (#140). Empty results from a successful Tier 2 run are NOT
				// enqueued — that's a settled design call (#134).
				if enrich.IsParseError(modelErr) {
					fallbackQueue = append(fallbackQueue, fallbackPending{
						job: enrich.FallbackJob{
							Path:        filePath,
							RelPath:     relPath,
							Body:        page.Body,
							EntityTypes: entityTypes,
							Predicates:  predicates,
							PrimaryErr:  modelErr,
						},
						preTier2:  merged,
						fileBytes: data,
						resultIdx: len(results), // index of the row appended below
					})
				}
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

		// If this file was queued for fallback (Tier 2 parse-error), record
		// it as pending — the dispatcher below will rewrite the row to
		// "recovered" or "failed" once the batch completes. Pending rows are
		// NOT counted toward `enriched` so the summary's primary/recovered/
		// failed split stays clean.
		queuedForFallback := len(fallbackQueue) > 0 &&
			fallbackQueue[len(fallbackQueue)-1].job.Path == filePath &&
			fallbackQueue[len(fallbackQueue)-1].resultIdx == len(results)
		if queuedForFallback {
			results = append(results, enrichResult{Path: filePath, Status: "pending-fallback"})
			continue
		}

		results = append(results, enrichResult{Path: filePath, Status: "enriched"})
		enriched++
	}

	// LLM fallback batch (#140). Sequential, post-main-loop. Recovered files
	// re-merge their model result into the captured post-Tier-1 frontmatter
	// and re-write — the merge path is identical to a successful primary run,
	// so recovered frontmatter is byte-comparable to what NuExtract would
	// have produced.
	var recovered, failedFallback int
	if len(fallbackQueue) > 0 && !enrichDryRun {
		// Resolve fallback config: explicit Enrich.Fallback fields fall back to
		// the same MARKEDUP_LLM_* values that wire markedup_reason.
		fbEndpoint := firstNonEmpty(appConfig.Enrich.Fallback.Endpoint, appConfig.LLM.Endpoint)
		fbModel := firstNonEmpty(appConfig.Enrich.Fallback.Model, appConfig.LLM.Model)
		fbAPIKey := firstNonEmpty(appConfig.Enrich.Fallback.APIKey, appConfig.LLM.APIKey)
		isLocal := enrich.IsLocalEndpoint(fbEndpoint)

		// Tri-state Enabled: nil = local default-on / cloud default-off.
		enabled := isLocal
		if appConfig.Enrich.Fallback.Enabled != nil {
			enabled = *appConfig.Enrich.Fallback.Enabled
		}
		// Tri-state MaxFiles: nil = local unbounded (0) / cloud 50.
		maxFiles := 0
		if !isLocal {
			maxFiles = 50
		}
		if appConfig.Enrich.Fallback.MaxFiles != nil {
			maxFiles = *appConfig.Enrich.Fallback.MaxFiles
		}

		switch {
		case !enabled:
			fmt.Fprintf(out, "LLM fallback disabled — %d files left unrecovered.\n", len(fallbackQueue))
			failedFallback = len(fallbackQueue)
			for _, p := range fallbackQueue {
				results[p.resultIdx] = enrichResult{
					Path:   p.job.Path,
					Status: "fallback-skipped",
					Reason: "fallback disabled",
				}
			}
		case fbEndpoint == "" || fbModel == "":
			fmt.Fprintf(out, "LLM fallback unavailable: MARKEDUP_LLM_ENDPOINT/MODEL not set — %d files left unrecovered.\n", len(fallbackQueue))
			failedFallback = len(fallbackQueue)
			for _, p := range fallbackQueue {
				results[p.resultIdx] = enrichResult{
					Path:   p.job.Path,
					Status: "fallback-skipped",
					Reason: "no fallback endpoint configured",
				}
			}
		default:
			extractor := enrich.NewLLMFallbackExtractor(enrich.LLMFallbackConfig{
				Endpoint:   fbEndpoint,
				Model:      fbModel,
				APIKey:     fbAPIKey,
				SchemaMode: !isLocal, // local runtimes (llama.cpp/Ollama) often lack json_schema; cloud has it.
			})
			jobs := make([]enrich.FallbackJob, len(fallbackQueue))
			for i, p := range fallbackQueue {
				jobs[i] = p.job
			}
			ctx, cancel := context.WithTimeout(context.Background(), enrichTimeout*time.Duration(len(jobs)))
			// Resolve parallel mode: explicit --fallback-parallel flag wins,
			// otherwise inherit cfg.Enrich.Fallback.Parallel.
			parallelMode := appConfig.Enrich.Fallback.Parallel
			if enrichFallbackParallelSet {
				parallelMode = enrichFallbackParallel
			}
			outcomes := enrich.RunFallbackBatch(ctx, extractor, jobs, enrich.FallbackBatchOptions{
				MaxFiles: maxFiles,
				Parallel: parallelMode,
				Workers:  appConfig.Enrich.Fallback.ParallelWorkers,
				Logger: func(format string, args ...any) {
					fmt.Fprintf(out, "  "+format+"\n", args...)
				},
			})
			cancel()

			// Apply outcomes back to the captured pre-Tier-2 frontmatter and
			// rewrite the file. Outcomes correspond 1:1 with fallbackQueue[:len(outcomes)]
			// (the dispatcher preserves order and may truncate at MaxFiles).
			for i, oc := range outcomes {
				p := fallbackQueue[i]
				if !oc.Recovered() {
					failedFallback++
					results[p.resultIdx] = enrichResult{
						Path:   p.job.Path,
						Status: "failed",
						Reason: oc.Err.Error(),
					}
					continue
				}
				// Re-merge through the canonical NuExtract→frontmatter path.
				merged := enrich.MergeModelResult(p.preTier2, oc.Result, opts)
				if merged.Summary == "" || enrichForce {
					if oc.Result.Summary != "" {
						merged = enrich.MergeSummary(merged, oc.Result.Summary, opts)
					} else if modelExtractor != nil {
						bodyPreview := enrich.BodyPreview(p.job.Body, 500)
						sctx, scancel := context.WithTimeout(context.Background(), enrichTimeout)
						summary, sErr := modelExtractor.GenerateSummary(sctx, merged.Title, merged.EntityType, merged.Tags, bodyPreview)
						scancel()
						if sErr == nil {
							merged = enrich.MergeSummary(merged, summary, opts)
						}
					}
				}
				content, wErr := markdown.ReplaceFrontmatter(&merged, p.fileBytes)
				if wErr != nil {
					failedFallback++
					results[p.resultIdx] = enrichResult{
						Path: p.job.Path, Status: "failed",
						Reason: fmt.Sprintf("recovered but failed to render frontmatter: %v", wErr),
					}
					continue
				}
				if wErr := markdown.WriteFrontmatterFile(p.job.Path, content); wErr != nil {
					failedFallback++
					results[p.resultIdx] = enrichResult{
						Path: p.job.Path, Status: "failed",
						Reason: fmt.Sprintf("recovered but failed to write: %v", wErr),
					}
					continue
				}
				recovered++
				results[p.resultIdx] = enrichResult{Path: p.job.Path, Status: "recovered"}
			}
			// Any queued jobs beyond MaxFiles are reported as fallback-skipped.
			for i := len(outcomes); i < len(fallbackQueue); i++ {
				p := fallbackQueue[i]
				failedFallback++
				results[p.resultIdx] = enrichResult{
					Path: p.job.Path, Status: "fallback-skipped",
					Reason: fmt.Sprintf("MaxFiles=%d cap reached", maxFiles),
				}
			}
		}
	} else if len(fallbackQueue) > 0 && enrichDryRun {
		// Dry-run: don't make LLM calls. Just report what would happen.
		fmt.Fprintf(out, "Dry-run: %d files would be queued for LLM fallback.\n", len(fallbackQueue))
		for _, p := range fallbackQueue {
			results[p.resultIdx] = enrichResult{
				Path:   p.job.Path,
				Status: "fallback-pending-dry-run",
			}
		}
	}

	// Print summary.
	failed := errors + failedFallback
	if jsonOutput {
		b, _ := json.MarshalIndent(struct {
			Enriched  int            `json:"enriched"`
			Skipped   int            `json:"skipped"`
			Recovered int            `json:"recovered"`
			Failed    int            `json:"failed"`
			Files     []enrichResult `json:"files"`
		}{
			Enriched:  enriched,
			Skipped:   skipped,
			Recovered: recovered,
			Failed:    failed,
			Files:     results,
		}, "", "  ")
		fmt.Fprintln(out, string(b))
	} else if !enrichDryRun {
		fmt.Fprintf(out, "Enriched %d. %d skipped. %d recovered via LLM fallback. %d failed.\n",
			enriched, skipped, recovered, failed)
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

// validateFormatName normalizes and validates a --format / config.format value.
// Empty input is allowed (returns "" for auto-detect). Unknown names error.
func validateFormatName(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "", "triplex", "nuextract", "generic":
		return n, nil
	}
	return "", fmt.Errorf("invalid --format %q: must be triplex, nuextract, or generic", name)
}

// validateNuExtractMode validates the --nuextract-mode value. Empty allowed.
func validateNuExtractMode(mode string) error {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch m {
	case "", "parallel", "single":
		return nil
	}
	return fmt.Errorf("invalid --nuextract-mode %q: must be parallel or single", mode)
}

// validateNuExtractTransport validates the --nuextract-transport value. Empty allowed.
func validateNuExtractTransport(transport string) error {
	t := strings.ToLower(strings.TrimSpace(transport))
	switch t {
	case "", "native", "manual":
		return nil
	}
	return fmt.Errorf("invalid --nuextract-transport %q: must be native or manual", transport)
}

// detectFormatFromEndpoint returns a format name if the endpoint matches a
// configured block. Triplex wins on tie to preserve legacy behavior; if both
// blocks share the same endpoint, the user should pass --format explicitly.
func detectFormatFromEndpoint(endpoint string) string {
	if appConfig.Triplex.Endpoint != "" && endpoint == appConfig.Triplex.Endpoint {
		return "triplex"
	}
	if appConfig.NuExtract.Endpoint != "" && endpoint == appConfig.NuExtract.Endpoint {
		return "nuextract"
	}
	return ""
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
