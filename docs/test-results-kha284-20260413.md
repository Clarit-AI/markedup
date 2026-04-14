# KHA-284 Fix Validation — Triplex E2E Test Results

**Date**: 2026-04-13  
**PR**: KHAEntertainment/markedup#50 (merged)  
**Issue**: KHA-284 — Triplex-native prompt format + entities_and_triples parser

## Background

`TestEnrichExtractTriplex` failed in the Wave 1.5 run (`docs/test-results-20260413.md`) because
`enrich.ModelExtractor.Extract()` sent a generic chat-completion prompt that Triplex ignores entirely.
Triplex is a Phi3-3.8B fine-tune trained on a rigid NER/triple-extraction schema.

## Fix Summary (PR #50)

- Added `ModelFormat` type (`FormatGeneric` / `FormatTriplex`) to `ModelConfig`
- `FormatTriplex` sends a user-only message (no system prompt) with exact NER preamble:
  ```
  Perform Named Entity Recognition (NER) and extract knowledge graph triplets from the text...

  **Entity Types:**
  {"entity_types": ["PERSON", "ORGANIZATION", ...]}

  **Predicates:**
  {"predicates": ["WORKS_FOR", "RESEARCHES", ...]}

  **Text:**
  {document body}
  ```
- `parseTriplexOutput()` parses `entities_and_triples` array:
  - `[N], TYPE:Name` lines → `schema.Entity`
  - `(N, PREDICATE, M)` lines → `schema.Relationship` with resolved entity names
- `sendChatRequest()` helper extracted (shared by `Extract` and `GenerateSummary`)
- `FormatGeneric` (zero value) — all existing behavior unchanged, no breaking change

## E2E Test Result

**Environment**

| Endpoint | Address | Model |
|----------|---------|-------|
| Triplex | `http://192.168.1.197:8081` | `triplex-Q4_K_M.gguf` (Q4_K_M, ~2.39GB) on Radxa Rock 5C |

**Run command**
```bash
MARKEDUP_TRIPLEX_ENDPOINT=http://192.168.1.197:8081 \
MARKEDUP_TRIPLEX_MODEL=triplex \
go test -v -run TestEnrichExtractTriplex -timeout 120s -tags localmodel
```

**Output**
```
=== RUN   TestEnrichExtractTriplex
    e2e_localmodel_test.go:198: triplex OK: model=triplex entity_type="concept" entities=12 hints=0
--- PASS: TestEnrichExtractTriplex (72.26s)
PASS
ok  	github.com/KHAEntertainment/markedup	72.373s
```

**Result**: PASS

- `entity_type`: `"concept"` (most-frequent entity type in result)
- `entities`: 12 (extracted from `alice.md` test fixture)
- `hints`: 0 (Triplex doesn't generate semantic hints — those come from the generic LLM path)
- Duration: 72.26s (expected; Q4_K_M quant on embedded inference server)

## Unit Test Results (enrich package)

All 12 tests pass (9 pre-existing + 3 new Triplex-specific):

```
--- PASS: TestModelExtractor_Extract
--- PASS: TestModelExtractor_CodeFencedResponse
--- PASS: TestModelExtractor_APIError
--- PASS: TestModelExtractor_MalformedJSON
--- PASS: TestModelExtractor_NoChoices
--- PASS: TestGenerateSummary
--- PASS: TestGenerateSummary_StripsQuotes
--- PASS: TestGenerateSummary_APIError
--- PASS: TestModelExtractor_Extract_TriplexFormat
--- PASS: TestModelExtractor_TriplexEntityOnly
--- PASS: TestModelExtractor_TriplexMalformedOutput
--- PASS: TestBodyPreview
PASS
ok  	github.com/KHAEntertainment/markedup/enrich	1.146s (race detector on)
```

## Hardware Note

The Q4_K_M quant (~2.39GB) runs comfortably on the Radxa Rock 5C alongside the Granite embedding
reranker. No memory pressure observed. Inference at ~72s for the `alice.md` test fixture is consistent
with the model's known throughput on this hardware.
