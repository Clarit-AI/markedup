# Design: Compact Graph Export

> Inspired by PageIndex's `get_document_structure()` tool. See `design-pageindex-research.md` for background.
> Status: Spec ready for implementation. Independent of other Part 2 features.

## Problem

markedup's MCP tools currently return full page content on every request. When an LLM agent is exploring a knowledge base, it often needs to understand the **shape** of the graph before diving into specific pages. Returning full content for every page wastes tokens and context window.

PageIndex separates this cleanly:
- `get_document_structure()` — returns tree with titles + summaries, NO full text
- `get_page_content(pages)` — returns full text for specific pages

This "browse structure, then fetch content" pattern is token-efficient and aligns with how humans navigate documents.

## Design

### New MCP Tool: `markedup_get_structure`

**Parameters:**
```json
{
  "filter_entity_type": "string (optional) — filter to specific entity type",
  "filter_tag": "string (optional) — filter to pages with this tag",
  "include_relationships": "boolean (optional, default true)",
  "include_temporal": "boolean (optional, default false)"
}
```

**Returns:** Compact graph JSON (same format as `CompactGraphSummary` from `design-graph-reasoning-tool.md`)

This tool lets agents:
1. Call `markedup_get_structure` to understand the knowledge base layout
2. Identify interesting pages by entity type, tags, relationships
3. Call `markedup_get_page` only for pages they actually need

### New CLI Command: `markedup export --compact`

For non-MCP use cases, expose the same compact graph as a CLI command:

```bash
# Export compact graph to stdout (JSON)
markedup export --compact [path]

# Filter by entity type
markedup export --compact --entity-type person [path]

# Include temporal metadata
markedup export --compact --temporal [path]
```

This is useful for:
- Piping into other tools
- Feeding into LLM prompts manually
- Debugging the graph structure
- Sharing the knowledge base structure without sharing content

### Compact Graph Format

Same as defined in `design-graph-reasoning-tool.md`:

```json
{
  "stats": {
    "pages": 42,
    "relationships": 87,
    "entity_types": ["person", "concept", "project"],
    "tags": ["ai-researcher", "knowledge-graphs", "temporal"]
  },
  "pages": [
    {
      "id": "alice",
      "title": "Alice Chen",
      "entity_type": "person",
      "tags": ["ai-researcher"],
      "summary": "AI researcher specializing in knowledge graphs",
      "confidence": 0.95,
      "relationships": [
        { "target": "bob", "type": "colleague", "strength": 0.9 }
      ]
    }
  ]
}
```

Fields intentionally excluded from compact format:
- `body` — full page content (fetch via `get_page` when needed)
- `entities` — detailed entity list with aliases/roles (available via `get_page`)
- `provenance` — source tracking (available via `get_page`)
- `semantic-hints`, `possible-questions` — enrichment metadata (available via `get_page`)

## Implementation Steps

1. Add `CompactGraphSummary()` method to `index/` package (shared with `markedup_reason`)
   - Accept filter options (entity type, tag, include relationships, include temporal)
   - Return `GraphSummary` struct
2. Add `markedup_get_structure` MCP tool definition + handler in `serve.go`
3. Add `markedup export` CLI command with `--compact` flag in `internal/cli/export.go`
4. Register in `root.go`

### Shared Code: `index/graph_summary.go`

This file serves both the MCP tool and the CLI command:

```go
type SummaryOption func(*summaryConfig)

func WithEntityTypeFilter(et string) SummaryOption
func WithTagFilter(tag string) SummaryOption
func WithRelationships(include bool) SummaryOption
func WithTemporal(include bool) SummaryOption
func WithMaxPages(n int) SummaryOption

func (idx *KnowledgeIndex) CompactGraphSummary(opts ...SummaryOption) *GraphSummary
```

## Verification

1. `go test ./index/...` — unit tests for CompactGraphSummary with filters
2. `markedup export --compact testdata/valid` — verify JSON output matches expected format
3. MCP tool test: `tools/call` with `markedup_get_structure` — verify compact response
4. Token counting: verify compact graph for test fixtures is significantly smaller than full content

## Relationship to Other Features

- **Independent of**: mcp-go migration (can work on current server, but better with SDK)
- **Shared code with**: `markedup_reason` tool (both use `CompactGraphSummary`)
- **Enhanced by**: page summaries (richer compact nodes when summaries are available)
- **Complements**: existing `markedup_get_page` (structure browsing + targeted content fetching)
