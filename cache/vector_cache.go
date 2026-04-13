// Package cache provides the vector tier of the two-tier .knowledge/ cache
// system. It stores and retrieves embedding vectors per file using efficient
// binary encoding, with model-aware invalidation.
package cache

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// EmbedderInfo provides the model metadata needed by VectorCache for
// model-change detection. This mirrors the relevant subset of the
// embed.Embedder interface (Issue #19) so that this package compiles
// standalone before that package is merged.
type EmbedderInfo interface {
	// Model returns the model identifier used for embedding.
	Model() string

	// Dimensions returns the dimensionality of the embedding vectors.
	Dimensions() int
}

// KnowledgeIndexInfo provides the minimal interface to query which files
// exist in the knowledge index. This avoids importing the index package
// directly, allowing standalone compilation before PR #28 merges.
type KnowledgeIndexInfo interface {
	// AllFileIDs returns all file IDs in the index.
	AllFileIDs() []string

	// ContentHash returns the content hash for a file ID, or "" if unknown.
	ContentHash(fileID string) string
}

// VectorMeta stores metadata about the cached vectors in meta.json.
type VectorMeta struct {
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
	Version    int    `json:"version"`
	CreatedAt  string `json:"created_at"`
}

// VectorCache manages the storage and retrieval of embedding vectors in
// the .knowledge/vectors/ directory. It uses binary encoding for float32
// slices and tracks model metadata for invalidation.
type VectorCache struct {
	dir string // project root directory
}

// NewVectorCache creates a VectorCache rooted at the given project directory.
// The .knowledge/vectors/ subdirectory is created lazily on first write.
func NewVectorCache(dir string) *VectorCache {
	return &VectorCache{dir: dir}
}

const (
	vectorsDir = "vectors"
	metaFile   = "meta.json"
	metaVersion = 1
)

// vectorsPath returns the path to .knowledge/vectors/ under the project root.
func (vc *VectorCache) vectorsPath() string {
	return filepath.Join(vc.dir, ".knowledge", vectorsDir)
}

// metaPath returns the path to .knowledge/meta.json.
func (vc *VectorCache) metaPath() string {
	return filepath.Join(vc.dir, ".knowledge", metaFile)
}

// vecFilePath returns the path to the vector file for a given fileID and
// contentHash. The filename is the SHA-256 of "fileID:contentHash" to
// produce a deterministic, filesystem-safe name.
func (vc *VectorCache) vecFilePath(fileID, contentHash string) string {
	h := sha256.Sum256([]byte(fileID + ":" + contentHash))
	name := hex.EncodeToString(h[:]) + ".vec"
	return filepath.Join(vc.vectorsPath(), name)
}

// SaveVectors writes embedding vectors for a file to the cache using
// efficient binary encoding (4-byte little-endian float32, no headers).
// The vectors directory is created if it does not exist.
func (vc *VectorCache) SaveVectors(fileID string, contentHash string, vectors []float32) error {
	if fileID == "" {
		return fmt.Errorf("cache: fileID must not be empty")
	}
	if contentHash == "" {
		return fmt.Errorf("cache: contentHash must not be empty")
	}

	vp := vc.vectorsPath()
	if err := os.MkdirAll(vp, 0o755); err != nil {
		return fmt.Errorf("cache: create vectors dir: %w", err)
	}

	path := vc.vecFilePath(fileID, contentHash)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("cache: create vector file %s: %w", path, err)
	}
	defer f.Close()

	if err := binary.Write(f, binary.LittleEndian, vectors); err != nil {
		return fmt.Errorf("cache: write vectors: %w", err)
	}

	return nil
}

// LoadVectors reads embedding vectors for a file from the cache. Returns
// an error if the cached vectors do not exist or cannot be read. The
// number of float32 values is inferred from the file size.
func (vc *VectorCache) LoadVectors(fileID string, contentHash string) ([]float32, error) {
	if fileID == "" {
		return nil, fmt.Errorf("cache: fileID must not be empty")
	}
	if contentHash == "" {
		return nil, fmt.Errorf("cache: contentHash must not be empty")
	}

	path := vc.vecFilePath(fileID, contentHash)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("cache: no cached vectors for %s: %w", fileID, err)
		}
		return nil, fmt.Errorf("cache: read vector file %s: %w", path, err)
	}

	if len(data)%4 != 0 {
		return nil, fmt.Errorf("cache: corrupt vector file %s: size %d not divisible by 4", path, len(data))
	}

	n := len(data) / 4
	vectors := make([]float32, n)
	for i := 0; i < n; i++ {
		vectors[i] = float32FromBytes(data[i*4 : (i+1)*4])
	}

	return vectors, nil
}

// HasVectors checks if cached vectors exist for the given fileID and
// contentHash. Returns false if the vector file does not exist (indicating
// the file needs (re-)embedding due to content change or missing cache).
func (vc *VectorCache) HasVectors(fileID string, contentHash string) bool {
	if fileID == "" || contentHash == "" {
		return false
	}
	path := vc.vecFilePath(fileID, contentHash)
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Size() > 0
}

// EnsureMeta checks or initializes the meta.json file. If meta.json exists
// and the model or dimensions differ from the provided embedder config,
// all cached vectors are invalidated (the vectors directory is cleared)
// and a new meta.json is written. If meta.json does not exist, it is created.
// Returns true if vectors were invalidated.
func (vc *VectorCache) EnsureMeta(info EmbedderInfo) (invalidated bool, err error) {
	if info == nil {
		return false, fmt.Errorf("cache: embedder info must not be nil")
	}

	existing, loadErr := vc.loadMeta()
	if loadErr == nil {
		// Meta exists — check for model change.
		if existing.Model == info.Model() && existing.Dimensions == info.Dimensions() {
			return false, nil // no change
		}
		// Model changed — invalidate all vectors.
		if err := vc.clearVectors(); err != nil {
			return false, fmt.Errorf("cache: clear vectors on model change: %w", err)
		}
		invalidated = true
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return false, fmt.Errorf("cache: load meta: %w", loadErr)
	}

	// Write new meta.json.
	meta := VectorMeta{
		Model:      info.Model(),
		Dimensions: info.Dimensions(),
		Version:    metaVersion,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := vc.saveMeta(meta); err != nil {
		return invalidated, err
	}

	return invalidated, nil
}

// PendingEmbeddings returns file IDs from the index that need (re-)embedding.
// A file needs embedding if its vectors are not cached for its current
// content hash.
func (vc *VectorCache) PendingEmbeddings(idx KnowledgeIndexInfo) []string {
	if idx == nil {
		return nil
	}

	fileIDs := idx.AllFileIDs()
	var pending []string
	for _, fid := range fileIDs {
		hash := idx.ContentHash(fid)
		if hash == "" {
			// No content hash available — needs embedding.
			pending = append(pending, fid)
			continue
		}
		if !vc.HasVectors(fid, hash) {
			pending = append(pending, fid)
		}
	}

	sort.Strings(pending)
	return pending
}

// loadMeta reads and parses .knowledge/meta.json.
func (vc *VectorCache) loadMeta() (VectorMeta, error) {
	var meta VectorMeta
	data, err := os.ReadFile(vc.metaPath())
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("cache: parse meta.json: %w", err)
	}
	return meta, nil
}

// saveMeta writes the VectorMeta to .knowledge/meta.json.
func (vc *VectorCache) saveMeta(meta VectorMeta) error {
	knowledgeDir := filepath.Join(vc.dir, ".knowledge")
	if err := os.MkdirAll(knowledgeDir, 0o755); err != nil {
		return fmt.Errorf("cache: create .knowledge dir: %w", err)
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("cache: marshal meta.json: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(vc.metaPath(), data, 0o644); err != nil {
		return fmt.Errorf("cache: write meta.json: %w", err)
	}
	return nil
}

// clearVectors removes all .vec files from the vectors directory.
func (vc *VectorCache) clearVectors() error {
	vp := vc.vectorsPath()
	entries, err := os.ReadDir(vp)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to clear
		}
		return err
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".vec" {
			if err := os.Remove(filepath.Join(vp, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// float32FromBytes converts 4 little-endian bytes to a float32.
func float32FromBytes(b []byte) float32 {
	bits := binary.LittleEndian.Uint32(b)
	return math.Float32frombits(bits)
}
