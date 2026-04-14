package cli

import (
	"testing"

	"github.com/KHAEntertainment/markedup/rerank"
	"github.com/stretchr/testify/assert"
)

func TestParseRerankFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected rerank.Format
	}{
		{name: "empty string defaults to Jina", input: "", expected: rerank.FormatJina},
		{name: "jina lowercase", input: "jina", expected: rerank.FormatJina},
		{name: "jina mixed case", input: "Jina", expected: rerank.FormatJina},
		{name: "openai lowercase", input: "openai", expected: rerank.FormatOpenAI},
		{name: "openai mixed case", input: "OpenAI", expected: rerank.FormatOpenAI},
		{name: "openai with whitespace", input: "  openai  ", expected: rerank.FormatOpenAI},
		{name: "unknown value defaults to Jina", input: "cohere", expected: rerank.FormatJina},
		{name: "whitespace only defaults to Jina", input: "   ", expected: rerank.FormatJina},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRerankFormat(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}
