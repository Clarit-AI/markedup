package cache

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubEmbedderInfo implements EmbedderInfo for testing.
type stubEmbedderInfo struct {
	model string
	dims  int
}

func (s *stubEmbedderInfo) Model() string  { return s.model }
func (s *stubEmbedderInfo) Dimensions() int { return s.dims }

// stubIndexInfo implements KnowledgeIndexInfo for testing.
type stubIndexInfo struct {
	files  []string
	hashes map[string]string
}

func (s *stubIndexInfo) AllFileIDs() []string { return s.files }
func (s *stubIndexInfo) ContentHash(fileID string) string {
	return s.hashes[fileID]
}

func TestSaveAndLoadVectorsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	vectors := []float32{1.0, 2.5, -3.14, 0.0, math.MaxFloat32, math.SmallestNonzeroFloat32}
	err := vc.SaveVectors("file-a", "hash123", vectors)
	require.NoError(t, err)

	loaded, err := vc.LoadVectors("file-a", "hash123")
	require.NoError(t, err)
	assert.Equal(t, vectors, loaded)
}

func TestSaveVectors_EmptySlice(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	err := vc.SaveVectors("file-a", "hash123", []float32{})
	require.NoError(t, err)

	// HasVectors should return false for empty vector file (size 0).
	assert.False(t, vc.HasVectors("file-a", "hash123"))
}

func TestSaveVectors_NilSlice(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	err := vc.SaveVectors("file-a", "hash123", nil)
	require.NoError(t, err)

	assert.False(t, vc.HasVectors("file-a", "hash123"))
}

func TestSaveVectors_EmptyFileID(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	err := vc.SaveVectors("", "hash123", []float32{1.0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fileID must not be empty")
}

func TestSaveVectors_EmptyContentHash(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	err := vc.SaveVectors("file-a", "", []float32{1.0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contentHash must not be empty")
}

func TestLoadVectors_NotFound(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	_, err := vc.LoadVectors("nonexistent", "hash123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no cached vectors")
}

func TestLoadVectors_EmptyFileID(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	_, err := vc.LoadVectors("", "hash123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fileID must not be empty")
}

func TestLoadVectors_EmptyContentHash(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	_, err := vc.LoadVectors("file-a", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contentHash must not be empty")
}

func TestLoadVectors_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	// Save valid vectors first, then corrupt the file.
	err := vc.SaveVectors("file-a", "hash123", []float32{1.0, 2.0})
	require.NoError(t, err)

	// Write 5 bytes (not divisible by 4) to the vector file.
	path := vc.vecFilePath("file-a", "hash123")
	require.NoError(t, os.WriteFile(path, []byte{1, 2, 3, 4, 5}, 0o644))

	_, err = vc.LoadVectors("file-a", "hash123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "corrupt vector file")
}

func TestHasVectors(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	// No vectors yet.
	assert.False(t, vc.HasVectors("file-a", "hash123"))

	// Save vectors.
	require.NoError(t, vc.SaveVectors("file-a", "hash123", []float32{1.0}))
	assert.True(t, vc.HasVectors("file-a", "hash123"))

	// Different content hash → false.
	assert.False(t, vc.HasVectors("file-a", "hash456"))

	// Empty fileID → false.
	assert.False(t, vc.HasVectors("", "hash123"))

	// Empty contentHash → false.
	assert.False(t, vc.HasVectors("file-a", ""))
}

func TestHasVectors_ContentHashMismatch(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	require.NoError(t, vc.SaveVectors("file-a", "hash-v1", []float32{1.0, 2.0}))

	// Old hash matches.
	assert.True(t, vc.HasVectors("file-a", "hash-v1"))

	// New hash (content changed) → stale, returns false.
	assert.False(t, vc.HasVectors("file-a", "hash-v2"))
}

func TestEnsureMeta_CreateNew(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	info := &stubEmbedderInfo{model: "text-embedding-3-small", dims: 1536}
	invalidated, err := vc.EnsureMeta(info)
	require.NoError(t, err)
	assert.False(t, invalidated)

	// Verify meta.json was created.
	meta, err := vc.loadMeta()
	require.NoError(t, err)
	assert.Equal(t, "text-embedding-3-small", meta.Model)
	assert.Equal(t, 1536, meta.Dimensions)
	assert.Equal(t, 1, meta.Version)
	assert.NotEmpty(t, meta.CreatedAt)
}

func TestEnsureMeta_SameModel(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	info := &stubEmbedderInfo{model: "model-a", dims: 768}
	_, err := vc.EnsureMeta(info)
	require.NoError(t, err)

	// Save some vectors.
	require.NoError(t, vc.SaveVectors("file-a", "hash1", []float32{1.0}))

	// Same model again — no invalidation.
	invalidated, err := vc.EnsureMeta(info)
	require.NoError(t, err)
	assert.False(t, invalidated)

	// Vectors still exist.
	assert.True(t, vc.HasVectors("file-a", "hash1"))
}

func TestEnsureMeta_ModelChange(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	info1 := &stubEmbedderInfo{model: "model-a", dims: 768}
	_, err := vc.EnsureMeta(info1)
	require.NoError(t, err)

	// Save some vectors.
	require.NoError(t, vc.SaveVectors("file-a", "hash1", []float32{1.0, 2.0}))
	assert.True(t, vc.HasVectors("file-a", "hash1"))

	// Change model.
	info2 := &stubEmbedderInfo{model: "model-b", dims: 1536}
	invalidated, err := vc.EnsureMeta(info2)
	require.NoError(t, err)
	assert.True(t, invalidated)

	// Vectors should be cleared.
	assert.False(t, vc.HasVectors("file-a", "hash1"))

	// Meta should reflect new model.
	meta, err := vc.loadMeta()
	require.NoError(t, err)
	assert.Equal(t, "model-b", meta.Model)
	assert.Equal(t, 1536, meta.Dimensions)
}

func TestEnsureMeta_DimensionsChange(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	info1 := &stubEmbedderInfo{model: "model-a", dims: 768}
	_, err := vc.EnsureMeta(info1)
	require.NoError(t, err)

	require.NoError(t, vc.SaveVectors("file-a", "hash1", []float32{1.0}))

	// Same model name, different dimensions.
	info2 := &stubEmbedderInfo{model: "model-a", dims: 1536}
	invalidated, err := vc.EnsureMeta(info2)
	require.NoError(t, err)
	assert.True(t, invalidated)
	assert.False(t, vc.HasVectors("file-a", "hash1"))
}

func TestEnsureMeta_NilEmbedder(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	_, err := vc.EnsureMeta(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedder info must not be nil")
}

func TestPendingEmbeddings_AllPending(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	idx := &stubIndexInfo{
		files:  []string{"file-a", "file-b", "file-c"},
		hashes: map[string]string{"file-a": "h1", "file-b": "h2", "file-c": "h3"},
	}

	pending := vc.PendingEmbeddings(idx)
	assert.Equal(t, []string{"file-a", "file-b", "file-c"}, pending)
}

func TestPendingEmbeddings_SomeCached(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	// Cache vectors for file-b.
	require.NoError(t, vc.SaveVectors("file-b", "h2", []float32{1.0}))

	idx := &stubIndexInfo{
		files:  []string{"file-a", "file-b", "file-c"},
		hashes: map[string]string{"file-a": "h1", "file-b": "h2", "file-c": "h3"},
	}

	pending := vc.PendingEmbeddings(idx)
	assert.Equal(t, []string{"file-a", "file-c"}, pending)
}

func TestPendingEmbeddings_NoneNeeded(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	require.NoError(t, vc.SaveVectors("file-a", "h1", []float32{1.0}))
	require.NoError(t, vc.SaveVectors("file-b", "h2", []float32{2.0}))

	idx := &stubIndexInfo{
		files:  []string{"file-a", "file-b"},
		hashes: map[string]string{"file-a": "h1", "file-b": "h2"},
	}

	pending := vc.PendingEmbeddings(idx)
	assert.Empty(t, pending)
}

func TestPendingEmbeddings_StaleContent(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	// Cache with old hash.
	require.NoError(t, vc.SaveVectors("file-a", "old-hash", []float32{1.0}))

	idx := &stubIndexInfo{
		files:  []string{"file-a"},
		hashes: map[string]string{"file-a": "new-hash"},
	}

	pending := vc.PendingEmbeddings(idx)
	assert.Equal(t, []string{"file-a"}, pending)
}

func TestPendingEmbeddings_EmptyHash(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	idx := &stubIndexInfo{
		files:  []string{"file-a"},
		hashes: map[string]string{}, // no hash for file-a
	}

	pending := vc.PendingEmbeddings(idx)
	assert.Equal(t, []string{"file-a"}, pending)
}

func TestPendingEmbeddings_NilIndex(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	pending := vc.PendingEmbeddings(nil)
	assert.Nil(t, pending)
}

func TestBinaryEncoding_Correctness(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	vectors := []float32{1.0, -1.0, 0.0, 3.14159, math.MaxFloat32}
	require.NoError(t, vc.SaveVectors("test", "hash", vectors))

	// Read the raw file and verify binary format.
	path := vc.vecFilePath("test", "hash")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	// Each float32 is 4 bytes, little-endian.
	assert.Len(t, data, len(vectors)*4)

	for i, expected := range vectors {
		bits := binary.LittleEndian.Uint32(data[i*4 : (i+1)*4])
		actual := math.Float32frombits(bits)
		assert.Equal(t, expected, actual, "vector[%d] mismatch", i)
	}
}

func TestConcurrentSaveLoad(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			fileID := "file-" + string(rune('a'+n))
			hash := "hash-" + string(rune('0'+n))
			vectors := []float32{float32(n), float32(n * 2)}

			err := vc.SaveVectors(fileID, hash, vectors)
			assert.NoError(t, err)

			loaded, err := vc.LoadVectors(fileID, hash)
			assert.NoError(t, err)
			assert.Equal(t, vectors, loaded)
		}(i)
	}
	wg.Wait()
}

func TestVecFilePath_DeterministicAndSafe(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	// Same inputs → same path.
	path1 := vc.vecFilePath("file-a", "hash123")
	path2 := vc.vecFilePath("file-a", "hash123")
	assert.Equal(t, path1, path2)

	// Different inputs → different path.
	path3 := vc.vecFilePath("file-b", "hash123")
	assert.NotEqual(t, path1, path3)

	// Path should be a .vec file in the vectors directory.
	assert.Equal(t, ".vec", filepath.Ext(path1))
	assert.Equal(t, vc.vectorsPath(), filepath.Dir(path1))
}

func TestClearVectors(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	require.NoError(t, vc.SaveVectors("file-a", "h1", []float32{1.0}))
	require.NoError(t, vc.SaveVectors("file-b", "h2", []float32{2.0}))

	assert.True(t, vc.HasVectors("file-a", "h1"))
	assert.True(t, vc.HasVectors("file-b", "h2"))

	require.NoError(t, vc.clearVectors())

	assert.False(t, vc.HasVectors("file-a", "h1"))
	assert.False(t, vc.HasVectors("file-b", "h2"))
}

func TestClearVectors_NoDir(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	// Should not error if vectors directory doesn't exist.
	err := vc.clearVectors()
	assert.NoError(t, err)
}

func TestLargeVectorSet(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	// 1536 dimensions (typical for text-embedding-3-small).
	dims := 1536
	vectors := make([]float32, dims)
	for i := range vectors {
		vectors[i] = float32(i) * 0.001
	}

	require.NoError(t, vc.SaveVectors("large-file", "hash", vectors))

	loaded, err := vc.LoadVectors("large-file", "hash")
	require.NoError(t, err)
	assert.Equal(t, vectors, loaded)

	// Verify file size is exactly dims * 4 bytes.
	path := vc.vecFilePath("large-file", "hash")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, int64(dims*4), info.Size())
}

func TestPendingEmbeddings_Sorted(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	idx := &stubIndexInfo{
		files:  []string{"z-file", "a-file", "m-file"},
		hashes: map[string]string{"z-file": "h1", "a-file": "h2", "m-file": "h3"},
	}

	pending := vc.PendingEmbeddings(idx)
	assert.True(t, sort.StringsAreSorted(pending), "pending should be sorted")
	assert.Equal(t, []string{"a-file", "m-file", "z-file"}, pending)
}

func TestSpecialCharactersInFileID(t *testing.T) {
	dir := t.TempDir()
	vc := NewVectorCache(dir)

	// File IDs with special characters should be handled via SHA-256 hashing.
	fileID := "path/to/file with spaces & symbols!.md"
	vectors := []float32{1.0, 2.0}

	require.NoError(t, vc.SaveVectors(fileID, "hash", vectors))
	assert.True(t, vc.HasVectors(fileID, "hash"))

	loaded, err := vc.LoadVectors(fileID, "hash")
	require.NoError(t, err)
	assert.Equal(t, vectors, loaded)
}
