# markedup

A knowledge graph built from plain markdown files. No database required.

![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-blue)
![License](https://img.shields.io/badge/license-TBD-lightgrey)

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

## Search and Scoring

markedup's search pipeline combines multiple signals to rank results:

- **Keyword matching** -- title, tags, entity names, body text
- **Graph signals** -- relationship density, link structure
- **Temporal decay** -- confidence scores degrade over time based on `last-verified` and `decay-rate`

### Semantic Search with Embedding Models

For deeper recall, markedup can generate vector embeddings for your files and blend cosine similarity into the scoring pipeline. It works with any embedding model served via the OpenAI-compatible `/v1/embeddings` API:

- **Local models** -- [Ollama](https://ollama.ai), [llama.cpp](https://github.com/ggerganov/llama.cpp), or any local inference server
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

# Launch the interactive TUI
markedup tui
```

## MCP Integration

markedup exposes an [MCP](https://modelcontextprotocol.io/) server (JSON-RPC 2.0 over stdio) so AI agents and LLMs can search, traverse, and query your knowledge graph as a tool:

```sh
markedup serve ./my-kb
```

This gives agents access to `markedup_search`, `markedup_get_page`, `markedup_traverse`, `embed_status`, and `embed_file` tools. See [docs/mcp-tools.md](docs/mcp-tools.md) for the full tool catalog and integration configs for Claude Desktop, Cursor, and Claude Code.

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
| [docs/go-library.md](docs/go-library.md) | Using markedup as a Go library |

## License

TBD
