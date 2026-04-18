package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Clarit-AI/markedup/cache"
	"github.com/Clarit-AI/markedup/embed"
	"github.com/Clarit-AI/markedup/index"
	"github.com/Clarit-AI/markedup/schema"
	"github.com/spf13/cobra"
)

var (
	embedEndpoint  string
	embedModel     string
	embedAPIKey    string
	embedBatchSize int
	embedDir       string
	embedStatus    bool
)

func newEmbedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "embed",
		Short: "Generate and manage embeddings for knowledge base files",
		Long: `Scans a directory of markdown files, identifies files needing (re-)embedding
via the vector cache, embeds them in batches, and saves results to .knowledge/vectors/.

Running embed twice with no file changes does no work (idempotent).`,
		RunE: runEmbed,
	}

	cmd.Flags().StringVar(&embedEndpoint, "endpoint", "", "embedding API endpoint (e.g. http://localhost:11434)")
	cmd.Flags().StringVar(&embedModel, "model", "", "embedding model name (e.g. text-embedding-3-small)")
	cmd.Flags().StringVar(&embedAPIKey, "api-key", "", "API key for authentication (optional for local backends)")
	cmd.Flags().IntVar(&embedBatchSize, "batch-size", 0, "number of texts per embedding request (default: 100)")
	cmd.Flags().StringVar(&embedDir, "dir", ".", "directory to scan for markdown files")
	cmd.Flags().BoolVar(&embedStatus, "status", false, "show embedding status without embedding")

	return cmd
}

func runEmbed(cmd *cobra.Command, args []string) error {
	if embedStatus {
		return runEmbedStatus(cmd)
	}

	// Fall back to config values when CLI flags are empty.
	if embedEndpoint == "" {
		embedEndpoint = appConfig.Embed.Endpoint
	}
	if embedModel == "" {
		embedModel = appConfig.Embed.Model
	}
	if embedAPIKey == "" {
		embedAPIKey = appConfig.Embed.APIKey
	}

	if embedEndpoint == "" {
		return fmt.Errorf("no embedding endpoint configured\n\n" +
			"Configure an endpoint with the --endpoint flag:\n" +
			"  markedup embed --endpoint http://localhost:11434 --model nomic-embed-text\n\n" +
			"Supported endpoints:\n" +
			"  - Ollama:      http://localhost:11434\n" +
			"  - OpenAI:      https://api.openai.com\n" +
			"  - OpenRouter:   https://openrouter.ai/api")
	}

	if embedModel == "" {
		return fmt.Errorf("no embedding model configured\n\n" +
			"Specify a model with the --model flag:\n" +
			"  markedup embed --endpoint http://localhost:11434 --model nomic-embed-text")
	}

	out := cmd.OutOrStdout()

	// Load the knowledge index.
	result, err := index.Load(context.Background(), embedDir, index.WithIgnoreErrors(true))
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", embedDir, err)
	}

	idx := result.Index
	if idx.Pages() == 0 {
		fmt.Fprintln(out, "No markdown files found.")
		return nil
	}

	// Create embedder with a timeout-aware HTTP client.
	cfg := embed.Config{
		Endpoint:  embedEndpoint,
		ModelName: embedModel,
		APIKey:    embedAPIKey,
		BatchSize: embedBatchSize,
	}
	embedder := embed.NewOpenAICompatible(cfg)

	// Probe the embedder to get dimensions and validate connectivity.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer probeCancel()

	probeVecs, err := embedder.Embed(probeCtx, []string{"probe"})
	if err != nil {
		if probeCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("embedding endpoint unreachable (timed out after 30s)\n\n" +
				"Check that the endpoint is running and accessible:\n" +
				"  Endpoint: %s\n" +
				"  Model:    %s", embedEndpoint, embedModel)
		}
		return fmt.Errorf("failed to connect to embedding endpoint: %w\n\n"+
			"Check that the endpoint is running and the model is available:\n"+
			"  Endpoint: %s\n"+
			"  Model:    %s", err, embedEndpoint, embedModel)
	}

	dims := len(probeVecs[0])

	// Set up vector cache.
	vc := cache.NewVectorCache(embedDir)

	// Ensure meta (handles model-change invalidation).
	invalidated, err := vc.EnsureMeta(embedder)
	if err != nil {
		return fmt.Errorf("failed to initialize vector cache: %w", err)
	}
	if invalidated {
		fmt.Fprintln(out, "Model changed — cleared cached vectors.")
	}

	// Build a KnowledgeIndexAdapter for PendingEmbeddings.
	adapter := &knowledgeIndexAdapter{idx: idx}
	pending := vc.PendingEmbeddings(adapter)

	total := idx.Pages()
	cached := total - len(pending)

	if len(pending) == 0 {
		fmt.Fprintf(out, "All %d files already embedded (model: %s, %d dimensions).\n",
			total, embedModel, dims)
		return nil
	}

	fmt.Fprintf(out, "Found %d files, %d cached, %d to embed.\n", total, cached, len(pending))

	// Collect texts and metadata for batch embedding.
	type fileInfo struct {
		id          string
		contentHash string
		text        string
	}
	var files []fileInfo

	for _, fileID := range pending {
		page, ok := idx.Get(fileID)
		if !ok {
			continue
		}
		text := pageText(page)
		hash := contentHash(text)
		files = append(files, fileInfo{
			id:          fileID,
			contentHash: hash,
			text:        text,
		})
	}

	// Embed in batches with progress.
	batchSize := embedBatchSize
	if batchSize <= 0 {
		batchSize = embed.DefaultBatchSize
	}

	embedded := 0
	for start := 0; start < len(files); start += batchSize {
		end := start + batchSize
		if end > len(files) {
			end = len(files)
		}
		batch := files[start:end]

		texts := make([]string, len(batch))
		for i, f := range batch {
			texts[i] = f.text
		}

		fmt.Fprintf(out, "Embedding %d/%d files...\n", start+len(batch), len(files))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		vecs, err := embedder.Embed(ctx, texts)
		cancel()

		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("embedding timed out after 5 minutes at batch starting at file %d\n\n"+
					"Try reducing --batch-size or check endpoint performance", start+1)
			}
			return fmt.Errorf("embedding failed at batch starting at file %d: %w", start+1, err)
		}

		// Save each vector to cache.
		for i, f := range batch {
			if err := vc.SaveVectors(f.id, f.contentHash, vecs[i]); err != nil {
				return fmt.Errorf("failed to save vectors for %s: %w", f.id, err)
			}
			embedded++
		}
	}

	fmt.Fprintf(out, "Done. Embedded %d files (%d skipped, cached).\n", embedded, cached)

	if jsonOutput {
		status := embedStatusJSON{
			Total:      total,
			Embedded:   total,
			Pending:    0,
			Model:      embedModel,
			Dimensions: dims,
		}
		b, _ := json.MarshalIndent(status, "", "  ")
		fmt.Fprintln(out, string(b))
	}

	return nil
}

func runEmbedStatus(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()

	result, err := index.Load(context.Background(), embedDir, index.WithIgnoreErrors(true))
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", embedDir, err)
	}

	idx := result.Index
	total := idx.Pages()

	vc := cache.NewVectorCache(embedDir)
	adapter := &knowledgeIndexAdapter{idx: idx}
	pending := vc.PendingEmbeddings(adapter)
	embedded := total - len(pending)

	// Try to load meta for model info.
	model := "(not configured)"
	dims := 0
	meta, metaErr := loadVectorMeta(embedDir)
	if metaErr == nil {
		model = meta.Model
		dims = meta.Dimensions
	}

	if jsonOutput {
		status := embedStatusJSON{
			Total:      total,
			Embedded:   embedded,
			Pending:    len(pending),
			Model:      model,
			Dimensions: dims,
		}
		b, _ := json.MarshalIndent(status, "", "  ")
		fmt.Fprintln(out, string(b))
		return nil
	}

	fmt.Fprintf(out, "Total files:  %d\n", total)
	fmt.Fprintf(out, "Embedded:     %d\n", embedded)
	fmt.Fprintf(out, "Pending:      %d\n", len(pending))
	fmt.Fprintf(out, "Model:        %s\n", model)
	fmt.Fprintf(out, "Dimensions:   %d\n", dims)

	return nil
}

type embedStatusJSON struct {
	Total      int    `json:"total"`
	Embedded   int    `json:"embedded"`
	Pending    int    `json:"pending"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

// loadVectorMeta reads the .knowledge/meta.json for status display.
func loadVectorMeta(dir string) (*cache.VectorMeta, error) {
	path := dir + "/.knowledge/meta.json"
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta cache.VectorMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// knowledgeIndexAdapter adapts index.KnowledgeIndex to cache.KnowledgeIndexInfo.
type knowledgeIndexAdapter struct {
	idx *index.KnowledgeIndex
}

func (a *knowledgeIndexAdapter) AllFileIDs() []string {
	pages := a.idx.All()
	ids := make([]string, len(pages))
	for i, p := range pages {
		ids[i] = p.Frontmatter.ID
	}
	return ids
}

func (a *knowledgeIndexAdapter) ContentHash(fileID string) string {
	page, ok := a.idx.Get(fileID)
	if !ok {
		return ""
	}
	return contentHash(pageText(page))
}

// pageText extracts the full text content of a page for embedding.
// It combines the title and body to produce a meaningful embedding input.
func pageText(page *schema.Page) string {
	return page.Frontmatter.Title + "\n\n" + page.Body
}

// contentHash computes a SHA-256 hash of the given text.
func contentHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// EmbedSingleFile embeds a single file and saves to cache. This is used by
// the MCP embed_file tool. It returns the vector dimensions on success.
func EmbedSingleFile(dir string, filePath string, embedder embed.Embedder) (int, error) {
	// Load the index to find the file.
	result, err := index.Load(context.Background(), dir, index.WithIgnoreErrors(true))
	if err != nil {
		return 0, fmt.Errorf("failed to load %s: %w", dir, err)
	}

	// Find the page by source path or ID.
	idx := result.Index

	var found bool
	var pageID string
	var pageBody string
	var pageTitle string
	for _, p := range idx.All() {
		if p.SourcePath == filePath || p.Frontmatter.ID == filePath {
			found = true
			pageID = p.Frontmatter.ID
			pageTitle = p.Frontmatter.Title
			pageBody = p.Body
			break
		}
	}

	if !found {
		return 0, fmt.Errorf("file %q not found in knowledge base", filePath)
	}

	text := pageTitle + "\n\n" + pageBody
	hash := contentHash(text)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	vecs, err := embedder.Embed(ctx, []string{text})
	if err != nil {
		return 0, fmt.Errorf("embedding failed for %s: %w", pageID, err)
	}

	vc := cache.NewVectorCache(dir)
	if _, err := vc.EnsureMeta(embedder); err != nil {
		return 0, fmt.Errorf("failed to initialize vector cache: %w", err)
	}

	if err := vc.SaveVectors(pageID, hash, vecs[0]); err != nil {
		return 0, fmt.Errorf("failed to save vectors for %s: %w", pageID, err)
	}

	return len(vecs[0]), nil
}

// GetEmbedStatus returns embedding status for the MCP embed_status tool.
func GetEmbedStatus(dir string) (*embedStatusJSON, error) {
	result, err := index.Load(context.Background(), dir, index.WithIgnoreErrors(true))
	if err != nil {
		return nil, fmt.Errorf("failed to load %s: %w", dir, err)
	}

	idx := result.Index
	total := idx.Pages()

	vc := cache.NewVectorCache(dir)
	adapter := &knowledgeIndexAdapter{idx: idx}
	pending := vc.PendingEmbeddings(adapter)
	embedded := total - len(pending)

	model := ""
	dims := 0
	meta, metaErr := loadVectorMeta(dir)
	if metaErr == nil {
		model = meta.Model
		dims = meta.Dimensions
	}

	return &embedStatusJSON{
		Total:      total,
		Embedded:   embedded,
		Pending:    len(pending),
		Model:      model,
		Dimensions: dims,
	}, nil
}

