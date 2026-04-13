# PageIndex Reference Architecture Study

> Research conducted 2026-04-13. Source: VectifyAI/PageIndex (Python) + VectifyAI/pageindex-mcp (TypeScript).
> This document captures architectural insights for markedup. PageIndex cannot be imported as a Go dependency.

## What PageIndex Is

PageIndex is the forerunner in **vectorless document retrieval**. It builds a hierarchical tree index from document structure, then uses LLM reasoning (not vector similarity) to navigate the tree and find relevant sections. Achieved **98.7% accuracy on FinanceBench**, significantly outperforming vector-based RAG.

Core thesis: **"Similarity is not relevance. Relevance requires reasoning."**

## How It Works

### Indexing Pipeline

**For Markdown** (`page_index_md.py`):
1. Regex scan for `#` through `######` headings, respecting code blocks
2. Each heading becomes a tree node with: `title`, `line_num`, `text` (content until next heading)
3. **Tree thinning** (optional): if a parent + all descendants total fewer tokens than threshold (default 5000), children are absorbed into parent — prevents over-deep trees for small sections
4. Stack-based tree builder: push nodes, pop back to correct parent based on heading level
5. LLM generates `summary` per node (nodes under 200 tokens use full text as summary)
6. LLM generates one-sentence `doc_description` for the whole document

**For PDFs** (`page_index.py`):
1. Extract text per page, count tokens
2. TOC detection: scan first N pages with LLM ("is this a table of contents?")
3. Three modes based on TOC quality: TOC with page numbers (best), TOC without page numbers, no TOC at all
4. Verification pass: LLM checks if section titles appear on claimed pages (>60% accuracy required)
5. Large node recursion: nodes exceeding 10 pages AND 20K tokens get sub-divided

### Tree Node Schema (TreeIndex)

```json
{
  "doc_name": "document.md",
  "doc_description": "One-sentence LLM-generated description",
  "line_count": 450,
  "structure": [
    {
      "title": "Section Heading",
      "node_id": "0001",
      "summary": "LLM-generated summary of this section",
      "prefix_summary": "Summary for parent nodes with children",
      "text": "Full raw text content (optional, stripped during retrieval)",
      "line_num": 15,
      "nodes": [
        { "title": "Subsection", "node_id": "0002", "...": "..." }
      ]
    }
  ]
}
```

### Retrieval Algorithm

1. **Present compact tree to LLM**: Tree structure with `node_id`, `title`, `summary` — full `text` stripped to save tokens
2. **LLM reasons over structure**: Examines titles and summaries to identify relevant nodes
3. **Node selection**: LLM outputs `{ "thinking": "...", "node_list": ["0001", "0003"] }`
4. **Fetch full text**: Selected nodes' `text` is retrieved from the complete tree
5. **Answer generation**: Retrieved text + query → LLM generates final answer

**Tree search prompt** (from tutorials):
```
You are given a query and the tree structure of a document.
Each node contains a node id, node title, and a corresponding summary.
Your task is to find all nodes that are likely to contain the answer.

Question: {query}
Document tree structure: {tree_without_text}

Reply in JSON: { "thinking": "...", "node_list": ["node_id_1", ...] }
```

### MCP Server Design (pageindex-mcp)

Three core retrieval tools:
- `get_document()` — returns doc metadata (name, description, status, page/line count)
- `get_document_structure()` — returns tree JSON with text fields stripped (token-efficient)
- `get_page_content(pages)` — returns raw text for specific page/line ranges

Plus one ingestion tool:
- `process_document` — upload PDF from URL or local path

Design patterns worth noting:
- Structured error responses with `next_steps` guidance for the LLM
- Lazy connection (connects on first request)
- JSON Schema to Zod conversion for dynamically proxied tools

## Key Innovations

| Innovation | Description |
|---|---|
| No vectors, no chunking | Documents divided at natural section boundaries, not arbitrary token windows |
| No Top-K | LLM identifies ALL relevant sections regardless of count (vs. fixed K) |
| Explainability | `thinking` field provides reasoning trace for selection |
| Expert knowledge injection | Domain-specific hints can be added to retrieval prompts |
| Token efficiency | Structure-first browsing, text-on-demand fetching |
| AlphaGo analogy | Tree index = game tree, LLM = value function evaluating branches |

## What PageIndex Lacks (Where markedup Already Exceeds It)

| Capability | PageIndex | markedup |
|---|---|---|
| Metadata model | node_id, title, summary only | id, title, entity-type, confidence, tags, entities (name/aliases/role), relationships (target/type/strength), temporal (valid-from/until/last-verified/decay-rate), provenance, semantic-hints, possible-questions |
| Cross-document links | None — single-document only | Typed relationships with strength + BFS/DFS graph traversal |
| Search scoring | None — LLM-only retrieval | Field-weighted keyword scoring + optional embedding + optional reranker |
| Entity extraction | None | Entities with aliases and roles |
| Temporal awareness | None | Valid-from/until, last-verified, confidence decay |
| Frontmatter support | None — heading-only | Rich YAML frontmatter with validation |
| Multi-document | External strategies only (SQL, vector, description-based) | Native knowledge graph across all documents |

## Transferable Ideas for markedup

### High Value
1. **Compact graph structure as LLM-navigable index** — present the knowledge graph (IDs, titles, entity types, relationship summaries) without full text for token-efficient browsing
2. **LLM reasoning-based retrieval** — complement keyword/vector with graph-aware reasoning for complex/multi-hop queries
3. **Structure-first, text-on-demand** — split `get_page` into lightweight structure view + full content fetch
4. **Per-page summaries** — auto-generate during enrichment, power the reasoning retrieval mode

### Medium Value
5. **Tree thinning** — merge small sections into parents for more useful navigation
6. **Expert knowledge injection** — add domain hints to retrieval prompts using entity types and relationship categories
7. **Document description** — one-sentence auto-generated summary per file for routing

### Low Value / Not Transferable
- PDF TOC detection pipeline (markedup is markdown-only)
- Cloud-proxied MCP architecture (markedup is local-first)
- Single-document focus (markedup already handles multi-doc graphs)
