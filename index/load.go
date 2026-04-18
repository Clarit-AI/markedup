package index

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Clarit-AI/markedup/enrich"
	"github.com/Clarit-AI/markedup/markdown"
	"github.com/Clarit-AI/markedup/schema"
	"golang.org/x/sync/errgroup"
)

// CacheProvider is a Phase 2 hook for caching parsed pages. Implementations
// can short-circuit parsing by returning a cached page for a given path and
// modification time.
type CacheProvider interface {
	Get(path string, modTime time.Time) (*schema.Page, bool)
	Set(path string, modTime time.Time, page *schema.Page)
}

// GraphCacheProvider is the interface for the graph tier cache. It provides
// save/load/stale-check for the entire KnowledgeIndex. Implemented by
// cache.GraphCache.
type GraphCacheProvider interface {
	Save(idx *KnowledgeIndex, dir string) error
	Load(dir string) (*KnowledgeIndex, error)
	Stale(dir string, files []string) (changed, added, removed []string, err error)
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
	autoEnrich   bool
	cache        CacheProvider
	cacheDir     string
	graphCache   GraphCacheProvider
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

// WithAutoEnrich enables or disables automatic frontmatter enrichment for
// files that lack frontmatter. When enabled (the default), files without
// frontmatter are automatically enriched with deterministic extraction and
// the enriched frontmatter is written back to disk.
func WithAutoEnrich(enable bool) LoadOption {
	return func(c *loadConfig) {
		c.autoEnrich = enable
	}
}

// WithCache sets a CacheProvider for the loader. This is a Phase 2 hook;
// passing nil (the default) disables caching.
func WithCache(cp CacheProvider) LoadOption {
	return func(c *loadConfig) {
		c.cache = cp
	}
}

// WithCacheDir sets the project root directory and graph cache provider for
// the graph tier cache. When set, Load will attempt to load a cached
// KnowledgeIndex from .knowledge/graph/ and only re-parse files whose
// SHA-256 checksums have changed. After building the index, it is saved
// back to the cache. An empty dir disables caching.
func WithCacheDir(dir string, gc GraphCacheProvider) LoadOption {
	return func(c *loadConfig) {
		c.cacheDir = dir
		c.graphCache = gc
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
		autoEnrich:  true,
	}
	for _, o := range opts {
		o(&cfg)
	}

	// 1. Collect file paths, skipping .knowledge/ cache directory.
	var paths []string
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

	// 2. If graph cache is configured, try to use it.
	if cfg.cacheDir != "" && cfg.graphCache != nil {
		cachedIdx, cacheErr := cfg.graphCache.Load(cfg.cacheDir)
		if cacheErr == nil {
			// Cache hit — check staleness.
			changed, added, removed, staleErr := cfg.graphCache.Stale(cfg.cacheDir, paths)
			if staleErr == nil && len(changed) == 0 && len(added) == 0 && len(removed) == 0 {
				// Fully cached, no stale files.
				return &LoadResult{Index: cachedIdx}, nil
			}
			// If staleness check succeeded but there are stale files,
			// we still need a full re-parse to rebuild the index correctly.
			// Future optimization: merge cached pages with re-parsed stale pages.
			_ = changed
			_ = added
			_ = removed
		}
		// Cache miss or stale — fall through to full parse.
	}

	// 3. Parse concurrently.
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
			if parseErr != nil && cfg.autoEnrich {
				// Try permissive parse and auto-enrich.
				enrichedPage, enrichErr := autoEnrichFile(p, root)
				if enrichErr == nil {
					page = enrichedPage
					parseErr = nil
					mu.Lock()
					results[i] = parseResult{
						page: page,
						warn: &LoadWarning{
							Path:    p,
							Message: "auto-enriched: frontmatter added",
						},
					}
					mu.Unlock()
					return nil
				}
			}
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

	// 4. Collect valid pages and warnings.
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

	// 5. Build index single-threaded.
	idx := buildIndex(pages)

	// 6. Check for dangling relationships.
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

	// 7. Save to graph cache if configured.
	if cfg.cacheDir != "" && cfg.graphCache != nil {
		// Best-effort save; don't fail the load if cache save fails.
		_ = cfg.graphCache.Save(idx, cfg.cacheDir)
	}

	return &LoadResult{
		Index:    idx,
		Warnings: warnings,
	}, nil
}

// Reload rebuilds the knowledge index from root using the given options. It
// is a semantic alias for Load intended for long-running hosts (daemons, MCP
// servers, TUI) that need to refresh the index after the underlying markdown
// files change.
//
// Load is already idempotent — calling it twice with the same root produces
// equivalent indexes — but Reload exists as a named entry point so callers
// can express intent clearly and so future implementations can add
// incremental or cache-aware refresh behavior without changing the signature.
//
// Concurrency: Reload returns a brand-new *KnowledgeIndex; it never mutates
// any previously returned index. Callers holding a reference to the old
// index may continue to read from it safely. See KnowledgeIndex for the
// recommended swap pattern.
func Reload(ctx context.Context, root string, opts ...LoadOption) (*LoadResult, error) {
	return Load(ctx, root, opts...)
}

// autoEnrichFile reads a file without frontmatter, enriches it with Tier 1
// deterministic extraction, writes the enriched frontmatter back to disk,
// and returns the enriched Page.
func autoEnrichFile(filePath, rootDir string) (*schema.Page, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	page, err := markdown.ParseBytesPermissive(data)
	if err != nil {
		return nil, err
	}

	extracted := enrich.ExtractFromDocument(filePath, page.Body, rootDir)
	merged := enrich.MergeFrontmatter(page.Frontmatter, extracted, enrich.MergeOptions{})

	content, err := markdown.ReplaceFrontmatter(&merged, data)
	if err != nil {
		return nil, err
	}

	if err := markdown.WriteFrontmatterFile(filePath, content); err != nil {
		return nil, err
	}

	page.Frontmatter = merged
	page.SourcePath = filePath
	return page, nil
}
