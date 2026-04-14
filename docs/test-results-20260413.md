# Local Model E2E Test Results — 2026-04-13

## Environment

| Endpoint | Address | Model |
|----------|---------|-------|
| Embed | `http://192.168.1.152:1234` | `text-embedding-granite-embedding-278m-multilingual` |
| LLM | `http://192.168.1.152:1234` | `ibm/granite-4-h-tiny` |
| Triplex | `http://192.168.1.197:8081` | `/home/radxa/hf_models/triplex-Q4_K_M.gguf` |
| Reranker | `http://127.0.0.1:8091` | `granite-embedding-reranker-english-r2` |

## Smoke Test

```
=== markedup local model smoke test ===

[PASS] embed endpoint
[PASS] llm endpoint
[PASS] triplex endpoint
[PASS] rerank endpoint

=== Results: 4 passed, 0 failed, 0 skipped ===
```

## E2E Test Results

Run: `go test -tags=localmodel -v -timeout 300s ./...`

### TestEmbedLocalModel — PASS (0.18s)

```
embed OK: model=text-embedding-granite-embedding-278m-multilingual dims=768
```

- Vector dimensionality: 768
- All values finite (no NaN/Inf)

### TestEnrichSummaryLocalModel — PASS (1.66s)

```
summary OK: model=ibm/granite-4-h-tiny len=221 summary="Alice Chen is an AI researcher specializing in developing methods to extract and maintain structured knowledge from unstructured text, focusing particularly on preserving temporal validity in knowledge graph construction."
```

### TestEnrichExtractLLM — PASS (6.90s)

```
extract OK: model=ibm/granite-4-h-tiny entity_type="PERSON" entities=2 hints=3
```

granite-4-h-tiny returned valid JSON despite being a small model. Lenient handling was not needed.

### TestEnrichExtractTriplex — FAIL (101.59s)

```
Error: Should NOT be empty, but was
Messages: Triplex must produce a non-empty entity_type

Error: Should be true
Messages: Triplex must produce at least one entity or semantic hint

triplex OK: model=/home/radxa/hf_models/triplex-Q4_K_M.gguf entity_type="" entities=0 hints=0
```

**Root cause**: Triplex uses a completely different I/O format from the generic chat prompt in `enrich.buildSystemPrompt()`.

Triplex expects:
```
entity_types: [PERSON, ORGANIZATION, CONCEPT, ...]
text: {document body}
```

Triplex returns:
```json
{
  "entities_and_triples": [
    "[1], PERSON:Alice Chen",
    "[2], ORGANIZATION:LucidityLabs",
    "[3], CONCEPT:knowledge graphs"
  ]
}
```

The model responded successfully (no network/parse error) but all `ModelResult` fields were empty because the JSON schema doesn't match. Tracked in **KHA-284**.

### TestRerankLocalModel — PASS (0.10s)

```
rerank OK: model=granite-embedding-reranker-english-r2 results=2 top="alice" score=0.8085
```

- Correctly ranked alice (knowledge graph researcher) above bob (distributed systems engineer) for query "knowledge graph"

## Summary

| Test | Result | Duration |
|------|--------|----------|
| `TestEmbedLocalModel` | PASS | 0.18s |
| `TestEnrichSummaryLocalModel` | PASS | 1.66s |
| `TestEnrichExtractLLM` | PASS | 6.90s |
| `TestEnrichExtractTriplex` | FAIL | 101.59s |
| `TestRerankLocalModel` | PASS | 0.10s |

**4/5 passing.** Failing test tracked in KHA-284 (Triplex-native prompt format).
