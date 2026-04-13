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
- **MCP protocol**: JSON-RPC 2.0 over stdio for agent integration
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

## Direct Dependencies (8 total)
1. github.com/spf13/cobra
2. gopkg.in/yaml.v3
3. github.com/stretchr/testify
4. golang.org/x/sync
5. github.com/mattn/go-isatty
6. github.com/charmbracelet/bubbletea
7. github.com/charmbracelet/lipgloss
8. github.com/charmbracelet/bubbles

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

## Current Status
- **Last updated**: 2026-04-13
- **Current iteration goal**: Phase 2 complete
- **Known tech debt**: `show` command 1-arg ambiguity (path vs id heuristic); go.mod at go 1.25 (plan said 1.22+); VectorCacheLookup interface in index/search.go to avoid import cycle
- **Open PRs**: none
- **Next phase**: TBD — config file (.markedup.yaml), OpenRouter OAuth, ANN indexing, embed-on-search
