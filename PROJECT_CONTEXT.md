# PROJECT_CONTEXT

## Repository Info
- **Repo URL**: https://github.com/KHAEntertainment/markedup
- **Main branch**: main
- **Created**: 2026-04-12

## Tech Stack
- **Language**: Go 1.22+
- **CLI framework**: cobra v1.8
- **YAML**: gopkg.in/yaml.v3
- **Test framework**: testing + testify
- **Concurrency**: golang.org/x/sync/errgroup
- **Module path**: github.com/KHAEntertainment/markedup

## Architecture Decisions
- **No database**: in-memory index built from filesystem scan
- **MCP protocol**: mark3labs/mcp-go SDK v0.47.1, stdio transport (was hand-rolled JSON-RPC 2.0, migrated PR #44)
- **Frontmatter parsing**: regex + yaml.v3 (no external frontmatter lib)
- **Output modes**: plain text (default) + JSON (--json flag) + BubbleTea TUI (`markedup tui`)
- **Obsidian compatibility**: [[wikilinks]] in body, tags array in frontmatter, ## Related auto-generated section
- **Search pipeline**: keyword scoring + optional embedding similarity (cosine, weight configurable) + optional cross-encoder reranking
- **Score blending**: `final = (1-w)*keyword + w*embedding`, default w=0.3; reranker processes top min(20, len) results
- **Embedder design**: Single `OpenAICompatibleEmbedder` covers all backends (ollama, llama.cpp, OpenRouter, OpenAI) via `/v1/embeddings`
- **Reranker design**: `CrossEncoderReranker` calls reranker APIs (Jina, Cohere, OpenAI-compatible); post-processing step after initial search
- **Cache architecture**: Two-tier `.knowledge/` sidecar — graph tier (gob-encoded index + SHA-256 checksums) + vector tier (binary float32 per file + meta.json for model tracking)
- **Auth config**: API key immediate, OAuth token field reserved for future OpenRouter browser auth integration
- **TUI framework**: BubbleTea + Lip Gloss + Bubbles (charmbracelet ecosystem)
- **New packages**: `embed/`, `rerank/`, `cache/`, `internal/tui/`

## Code Style Conventions
- **Naming**: Go standard (PascalCase exports, camelCase internal)
- **YAML tags**: kebab-case (`entity-type`, `valid-from`, `semantic-hints`)
- **Directory structure**:
  ```
  schema/         # Core types + validation
  markdown/       # Parse + serialize
  index/          # KnowledgeIndex, load, search, traverse
  temporal/       # Confidence decay
  embed/          # Embedder interface + OpenAI-compatible provider + cosine similarity
  rerank/         # Reranker interface + cross-encoder provider
  cache/          # Two-tier .knowledge/ cache (graph + vector)
  internal/cli/   # Cobra commands + output formatting
  internal/tui/   # BubbleTea interactive UI
  cmd/markedup/   # Entry point
  testdata/       # Valid, invalid, temporal test fixtures
  ```
- **Error handling**: return errors, wrap with fmt.Errorf + %w; CLI shows file + field + fix suggestion
- **Tests**: table-driven with testify require/assert

## Direct Dependencies (9 total)
1. github.com/spf13/cobra
2. gopkg.in/yaml.v3
3. github.com/stretchr/testify
4. golang.org/x/sync
5. github.com/mattn/go-isatty
6. github.com/charmbracelet/bubbletea
7. github.com/charmbracelet/lipgloss
8. github.com/charmbracelet/bubbles
9. github.com/mark3labs/mcp-go

## Completed Features
### Phase 1 — Vectorless Core
- [x] Schema types + go.mod initialization (PR #9, merged 2026-04-12)
- [x] Schema validation rules + temporal confidence decay (PR #10, merged 2026-04-12)
- [x] Markdown parse + serialize with Obsidian wikilinks (PR #11, merged 2026-04-12)
- [x] KnowledgeIndex + concurrent filesystem loader (PR #12, merged 2026-04-12)
- [x] Multi-signal search scoring pipeline (PR #13, merged 2026-04-12)
- [x] Iterative BFS/DFS graph traversal (PR #14, merged 2026-04-12)
- [x] CLI commands (init, check, search, explore, show) + MCP stdio server (PR #15, merged 2026-04-12)
- [x] Testdata fixtures + integration tests (PR #16, merged 2026-04-12)

### Phase 2 — Smart Layer
- [x] Embedder interface + OpenAI-compatible provider (PR #26, merged 2026-04-13)
- [x] Reranker interface + cross-encoder provider (PR #27, merged 2026-04-13)
- [x] Cache provider — graph tier (PR #28, merged 2026-04-13)
- [x] BubbleTea TUI interactive mode (PR #29, merged 2026-04-13)
- [x] Cache provider — vector tier (PR #30, merged 2026-04-13)
- [x] CLI embed subcommand + MCP embedding tools (PR #31, merged 2026-04-13)
- [x] Search pipeline integration — embeddings + reranker (PR #32, merged 2026-04-13)

### Phase 3 — Enrich
- [x] `markedup enrich` command with auto-enrichment on load (commit 50c7a67, merged 2026-04-13)

### Infrastructure — MCP SDK Migration
- [x] Replace hand-rolled MCP server with mark3labs/mcp-go SDK (PR #44, merged 2026-04-13)

### Wave 1 — Graph Export + Summaries + Test Coverage
- [x] Compact graph export + `markedup_get_structure` MCP tool (PR #46, merged 2026-04-14)
- [x] Page summaries in enrich Tier 2 pipeline (PR #47, merged 2026-04-14)
- [x] MCP server integration tests — serve_test.go (PR #45, merged 2026-04-14)

### Wave 1.5 — Local Model Test Infrastructure
- [x] E2E tests + smoke script + docs for local model endpoints (PR #49, merged 2026-04-13, KHA-281)

## Current Status
- **Last updated**: 2026-04-13
- **Current iteration goal**: Wave 1.5 complete, Wave 2 (markedup_reason) next
- **Known tech debt**: `show` command 1-arg ambiguity (path vs id heuristic); go.mod at go 1.25 (plan said 1.22+); VectorCacheLookup interface in index/search.go to avoid import cycle; `SummaryNode` in graph_summary.go missing `Summary` field (needs connecting KHA-275 + KHA-276 output)
- **CRITICAL GAP**: Files without frontmatter are silently skipped by index.Load() — the entire pipeline requires manual frontmatter authoring, making markedup unusable for existing markdown corpora
- **MCP server**: Now uses `mark3labs/mcp-go` v0.47.1 SDK. 6 tools: `markedup_search`, `markedup_get_page`, `markedup_traverse`, `markedup_get_structure`, `embed_status`, `embed_file`. Integration tests in serve_test.go.
- **New packages/files**: `index/graph_summary.go` (CompactGraphSummary), `internal/cli/export.go` (export --compact command)
- **Schema additions**: `Summary string` field in `GraphFrontmatter`; `GenerateSummary()` in enrich Tier 2
- **Local model E2E**: `e2e_localmodel_test.go` (`//go:build localmodel`), `scripts/smoke-test.sh`, `docs/local-testing.md`. New env vars: `MARKEDUP_LLM_ENDPOINT/MODEL`, `MARKEDUP_TRIPLEX_ENDPOINT/MODEL` (in addition to existing `MARKEDUP_EMBED_*` and `MARKEDUP_RERANK_*`)
- **Open PRs**: none
- **PageIndex investigation**: COMPLETE — Full research in `docs/design-pageindex-research.md`.
- **Queued — Next up** (Linear project: MarkedUp — Knowledge Graph CLI):
  - KHA-278: `markedup_reason` — LLM graph reasoning retrieval tool (Wave 2, depends on KHA-275 + model endpoints)
  - KHA-279: Config file (.markedup.yaml) (Wave 3)
  - KHA-280: OpenRouter OAuth browser auth (Future)
  - KHA-282: Remote model E2E testing — OpenRouter + HF Inference Endpoints (Wave 1.5, owner: human)
