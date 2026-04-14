#!/usr/bin/env bash
# smoke-test.sh — validates local model endpoints are reachable and functional.
#
# Usage: ./scripts/smoke-test.sh
#
# Reads env vars:
#   MARKEDUP_EMBED_ENDPOINT     — base URL for embedding server (e.g. http://192.168.1.152:1234)
#   MARKEDUP_EMBED_MODEL        — embedding model name
#   MARKEDUP_LLM_ENDPOINT       — base URL for LLM chat server
#   MARKEDUP_LLM_MODEL          — LLM model name
#   MARKEDUP_TRIPLEX_ENDPOINT   — base URL for Triplex extraction server
#   MARKEDUP_TRIPLEX_MODEL      — Triplex model name
#   MARKEDUP_RERANK_ENDPOINT    — base URL for reranker server (e.g. http://192.168.1.152:8091)
#   MARKEDUP_RERANK_MODEL       — reranker model name
#
# Exits 0 if all configured endpoints pass; exits 1 if any configured endpoint fails.
# Endpoints with unset env vars are skipped with [SKIP].

set -euo pipefail

PASS=0
FAIL=0
SKIP=0

# check_endpoint name method url body
# Sends a curl request and prints [PASS]/[FAIL] based on HTTP status.
check_endpoint() {
    local name="$1"
    local method="$2"
    local url="$3"
    local body="$4"

    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" \
        -X "$method" \
        -H "Content-Type: application/json" \
        -d "$body" \
        --connect-timeout 5 \
        --max-time 30 \
        "$url" 2>/dev/null) || http_code="000"

    if [ "$http_code" = "200" ]; then
        echo "[PASS] $name"
        PASS=$((PASS + 1))
    else
        echo "[FAIL] $name — HTTP $http_code"
        FAIL=$((FAIL + 1))
    fi
}

echo "=== markedup local model smoke test ==="
echo ""

# --- Embed endpoint ---
if [ -z "${MARKEDUP_EMBED_ENDPOINT:-}" ]; then
    echo "[SKIP] embed endpoint — MARKEDUP_EMBED_ENDPOINT not set"
    SKIP=$((SKIP + 1))
else
    model="${MARKEDUP_EMBED_MODEL:-text-embedding-nomic-embed-text-v1-5}"
    body="{\"model\":\"${model}\",\"input\":[\"test\"]}"
    check_endpoint "embed endpoint" "POST" "${MARKEDUP_EMBED_ENDPOINT}/v1/embeddings" "$body"
fi

# --- LLM chat endpoint ---
if [ -z "${MARKEDUP_LLM_ENDPOINT:-}" ]; then
    echo "[SKIP] llm endpoint — MARKEDUP_LLM_ENDPOINT not set"
    SKIP=$((SKIP + 1))
else
    model="${MARKEDUP_LLM_MODEL:-granite-3-3-8b-instruct}"
    body="{\"model\":\"${model}\",\"messages\":[{\"role\":\"user\",\"content\":\"Say hi\"}]}"
    check_endpoint "llm endpoint" "POST" "${MARKEDUP_LLM_ENDPOINT}/v1/chat/completions" "$body"
fi

# --- Triplex endpoint ---
if [ -z "${MARKEDUP_TRIPLEX_ENDPOINT:-}" ]; then
    echo "[SKIP] triplex endpoint — MARKEDUP_TRIPLEX_ENDPOINT not set"
    SKIP=$((SKIP + 1))
else
    model="${MARKEDUP_TRIPLEX_MODEL:-sciphi-triplex}"
    body="{\"model\":\"${model}\",\"messages\":[{\"role\":\"user\",\"content\":\"Say hi\"}]}"
    check_endpoint "triplex endpoint" "POST" "${MARKEDUP_TRIPLEX_ENDPOINT}/v1/chat/completions" "$body"
fi

# --- Rerank endpoint ---
if [ -z "${MARKEDUP_RERANK_ENDPOINT:-}" ]; then
    echo "[SKIP] rerank endpoint — MARKEDUP_RERANK_ENDPOINT not set"
    SKIP=$((SKIP + 1))
else
    model="${MARKEDUP_RERANK_MODEL:-jina-reranker-v2-base-multilingual}"
    body="{\"model\":\"${model}\",\"query\":\"test\",\"documents\":[\"a\",\"b\"]}"
    check_endpoint "rerank endpoint" "POST" "${MARKEDUP_RERANK_ENDPOINT}/v1/rerank" "$body"
fi

echo ""
echo "=== Results: ${PASS} passed, ${FAIL} failed, ${SKIP} skipped ==="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi

exit 0
