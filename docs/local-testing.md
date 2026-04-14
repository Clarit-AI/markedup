# Local Model Testing

## Overview

markedup supports local model inference for all AI-powered features:

- **Embeddings** — `/v1/embeddings` compatible server (LM Studio, ollama, llama.cpp)
- **LLM enrichment** — `/v1/chat/completions` compatible server for Tier 2 extraction and page summaries
- **Triplex extraction** — `/v1/chat/completions` server running the SciPhi Triplex model (fine-tuned for structured knowledge extraction)
- **Reranking** — `/v1/rerank` compatible server (infinity-emb, Jina AI API, or a self-hosted Jina reranker)

This document covers the smoke test script and Go E2E test suite for validating local endpoints before running the full pipeline.

---

## Prerequisites

- [LM Studio](https://lmstudio.ai) (for embedding and LLM models) — or any OpenAI-compatible server
- [infinity-emb](https://github.com/michaelfeil/infinity) (for the Jina reranker)
- Go 1.22+
- `curl` (for the smoke test script)

---

## Reference Models

The following models have been tested with markedup. Any OpenAI-compatible endpoint should work.

| Purpose        | Recommended Model                                   | Server Port | Env Vars                                               |
|----------------|-----------------------------------------------------|-------------|--------------------------------------------------------|
| Embeddings     | `text-embedding-granite-embedding-278m-multilingual` | 1234 (LM Studio) | `MARKEDUP_EMBED_ENDPOINT`, `MARKEDUP_EMBED_MODEL` |
| LLM chat       | `granite-3-3-8b-instruct`                           | 1234 (LM Studio) | `MARKEDUP_LLM_ENDPOINT`, `MARKEDUP_LLM_MODEL`     |
| LLM (small)    | `granite-4-h-tiny` (fast, lower accuracy)           | 1234 (LM Studio) | `MARKEDUP_LLM_ENDPOINT`, `MARKEDUP_LLM_MODEL`     |
| Triplex        | `sciphi-triplex`                                    | 1234 (separate LM Studio or llama.cpp instance) | `MARKEDUP_TRIPLEX_ENDPOINT`, `MARKEDUP_TRIPLEX_MODEL` |
| Reranking      | `jina-reranker-v2-base-multilingual`                | 8091 (infinity-emb) | `MARKEDUP_RERANK_ENDPOINT`, `MARKEDUP_RERANK_MODEL` |

---

## Environment Setup

Export the env vars for whichever endpoints you have running. All are optional — tests and the smoke script skip any endpoint whose env var is unset.

```sh
# Embedding server (LM Studio or ollama)
export MARKEDUP_EMBED_ENDPOINT=http://192.168.1.152:1234
export MARKEDUP_EMBED_MODEL=text-embedding-granite-embedding-278m-multilingual

# LLM enrichment server
export MARKEDUP_LLM_ENDPOINT=http://192.168.1.152:1234
export MARKEDUP_LLM_MODEL=granite-3-3-8b-instruct

# Triplex extraction server (can be same or different port/instance)
export MARKEDUP_TRIPLEX_ENDPOINT=http://192.168.1.152:1234
export MARKEDUP_TRIPLEX_MODEL=sciphi-triplex

# Reranker server (infinity-emb or other /v1/rerank server)
export MARKEDUP_RERANK_ENDPOINT=http://192.168.1.152:8091
export MARKEDUP_RERANK_MODEL=jina-reranker-v2-base-multilingual
```

Put these in your shell profile or a local `.env` file (gitignored) for convenience.

---

## Quick Start

1. **Start LM Studio** — load the desired model and verify the API server is running (default port 1234).

2. **Run the smoke test** to verify all configured endpoints respond correctly:

   ```sh
   ./scripts/smoke-test.sh
   ```

   Expected output when all endpoints are configured and running:

   ```
   === markedup local model smoke test ===

   [PASS] embed endpoint
   [PASS] llm endpoint
   [PASS] triplex endpoint
   [PASS] rerank endpoint

   === Results: 4 passed, 0 failed, 0 skipped ===
   ```

3. **Run the full E2E test suite** with the `localmodel` build tag:

   ```sh
   go test -tags=localmodel -v ./...
   ```

   Tests skip gracefully if their corresponding env var is not set.

---

## Running Individual Tests

Run a single test by name:

```sh
# Test the embedding endpoint only
go test -tags=localmodel -run TestEmbedLocalModel -v ./...

# Test the LLM summary generation
go test -tags=localmodel -run TestEnrichSummaryLocalModel -v ./...

# Test Triplex structured extraction (strict — must return valid JSON)
go test -tags=localmodel -run TestEnrichExtractTriplex -v ./...

# Test generic LLM extraction (lenient — JSON parse failures are logged, not failed)
go test -tags=localmodel -run TestEnrichExtractLLM -v ./...

# Test the reranker
go test -tags=localmodel -run TestRerankLocalModel -v ./...
```

---

## Test Behavior Reference

| Test | Env Vars Required | Pass Condition | Leniency |
|------|-------------------|----------------|----------|
| `TestEmbedLocalModel` | `MARKEDUP_EMBED_ENDPOINT`, `MARKEDUP_EMBED_MODEL` | Returns 1 vector with finite values | Strict |
| `TestEnrichSummaryLocalModel` | `MARKEDUP_LLM_ENDPOINT`, `MARKEDUP_LLM_MODEL` | Summary > 10 chars | Strict |
| `TestEnrichExtractLLM` | `MARKEDUP_LLM_ENDPOINT`, `MARKEDUP_LLM_MODEL` | No network error; JSON parse errors logged | Lenient |
| `TestEnrichExtractTriplex` | `MARKEDUP_TRIPLEX_ENDPOINT`, `MARKEDUP_TRIPLEX_MODEL` | Valid JSON, non-empty entity_type, at least one entity or hint | Strict |
| `TestRerankLocalModel` | `MARKEDUP_RERANK_ENDPOINT`, `MARKEDUP_RERANK_MODEL` | At least one ranked result returned | Strict |

---

## Troubleshooting

**"skipping: MARKEDUP_EMBED_ENDPOINT not set"**
The env var is not exported in the current shell. Export it and re-run.

**"skipping: ... endpoint unreachable"**
The server is not running or not reachable at the configured address. Check that LM Studio or infinity-emb is started and the API server is enabled.

**`[FAIL] embed endpoint — HTTP 000`**
`curl` could not connect (timeout or connection refused). Check firewall rules if using a remote IP.

**`[FAIL] embed endpoint — HTTP 422`**
The model name is wrong or the server does not recognize the request body format. Verify `MARKEDUP_EMBED_MODEL` matches the model loaded in LM Studio.

**TestEnrichExtractLLM logs "model did not return valid JSON"**
This is expected for small models (e.g. `granite-4-h-tiny`). Use `granite-3-3-8b-instruct` or larger for reliable JSON extraction. The Triplex model (`TestEnrichExtractTriplex`) is specifically fine-tuned for this task and should always return valid JSON.

**TestRerankLocalModel skips even though the server is running**
The rerank test does not probe `/v1/models` (many rerankers don't serve this route). If the env var is set, the test will attempt the actual rerank call. A connection error at POST time will produce a hard test failure with a clear message.

---

## Notes on Triplex

[SciPhi Triplex](https://huggingface.co/SciPhi/Triplex) is a fine-tuned LLM specifically trained to output structured knowledge graph JSON. It should reliably return the JSON format required by markedup's `enrich.Extract()`. If Triplex returns malformed JSON, it is treated as a hard test failure (unlike the lenient `TestEnrichExtractLLM`).

Triplex can be served from the same LM Studio instance as the main LLM by loading it as an alternative model, or from a separate llama.cpp process. Point `MARKEDUP_TRIPLEX_ENDPOINT` to whichever address is serving Triplex.

---

## Notes on Reranker

The reranker uses the Jina API format (`/v1/rerank` with `{"query": ..., "documents": [...], "model": ...}`). infinity-emb supports this format natively when loaded with a compatible cross-encoder checkpoint (e.g. `cross-encoder/ms-marco-MiniLM-L-6-v2` or `jinaai/jina-reranker-v2-base-multilingual`).

The Jina AI cloud API is also compatible — set `MARKEDUP_RERANK_ENDPOINT=https://api.jina.ai` and provide `MARKEDUP_RERANK_MODEL=jina-reranker-v2-base-multilingual` along with a valid Jina API key (auth is not exercised by the E2E tests but would be needed for production use).
