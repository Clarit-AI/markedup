// Package cache provides the graph tier of the two-tier .knowledge/ cache
// system. It serializes/deserializes the KnowledgeIndex to gob format and
// uses SHA-256 content hashes for file-level invalidation.
package cache

import (
	"bufio"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Clarit-AI/markedup/index"
	"github.com/Clarit-AI/markedup/schema"
)

func init() {
	// Register types that appear inside interface{} or map values for gob.
	gob.Register(&schema.Page{})
	gob.Register(schema.GraphFrontmatter{})
	gob.Register(schema.Entity{})
	gob.Register(schema.Relationship{})
	gob.Register(schema.TemporalInfo{})
	gob.Register(schema.Provenance{})
}

// ErrNoCacheFound is returned when the .knowledge/ cache directory or its
// required files do not exist. Callers should fall back to a full load.
var ErrNoCacheFound = errors.New("cache: no cache found")

const (
	graphDir      = "graph"
	indexFile     = "index.gob"
	checksumsFile = "checksums"
)

// GraphCache provides save/load/stale-check for the graph tier cache.
type GraphCache struct{}

// graphPath returns the path to .knowledge/graph/ under the given dir.
func graphPath(dir string) string {
	return filepath.Join(dir, ".knowledge", graphDir)
}

// Save serializes the KnowledgeIndex to .knowledge/graph/index.gob under
// dir and writes per-file SHA-256 checksums to .knowledge/graph/checksums.
// The .knowledge/ directory is auto-created if it does not exist.
func (gc *GraphCache) Save(idx *index.KnowledgeIndex, dir string) error {
	if idx == nil {
		return fmt.Errorf("cache: cannot save nil index")
	}

	gp := graphPath(dir)
	if err := os.MkdirAll(gp, 0o755); err != nil {
		return fmt.Errorf("cache: create dir %s: %w", gp, err)
	}

	// Ensure .knowledge is in .gitignore.
	ensureGitignore(dir)

	// Serialize index to gob.
	data := idx.Export()
	gobPath := filepath.Join(gp, indexFile)
	f, err := os.Create(gobPath)
	if err != nil {
		return fmt.Errorf("cache: create %s: %w", gobPath, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	if err := gob.NewEncoder(w).Encode(data); err != nil {
		return fmt.Errorf("cache: encode index: %w", err)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("cache: flush %s: %w", gobPath, err)
	}

	// Compute and write checksums for all source files.
	checksums, err := computeChecksums(idx)
	if err != nil {
		return err
	}
	csPath := filepath.Join(gp, checksumsFile)
	if err := writeChecksums(csPath, checksums); err != nil {
		return err
	}

	return nil
}

// Load deserializes the KnowledgeIndex from .knowledge/graph/index.gob
// under dir. Returns ErrNoCacheFound if the cache directory or index file
// does not exist.
func (gc *GraphCache) Load(dir string) (*index.KnowledgeIndex, error) {
	gp := graphPath(dir)
	gobPath := filepath.Join(gp, indexFile)

	f, err := os.Open(gobPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoCacheFound
		}
		return nil, fmt.Errorf("cache: open %s: %w", gobPath, err)
	}
	defer f.Close()

	var data index.IndexData
	if err := gob.NewDecoder(bufio.NewReader(f)).Decode(&data); err != nil {
		return nil, fmt.Errorf("cache: decode index: %w", err)
	}

	return index.Import(&data), nil
}

// Stale compares current file SHA-256 hashes against the cached checksums
// and returns which files have changed, been added, or been removed.
// Returns ErrNoCacheFound if the checksums file does not exist.
func (gc *GraphCache) Stale(dir string, files []string) (changed, added, removed []string, err error) {
	gp := graphPath(dir)
	csPath := filepath.Join(gp, checksumsFile)

	cached, err := readChecksums(csPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil, ErrNoCacheFound
		}
		return nil, nil, nil, fmt.Errorf("cache: read checksums: %w", err)
	}

	// Build current checksums for the provided files.
	current := make(map[string]string, len(files))
	for _, path := range files {
		h, hashErr := hashFile(path)
		if hashErr != nil {
			return nil, nil, nil, fmt.Errorf("cache: hash %s: %w", path, hashErr)
		}
		current[path] = h
	}

	// Detect changed and added.
	for path, curHash := range current {
		cachedHash, exists := cached[path]
		if !exists {
			added = append(added, path)
		} else if cachedHash != curHash {
			changed = append(changed, path)
		}
	}

	// Detect removed.
	for path := range cached {
		if _, exists := current[path]; !exists {
			removed = append(removed, path)
		}
	}

	// Sort for deterministic output.
	sort.Strings(changed)
	sort.Strings(added)
	sort.Strings(removed)

	return changed, added, removed, nil
}

// hashFile returns the hex-encoded SHA-256 hash of a file's contents.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// computeChecksums generates SHA-256 hashes for all source files in the index.
func computeChecksums(idx *index.KnowledgeIndex) (map[string]string, error) {
	pages := idx.All()
	checksums := make(map[string]string, len(pages))
	for _, p := range pages {
		if p.SourcePath == "" {
			continue
		}
		h, err := hashFile(p.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("cache: hash %s: %w", p.SourcePath, err)
		}
		checksums[p.SourcePath] = h
	}
	return checksums, nil
}

// writeChecksums writes a path→hash mapping as "hash  path\n" lines.
func writeChecksums(path string, checksums map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("cache: create %s: %w", path, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	// Write sorted for deterministic output.
	paths := make([]string, 0, len(checksums))
	for p := range checksums {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		if _, err := fmt.Fprintf(w, "%s  %s\n", checksums[p], p); err != nil {
			return fmt.Errorf("cache: write checksum: %w", err)
		}
	}
	return w.Flush()
}

// readChecksums parses a checksums file into a path→hash map.
func readChecksums(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	checksums := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		// Format: "hash  path" (two spaces, matching sha256sum format).
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("cache: malformed checksum line: %q", line)
		}
		checksums[parts[1]] = parts[0]
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return checksums, nil
}

// ensureGitignore adds .knowledge/ to .gitignore in dir if not already present.
func ensureGitignore(dir string) {
	giPath := filepath.Join(dir, ".gitignore")
	entry := ".knowledge/"

	// Check if already present.
	if data, err := os.ReadFile(giPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) == entry {
				return
			}
		}
	}

	// Append to .gitignore.
	f, err := os.OpenFile(giPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return // best effort
	}
	defer f.Close()

	// Add a newline before if file is non-empty and doesn't end with newline.
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		// Read last byte to check for trailing newline.
		if data, err := os.ReadFile(giPath); err == nil && len(data) > 0 && data[len(data)-1] != '\n' {
			fmt.Fprintln(f)
		}
	}
	fmt.Fprintln(f, entry)
}
