package cli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/KHAEntertainment/markedup/index"
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
