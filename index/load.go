package index

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
	"time"

	"github.com/KHAEntertainment/markedup/markdown"
	"github.com/KHAEntertainment/markedup/schema"
	"golang.org/x/sync/errgroup"
)

// CacheProvider is a Phase 2 hook for caching parsed pages. Implementations
// can short-circuit parsing by returning a cached page for a given path and
// modification time.
type CacheProvider interface {
	Get(path string, modTime time.Time) (*schema.Page, bool)
	Set(path string, modTime time.Time, page *schema.Page)
}

// LoadWarning represents a non-fatal issue encountered during loading.
type LoadWarning struct {
	Path    string
	Message string
	Errors  []schema.ValidationError
}

// LoadResult carries the built index along with any warnings collected
// during the load process.
type LoadResult struct {
	Index    *KnowledgeIndex
	Warnings []LoadWarning
}

// loadConfig holds options for Load.
type loadConfig struct {
	concurrency  int
	filePattern  string
	ignoreErrors bool
	cache        CacheProvider
}

// LoadOption configures the behaviour of Load.
type LoadOption func(*loadConfig)

// WithConcurrency sets the maximum number of concurrent file parsers.
// Values less than 1 are treated as 1.
func WithConcurrency(n int) LoadOption {
	return func(c *loadConfig) {
		if n < 1 {
			n = 1
		}
		c.concurrency = n
	}
}

// WithFilePattern sets the glob pattern used to match markdown files.
// The default is "*.md".
func WithFilePattern(pattern string) LoadOption {
	return func(c *loadConfig) {
		c.filePattern = pattern
	}
}

// WithIgnoreErrors causes Load to collect parse/validation errors as
// warnings instead of failing. Valid pages are still indexed.
func WithIgnoreErrors(ignore bool) LoadOption {
	return func(c *loadConfig) {
		c.ignoreErrors = ignore
	}
}

// WithCache sets a CacheProvider for the loader. This is a Phase 2 hook;
// passing nil (the default) disables caching.
func WithCache(cp CacheProvider) LoadOption {
	return func(c *loadConfig) {
		c.cache = cp
	}
}

// Load walks root to discover markdown files, parses them concurrently
// using bounded workers, validates each page, and builds a KnowledgeIndex.
//
// Dangling relationships (target ID not found in the index) produce
// warnings, not errors. Parse or validation failures are collected as
// warnings when WithIgnoreErrors is true; otherwise the first parse error
// causes Load to return an error.
func Load(ctx context.Context, root string, opts ...LoadOption) (*LoadResult, error) {
	cfg := loadConfig{
		concurrency: 8,
		filePattern: "*.md",
	}
	for _, o := range opts {
		o(&cfg)
	}

	// 1. Collect file paths.
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		matched, matchErr := filepath.Match(cfg.filePattern, d.Name())
		if matchErr != nil {
			return fmt.Errorf("index: bad file pattern %q: %w", cfg.filePattern, matchErr)
		}
		if matched {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("index: walk %s: %w", root, err)
	}

	// 2. Parse concurrently.
	type parseResult struct {
		page *schema.Page
		warn *LoadWarning
	}

	results := make([]parseResult, len(paths))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(cfg.concurrency)

	var mu sync.Mutex
	var firstErr error

	for i, p := range paths {
		i, p := i, p
		g.Go(func() error {
			// Check context cancellation.
			if gctx.Err() != nil {
				return gctx.Err()
			}

			page, parseErr := markdown.ParseFile(p)
			if parseErr != nil {
				if cfg.ignoreErrors {
					mu.Lock()
					results[i] = parseResult{
						warn: &LoadWarning{
							Path:    p,
							Message: parseErr.Error(),
						},
					}
					mu.Unlock()
					return nil
				}
				return fmt.Errorf("index: parse %s: %w", p, parseErr)
			}

			// Validate.
			valErrs := schema.ValidatePage(page)
			if len(valErrs) > 0 && !cfg.ignoreErrors {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("index: validation errors in %s: %s", p, valErrs[0].Error())
				}
				mu.Unlock()
				// Still store the page — we'll check firstErr after the group.
			}

			r := parseResult{page: page}
			if len(valErrs) > 0 {
				r.warn = &LoadWarning{
					Path:    p,
					Message: fmt.Sprintf("%d validation error(s)", len(valErrs)),
					Errors:  valErrs,
				}
			}

			mu.Lock()
			results[i] = r
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	if firstErr != nil {
		return nil, firstErr
	}

	// 3. Collect valid pages and warnings.
	var pages []*schema.Page
	var warnings []LoadWarning

	for _, r := range results {
		if r.warn != nil {
			warnings = append(warnings, *r.warn)
		}
		if r.page != nil {
			pages = append(pages, r.page)
		}
	}

	// 4. Build index single-threaded.
	idx := buildIndex(pages)

	// 5. Check for dangling relationships.
	for _, p := range pages {
		for _, rel := range p.Frontmatter.Relationships {
			if _, ok := idx.byID[rel.Target]; !ok {
				warnings = append(warnings, LoadWarning{
					Path:    p.SourcePath,
					Message: fmt.Sprintf("dangling relationship: target %q not found in index", rel.Target),
				})
			}
		}
	}

	return &LoadResult{
		Index:    idx,
		Warnings: warnings,
	}, nil
}
