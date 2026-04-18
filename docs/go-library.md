# Go Library Usage Guide

This guide shows how to use markedup as a Go library for building, querying, and traversing markdown-based knowledge graphs.

## Install

```bash
go get github.com/Clarit-AI/markedup
```

---

## Loading an Index

The `index` package loads a directory of markdown files into an in-memory `KnowledgeIndex`. Parsing runs concurrently with configurable worker count.

### Key Types and Functions

- **`index.Load(ctx, root, ...LoadOption) (*LoadResult, error)`** -- walks `root`, parses markdown files, validates pages, and builds a `KnowledgeIndex`.
- **`index.KnowledgeIndex`** -- the core read-only index. Safe for concurrent reads after construction.
- **`index.LoadResult`** -- carries the built index and any warnings (dangling relationships, validation issues).
- **`index.LoadWarning`** -- a non-fatal issue encountered during loading.

### Load Options

| Option | Description |
|---|---|
| `WithConcurrency(n)` | Max concurrent file parsers (default 8). |
| `WithIgnoreErrors(true)` | Collect parse/validation errors as warnings instead of failing. |
| `WithFilePattern(pattern)` | Glob pattern for matching files (default `"*.md"`). |
| `WithAutoEnrich(bool)` | Auto-enrich files without frontmatter on load (default `true`). |
| `WithCacheDir(dir, gc)` | Enable graph-tier cache via a `GraphCacheProvider`. |
| `WithCache(cp)` | Set a `CacheProvider` for per-file caching. |

### Example

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Clarit-AI/markedup/index"
)

func main() {
	result, err := index.Load(
		context.Background(),
		"./knowledge-base",
		index.WithConcurrency(4),
		index.WithIgnoreErrors(true),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Loaded %d pages, %d warnings\n",
		result.Index.Pages(), len(result.Warnings))

	// Access all tags across the knowledge graph.
	for _, tag := range result.Index.Tags() {
		fmt.Println("tag:", tag)
	}

	// Look up a page by ID.
	if page, ok := result.Index.Get("my-page-id"); ok {
		fmt.Println("Found:", page.Frontmatter.Title)
	}
}
```

---

## Searching

The `index.Search` function scores every page in the index against a query using multi-signal field weighting, match-type multipliers, and temporal confidence decay. It supports optional embedding similarity and reranking.

### Key Types and Functions

- **`index.Search(idx, query, ...SearchOption) []Result`** -- returns results sorted by descending score.
- **`index.Result`** -- holds the matched page, score, and match details.
- **`index.Match`** -- describes a single field-level match (field name, matched value, match type).
- **`index.MatchType`** -- classifies match quality: `MatchExact`, `MatchPrefix`, `MatchContains`, `MatchFuzzy`.

### Search Options

| Option | Description |
|---|---|
| `WithLimit(n)` | Maximum number of results. |
| `WithMinScore(f)` | Minimum score threshold. |
| `WithEmbedder(e)` | Enable semantic similarity via an `embed.Embedder`. |
| `WithVectorCache(vc)` | Set the vector cache for pre-computed embeddings. |
| `WithReranker(r)` | Post-process top results with a `rerank.Reranker`. |
| `WithEmbeddingWeight(w)` | Balance keyword vs embedding score: `final = (1-w)*keyword + w*embedding`. Default 0.3. |
| `WithContext(ctx)` | Context for embedding/reranking HTTP calls. |

### Example

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Clarit-AI/markedup/index"
)

func main() {
	result, err := index.Load(context.Background(), "./knowledge-base")
	if err != nil {
		log.Fatal(err)
	}

	results := index.Search(
		result.Index,
		"kubernetes deployment",
		index.WithLimit(5),
		index.WithMinScore(0.1),
	)

	for _, r := range results {
		fmt.Printf("%.2f  %s\n", r.Score, r.Page.Frontmatter.Title)
		for _, m := range r.Matches {
			fmt.Printf("       [%s] %s\n", m.Field, m.Value)
		}
	}
}
```

---

## Traversing the Graph

The `index.Traverse` function performs iterative BFS or DFS traversal starting from a node. It is cycle-safe and supports forward, reverse, and bidirectional edge following.

### Key Types and Functions

- **`index.Traverse(idx, fromID, ...TraverseOption) (*TraversalResult, error)`** -- traverses the graph from the given node.
- **`index.TraversalResult`** -- holds discovered nodes, edges, and the max depth reached.
- **`index.TraversalNode`** -- pairs a page with the depth at which it was discovered.
- **`index.TraversalEdge`** -- represents a directed edge (from, to, relationship).
- **`index.TraversalStrategy`** -- `BFS` (default) or `DFS`.
- **`index.TraversalDirection`** -- `Forward` (default), `Reverse`, or `Both`.

### Traverse Options

| Option | Description |
|---|---|
| `WithDepth(n)` | Maximum traversal depth (default 2). |
| `WithStrategy(s)` | `index.BFS` or `index.DFS`. |
| `WithDirection(d)` | `index.Forward`, `index.Reverse`, or `index.Both`. |
| `WithRelationshipTypes(types)` | Restrict to specific edge types. `nil` follows all. |

### Example

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Clarit-AI/markedup/index"
)

func main() {
	result, err := index.Load(context.Background(), "./knowledge-base")
	if err != nil {
		log.Fatal(err)
	}

	traversal, err := index.Traverse(
		result.Index,
		"kubernetes-overview",
		index.WithDepth(3),
		index.WithStrategy(index.DFS),
		index.WithDirection(index.Both),
		index.WithRelationshipTypes([]string{"relates-to", "depends-on"}),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Traversal from %s: %d nodes, %d edges, depth %d\n",
		traversal.Root, len(traversal.Nodes), len(traversal.Edges), traversal.Depth)

	for _, node := range traversal.Nodes {
		fmt.Printf("  depth %d: %s\n", node.Depth, node.Page.Frontmatter.Title)
	}
}
```

---

## Parsing Markdown

The `markdown` package parses and serializes Obsidian-compatible markdown files with YAML frontmatter.

### Key Functions

- **`markdown.ParseFile(path) (*schema.Page, error)`** -- reads a file from disk and returns a parsed page.
- **`markdown.ParseBytes(data) (*schema.Page, error)`** -- parses raw markdown bytes.
- **`markdown.ParseReader(r) (*schema.Page, error)`** -- parses from an `io.Reader`.
- **`markdown.WritePage(w, page) error`** -- serializes a page to an `io.Writer` with auto-generated `## Related` section.
- **`markdown.WriteFile(path, page) error`** -- atomically writes a page to disk.

### Example

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/Clarit-AI/markedup/markdown"
	"github.com/Clarit-AI/markedup/schema"
)

func main() {
	// Parse a file.
	page, err := markdown.ParseFile("./notes/kubernetes.md")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Title:", page.Frontmatter.Title)
	fmt.Println("Tags:", page.Frontmatter.Tags)

	// Create and serialize a new page.
	newPage := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:         "docker-basics",
			Title:      "Docker Basics",
			EntityType: "concept",
			Confidence: 0.95,
			Tags:       []string{"docker", "containers"},
			Relationships: []schema.Relationship{
				{Target: "kubernetes-overview", Type: "relates-to", Strength: 0.8},
			},
		},
		Body: "Docker is a platform for containerizing applications.",
	}

	if err := markdown.WritePage(os.Stdout, newPage); err != nil {
		log.Fatal(err)
	}
}
```

---

## Validation

The `schema` package provides validation rules for knowledge graph pages. Validation checks required fields, confidence ranges, relationship strength bounds, temporal date formats, entity names, and decay rates.

### Key Types and Functions

- **`schema.ValidatePage(page) []ValidationError`** -- runs all validation rules. An empty slice means the page is valid.
- **`schema.ValidationError`** -- describes a single failure with `Field`, `Message`, and `Fix` fields.

### Example

```go
package main

import (
	"fmt"

	"github.com/Clarit-AI/markedup/schema"
)

func main() {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:         "", // missing required field
			Title:      "Test",
			Confidence: 1.5, // out of range
		},
	}

	errs := schema.ValidatePage(page)
	for _, e := range errs {
		fmt.Printf("  %s: %s (fix: %s)\n", e.Field, e.Message, e.Fix)
	}
}
```

---

## Embeddings

The `embed` package provides an `Embedder` interface and an implementation that works with any OpenAI-compatible `/v1/embeddings` endpoint (OpenAI, ollama, llama.cpp, OpenRouter).

### Key Types and Functions

- **`embed.Embedder`** -- interface with `Embed(ctx, texts) ([][]float32, error)`, `Dimensions() int`, and `Model() string`.
- **`embed.Config`** -- configuration struct with `Endpoint`, `ModelName`, `APIKey`, `Token`, `BatchSize`, `Dims`, and `HTTPClient`.
- **`embed.NewOpenAICompatible(cfg) *OpenAICompatibleEmbedder`** -- creates an embedder from config.
- **`embed.CosineSimilarity(a, b []float32) float64`** -- computes cosine similarity between two vectors. Returns a value in `[-1.0, 1.0]`.

### Example

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Clarit-AI/markedup/embed"
)

func main() {
	embedder := embed.NewOpenAICompatible(embed.Config{
		Endpoint:  "http://localhost:11434",
		ModelName: "nomic-embed-text",
		Dims:      768,
	})

	vecs, err := embedder.Embed(context.Background(), []string{
		"Kubernetes orchestrates containers",
		"Docker packages applications",
	})
	if err != nil {
		log.Fatal(err)
	}

	sim := embed.CosineSimilarity(vecs[0], vecs[1])
	fmt.Printf("Similarity: %.4f\n", sim)
}
```

---

## Reranking

The `rerank` package provides a `Reranker` interface and a cross-encoder implementation for re-scoring search results using external models (Jina, Cohere, OpenAI-compatible).

### Key Types and Functions

- **`rerank.Reranker`** -- interface with `Rerank(ctx, query, candidates) ([]RankedResult, error)`.
- **`rerank.Candidate`** -- input struct with `ID`, `Text`, and `OriginalScore`.
- **`rerank.RankedResult`** -- output struct embedding `Candidate` plus `Score` and `Rank` (1-based).
- **`rerank.Config`** -- configuration with `Endpoint`, `Model`, `APIKey`, `Token`, `TopN`, `Format`, and `HTTPClient`.
- **`rerank.Format`** -- `FormatJina` (default) or `FormatOpenAI`.
- **`rerank.NewCrossEncoder(cfg) *CrossEncoderReranker`** -- creates a reranker from config.

### Example

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Clarit-AI/markedup/rerank"
)

func main() {
	ranker := rerank.NewCrossEncoder(rerank.Config{
		Endpoint: "https://api.jina.ai",
		Model:    "jina-reranker-v2-base-multilingual",
		APIKey:   "jina_xxx",
		TopN:     3,
	})

	results, err := ranker.Rerank(
		context.Background(),
		"container orchestration",
		[]rerank.Candidate{
			{ID: "k8s", Text: "Kubernetes manages container workloads", OriginalScore: 0.9},
			{ID: "docker", Text: "Docker builds container images", OriginalScore: 0.7},
			{ID: "git", Text: "Git is a version control system", OriginalScore: 0.3},
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	for _, r := range results {
		fmt.Printf("#%d  %.4f  %s\n", r.Rank, r.Score, r.ID)
	}
}
```

---

## LLM Chat Completion

The `llm` package provides a shared OpenAI-compatible chat completion HTTP client. It is used by the `markedup_reason` MCP tool and can be used directly for custom LLM integrations.

### Key Types and Functions

- **`llm.Config`** -- configuration struct:
  - `Endpoint string` -- Base URL (e.g. `http://localhost:11434`)
  - `Model string` -- Model name (e.g. `llama3`, `mistral`)
  - `APIKey string` -- Optional API key for authentication
  - `HTTPClient *http.Client` -- Optional; defaults to `http.DefaultClient`
- **`llm.NewClient(cfg Config) *Client`** -- creates a new chat completion client.
- **`llm.Message`** -- a single chat message with `Role` and `Content` string fields.
- **`(*Client).ChatCompletion(ctx context.Context, messages []Message) (string, error)`** -- sends a chat completion request to `{Endpoint}/v1/chat/completions` and returns the first choice's content.

### Example

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Clarit-AI/markedup/llm"
)

func main() {
	client := llm.NewClient(llm.Config{
		Endpoint: "http://localhost:11434",
		Model:    "llama3",
	})

	messages := []llm.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "What is a knowledge graph?"},
	}

	response, err := client.ChatCompletion(context.Background(), messages)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(response)
}
```

---

## Compact Graph Summary

The `index.CompactGraphSummary` method builds a token-efficient summary of the knowledge graph, excluding body text. It is used by the `markedup_get_structure` MCP tool and the `export --compact` CLI command.

### Key Types and Functions

- **`(*KnowledgeIndex).CompactGraphSummary(opts ...SummaryOption) *GraphSummary`** -- builds the compact summary.
- **`index.GraphSummary`** -- top-level result with `Stats SummaryStats` and `Pages []SummaryNode`.
- **`index.SummaryStats`** -- aggregate counts: `Pages`, `Relationships`, `EntityTypes`, `Tags`.
- **`index.SummaryNode`** -- compact page representation: `ID`, `Title`, `Summary`, `EntityType`, `Tags`, `Confidence`, `Relationships` (optional), and temporal fields (optional).

### Summary Options

| Option | Description |
|---|---|
| `WithEntityTypeFilter(et)` | Restrict to pages matching the given entity type. |
| `WithTagFilter(tag)` | Restrict to pages containing the given tag. |
| `WithRelationships(bool)` | Include relationship edges in each node. Default `true`. |
| `WithTemporal(bool)` | Include temporal metadata in each node. Default `false`. |
| `WithMaxPages(n)` | Limit the number of pages in the summary. Applied after filtering. |
| `WithPageIDs(ids)` | Restrict to pages whose ID is in the given slice. |

### Example

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/Clarit-AI/markedup/index"
)

func main() {
	result, err := index.Load(context.Background(), "./knowledge-base")
	if err != nil {
		log.Fatal(err)
	}

	// Get a compact summary filtered to "person" entities with temporal metadata.
	summary := result.Index.CompactGraphSummary(
		index.WithEntityTypeFilter("person"),
		index.WithTemporal(true),
		index.WithMaxPages(50),
	)

	out, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Println(string(out))
}
```

---

## Caching

The `cache` package provides a two-tier `.knowledge/` sidecar cache: a graph tier for the full index and a vector tier for embedding vectors.

### Graph Cache

- **`cache.GraphCache`** -- implements `index.GraphCacheProvider`. Serializes the index to gob format with SHA-256 file checksums for invalidation.

Use `GraphCache` with `index.Load` via the `WithCacheDir` option:

```go
package main

import (
	"context"
	"log"

	"github.com/Clarit-AI/markedup/cache"
	"github.com/Clarit-AI/markedup/index"
)

func main() {
	gc := &cache.GraphCache{}
	result, err := index.Load(
		context.Background(),
		"./knowledge-base",
		index.WithCacheDir("./knowledge-base", gc),
	)
	if err != nil {
		log.Fatal(err)
	}
	_ = result.Index
}
```

### Vector Cache

- **`cache.NewVectorCache(dir) *VectorCache`** -- creates a vector cache rooted at the project directory.
- **`(*VectorCache).SaveVectors(fileID, contentHash, vectors)`** -- stores embedding vectors as binary float32.
- **`(*VectorCache).LoadVectors(fileID, contentHash) ([]float32, error)`** -- retrieves cached vectors.
- **`(*VectorCache).HasVectors(fileID, contentHash) bool`** -- checks if vectors are cached.
- **`(*VectorCache).EnsureMeta(info EmbedderInfo) (invalidated bool, err error)`** -- checks or initializes model metadata; clears vectors on model change.

```go
package main

import (
	"fmt"
	"log"

	"github.com/Clarit-AI/markedup/cache"
)

func main() {
	vc := cache.NewVectorCache("./knowledge-base")

	// Save vectors for a page.
	vectors := []float32{0.1, 0.2, 0.3, 0.4}
	if err := vc.SaveVectors("my-page", "abc123", vectors); err != nil {
		log.Fatal(err)
	}

	// Load them back.
	loaded, err := vc.LoadVectors("my-page", "abc123")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Vectors:", loaded)
}
```

---

## Complete Example

This example loads a knowledge base, searches with embeddings enabled, and displays the results.

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Clarit-AI/markedup/cache"
	"github.com/Clarit-AI/markedup/embed"
	"github.com/Clarit-AI/markedup/index"
)

func main() {
	ctx := context.Background()
	projectDir := "./knowledge-base"

	// 1. Set up caching.
	gc := &cache.GraphCache{}
	vc := cache.NewVectorCache(projectDir)

	// 2. Load the knowledge graph with caching enabled.
	result, err := index.Load(ctx, projectDir,
		index.WithConcurrency(8),
		index.WithIgnoreErrors(true),
		index.WithCacheDir(projectDir, gc),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Loaded %d pages, %d entities, %d relationships\n",
		result.Index.Pages(),
		result.Index.Entities(),
		result.Index.Relationships(),
	)

	// 3. Set up an embedder (optional, for semantic search).
	embedder := embed.NewOpenAICompatible(embed.Config{
		Endpoint:  "http://localhost:11434",
		ModelName: "nomic-embed-text",
		Dims:      768,
	})

	// 4. Search with keyword + embedding hybrid scoring.
	results := index.Search(result.Index, "container orchestration",
		index.WithLimit(5),
		index.WithMinScore(0.1),
		index.WithEmbedder(embedder),
		index.WithVectorCache(vc),
		index.WithEmbeddingWeight(0.3),
		index.WithContext(ctx),
	)

	// 5. Display results.
	for i, r := range results {
		fmt.Printf("\n--- Result %d (score: %.4f) ---\n", i+1, r.Score)
		fmt.Printf("Title: %s\n", r.Page.Frontmatter.Title)
		fmt.Printf("ID:    %s\n", r.Page.Frontmatter.ID)
		if r.Page.Frontmatter.EntityType != "" {
			fmt.Printf("Type:  %s\n", r.Page.Frontmatter.EntityType)
		}
		for _, m := range r.Matches {
			fmt.Printf("  matched [%s]: %s\n", m.Field, m.Value)
		}
	}

	// 6. Traverse the graph from the top result.
	if len(results) > 0 {
		topID := results[0].Page.Frontmatter.ID
		traversal, err := index.Traverse(result.Index, topID,
			index.WithDepth(2),
			index.WithDirection(index.Both),
		)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\nGraph neighborhood of %q: %d nodes, %d edges\n",
			topID, len(traversal.Nodes), len(traversal.Edges))
		for _, node := range traversal.Nodes {
			fmt.Printf("  depth %d: %s\n", node.Depth, node.Page.Frontmatter.Title)
		}
	}
}
```

---

## Enriching Files Programmatically

The `enrich` package provides deterministic frontmatter extraction and merge logic. Use it to programmatically enrich files that lack frontmatter.

### Key Types and Functions

- **`enrich.ExtractFromDocument(filePath, body, rootDir) ExtractedFields`** -- Tier 1 deterministic extraction from document structure. Extracts `id`, `title`, `tags`, `relationships`, and `provenance` from headings, `#hashtags`, `[[wikilinks]]`, and URLs.
- **`enrich.MergeFrontmatter(existing, extracted, opts) GraphFrontmatter`** -- Merge extracted fields into existing frontmatter (fill missing, union arrays).
- **`enrich.IsComplete(fm) bool`** -- Check if frontmatter has all required fields.
- **`enrich.NewModelExtractor(cfg ModelConfig) *ModelExtractor`** -- Create a Tier 2 model extractor for chat-completion APIs.
- **`(*ModelExtractor).Extract(ctx, body, entityTypes, predicates) (*ModelResult, error)`** -- Send document body to the model for entity/relationship extraction. Pass `nil` for entityTypes/predicates to use defaults.
- **`(*ModelExtractor).GenerateSummary(ctx, title, entityType, tags, bodyPreview) (string, error)`** -- Generate a one-sentence entity description using the model.
- **`enrich.MergeModelResult(existing, model, opts) GraphFrontmatter`** -- Merge model extraction results into frontmatter (fill missing or force overwrite).
- **`enrich.BodyPreview(body, maxTokens) string`** -- Return an approximate preview of the body for summary generation.

### Model Configuration

`enrich.ModelConfig` configures the Tier 2 model extractor:

| Field | Type | Description |
|---|---|---|
| `Endpoint` | `string` | Base URL for the chat completion API (e.g. `http://localhost:11434`). |
| `Model` | `string` | Model name (e.g. `triplex`). |
| `APIKey` | `string` | Optional API key for authentication. |
| `HTTPClient` | `*http.Client` | Optional; defaults to `http.DefaultClient`. |
| `Format` | `ModelFormat` | Prompt/parse strategy: `FormatGeneric` (default) or `FormatTriplex`. |

`enrich.ModelFormat` controls how the model is prompted and how its output is parsed:

- **`FormatGeneric`** -- Generic chat completion: sends a system prompt requesting JSON output with entities, relationships, entity_type, semantic_hints, and possible_questions.
- **`FormatTriplex`** -- Triplex NER fine-tune format: uses the Triplex-specific prompt preamble and parses the `entities_and_triples` output format.

### Permissive Parsing

The `markdown` package provides permissive parsing for files that may lack frontmatter:

- **`markdown.ParseBytesPermissive(data) (*Page, error)`** -- Returns a Page with zero-value Frontmatter when no `---` delimiters are found.
- **`markdown.ParseFilePermissive(path) (*Page, error)`** -- File-based variant.
- **`markdown.HasFrontmatter(data) bool`** -- Quick check for frontmatter presence.

### Frontmatter Writing

- **`markdown.PrependFrontmatter(fm, body) ([]byte, error)`** -- Serialize frontmatter and prepend to body (body preserved byte-for-byte).
- **`markdown.ReplaceFrontmatter(fm, data) ([]byte, error)`** -- Replace existing frontmatter in file data, preserving the body exactly.
- **`markdown.WriteFrontmatterFile(path, content) error`** -- Atomic write (temp file + rename).

### Example

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/Clarit-AI/markedup/enrich"
	"github.com/Clarit-AI/markedup/markdown"
)

func main() {
	path := "./notes/paper.md"
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}

	// Parse permissively (handles files with or without frontmatter).
	page, err := markdown.ParseBytesPermissive(data)
	if err != nil {
		log.Fatal(err)
	}

	// Skip if already complete.
	if enrich.IsComplete(page.Frontmatter) {
		fmt.Println("Already enriched, skipping.")
		return
	}

	// Extract fields from document structure.
	extracted := enrich.ExtractFromDocument(path, page.Body, "./notes")

	// Merge into existing frontmatter (non-destructive).
	merged := enrich.MergeFrontmatter(page.Frontmatter, extracted, enrich.MergeOptions{})

	// Write back to file.
	content, err := markdown.ReplaceFrontmatter(&merged, data)
	if err != nil {
		log.Fatal(err)
	}
	if err := markdown.WriteFrontmatterFile(path, content); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Enriched %s: id=%s title=%q\n", path, merged.ID, merged.Title)
}
```
