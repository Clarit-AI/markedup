# MCP Tools Reference

markedup exposes a set of tools via the [Model Context Protocol (MCP)](https://modelcontextprotocol.io/), allowing AI agents and LLM-powered applications to query, traverse, and manage a markdown knowledge base programmatically.

The protocol uses **JSON-RPC 2.0 over stdio**. The server reads newline-delimited JSON-RPC requests from stdin and writes responses to stdout.

---

## Starting the Server

```sh
markedup serve [path]
```

- `path` (optional) -- directory containing markdown files. Defaults to `.` (current directory).
- The server loads the knowledge index from the given path, then enters a read-eval-print loop on stdio.
- One JSON-RPC request per line; responses are written one per line.

---

## Protocol Overview

All communication follows JSON-RPC 2.0. A request has the form:

```json
{"jsonrpc": "2.0", "id": 1, "method": "...", "params": {...}}
```

A successful response:

```json
{"jsonrpc": "2.0", "id": 1, "result": {...}}
```

An error response:

```json
{"jsonrpc": "2.0", "id": 1, "error": {"code": -32601, "message": "Method not found: foo"}}
```

### MCP Lifecycle

Before calling tools, clients must complete the MCP initialization handshake:

1. Send `initialize` to receive server capabilities.
2. Send `notifications/initialized` to confirm.
3. Send `tools/list` to discover available tools.
4. Send `tools/call` to invoke a tool.

---

## MCP Methods

### `initialize`

Returns server info and capabilities.

**Request:**

```json
{"jsonrpc": "2.0", "id": 1, "method": "initialize"}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2024-11-05",
    "capabilities": {
      "tools": {}
    },
    "serverInfo": {
      "name": "markedup",
      "version": "0.1.0"
    }
  }
}
```

### `notifications/initialized`

Sent by the client after processing the `initialize` response. No meaningful result.

**Request:**

```json
{"jsonrpc": "2.0", "id": 2, "method": "notifications/initialized"}
```

**Response:**

```json
{"jsonrpc": "2.0", "id": 2, "result": {}}
```

### `tools/list`

Returns the list of available tools with their input schemas.

**Request:**

```json
{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}
```

**Response:** see the tool reference below for the full schema of each tool. The response wraps them in:

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "tools": [ ... ]
  }
}
```

### `tools/call`

Invokes a tool by name. All tool calls use this method with `params.name` and `params.arguments`.

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "tools/call",
  "params": {
    "name": "markedup_search",
    "arguments": { "query": "authentication" }
  }
}
```

Tool responses always have this shape:

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "result": {
    "content": [
      {"type": "text", "text": "..."}
    ],
    "isError": false
  }
}
```

The `content[].text` field contains the tool output as a string (often JSON-encoded). When `isError` is `true`, the text contains an error message.

---

## Tool Reference

### `markedup_search`

Search the knowledge base for pages matching a query. Returns results scored by keyword relevance, with optional semantic (embedding-based) search and cross-encoder reranking.

#### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `query` | string | Yes | Search query string. |
| `semantic` | boolean | No | Enable semantic search using cached embeddings. Requires `MARKEDUP_EMBED_ENDPOINT` and `MARKEDUP_EMBED_MODEL` environment variables. |
| `rerank` | boolean | No | Re-rank results using a cross-encoder model. Requires `MARKEDUP_RERANK_ENDPOINT` and `MARKEDUP_RERANK_MODEL` environment variables. |

#### Example Request

```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "method": "tools/call",
  "params": {
    "name": "markedup_search",
    "arguments": {
      "query": "authentication flow",
      "semantic": true,
      "rerank": false
    }
  }
}
```

#### Example Response

```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "[\n  {\n    \"id\": \"auth-overview\",\n    \"title\": \"Authentication Overview\",\n    \"entity_type\": \"concept\",\n    \"score\": 0.85,\n    \"matches\": [\n      {\"field\": \"title\", \"value\": \"Authentication Overview\"},\n      {\"field\": \"body\", \"value\": \"...authentication flow...\"}\n    ]\n  }\n]"
      }
    ]
  }
}
```

The `text` field contains a JSON array of results. Each result has the following fields:

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Page ID from frontmatter. |
| `title` | string | Page title. |
| `entity_type` | string | Entity type (e.g. "concept", "person"). |
| `score` | number | Relevance score (0.0 to 1.0+). |
| `matches` | array | List of `{field, value}` objects showing where the query matched. |

---

### `markedup_get_page`

Retrieve a specific page by its ID, including frontmatter metadata and body content.

#### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | string | Yes | Page ID to retrieve. |

#### Example Request

```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "method": "tools/call",
  "params": {
    "name": "markedup_get_page",
    "arguments": {
      "id": "auth-overview"
    }
  }
}
```

#### Example Response

```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\n  \"id\": \"auth-overview\",\n  \"title\": \"Authentication Overview\",\n  \"entity_type\": \"concept\",\n  \"confidence\": 0.95,\n  \"tags\": [\"security\", \"auth\"],\n  \"body\": \"# Authentication Overview\\n\\nThis page describes...\",\n  \"source_path\": \"docs/auth-overview.md\",\n  \"entities\": [],\n  \"relationships\": []\n}"
      }
    ]
  }
}
```

The `text` field contains a JSON object with:

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Page ID. |
| `title` | string | Page title. |
| `entity_type` | string | Entity type. |
| `confidence` | number | Confidence score (0.0 to 1.0), subject to temporal decay. |
| `tags` | array | String array of tags. |
| `body` | string | Full markdown body content. |
| `source_path` | string | Filesystem path to the source file. |
| `entities` | array | Entities defined in frontmatter. |
| `relationships` | array | Relationships defined in frontmatter. |

If the page is not found, the response has `isError: true` with a message like `Page "foo" not found`.

---

### `markedup_traverse`

Traverse the knowledge graph starting from a given node, following relationships to discover connected pages.

#### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `from` | string | Yes | Starting node ID. |
| `depth` | integer | No | Maximum traversal depth. Default: `2`. |
| `direction` | string | No | Traversal direction. One of `forward`, `reverse`, or `both`. Default: `forward`. |

#### Example Request

```json
{
  "jsonrpc": "2.0",
  "id": 12,
  "method": "tools/call",
  "params": {
    "name": "markedup_traverse",
    "arguments": {
      "from": "auth-overview",
      "depth": 3,
      "direction": "both"
    }
  }
}
```

#### Example Response

```json
{
  "jsonrpc": "2.0",
  "id": 12,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\n  \"root\": \"auth-overview\",\n  \"max_depth\": 3,\n  \"nodes\": [\n    {\"id\": \"auth-overview\", \"title\": \"Authentication Overview\", \"entity_type\": \"concept\", \"depth\": 0},\n    {\"id\": \"oauth-provider\", \"title\": \"OAuth Provider\", \"entity_type\": \"system\", \"depth\": 1}\n  ],\n  \"edges\": [\n    {\"from\": \"auth-overview\", \"to\": \"oauth-provider\", \"type\": \"uses\", \"strength\": 0.9}\n  ]\n}"
      }
    ]
  }
}
```

The `text` field contains a JSON object with:

| Field | Type | Description |
|-------|------|-------------|
| `root` | string | ID of the starting node. |
| `max_depth` | integer | Maximum depth that was traversed. |
| `nodes` | array | List of discovered nodes, each with `id`, `title`, `entity_type`, and `depth`. |
| `edges` | array | List of edges, each with `from`, `to`, `type`, and `strength`. |

---

### `embed_status`

Get embedding coverage statistics for the knowledge base. Reports how many files have cached embeddings and which model was used.

#### Parameters

None. This tool takes no arguments.

#### Example Request

```json
{
  "jsonrpc": "2.0",
  "id": 13,
  "method": "tools/call",
  "params": {
    "name": "embed_status",
    "arguments": {}
  }
}
```

#### Example Response

```json
{
  "jsonrpc": "2.0",
  "id": 13,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\n  \"total\": 42,\n  \"embedded\": 38,\n  \"pending\": 4,\n  \"model\": \"nomic-embed-text\",\n  \"dimensions\": 768\n}"
      }
    ]
  }
}
```

The `text` field contains a JSON object with:

| Field | Type | Description |
|-------|------|-------------|
| `total` | integer | Total number of markdown files in the knowledge base. |
| `embedded` | integer | Number of files with cached embeddings. |
| `pending` | integer | Number of files needing embedding. |
| `model` | string | Embedding model name, or empty string if not configured. |
| `dimensions` | integer | Vector dimensions, or 0 if not configured. |

---

### `embed_file`

Embed a single file on demand and cache the result. Requires that the server has an embedder configured (see Environment Variables below).

#### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `path` | string | Yes | File path or page ID to embed. |

#### Example Request

```json
{
  "jsonrpc": "2.0",
  "id": 14,
  "method": "tools/call",
  "params": {
    "name": "embed_file",
    "arguments": {
      "path": "docs/auth-overview.md"
    }
  }
}
```

#### Example Response

```json
{
  "jsonrpc": "2.0",
  "id": 14,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\n  \"path\": \"docs/auth-overview.md\",\n  \"dimensions\": 768,\n  \"status\": \"embedded\"\n}"
      }
    ]
  }
}
```

The `text` field contains a JSON object with:

| Field | Type | Description |
|-------|------|-------------|
| `path` | string | The path that was requested. |
| `dimensions` | integer | Number of dimensions in the generated vector. |
| `status` | string | Always `"embedded"` on success. |

If no embedder is configured, the response has `isError: true` with the message `No embedder configured. Start the server with embedding configuration.`

---

## Environment Variables

The following environment variables configure optional embedding and reranking backends for `markedup_search` (with `semantic: true` or `rerank: true`) and `embed_file`:

| Variable | Description |
|----------|-------------|
| `MARKEDUP_EMBED_ENDPOINT` | Embedding API endpoint (e.g. `http://localhost:11434`, `https://api.openai.com`). |
| `MARKEDUP_EMBED_MODEL` | Embedding model name (e.g. `nomic-embed-text`, `text-embedding-3-small`). |
| `MARKEDUP_EMBED_API_KEY` | API key for the embedding endpoint (optional for local backends). |
| `MARKEDUP_RERANK_ENDPOINT` | Reranker API endpoint (e.g. Jina, Cohere). |
| `MARKEDUP_RERANK_MODEL` | Reranker model name. |
| `MARKEDUP_RERANK_API_KEY` | API key for the reranker endpoint. |

---

## Integration Examples

### Claude Desktop

Add markedup as an MCP server in your Claude Desktop configuration file (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS):

```json
{
  "mcpServers": {
    "markedup": {
      "command": "markedup",
      "args": ["serve", "/path/to/your/knowledge-base"],
      "env": {
        "MARKEDUP_EMBED_ENDPOINT": "http://localhost:11434",
        "MARKEDUP_EMBED_MODEL": "nomic-embed-text"
      }
    }
  }
}
```

### Cursor

Add to your Cursor MCP configuration (`.cursor/mcp.json` in your project root):

```json
{
  "mcpServers": {
    "markedup": {
      "command": "markedup",
      "args": ["serve", "."]
    }
  }
}
```

### Claude Code

Add to your Claude Code MCP settings (`.claude/settings.json`):

```json
{
  "mcpServers": {
    "markedup": {
      "command": "markedup",
      "args": ["serve", "/path/to/your/knowledge-base"]
    }
  }
}
```

### Manual / Programmatic Usage

Pipe JSON-RPC requests directly via stdin:

```sh
echo '{"jsonrpc":"2.0","id":1,"method":"initialize"}' | markedup serve /path/to/kb
```

Or use an interactive session:

```sh
markedup serve /path/to/kb
# Then type JSON-RPC requests, one per line:
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"notifications/initialized"}
{"jsonrpc":"2.0","id":3,"method":"tools/list"}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"markedup_search","arguments":{"query":"auth"}}}
```

---

## Error Codes

Standard JSON-RPC 2.0 error codes used by the server:

| Code | Meaning |
|------|---------|
| `-32700` | Parse error -- malformed JSON. |
| `-32601` | Method not found -- unknown JSON-RPC method. |
| `-32602` | Invalid params -- malformed tool call parameters or arguments. |
