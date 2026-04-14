package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KHAEntertainment/markedup/index"
	"github.com/KHAEntertainment/markedup/llm"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testMCPServer builds an mcpServer with a small in-memory KnowledgeIndex
// loaded from testdata/valid fixtures via index.Load.
func testMCPServer(t *testing.T) *mcpServer {
	t.Helper()
	result, err := index.Load(context.Background(), "../../testdata/valid", index.WithIgnoreErrors(true))
	require.NoError(t, err, "failed to load testdata/valid")
	require.NotNil(t, result.Index, "loaded index should not be nil")

	return &mcpServer{
		idx:  result.Index,
		path: "../../testdata/valid",
	}
}

// callToolReq constructs a mcp.CallToolRequest with the given arguments map.
func callToolReq(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	}
}

// resultText extracts the first TextContent text from a CallToolResult.
func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, result, "result should not be nil")
	require.NotEmpty(t, result.Content, "result should have at least one content block")
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "first content block should be TextContent, got %T", result.Content[0])
	return tc.Text
}

// ---------------------------------------------------------------------------
// Tool definition tests
// ---------------------------------------------------------------------------

func TestMCP_ToolDefinitions(t *testing.T) {
	srv := testMCPServer(t)

	tests := []struct {
		name     string
		def      mcp.Tool
		required []string
	}{
		{
			name:     "markedup_search",
			def:      srv.searchToolDef(),
			required: []string{"query"},
		},
		{
			name:     "markedup_get_page",
			def:      srv.getPageToolDef(),
			required: []string{"id"},
		},
		{
			name:     "markedup_traverse",
			def:      srv.traverseToolDef(),
			required: []string{"from"},
		},
		{
			name:     "embed_status",
			def:      srv.embedStatusToolDef(),
			required: nil,
		},
		{
			name:     "embed_file",
			def:      srv.embedFileToolDef(),
			required: []string{"path"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.name, tt.def.Name)
			assert.NotEmpty(t, tt.def.Description)

			if len(tt.required) > 0 {
				assert.ElementsMatch(t, tt.required, tt.def.InputSchema.Required,
					"required parameters should match")
			}

			// All properties referenced as required must exist in Properties.
			for _, req := range tt.required {
				_, exists := tt.def.InputSchema.Properties[req]
				assert.True(t, exists, "required param %q must be in Properties", req)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// markedup_search
// ---------------------------------------------------------------------------

func TestMCP_Search_MatchingQuery(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	result, err := srv.toolSearch(ctx, callToolReq(map[string]any{"query": "alice"}))
	require.NoError(t, err)
	require.False(t, result.IsError, "search should not return an error result")

	text := resultText(t, result)
	assert.Contains(t, text, "alice", "result should mention alice")

	// Verify JSON is parseable and contains scored results.
	var parsed []map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &parsed), "result should be valid JSON array")
	require.NotEmpty(t, parsed, "search for 'alice' should return at least one result")

	first := parsed[0]
	score, ok := first["score"].(float64)
	assert.True(t, ok, "result should have a numeric score")
	assert.Greater(t, score, float64(0), "score should be > 0")
}

func TestMCP_Search_NonexistentQuery(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	result, err := srv.toolSearch(ctx, callToolReq(map[string]any{"query": "zzz_nonexistent_xyz"}))
	require.NoError(t, err)
	require.False(t, result.IsError, "search with no matches should not be an error")

	text := resultText(t, result)
	// Should be valid JSON — either empty array or null.
	var parsed []map[string]any
	if text != "null" {
		require.NoError(t, json.Unmarshal([]byte(text), &parsed))
		assert.Empty(t, parsed, "nonexistent query should return empty results")
	}
}

func TestMCP_Search_EmptyQuery(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	// Empty query should not panic or error — may return all or nothing.
	result, err := srv.toolSearch(ctx, callToolReq(map[string]any{"query": ""}))
	require.NoError(t, err)
	require.False(t, result.IsError)
}

// ---------------------------------------------------------------------------
// markedup_get_page
// ---------------------------------------------------------------------------

func TestMCP_GetPage_Exists(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	result, err := srv.toolGetPage(ctx, callToolReq(map[string]any{"id": "alice"}))
	require.NoError(t, err)
	require.False(t, result.IsError, "get_page for existing ID should succeed")

	text := resultText(t, result)
	assert.Contains(t, text, "alice", "page content should reference alice")

	// Should be valid JSON with page data.
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &parsed), "result should be valid JSON")
	assert.NotEmpty(t, parsed, "page JSON should not be empty")
}

func TestMCP_GetPage_Missing(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	result, err := srv.toolGetPage(ctx, callToolReq(map[string]any{"id": "missing_page_xyz"}))
	require.NoError(t, err, "missing page should not return a Go error")
	require.True(t, result.IsError, "missing page should set IsError=true")

	text := resultText(t, result)
	assert.Contains(t, text, "not found", "error message should say 'not found'")
	assert.Contains(t, text, "missing_page_xyz", "error message should include the requested ID")
}

// ---------------------------------------------------------------------------
// markedup_traverse
// ---------------------------------------------------------------------------

func TestMCP_Traverse_FromAlice(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	result, err := srv.toolTraverse(ctx, callToolReq(map[string]any{
		"from":  "alice",
		"depth": float64(1),
	}))
	require.NoError(t, err)
	require.False(t, result.IsError, "traverse from existing node should succeed")

	text := resultText(t, result)
	assert.Contains(t, text, "alice", "traversal result should include alice as root")

	// Should be valid JSON.
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &parsed), "result should be valid JSON")
}

func TestMCP_Traverse_DefaultDepth(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	// depth=0 should default to 2 inside the handler.
	result, err := srv.toolTraverse(ctx, callToolReq(map[string]any{
		"from": "alice",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "alice")
}

func TestMCP_Traverse_MissingFrom(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	result, err := srv.toolTraverse(ctx, callToolReq(map[string]any{
		"from":  "nonexistent_node_xyz",
		"depth": float64(1),
	}))
	require.NoError(t, err, "traverse with bad start node should not return Go error")
	require.True(t, result.IsError, "traverse with unknown start node should set IsError=true")

	text := resultText(t, result)
	assert.NotEmpty(t, text, "error result should have a descriptive message")
}

func TestMCP_Traverse_DirectionBoth(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	result, err := srv.toolTraverse(ctx, callToolReq(map[string]any{
		"from":      "alice",
		"depth":     float64(1),
		"direction": "both",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "alice")
}

// ---------------------------------------------------------------------------
// embed_status
// ---------------------------------------------------------------------------

func TestMCP_EmbedStatus_NoEmbedder(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	// embed_status takes no params — it reads from disk.
	// With testdata dir it should return status JSON (possibly zeros).
	result, err := srv.toolEmbedStatus(ctx, callToolReq(nil))
	require.NoError(t, err)

	text := resultText(t, result)
	// Should be valid JSON regardless of whether embeddings exist.
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &parsed), "embed_status should return valid JSON")
}

// ---------------------------------------------------------------------------
// embed_file — no embedder configured
// ---------------------------------------------------------------------------

func TestMCP_EmbedFile_NoEmbedder(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	result, err := srv.toolEmbedFile(ctx, callToolReq(map[string]any{"path": "alice.md"}))
	require.NoError(t, err, "embed_file should not return Go error")
	require.True(t, result.IsError, "embed_file without embedder should return error result")

	text := resultText(t, result)
	assert.Contains(t, text, "embedder", "error should mention embedder")
}

func TestMCP_EmbedFile_EmptyPath(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	result, err := srv.toolEmbedFile(ctx, callToolReq(map[string]any{"path": ""}))
	require.NoError(t, err)
	require.True(t, result.IsError, "embed_file with empty path should error")

	text := resultText(t, result)
	assert.Contains(t, text, "required", "error should mention path is required")
}

// ---------------------------------------------------------------------------
// Index loading sanity check
// ---------------------------------------------------------------------------

func TestMCP_IndexFromTestdata(t *testing.T) {
	srv := testMCPServer(t)

	// Verify the index loaded expected pages.
	page, ok := srv.idx.Get("alice")
	require.True(t, ok, "index should contain alice page")
	assert.Equal(t, "Alice Chen", page.Frontmatter.Title)
	assert.Equal(t, "person", page.Frontmatter.EntityType)
}

// ---------------------------------------------------------------------------
// markedup_reason
// ---------------------------------------------------------------------------

// mockLLMServer creates an httptest.Server that returns canned LLM responses.
// It captures the last request body for assertion.
func mockLLMServer(t *testing.T, response string) (*httptest.Server, *string) {
	t.Helper()
	var lastBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture request body for assertions.
		bodyBytes, _ := io.ReadAll(r.Body)
		lastBody = string(bodyBytes)

		resp := fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":%s}}]}`, response)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	return ts, &lastBody
}

func testMCPServerWithLLM(t *testing.T, endpoint, model string) *mcpServer {
	t.Helper()
	srv := testMCPServer(t)
	srv.llmClient = llm.NewClient(llm.Config{
		Endpoint: endpoint,
		Model:    model,
	})
	return srv
}

func TestMCP_Reason_ToolDef(t *testing.T) {
	srv := testMCPServer(t)
	def := srv.reasonToolDef()

	assert.Equal(t, "markedup_reason", def.Name)
	assert.NotEmpty(t, def.Description)
	assert.ElementsMatch(t, []string{"query"}, def.InputSchema.Required)

	_, hasQuery := def.InputSchema.Properties["query"]
	assert.True(t, hasQuery, "should have query param")
	_, hasMaxPages := def.InputSchema.Properties["max_pages"]
	assert.True(t, hasMaxPages, "should have max_pages param")
	_, hasRels := def.InputSchema.Properties["include_relationships"]
	assert.True(t, hasRels, "should have include_relationships param")
	_, hasTemporal := def.InputSchema.Properties["include_temporal"]
	assert.True(t, hasTemporal, "should have include_temporal param")
}

func TestMCP_Reason_NoLLMConfigured(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	result, err := srv.toolReason(ctx, callToolReq(map[string]any{"query": "who works with alice?"}))
	require.NoError(t, err, "should not return Go error")
	require.True(t, result.IsError, "should return MCP error when LLM not configured")

	text := resultText(t, result)
	assert.Contains(t, text, "LLM not configured")
	assert.Contains(t, text, "MARKEDUP_LLM_ENDPOINT")
}

func TestMCP_Reason_EmptyQuery(t *testing.T) {
	srv := testMCPServer(t)
	ctx := context.Background()

	result, err := srv.toolReason(ctx, callToolReq(map[string]any{"query": ""}))
	require.NoError(t, err)
	require.True(t, result.IsError, "empty query should return error")

	text := resultText(t, result)
	assert.Contains(t, text, "required")
}

func TestMCP_Reason_HappyPath(t *testing.T) {
	// Canned LLM response.
	cannedJSON := `"{\"thinking\":\"Alice is connected to Bob through project-alpha.\",\"page_ids\":[\"alice\",\"bob\"],\"relationship_paths\":[\"alice --colleague--> bob\"]}"`
	ts, lastBody := mockLLMServer(t, cannedJSON)
	defer ts.Close()

	srv := testMCPServerWithLLM(t, ts.URL, "test-model")
	ctx := context.Background()

	result, err := srv.toolReason(ctx, callToolReq(map[string]any{"query": "who works with alice?"}))
	require.NoError(t, err)
	require.False(t, result.IsError, "happy path should not return error")

	text := resultText(t, result)

	// Verify response structure.
	var parsed reasoningResult
	require.NoError(t, json.Unmarshal([]byte(text), &parsed), "result should be valid JSON")

	assert.Equal(t, "Alice is connected to Bob through project-alpha.", parsed.Reasoning.Thinking)
	assert.Contains(t, parsed.Reasoning.RelationshipPaths, "alice --colleague--> bob")
	assert.Len(t, parsed.Pages, 2, "should have 2 pages (alice, bob)")

	// Verify pages have full content.
	pageIDs := make([]string, len(parsed.Pages))
	for i, p := range parsed.Pages {
		pageIDs[i] = p.ID
		assert.NotEmpty(t, p.Title, "page should have title")
		assert.NotEmpty(t, p.Body, "page should have body content")
	}
	assert.Contains(t, pageIDs, "alice")
	assert.Contains(t, pageIDs, "bob")

	// Verify prompt construction: last request body should contain the query and graph JSON.
	assert.Contains(t, *lastBody, "who works with alice?", "prompt should contain the query")
	assert.Contains(t, *lastBody, "knowledge graph", "prompt should reference knowledge graph")
}

func TestMCP_Reason_MarkdownFencesStripped(t *testing.T) {
	// LLM response wrapped in markdown fences.
	cannedJSON := "\"```json\\n{\\\"thinking\\\":\\\"test\\\",\\\"page_ids\\\":[\\\"alice\\\"],\\\"relationship_paths\\\":[]}\\n```\""
	ts, _ := mockLLMServer(t, cannedJSON)
	defer ts.Close()

	srv := testMCPServerWithLLM(t, ts.URL, "test-model")
	ctx := context.Background()

	result, err := srv.toolReason(ctx, callToolReq(map[string]any{"query": "test query"}))
	require.NoError(t, err)
	require.False(t, result.IsError, "should handle markdown fences gracefully")

	text := resultText(t, result)
	var parsed reasoningResult
	require.NoError(t, json.Unmarshal([]byte(text), &parsed))
	assert.Equal(t, "test", parsed.Reasoning.Thinking)
	assert.Len(t, parsed.Pages, 1)
}

func TestMCP_Reason_InvalidLLMResponse(t *testing.T) {
	// LLM returns garbage.
	cannedJSON := `"this is not json at all"`
	ts, _ := mockLLMServer(t, cannedJSON)
	defer ts.Close()

	srv := testMCPServerWithLLM(t, ts.URL, "test-model")
	ctx := context.Background()

	result, err := srv.toolReason(ctx, callToolReq(map[string]any{"query": "test query"}))
	require.NoError(t, err, "should not return Go error")
	require.True(t, result.IsError, "invalid LLM response should return MCP error")

	text := resultText(t, result)
	assert.Contains(t, text, "Failed to parse LLM response")
	assert.Contains(t, text, "this is not json at all", "error should include raw output for debugging")
}

func TestMCP_Reason_UnknownPageIDs(t *testing.T) {
	// LLM returns page IDs that don't exist in the index — should be skipped gracefully.
	cannedJSON := `"{\"thinking\":\"found stuff\",\"page_ids\":[\"alice\",\"nonexistent_xyz\"],\"relationship_paths\":[]}"`
	ts, _ := mockLLMServer(t, cannedJSON)
	defer ts.Close()

	srv := testMCPServerWithLLM(t, ts.URL, "test-model")
	ctx := context.Background()

	result, err := srv.toolReason(ctx, callToolReq(map[string]any{"query": "test"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	text := resultText(t, result)
	var parsed reasoningResult
	require.NoError(t, json.Unmarshal([]byte(text), &parsed))
	assert.Len(t, parsed.Pages, 1, "should only return alice, skipping nonexistent")
	assert.Equal(t, "alice", parsed.Pages[0].ID)
}

func TestMCP_Reason_PreFilterPath(t *testing.T) {
	// Use max_pages=1 to force pre-filtering (testdata has 6 pages).
	cannedJSON := `"{\"thinking\":\"filtered\",\"page_ids\":[\"alice\"],\"relationship_paths\":[]}"`
	ts, lastBody := mockLLMServer(t, cannedJSON)
	defer ts.Close()

	srv := testMCPServerWithLLM(t, ts.URL, "test-model")
	ctx := context.Background()

	result, err := srv.toolReason(ctx, callToolReq(map[string]any{
		"query":     "alice",
		"max_pages": float64(1),
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// The prompt should contain a graph summary — verify it was sent.
	assert.Contains(t, *lastBody, "alice", "pre-filtered prompt should include alice")

	text := resultText(t, result)
	var parsed reasoningResult
	require.NoError(t, json.Unmarshal([]byte(text), &parsed))
	assert.Equal(t, "filtered", parsed.Reasoning.Thinking)
}

func TestStripMarkdownFences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no fences",
			input:    `{"thinking": "test"}`,
			expected: `{"thinking": "test"}`,
		},
		{
			name:     "json fences",
			input:    "```json\n{\"thinking\": \"test\"}\n```",
			expected: `{"thinking": "test"}`,
		},
		{
			name:     "plain fences",
			input:    "```\n{\"thinking\": \"test\"}\n```",
			expected: `{"thinking": "test"}`,
		},
		{
			name:     "whitespace around fences",
			input:    "  \n```json\n{\"thinking\": \"test\"}\n```\n  ",
			expected: `{"thinking": "test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, stripMarkdownFences(tt.input))
		})
	}
}
