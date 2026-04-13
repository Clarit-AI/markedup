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
- **Output modes**: plain text (default) + JSON (--json flag); BubbleTea TUI deferred to Phase 2
- **Obsidian compatibility**: [[wikilinks]] in body, tags array in frontmatter, ## Related auto-generated section
- **Phase 2 extension points**: Reranker interface (nil), WithCache option (no-op), semantic-hints parsed but keyword-only

## Code Style Conventions
- **Naming**: Go standard (PascalCase exports, camelCase internal)
- **YAML tags**: kebab-case (`entity-type`, `valid-from`, `semantic-hints`)
- **Directory structure**:
  ```
  schema/         # Core types + validation
  markdown/       # Parse + serialize
  index/          # KnowledgeIndex, load, search, traverse
  temporal/       # Confidence decay
  internal/cli/   # Cobra commands + output formatting
  cmd/markedup/   # Entry point
  testdata/       # Valid, invalid, temporal test fixtures
  ```
- **Error handling**: return errors, wrap with fmt.Errorf + %w; CLI shows file + field + fix suggestion
- **Tests**: table-driven with testify require/assert

## Direct Dependencies (5 total)
1. github.com/spf13/cobra
2. gopkg.in/yaml.v3
3. github.com/stretchr/testify
4. golang.org/x/sync
5. github.com/mattn/go-isatty

## Completed Features
_(none yet — greenfield)_

## Current Status
- **Last updated**: 2026-04-12
- **Current iteration goal**: Phase 1 — vectorless core (types, parse, validate, index, search, traverse, CLI, MCP serve)
- **Known tech debt**: none
- **Open PRs**: none
