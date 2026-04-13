# markedup

A knowledge graph built from plain markdown files. No database required.

![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-blue)
![License](https://img.shields.io/badge/license-TBD-lightgrey)

## What is markedup?

markedup treats markdown files with YAML frontmatter as nodes in a knowledge graph. It indexes entities, relationships, tags, and confidence scores from your existing notes, then lets you search, traverse, and query them -- all from the filesystem. Compatible with Obsidian-style `[[wikilinks]]` and frontmatter conventions.

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

# Traverse the graph from an entity
markedup explore . knowledge-graph --depth 3

# Launch the interactive TUI
markedup tui
```

## Embedding Quick Start

Add semantic (vector) search by embedding your files against any OpenAI-compatible endpoint:

```sh
# Generate embeddings (idempotent -- skips unchanged files)
markedup embed --dir . --endpoint http://localhost:11434 --model nomic-embed-text

# Search with semantic similarity blended into scoring
markedup search . --semantic "distributed systems"

# Re-rank results with a cross-encoder for higher precision
markedup search . --semantic --rerank "distributed systems"
```

Embeddings are cached in `.knowledge/vectors/` and reused across runs. See [docs/cli-reference.md](docs/cli-reference.md) for all flags.

## MCP Integration

markedup exposes an [MCP](https://modelcontextprotocol.io/) JSON-RPC 2.0 server over stdio for agent and LLM tool integration:

```sh
markedup serve ./my-kb
```

See [docs/mcp-tools.md](docs/mcp-tools.md) for the full tool catalog.

## Documentation

| Document | Contents |
|----------|----------|
| [docs/cli-reference.md](docs/cli-reference.md) | All commands, flags, and output formats |
| [docs/schema-reference.md](docs/schema-reference.md) | Frontmatter fields, entity types, validation rules |
| [docs/mcp-tools.md](docs/mcp-tools.md) | MCP tool names, parameters, and example payloads |
| [docs/go-library.md](docs/go-library.md) | Using markedup as a Go library (`schema/`, `index/`, `embed/`) |

## License

TBD
