# markedup

A knowledge graph built from plain markdown files. No database required.

![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-blue)
![License](https://img.shields.io/badge/license-Apache%202.0-blue)

## The Problem

Knowledge lives in markdown files -- notes, documentation, research, wikis. But finding connections between documents means either manually linking everything, building a database, or surrendering your files to a proprietary tool.

Existing solutions force a choice: human-readable files *or* structured data. You can have a wiki that's easy to browse, or a database that's easy to query, but not both.

## What markedup Does

markedup turns your markdown files into a queryable knowledge graph by reading structured YAML frontmatter -- entities, relationships, confidence scores, temporal metadata -- and building an in-memory index directly from the filesystem.

Every file is simultaneously:

- **A readable document** you can open in any editor or Obsidian
- **A graph node** with typed relationships to other nodes
- **A search target** with keyword, semantic, and cross-encoder scoring
- **A self-contained unit** -- no sidecar database, no sync process, no lock-in

There is no external database. The filesystem *is* the database. `git diff` is your changelog. `cp` is your backup. Your files never leave your machine unless you push them.

## How It Works

A markedup file is standard markdown with YAML frontmatter:

```yaml
---
id: distributed-consensus
title: Distributed Consensus Protocols
entity-type: concept
confidence: 0.92
tags: [distributed-systems, algorithms]
entities:
  - name: Raft
    role: subject
    aliases: [raft-protocol]
relationships:
  - target: paxos
    type: derived-from
    strength: 0.8
  - target: etcd
    type: implemented-by
    strength: 0.9
temporal:
  valid-from: "2014-01-01"
  last-verified: "2024-06-15"
  decay-rate: 0.05
semantic-hints:
  - leader election
  - log replication
  - fault tolerance
---

Raft is a consensus algorithm designed to be more understandable than Paxos...
```

markedup parses this frontmatter, builds a graph of relationships between files, and exposes it through CLI commands, a TUI, an MCP server for AI agents, and a Go library API. Obsidian users get compatibility out of the box -- `[[wikilinks]]` in the body and `tags` arrays in frontmatter work as expected.

See [docs/schema-reference.md](docs/schema-reference.md) for the complete field specification.

## Compatibility

markedup files are standard markdown. Any tool that reads `.md` files works normally -- the YAML frontmatter is either rendered (Obsidian, Hugo, Jekyll) or ignored (GitHub, VS Code, plain text editors). This means your knowledge base is not locked into markedup:

- **[Obsidian](https://obsidian.md)** -- `[[wikilinks]]` in the body and `tags` arrays in frontmatter are fully compatible. markedup auto-generates a `## Related` section for Obsidian's graph view.
- **GitHub Wikis and READMEs** -- GitHub renders markdown natively and displays YAML frontmatter in a table. Your knowledge base doubles as browsable documentation.
- **Static site generators** -- Hugo, Jekyll, Zola, and others already consume YAML frontmatter. markedup files can serve as content sources without modification.
- **Plain text** -- Every file is readable in `cat`, `less`, `grep`, or any editor. No binary formats, no proprietary encoding.

You can adopt markedup incrementally -- add frontmatter to existing files one at a time, and they become graph nodes without breaking anything that already reads them.

### Working with Existing Files

markedup works with your existing markdown files out of the box. Files without frontmatter are automatically enriched when loaded -- `id`, `title`, `tags`, and relationships are extracted from the document structure and written back as YAML frontmatter.

```sh
# Preview what markedup would extract from your files
markedup enrich ./my-notes --dry-run

# Enrich all files (writes frontmatter, non-destructive)
markedup enrich ./my-notes

# Or just use any command -- auto-enrichment happens on load
markedup search ./my-notes "knowledge graph"
```

For richer extraction, use a local model like [Triplex](https://huggingface.co/SciPhi/Triplex) (Phi3-3.8B KG extraction model) via Ollama to classify entities, infer relationship types, and generate semantic hints:

```sh
ollama run triplex
markedup enrich . --model triplex --endpoint http://localhost:11434
```

See [docs/cli-reference.md](docs/cli-reference.md#enrich) for all options.

## Search and Scoring

markedup's search pipeline combines multiple signals to rank results:

- **Keyword matching** -- title, tags, entity names, body text
- **Graph signals** -- relationship density, link structure
- **Temporal decay** -- confidence scores degrade over time based on `last-verified` and `decay-rate`

### Semantic Search with Embedding Models

For deeper recall, markedup can generate vector embeddings for your files and blend cosine similarity into the scoring pipeline. It works with any embedding model served via the OpenAI-compatible `/v1/embeddings` API:

- **Local models** -- [Ollama](https://ollama.ai), [llama.cpp](https://github.com/ggerganov/llama.cpp), [Synapse](https://github.com/Clarit-AI/Synapse), or any local inference server
- **Cloud providers** -- [OpenRouter](https://openrouter.ai), OpenAI, or any OpenAI-compatible endpoint

```sh
# Embed using a local Ollama model
markedup embed --endpoint http://localhost:11434 --model nomic-embed-text

# Embed using OpenRouter
markedup embed --endpoint https://openrouter.ai/api --model openai/text-embedding-3-small --api-key $OPENROUTER_KEY
```

Embeddings are cached in `.knowledge/vectors/` and only recomputed when file content changes. Switching models automatically invalidates the cache.

### Cross-Encoder Reranking

For highest precision, results can be re-scored with a cross-encoder model after initial retrieval. Cross-encoders evaluate each (query, document) pair directly -- slower but significantly more accurate than embedding similarity alone.

```sh
# Combine keyword scoring, semantic similarity, and cross-encoder reranking
markedup search . --semantic --rerank "consensus algorithms"
```

Reranking supports the same provider model -- local via Ollama or remote via API (Jina, Cohere, OpenAI-compatible endpoints).

## Install

```sh
go install github.com/KHAEntertainment/markedup/cmd/markedup@latest
```

## Quick Start

```sh
# Scaffold a sample knowledge base
markedup init my-kb
cd my-kb

# Validate frontmatter across all files
markedup check .

# Search by keyword
markedup search . "knowledge graph"

# Traverse the graph from a node
markedup explore . knowledge-graph --depth 3

# Launch the interactive TUI (runs setup wizard on first run)
markedup tui
```

On first run, `markedup tui` will launch an interactive setup wizard to configure embedding, LLM, and reranker endpoints. You can also run `markedup setup` directly from the CLI at any time.

### Quick Start with Existing Markdown

```sh
# Point markedup at your existing notes -- auto-enrichment handles the rest
markedup search ./my-notes "topic"

# Or explicitly enrich first to review what gets generated
markedup enrich ./my-notes --dry-run
markedup enrich ./my-notes
```

## MCP Integration

markedup exposes an [MCP](https://modelcontextprotocol.io/) server (JSON-RPC 2.0 over stdio) so AI agents and LLMs can search, traverse, and query your knowledge graph as a tool:

```sh
markedup serve ./my-kb
```

This gives agents access to 7 tools: `markedup_search`, `markedup_get_page`, `markedup_traverse`, `markedup_get_structure`, `markedup_reason`, `embed_status`, and `embed_file`. See [docs/mcp-tools.md](docs/mcp-tools.md) for the full tool catalog and integration configs for Claude Desktop, Cursor, and Claude Code.

## Using as a Go Library

markedup is also a Go library. You can import it as a dependency to load, search, and traverse knowledge graphs programmatically:

```go
import (
    "github.com/KHAEntertainment/markedup/index"
    "github.com/KHAEntertainment/markedup/embed"
)

result, _ := index.Load(ctx, "./my-kb")
results := index.Search(result.Index, "consensus", index.WithLimit(10))
```

See [docs/go-library.md](docs/go-library.md) for the full API guide.

## Documentation

| Document | Contents |
|----------|----------|
| [docs/cli-reference.md](docs/cli-reference.md) | All commands, flags, and output formats |
| [docs/schema-reference.md](docs/schema-reference.md) | Frontmatter fields, validation rules, Obsidian compatibility |
| [docs/mcp-tools.md](docs/mcp-tools.md) | MCP tool names, parameters, and example payloads |
| [docs/go-library.md](docs/go-library.md) | Using markedup as a Go library (including enrich package) |
| [docs/architecture.md](docs/architecture.md) | Tech-stack decisions, module layout, and design rationale |

## License

Licensed under the [Apache License, Version 2.0](LICENSE). You may not use this project except in compliance with that license.
