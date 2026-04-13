package cli

import (
	"context"
	"fmt"

	"github.com/KHAEntertainment/markedup/index"
	"github.com/KHAEntertainment/markedup/schema"
	"github.com/spf13/cobra"
)

func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check [path]",
		Short: "Validate all pages in a knowledge base",
		Long:  "Loads all markdown files, validates frontmatter, and reports errors grouped by file.",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runCheck,
	}
}

func runCheck(cmd *cobra.Command, args []string) error {
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	result, err := index.Load(context.Background(), path, index.WithIgnoreErrors(true))
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", path, err)
	}

	// Count pages and collect validation errors grouped by file.
	totalPages := result.Index.Pages()
	type fileErrors struct {
		path string
		errs []schema.ValidationError
	}

	var errorFiles []fileErrors
	for _, w := range result.Warnings {
		if len(w.Errors) > 0 {
			errorFiles = append(errorFiles, fileErrors{path: w.Path, errs: w.Errors})
		}
	}

	// Also validate pages that loaded successfully (they may have been counted
	// but warnings only appear for parse errors when WithIgnoreErrors is used).
	for _, page := range result.Index.All() {
		errs := schema.ValidatePage(page)
		if len(errs) > 0 {
			errorFiles = append(errorFiles, fileErrors{path: page.SourcePath, errs: errs})
		}
	}

	totalErrors := 0
	for _, fe := range errorFiles {
		totalErrors += len(fe.errs)
	}

	out := cmd.OutOrStdout()

	if totalErrors == 0 {
		if jsonOutput {
			fmt.Fprintf(out, `{"pages":%d,"errors":0,"status":"valid"}`+"\n", totalPages)
		} else {
			fmt.Fprintf(out, "%d pages checked. All valid.\n", totalPages)
		}
		return nil
	}

	// Print errors.
	if jsonOutput {
		// JSON mode: structured error output.
		type jsonErr struct {
			File    string `json:"file"`
			Field   string `json:"field"`
			Message string `json:"message"`
			Fix     string `json:"fix"`
		}
		type jsonCheck struct {
			Pages  int       `json:"pages"`
			Errors int       `json:"errors"`
			Files  int       `json:"files"`
			Issues []jsonErr `json:"issues"`
		}
		jc := jsonCheck{
			Pages:  totalPages,
			Errors: totalErrors,
			Files:  len(errorFiles),
		}
		for _, fe := range errorFiles {
			for _, e := range fe.errs {
				jc.Issues = append(jc.Issues, jsonErr{
					File:    fe.path,
					Field:   e.Field,
					Message: e.Message,
					Fix:     e.Fix,
				})
			}
		}
		b, _ := marshalJSON(jc)
		fmt.Fprintln(out, string(b))
	} else {
		fmt.Fprintf(out, "Errors found:\n\n")
		for _, fe := range errorFiles {
			fmt.Fprint(out, formatValidationErrors(fe.path, fe.errs))
		}
		fmt.Fprintf(out, "\n%d pages checked. %d errors in %d files.\n", totalPages, totalErrors, len(errorFiles))
		fmt.Fprintln(out, "Fix errors above, then run `markedup check` again.")
	}

	// Exit 1 on errors — return a sentinel to trigger os.Exit(1).
	cmd.SilenceErrors = true
	return fmt.Errorf("validation failed: %d errors", totalErrors)
}
