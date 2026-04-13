# Design: `markedup_reason` — Graph Reasoning MCP Tool

> Inspired by PageIndex's tree navigation retrieval. See `design-pageindex-research.md` for background.
> Status: Spec ready for implementation. Depends on mcp-go SDK migration (Part 1).

## Problem

markedup's current search pipeline (keyword + optional embedding + optional reranker) excels at finding pages that contain query terms or are semantically similar. But it struggles with:

- **Multi-hop queries**: "Which researchers are connected to Alice through projects active in Q3 2024?" — requires traversing relationships AND applying temporal filters
- **Structural queries**: "What are the main topic clusters?" — requires understanding the graph shape, not matching keywords
- **Reasoning queries**: "Which concept should I study first to understand X?" — requires understanding dependency relationships

PageIndex showed that LLM reasoning over document structure outperforms vector similarity for these cases. markedup's knowledge graph is a much richer structure than PageIndex's heading tree — we should leverage it.

## Design

### New MCP Tool: `markedup_reason`

**Parameters:**
```json
{
  "query": "string (required) — the question to reason about",
  "max_pages": "integer (optional, default 20) — max pages to include in graph summary",
  "include_relationships": "boolean (optional, default true) — include relationship edges",
  "include_temporal": "boolean (optional, default false) — include temporal metadata"
}
```

**Behavior:**
1. Build a **compact graph summary** from the KnowledgeIndex:
   - For each page: `id`, `title`, `entity-type`, `tags`, `summary` (if available), `confidence`
   - If `include_relationships`: relationship edges with `target`, `type`, `strength`
   - If `include_temporal`: `valid-from`, `valid-until`, `last-verified`
   - Exclude: full body text, entities array, provenance, semantic-hints, possible-questions
2. Format as JSON, verify it fits in a reasonable token budget (~4K tokens for structure)
3. Send to LLM with a graph navigation prompt (see below)
4. Parse LLM response to get selected page IDs + reasoning
5. Fetch full content for selected pages
6. Return: selected pages with reasoning trace

**Graph Navigation Prompt:**
```
You are given a question and the structure of a knowledge graph.
Each node represents a markdown page with metadata. Edges represent typed relationships between pages.

Your task: identify which pages are most likely to contain or contribute to answering the question.
Consider relationship chains, entity types, temporal validity, and confidence scores.

Question: {query}

Knowledge graph structure:
{compact_graph_json}

Reply in JSON:
{
  "thinking": "Your reasoning about which pages are relevant and why, considering the graph structure",
  "page_ids": ["id1", "id2", ...],
  "relationship_paths": ["id1 --studies--> id2 --colleague--> id3"]
}
```

### Compact Graph Format

```json
{
  "stats": { "pages": 42, "relationships": 87, "entity_types": ["person", "concept", "project"] },
  "pages": [
    {
      "id": "alice",
      "title": "Alice Chen",
      "entity_type": "person",
      "tags": ["ai-researcher", "knowledge-graphs"],
      "summary": "AI researcher specializing in knowledge graph construction",
      "confidence": 0.95,
      "relationships": [
        { "target": "bob", "type": "colleague", "strength": 0.9 },
        { "target": "concept-graph", "type": "studies", "strength": 0.8 }
      ],
      "temporal": { "valid_from": "2023-01-01", "last_verified": "2026-04-01" }
    }
  ]
}
```

### Token Budget Management

For large knowledge bases, we can't fit the entire graph in context. Strategy:
1. If total pages <= `max_pages`: include all
2. If total pages > `max_pages`: pre-filter using keyword search on the query, take top `max_pages` results plus their 1-hop relationship neighbors
3. Always include a `stats` summary so the LLM knows the full graph size

## Implementation Details

### New files
- Add `markedup_reason` tool definition and handler to `internal/cli/serve.go`
- Add `CompactGraphSummary()` method to `index/` package (or a new `index/graph_summary.go`)

### Dependencies
- Requires an LLM endpoint configured (same env vars as embedding: `MARKEDUP_EMBED_ENDPOINT` or a new `MARKEDUP_LLM_ENDPOINT`)
- Requires mcp-go SDK (Part 1 must be done first)

### Index Package Addition: `CompactGraphSummary`

```go
type CompactNode struct {
    ID            string            `json:"id"`
    Title         string            `json:"title"`
    EntityType    string            `json:"entity_type"`
    Tags          []string          `json:"tags"`
    Summary       string            `json:"summary,omitempty"`
    Confidence    float64           `json:"confidence"`
    Relationships []CompactRelation `json:"relationships,omitempty"`
    Temporal      *CompactTemporal  `json:"temporal,omitempty"`
}

type CompactRelation struct {
    Target   string  `json:"target"`
    Type     string  `json:"type"`
    Strength float64 `json:"strength"`
}

type CompactTemporal struct {
    ValidFrom    string `json:"valid_from,omitempty"`
    ValidUntil   string `json:"valid_until,omitempty"`
    LastVerified string `json:"last_verified,omitempty"`
}

type GraphSummary struct {
    Stats GraphStats    `json:"stats"`
    Pages []CompactNode `json:"pages"`
}

func (idx *KnowledgeIndex) CompactGraphSummary(opts ...SummaryOption) *GraphSummary
```

### MCP Tool Handler Flow

```
query arrives
  → keyword pre-filter if graph too large (reuse existing Search)
  → build CompactGraphSummary from filtered pages + 1-hop neighbors
  → format graph navigation prompt
  → call LLM endpoint (OpenAI-compatible /v1/chat/completions)
  → parse JSON response (thinking + page_ids)
  → fetch full pages via idx.Get() for each selected ID
  → return combined result: reasoning trace + full page content
```

## Verification

1. Test with `testdata/valid` fixtures — query "who works with Alice?" should reason through colleague relationships
2. Test token budget: verify compact graph for N pages fits within ~4K tokens
3. Test pre-filtering: large knowledge base correctly narrows to relevant subgraph
4. Compare results with keyword search — reasoning should find pages that keyword misses for multi-hop queries

## Relationship to Other Features

- **Depends on**: mcp-go SDK migration (Part 1), LLM endpoint configuration
- **Enhanced by**: auto-generated page summaries (see `design-page-summaries.md`) — summaries make the compact graph more useful for LLM reasoning
- **Complements**: existing keyword + embedding search — this is an additional retrieval mode, not a replacement
