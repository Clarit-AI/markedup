# CLI Reference

`markedup` is a knowledge graph toolkit for Obsidian-compatible markdown files with YAML frontmatter. It builds an in-memory knowledge graph from your markdown files and provides commands for validation, search, graph traversal, embedding, and interactive exploration.

## Global Flags

These flags are available on every command.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--json` | bool | `false` | Output in JSON format |
| `--depth` | int | `2` | Traversal depth for the `explore` command |
| `--no-enrich` | bool | `false` | Disable auto-enrichment of files without frontmatter |

## Output Modes

markedup supports three output modes:

- **Plain text** (default) -- Human-readable output to the terminal.
- **JSON** (`--json`) -- Machine-readable structured output, suitable for piping to `jq` or integrating with scripts.
- **TUI** (`markedup tui`) -- Interactive terminal UI powered by BubbleTea for searching, exploring, and viewing the knowledge graph.

---

## init

Initialize a new knowledge base directory with a sample markdown file demonstrating all frontmatter fields.

### Usage

```
markedup init [path]
```

### Arguments

| Argument | Required | Default | Description |
|----------|----------|---------|-------------|
| `path` | No | `.` (current directory) | Directory to initialize |

### Flags

No command-specific flags. See [Global Flags](#global-flags).

### Examples

```sh
# Initialize in the current directory
markedup init

# Initialize in a new directory
markedup init ./my-knowledge-base
```

### Output

**Plain text:**

```
Knowledge base created at ./my-knowledge-base.
Add .md files, then run `markedup check`.
```

**JSON** (`--json`):

```json
{"status":"created","path":"./my-knowledge-base"}
```

The command creates a sample `example.md` file with a complete frontmatter template showing all supported fields. See [schema-reference.md](schema-reference.md) for details on each frontmatter field.

---

## check

Validate all markdown pages in a knowledge base. Loads every `.md` file, parses frontmatter, runs schema validation, and reports errors grouped by file.

### Usage

```
markedup check [path]
```

### Arguments

| Argument | Required | Default | Description |
|----------|----------|---------|-------------|
| `path` | No | `.` (current directory) | Directory to validate |

### Flags

No command-specific flags. See [Global Flags](#global-flags).

### Examples

```sh
# Check the current directory
markedup check

# Check a specific directory
markedup check ./my-knowledge-base

# Check and get JSON output for CI pipelines
markedup check ./my-knowledge-base --json
```

### Output

**Plain text (valid):**

```
5 pages checked. All valid.
```

**Plain text (errors):**

```
Errors found:

  notes/broken.md
    - id: missing required field
      Fix: add id: <unique-identifier> to frontmatter

3 pages checked. 1 errors in 1 files.
Fix errors above, then run `markedup check` again.
```

**JSON** (`--json`):

```json
{"pages":5,"errors":0,"status":"valid"}
```

When errors are found, the JSON output includes an `issues` array with `file`, `field`, `message`, and `fix` for each error. The command exits with code 1 when validation errors are present. Each error includes the field name and a suggested fix. See [schema-reference.md](schema-reference.md) for frontmatter validation rules.

---

## search

Search the knowledge base using a scored keyword pipeline. Optionally enable semantic search (embedding similarity) and/or cross-encoder reranking.

### Usage

```
markedup search [path] <query>
```

### Arguments

| Argument | Required | Default | Description |
|----------|----------|---------|-------------|
| `path` | No | `.` (current directory) | Directory containing the knowledge base |
| `query` | Yes | -- | Search query string |

When one argument is provided, it is treated as the query and the current directory is used as the path.

### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--semantic` | bool | `false` | Enable semantic search using cached embeddings (requires prior `markedup embed`) |
| `--rerank` | bool | `false` | Re-rank results using a cross-encoder model (requires endpoint config) |

### Environment Variables (for `--semantic`)

| Variable | Description |
|----------|-------------|
| `MARKEDUP_EMBED_ENDPOINT` | Embedding API endpoint (e.g. `http://localhost:11434`) |
| `MARKEDUP_EMBED_MODEL` | Embedding model name |
| `MARKEDUP_EMBED_API_KEY` | API key for authentication (optional for local backends) |

### Environment Variables (for `--rerank`)

| Variable | Description |
|----------|-------------|
| `MARKEDUP_RERANK_ENDPOINT` | Reranker API endpoint |
| `MARKEDUP_RERANK_MODEL` | Reranker model name |
| `MARKEDUP_RERANK_API_KEY` | API key for authentication |

### Examples

```sh
# Keyword search in the current directory
markedup search "knowledge graph"

# Search a specific directory
markedup search ./notes "temporal decay"

# Keyword + semantic search (requires prior embedding)
markedup search --semantic "how does confidence decay work"

# Full pipeline: keyword + semantic + reranking
markedup search --semantic --rerank "relationship types"

# JSON output for programmatic use
markedup search --json "knowledge graph"
```

### Output

**Plain text:**

Results are listed by score with page ID and title. A hint to use `markedup show <id>` is appended.

**JSON** (`--json`):

Returns a JSON array of scored results with page metadata.

---

## explore

Traverse the knowledge graph starting from a given entity ID using iterative BFS. The traversal depth is controlled by the global `--depth` flag.

### Usage

```
markedup explore [path] <entity>
```

### Arguments

| Argument | Required | Default | Description |
|----------|----------|---------|-------------|
| `path` | No | `.` (current directory) | Directory containing the knowledge base |
| `entity` | Yes | -- | Entity ID to start traversal from |

When one argument is provided, it is treated as the entity ID and the current directory is used as the path.

### Flags

No command-specific flags. Uses the global `--depth` flag (default `2`) to control traversal depth. See [Global Flags](#global-flags).

### Examples

```sh
# Explore from an entity in the current directory
markedup explore example-page

# Explore with a specific depth
markedup explore --depth 3 example-page

# Explore a specific directory
markedup explore ./notes example-page

# JSON output
markedup explore --json example-page
```

### Output

**Plain text:**

Displays the traversal tree showing nodes and their relationships at each depth level.

**JSON** (`--json`):

Returns a structured JSON representation of the traversal graph.

---

## show

Show index statistics or display a specific page's frontmatter and body.

### Usage

```
markedup show [path] [id]
```

### Arguments

| Argument | Required | Default | Description |
|----------|----------|---------|-------------|
| `path` | No | `.` (current directory) | Directory containing the knowledge base |
| `id` | No | -- | Page ID to display |

Argument resolution:

- **0 arguments** -- Shows index statistics for the current directory.
- **1 argument** -- Treated as a page ID; loads from the current directory.
- **2 arguments** -- First is the path, second is the page ID.

### Flags

No command-specific flags. See [Global Flags](#global-flags).

### Examples

```sh
# Show index statistics
markedup show

# Show a specific page by ID
markedup show example-page

# Show a page from a specific directory
markedup show ./notes example-page

# JSON output for statistics
markedup show --json

# JSON output for a specific page
markedup show --json example-page
```

### Output

**Plain text (statistics):**

Displays page count and index summary, followed by a hint to use `markedup show <id>`.

**Plain text (page):**

Dumps the page's frontmatter fields and body content, followed by a hint to use `markedup explore`. See [schema-reference.md](schema-reference.md) for details on frontmatter fields.

**JSON** (`--json`):

Returns structured JSON for either the index statistics or the full page data.

---

## serve

Start an MCP (Model Context Protocol) JSON-RPC 2.0 server over stdio. This allows AI agents and tools to interact with the knowledge base programmatically.

### Usage

```
markedup serve [path]
```

### Arguments

| Argument | Required | Default | Description |
|----------|----------|---------|-------------|
| `path` | No | `.` (current directory) | Directory containing the knowledge base |

### Flags

No command-specific flags. See [Global Flags](#global-flags).

### MCP Tools Exposed

The server registers the following tools via the `tools/list` method:

| Tool | Description |
|------|-------------|
| `markedup_search` | Search the knowledge base for pages matching a query. Accepts `query` (string, required), `semantic` (bool), `rerank` (bool). |
| `markedup_get_page` | Get a specific page by ID with its frontmatter and body. Accepts `id` (string, required). |
| `markedup_traverse` | Traverse the knowledge graph from a starting node. Accepts `from` (string, required), `depth` (int, default 2), `direction` (string: `forward`, `reverse`, or `both`, default `forward`). |
| `embed_status` | Get embedding coverage statistics for the knowledge base. No arguments. |
| `embed_file` | Embed a single file on demand and cache the result. Accepts `path` (string, required). |

### Examples

```sh
# Start the MCP server for the current directory
markedup serve

# Start the MCP server for a specific directory
markedup serve ./my-knowledge-base
```

### Output

The server reads newline-delimited JSON-RPC 2.0 requests from stdin and writes JSON-RPC 2.0 responses to stdout. It does not produce human-readable terminal output.

---

## embed

Generate and manage vector embeddings for knowledge base files. Scans markdown files, identifies files needing embedding via the vector cache (`.knowledge/vectors/`), embeds them in batches, and saves results. Running `embed` twice with no file changes does no work (idempotent).

### Usage

```
markedup embed [flags]
```

### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--endpoint` | string | `""` | Embedding API endpoint (e.g. `http://localhost:11434`) |
| `--model` | string | `""` | Embedding model name (e.g. `text-embedding-3-small`) |
| `--api-key` | string | `""` | API key for authentication (optional for local backends) |
| `--batch-size` | int | `0` (uses internal default of 100) | Number of texts per embedding request |
| `--dir` | string | `"."` | Directory to scan for markdown files |
| `--status` | bool | `false` | Show embedding status without embedding |

### Examples

```sh
# Embed using a local Ollama instance
markedup embed --endpoint http://localhost:11434 --model nomic-embed-text

# Embed using OpenAI
markedup embed --endpoint https://api.openai.com --model text-embedding-3-small --api-key sk-...

# Embed a specific directory with a custom batch size
markedup embed --dir ./notes --endpoint http://localhost:11434 --model nomic-embed-text --batch-size 50

# Check embedding status without running embeddings
markedup embed --status

# Check status for a specific directory
markedup embed --status --dir ./notes
```

### Output

**Plain text:**

```
Found 10 files, 3 cached, 7 to embed.
Embedding 7/7 files...
Done. Embedded 7 files (3 skipped, cached).
```

When all files are already embedded:

```
All 10 files already embedded (model: nomic-embed-text, 768 dimensions).
```

**Plain text (`--status`):**

```
Total files:  10
Embedded:     7
Pending:      3
Model:        nomic-embed-text
Dimensions:   768
```

**JSON** (`--json --status`):

```json
{
  "total": 10,
  "embedded": 7,
  "pending": 3,
  "model": "nomic-embed-text",
  "dimensions": 768
}
```

---

## enrich

Auto-generate YAML frontmatter for markdown files. Scans files and extracts `id` from filename, `title` from headings, `tags` from `#hashtags`, `relationships` from `[[wikilinks]]`, and provenance URLs from the body. Optionally uses a chat-completion model (e.g. Triplex) for richer knowledge graph extraction.

Files with complete frontmatter are skipped unless `--force` is used. Partial frontmatter is filled in without overwriting existing fields.

### Auto-Enrichment

By default, markedup automatically enriches files without frontmatter when loading the knowledge base (via `search`, `check`, `explore`, etc.). This means files without frontmatter are indexed transparently. Use `--no-enrich` to disable this behavior.

### Usage

```
markedup enrich [path] [flags]
```

### Arguments

| Argument | Required | Default | Description |
|----------|----------|---------|-------------|
| `path` | No | `.` (current directory) | File or directory to enrich |

### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--dry-run` | bool | `false` | Show proposed frontmatter without modifying files |
| `--force` | bool | `false` | Overwrite existing frontmatter fields |
| `--skip-existing` | bool | `false` | Only process files with no frontmatter at all |
| `--model` | string | `""` | Model name for Tier 2 extraction (e.g. `triplex`) |
| `--endpoint` | string | `""` | Chat completion API endpoint (e.g. `http://localhost:11434`) |
| `--api-key` | string | `""` | API key for model endpoint (optional) |
| `--entity-types` | string | `""` | Comma-separated entity types for model extraction |
| `--predicates` | string | `""` | Comma-separated relationship predicates for model extraction |

### Tier 1 -- Deterministic Extraction

Runs by default with no model required. Extracts:

| Field | Source |
|-------|--------|
| `id` | Filename (slugified) |
| `title` | First `# heading`, or filename if none |
| `entity-type` | `"document"` (default) |
| `confidence` | `0.7` (auto-generated) |
| `tags` | `#hashtags` from body + directory path segments |
| `relationships` | `[[wikilinks]]` as `related-to` with strength `0.5` |
| `provenance.sources` | HTTP/HTTPS URLs from body |
| `provenance.created-by` | `"markedup-enrich-v1"` |

### Tier 2 -- Model-Assisted Extraction

Enabled with `--model` and `--endpoint`. Uses a chat-completion API (OpenAI-compatible `/v1/chat/completions`) to extract richer metadata:

- Named entities with roles and aliases
- Typed relationships (`derived-from`, `implements`, `depends-on`, etc.)
- Intelligent `entity-type` classification
- `semantic-hints` and `possible-questions`

Tier 1 runs first, then Tier 2 augments the result.

**Triplex model** (SciPhi/Triplex, Phi3-3.8B finetune for KG extraction):
```sh
ollama run triplex
markedup enrich . --model triplex --endpoint http://localhost:11434
```

> **License note:** Triplex is licensed under cc-by-nc-sa-4.0 (non-commercial use only).

### Merge Behavior

- **Default:** Fill missing scalar fields. Union/append arrays (tags, relationships, sources) with deduplication. Never overwrite existing values.
- **`--force`:** Overwrite all fields with extracted values (but never blank out a field if the extracted value is empty).
- **Idempotent:** Running `enrich` twice produces identical results.

### Examples

```sh
# Enrich all files in the current directory (Tier 1)
markedup enrich .

# Preview what would change without modifying files
markedup enrich . --dry-run

# Enrich a single file
markedup enrich ./notes/paper.md

# Only process files that have no frontmatter at all
markedup enrich . --skip-existing

# Overwrite existing frontmatter fields
markedup enrich . --force

# Tier 2: model-assisted extraction with Triplex via Ollama
markedup enrich . --model triplex --endpoint http://localhost:11434

# Tier 2: custom entity types and predicates
markedup enrich . --model triplex --endpoint http://localhost:11434 \
  --entity-types "PERSON,CONCEPT,TOOL,EVENT" \
  --predicates "derived-from,implements,related-to,depends-on"

# JSON output for programmatic use
markedup enrich . --json
```

### Output

**Plain text:**

```
Enriched 485 files. 15 skipped. 0 errors.
```

**Plain text (`--dry-run`):**

```
--- notes/paper.md ---
id: paper
title: My Research Paper
entity-type: document
confidence: 0.7
tags:
    - machine-learning
    - notes
relationships:
    - target: neural-networks
      type: related-to
      strength: 0.5
provenance:
    sources:
        - https://arxiv.org/abs/1234.5678
    created-by: markedup-enrich-v1
```

**JSON** (`--json`):

```json
{
  "enriched": 485,
  "skipped": 15,
  "errors": 0,
  "files": [
    {"path": "notes/paper.md", "status": "enriched"},
    {"path": "notes/complete.md", "status": "skipped", "reason": "already complete"}
  ]
}
```

---

## tui

Launch an interactive terminal UI for searching, exploring, and viewing the knowledge graph. The TUI is built with the BubbleTea framework and provides keyboard-driven navigation.

### Usage

```
markedup tui [flags]
```

### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--dir` | string | `"."` | Directory containing markdown files |

### Examples

```sh
# Launch TUI for the current directory
markedup tui

# Launch TUI for a specific directory
markedup tui --dir ./my-knowledge-base
```

### Output

Opens a full-screen interactive terminal application. The TUI provides views for searching pages, exploring graph relationships, and viewing page content inline.
