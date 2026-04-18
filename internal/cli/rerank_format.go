package cli

import (
	"strings"

	"github.com/Clarit-AI/markedup/rerank"
)

// parseRerankFormat maps a user-provided string to a rerank.Format constant.
// Recognised values (case-insensitive): "openai" -> FormatOpenAI.
// Everything else (including "" and "jina") defaults to FormatJina, which is
// the correct format for TEI and Jina AI endpoints.
func parseRerankFormat(s string) rerank.Format {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "openai":
		return rerank.FormatOpenAI
	default:
		return rerank.FormatJina
	}
}
