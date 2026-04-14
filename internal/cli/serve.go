package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/KHAEntertainment/markedup/cache"
	"github.com/KHAEntertainment/markedup/embed"
	"github.com/KHAEntertainment/markedup/enrich"
	"github.com/KHAEntertainment/markedup/index"
	"github.com/KHAEntertainment/markedup/rerank"
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
	idx      *index.KnowledgeIndex
	path     string         // project root directory
	embedder embed.Embedder // optional; nil if not configured
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
		embedEndpoint := os.Getenv("MARKEDUP_EMBED_ENDPOINT")
		embedModel := os.Getenv("MARKEDUP_EMBED_MODEL")
		embedAPIKey := os.Getenv("MARKEDUP_EMBED_API_KEY")

		if embedEndpoint != "" && embedModel != "" {
			emb := embed.NewOpenAICompatible(embed.Config{
				Endpoint:  embedEndpoint,
				ModelName: embedModel,
				APIKey:    embedAPIKey,
			})
			vc := cache.NewVectorCache(".")
			searchOpts = append(searchOpts,
				index.WithEmbedder(emb),
				index.WithVectorCache(vc),
			)
		}
	}

	if params.Rerank {
		rerankEndpoint := os.Getenv("MARKEDUP_RERANK_ENDPOINT")
		rerankModel := os.Getenv("MARKEDUP_RERANK_MODEL")
		rerankAPIKey := os.Getenv("MARKEDUP_RERANK_API_KEY")

		if rerankEndpoint != "" && rerankModel != "" {
			rr := rerank.NewCrossEncoder(rerank.Config{
				Endpoint: rerankEndpoint,
				Model:    rerankModel,
				APIKey:   rerankAPIKey,
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
		mcp.WithDescription("Use LLM reasoning over the knowledge graph structure to answer multi-hop, structural, and dependency queries. Returns reasoning trace and full content of selected pages. Requires MARKEDUP_LLM_ENDPOINT and MARKEDUP_LLM_MODEL to be set."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("The question to reason about using the graph structure"),
		),
		mcp.WithNumber("max_pages",
			mcp.Description("Maximum pages to include in graph context (default 20). Larger values give the LLM more context but use more tokens."),
		),
		mcp.WithBoolean("include_relationships",
			mcp.Description("Include relationship edges in the graph context (default true)"),
		),
		mcp.WithBoolean("include_temporal",
			mcp.Description("Include temporal metadata in the graph context (default false)"),
		),
	)
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

	llmEndpoint := os.Getenv("MARKEDUP_LLM_ENDPOINT")
	llmModel := os.Getenv("MARKEDUP_LLM_MODEL")
	llmAPIKey := os.Getenv("MARKEDUP_LLM_API_KEY")
	if llmEndpoint == "" || llmModel == "" {
		return mcp.NewToolResultError("LLM endpoint not configured. Set MARKEDUP_LLM_ENDPOINT and MARKEDUP_LLM_MODEL."), nil
	}

	maxPages := int(params.MaxPages)
	if maxPages <= 0 {
		maxPages = 20
	}

	// Build summary options. If the graph is larger than maxPages, use keyword
	// search to pre-filter to the most relevant pages plus their 1-hop neighbors,
	// then pass those IDs via WithIDFilter. When using an ID filter the set is
	// already bounded, so we omit WithMaxPages.
	var opts []index.SummaryOption

	if s.idx.Pages() > maxPages {
		results := index.Search(s.idx, params.Query, index.WithContext(ctx))
		ids := make(map[string]struct{})
		for _, r := range results {
			ids[r.Page.Frontmatter.ID] = struct{}{}
			// Add 1-hop relationship targets.
			for _, rel := range r.Page.Frontmatter.Relationships {
				ids[rel.Target] = struct{}{}
			}
		}
		idList := make([]string, 0, len(ids))
		for id := range ids {
			idList = append(idList, id)
		}
		opts = append(opts, index.WithIDFilter(idList))
	} else {
		opts = append(opts, index.WithMaxPages(maxPages))
	}

	// include_relationships defaults to true; only disable if explicitly false.
	if params.IncludeRelationships != nil && !*params.IncludeRelationships {
		opts = append(opts, index.WithRelationships(false))
	}
	if params.IncludeTemporal {
		opts = append(opts, index.WithTemporal(true))
	}

	summary := s.idx.CompactGraphSummary(opts...)
	graphJSON, err := json.Marshal(summary)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to marshal graph: %s", err.Error())), nil
	}

	reasoner := enrich.NewGraphReasoner(enrich.ReasonConfig{
		Endpoint: llmEndpoint,
		Model:    llmModel,
		APIKey:   llmAPIKey,
	})

	reasonResult, err := reasoner.Reason(ctx, params.Query, string(graphJSON))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Reasoning failed: %s", err.Error())), nil
	}

	// Fetch full page content for each selected ID.
	type pageResult struct {
		ID      string      `json:"id"`
		Page    interface{} `json:"page,omitempty"`
		Missing bool        `json:"missing,omitempty"`
	}
	pages := make([]pageResult, 0, len(reasonResult.PageIDs))
	for _, id := range reasonResult.PageIDs {
		p, ok := s.idx.Get(id)
		if !ok {
			pages = append(pages, pageResult{ID: id, Missing: true})
			continue
		}
		pages = append(pages, pageResult{ID: id, Page: p})
	}

	result := map[string]interface{}{
		"reasoning":          reasonResult.Thinking,
		"selected_page_ids":  reasonResult.PageIDs,
		"relationship_paths": reasonResult.RelationshipPaths,
		"pages":              pages,
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}
