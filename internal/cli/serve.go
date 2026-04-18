package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Clarit-AI/markedup/cache"
	"github.com/Clarit-AI/markedup/embed"
	"github.com/Clarit-AI/markedup/index"
	"github.com/Clarit-AI/markedup/llm"
	"github.com/Clarit-AI/markedup/rerank"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve [path]",
		Short: "Start MCP JSON-RPC 2.0 stdio server",
		Long:  "Reads JSON-RPC 2.0 requests from stdin, processes them, and writes responses to stdout.",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runServe,
	}
}

func runServe(cmd *cobra.Command, args []string) error {
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	result, err := index.Load(context.Background(), path, index.WithIgnoreErrors(true))
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", path, err)
	}

	srv := &mcpServer{idx: result.Index, path: path}

	// Initialize LLM client from config if configured.
	if appConfig.LLM.Endpoint != "" && appConfig.LLM.Model != "" {
		srv.llmClient = llm.NewClient(llm.Config{
			Endpoint: appConfig.LLM.Endpoint,
			Model:    appConfig.LLM.Model,
			APIKey:   appConfig.LLM.APIKey,
		})
	}

	s := server.NewMCPServer("markedup", "0.1.0",
		server.WithToolCapabilities(true),
	)

	s.AddTool(srv.searchToolDef(), srv.toolSearch)
	s.AddTool(srv.getPageToolDef(), srv.toolGetPage)
	s.AddTool(srv.traverseToolDef(), srv.toolTraverse)
	s.AddTool(srv.getStructureToolDef(), srv.toolGetStructure)
	s.AddTool(srv.embedStatusToolDef(), srv.toolEmbedStatus)
	s.AddTool(srv.embedFileToolDef(), srv.toolEmbedFile)
	s.AddTool(srv.reasonToolDef(), srv.toolReason)

	return server.ServeStdio(s)
}

type mcpServer struct {
	idx       *index.KnowledgeIndex
	path      string         // project root directory
	embedder  embed.Embedder // optional; nil if not configured
	llmClient *llm.Client    // optional; nil if LLM env vars not set
}

// Tool definitions.

func (s *mcpServer) searchToolDef() mcp.Tool {
	return mcp.NewTool("markedup_search",
		mcp.WithDescription("Search the knowledge base for pages matching a query"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query string"),
		),
		mcp.WithBoolean("semantic",
			mcp.Description("Enable semantic search using cached embeddings"),
		),
		mcp.WithBoolean("rerank",
			mcp.Description("Re-rank results using a cross-encoder model"),
		),
	)
}

func (s *mcpServer) getPageToolDef() mcp.Tool {
	return mcp.NewTool("markedup_get_page",
		mcp.WithDescription("Get a specific page by ID with its frontmatter and body"),
		mcp.WithString("id",
			mcp.Required(),
			mcp.Description("Page ID to retrieve"),
		),
	)
}

func (s *mcpServer) traverseToolDef() mcp.Tool {
	return mcp.NewTool("markedup_traverse",
		mcp.WithDescription("Traverse the knowledge graph from a starting node"),
		mcp.WithString("from",
			mcp.Required(),
			mcp.Description("Starting node ID"),
		),
		mcp.WithNumber("depth",
			mcp.Description("Maximum traversal depth (default 2)"),
		),
		mcp.WithString("direction",
			mcp.Description("Traversal direction: forward, reverse, or both (default forward)"),
			mcp.Enum("forward", "reverse", "both"),
		),
	)
}

func (s *mcpServer) embedStatusToolDef() mcp.Tool {
	return mcp.NewTool("embed_status",
		mcp.WithDescription("Get embedding coverage statistics for the knowledge base"),
	)
}

func (s *mcpServer) embedFileToolDef() mcp.Tool {
	return mcp.NewTool("embed_file",
		mcp.WithDescription("Embed a single file on demand and cache the result"),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("File path or page ID to embed"),
		),
	)
}

func (s *mcpServer) getStructureToolDef() mcp.Tool {
	return mcp.NewTool("markedup_get_structure",
		mcp.WithDescription("Get a compact summary of the knowledge graph structure (no body text). Call this first to understand the graph, then use markedup_get_page for specific pages."),
		mcp.WithString("filter_entity_type",
			mcp.Description("Filter to pages with this entity type (e.g. person, concept, project)"),
		),
		mcp.WithString("filter_tag",
			mcp.Description("Filter to pages containing this tag"),
		),
		mcp.WithBoolean("include_relationships",
			mcp.Description("Include relationship edges in each node (default true)"),
		),
		mcp.WithBoolean("include_temporal",
			mcp.Description("Include temporal metadata (valid-from, valid-until, last-verified) in each node (default false)"),
		),
	)
}

// Tool handlers.

func (s *mcpServer) toolSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		Query    string `json:"query"`
		Semantic bool   `json:"semantic"`
		Rerank   bool   `json:"rerank"`
	}
	if err := request.BindArguments(&params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %s", err.Error())), nil
	}

	var searchOpts []index.SearchOption
	searchOpts = append(searchOpts, index.WithContext(ctx))

	if params.Semantic {
		if appConfig.Embed.Endpoint != "" && appConfig.Embed.Model != "" {
			emb := embed.NewOpenAICompatible(embed.Config{
				Endpoint:  appConfig.Embed.Endpoint,
				ModelName: appConfig.Embed.Model,
				APIKey:    appConfig.Embed.APIKey,
			})
			vc := cache.NewVectorCache(".")
			searchOpts = append(searchOpts,
				index.WithEmbedder(emb),
				index.WithVectorCache(vc),
			)
		}
	}

	if params.Rerank {
		if appConfig.Rerank.Endpoint != "" && appConfig.Rerank.Model != "" {
			rr := rerank.NewCrossEncoder(rerank.Config{
				Endpoint: appConfig.Rerank.Endpoint,
				Model:    appConfig.Rerank.Model,
				APIKey:   appConfig.Rerank.APIKey,
				Format:   parseRerankFormat(appConfig.Rerank.Format),
			})
			searchOpts = append(searchOpts, index.WithReranker(rr))
		}
	}

	results := index.Search(s.idx, params.Query, searchOpts...)
	text := formatResults(results, FormatJSON)

	return mcp.NewToolResultText(text), nil
}

func (s *mcpServer) toolGetPage(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		ID string `json:"id"`
	}
	if err := request.BindArguments(&params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %s", err.Error())), nil
	}

	page, ok := s.idx.Get(params.ID)
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("Page %q not found", params.ID)), nil
	}

	text := formatPage(page, FormatJSON)
	return mcp.NewToolResultText(text), nil
}

func (s *mcpServer) toolTraverse(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		From      string  `json:"from"`
		Depth     float64 `json:"depth"`
		Direction string  `json:"direction"`
	}
	if err := request.BindArguments(&params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %s", err.Error())), nil
	}

	depth := int(params.Depth)
	if depth <= 0 {
		depth = 2
	}

	var opts []index.TraverseOption
	opts = append(opts, index.WithDepth(depth))

	switch params.Direction {
	case "reverse":
		opts = append(opts, index.WithDirection(index.Reverse))
	case "both":
		opts = append(opts, index.WithDirection(index.Both))
	default:
		opts = append(opts, index.WithDirection(index.Forward))
	}

	result, err := index.Traverse(s.idx, params.From, opts...)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	text := formatTraversal(result, FormatJSON)
	return mcp.NewToolResultText(text), nil
}

func (s *mcpServer) toolEmbedStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	status, err := GetEmbedStatus(s.path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get embed status: %s", err.Error())), nil
	}

	b, _ := json.MarshalIndent(status, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func (s *mcpServer) toolGetStructure(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		FilterEntityType     string `json:"filter_entity_type"`
		FilterTag            string `json:"filter_tag"`
		IncludeRelationships *bool  `json:"include_relationships"`
		IncludeTemporal      bool   `json:"include_temporal"`
	}
	if err := request.BindArguments(&params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %s", err.Error())), nil
	}

	var opts []index.SummaryOption
	if params.FilterEntityType != "" {
		opts = append(opts, index.WithEntityTypeFilter(params.FilterEntityType))
	}
	if params.FilterTag != "" {
		opts = append(opts, index.WithTagFilter(params.FilterTag))
	}
	// include_relationships defaults to true; only disable if explicitly false.
	if params.IncludeRelationships != nil && !*params.IncludeRelationships {
		opts = append(opts, index.WithRelationships(false))
	}
	if params.IncludeTemporal {
		opts = append(opts, index.WithTemporal(true))
	}

	summary := s.idx.CompactGraphSummary(opts...)
	b, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to marshal summary: %s", err.Error())), nil
	}

	return mcp.NewToolResultText(string(b)), nil
}

func (s *mcpServer) toolEmbedFile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := request.BindArguments(&params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %s", err.Error())), nil
	}

	if params.Path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}

	if s.embedder == nil {
		return mcp.NewToolResultError("No embedder configured. Start the server with embedding configuration."), nil
	}

	dims, err := EmbedSingleFile(s.path, params.Path, s.embedder)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	result := map[string]interface{}{
		"path":       params.Path,
		"dimensions": dims,
		"status":     "embedded",
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func (s *mcpServer) reasonToolDef() mcp.Tool {
	return mcp.NewTool("markedup_reason",
		mcp.WithDescription("Use LLM reasoning to answer multi-hop, structural, or dependency questions about the knowledge graph. Call this when keyword search is insufficient — e.g. 'who is connected to Alice through projects?' or 'what are the main topic clusters?'"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("The question to reason about using the knowledge graph structure"),
		),
		mcp.WithNumber("max_pages",
			mcp.Description("Maximum pages to include in graph summary sent to LLM (default 20)"),
		),
		mcp.WithBoolean("include_relationships",
			mcp.Description("Include relationship edges in graph summary (default true)"),
		),
		mcp.WithBoolean("include_temporal",
			mcp.Description("Include temporal metadata (valid-from, valid-until, last-verified) in graph summary (default false)"),
		),
	)
}

// reasoningLLMResponse is the expected JSON structure from the LLM.
type reasoningLLMResponse struct {
	Thinking          string   `json:"thinking"`
	PageIDs           []string `json:"page_ids"`
	RelationshipPaths []string `json:"relationship_paths"`
}

// reasoningResult is the final response returned to the MCP caller.
type reasoningResult struct {
	Reasoning reasoningReasoning `json:"reasoning"`
	Pages     []reasoningPage    `json:"pages"`
}

type reasoningReasoning struct {
	Thinking          string   `json:"thinking"`
	RelationshipPaths []string `json:"relationship_paths"`
}

type reasoningPage struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	EntityType string `json:"entity_type"`
	Body       string `json:"body"`
}

func (s *mcpServer) toolReason(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		Query                string  `json:"query"`
		MaxPages             float64 `json:"max_pages"`
		IncludeRelationships *bool   `json:"include_relationships"`
		IncludeTemporal      bool    `json:"include_temporal"`
	}
	if err := request.BindArguments(&params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %s", err.Error())), nil
	}

	if params.Query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	if s.llmClient == nil {
		return mcp.NewToolResultError("LLM not configured. Set MARKEDUP_LLM_ENDPOINT and MARKEDUP_LLM_MODEL environment variables."), nil
	}

	maxPages := int(params.MaxPages)
	if maxPages <= 0 {
		maxPages = 20
	}

	// Build summary options.
	var summaryOpts []index.SummaryOption
	if params.IncludeRelationships != nil && !*params.IncludeRelationships {
		summaryOpts = append(summaryOpts, index.WithRelationships(false))
	}
	if params.IncludeTemporal {
		summaryOpts = append(summaryOpts, index.WithTemporal(true))
	}

	// Token budget management: pre-filter if graph is too large.
	allPages := s.idx.All()
	if len(allPages) > maxPages {
		// Pre-filter with keyword search, then expand 1-hop neighbors.
		results := index.Search(s.idx, params.Query)
		limit := maxPages
		if len(results) < limit {
			limit = len(results)
		}

		idSet := make(map[string]struct{})
		for _, r := range results[:limit] {
			idSet[r.Page.Frontmatter.ID] = struct{}{}
		}

		// Expand 1-hop neighbors.
		var toExpand []string
		for id := range idSet {
			toExpand = append(toExpand, id)
		}
		for _, id := range toExpand {
			for _, rel := range s.idx.ForwardRels(id) {
				idSet[rel.Target] = struct{}{}
			}
			for _, refID := range s.idx.ReverseRefs(id) {
				idSet[refID] = struct{}{}
			}
		}

		ids := make([]string, 0, len(idSet))
		for id := range idSet {
			ids = append(ids, id)
		}
		summaryOpts = append(summaryOpts, index.WithPageIDs(ids))
	}

	summary := s.idx.CompactGraphSummary(summaryOpts...)

	graphJSON, err := json.Marshal(summary)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to marshal graph summary: %s", err.Error())), nil
	}

	// Build graph navigation prompt.
	userPrompt := fmt.Sprintf(`You are given a question and the structure of a knowledge graph.
Each node represents a markdown page with metadata. Edges represent typed relationships between pages.

Your task: identify which pages are most likely to contain or contribute to answering the question.
Consider relationship chains, entity types, temporal validity, and confidence scores.

Question: %s

Knowledge graph structure:
%s

Reply in JSON:
{
  "thinking": "Your reasoning about which pages are relevant and why, considering the graph structure",
  "page_ids": ["id1", "id2"],
  "relationship_paths": ["id1 --type--> id2"]
}`, params.Query, string(graphJSON))

	messages := []llm.Message{
		{Role: "system", Content: "You are a knowledge graph reasoning assistant."},
		{Role: "user", Content: userPrompt},
	}

	llmContent, err := s.llmClient.ChatCompletion(ctx, messages)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("LLM request failed: %s", err.Error())), nil
	}

	// Parse LLM response with lenient JSON handling.
	var llmResp reasoningLLMResponse
	cleaned := stripMarkdownFences(llmContent)
	if err := json.Unmarshal([]byte(cleaned), &llmResp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to parse LLM response: %s\nRaw output: %s", err.Error(), truncateStr(llmContent, 500))), nil
	}

	// Fetch full pages for each page_id.
	var pages []reasoningPage
	for _, id := range llmResp.PageIDs {
		page, ok := s.idx.Get(id)
		if !ok {
			continue // skip unknown page IDs from the LLM
		}
		pages = append(pages, reasoningPage{
			ID:         page.Frontmatter.ID,
			Title:      page.Frontmatter.Title,
			EntityType: page.Frontmatter.EntityType,
			Body:       page.Body,
		})
	}

	result := reasoningResult{
		Reasoning: reasoningReasoning{
			Thinking:          llmResp.Thinking,
			RelationshipPaths: llmResp.RelationshipPaths,
		},
		Pages: pages,
	}

	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to marshal result: %s", err.Error())), nil
	}

	return mcp.NewToolResultText(string(b)), nil
}

// stripMarkdownFences removes markdown code fences (```json ... ```) from LLM output.
func stripMarkdownFences(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) >= 3 {
			content = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	return strings.TrimSpace(content)
}

// truncateStr returns the first maxLen characters of s, appending "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
