package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/KHAEntertainment/markedup/index"
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

// JSON-RPC 2.0 types.

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// MCP protocol types.

type mcpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type mcpCapabilities struct {
	Tools *struct{} `json:"tools,omitempty"`
}

type mcpInitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    mcpCapabilities `json:"capabilities"`
	ServerInfo      mcpServerInfo   `json:"serverInfo"`
}

type mcpTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type mcpToolsListResult struct {
	Tools []mcpTool `json:"tools"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type mcpContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolCallResult struct {
	Content []mcpContentItem `json:"content"`
	IsError bool             `json:"isError,omitempty"`
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

	srv := &mcpServer{idx: result.Index}
	return srv.run()
}

type mcpServer struct {
	idx *index.KnowledgeIndex
}

func (s *mcpServer) run() error {
	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer size for large requests.
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	encoder := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				Error: &jsonRPCError{
					Code:    -32700,
					Message: "Parse error",
					Data:    err.Error(),
				},
			}
			encoder.Encode(resp)
			continue
		}

		resp := s.handleRequest(req)
		encoder.Encode(resp)
	}

	return scanner.Err()
}

func (s *mcpServer) handleRequest(req jsonRPCRequest) jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpInitializeResult{
				ProtocolVersion: "2024-11-05",
				Capabilities: mcpCapabilities{
					Tools: &struct{}{},
				},
				ServerInfo: mcpServerInfo{
					Name:    "markedup",
					Version: "0.1.0",
				},
			},
		}

	case "notifications/initialized":
		// No response needed for notifications, but we return an empty result.
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{},
		}

	case "tools/list":
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpToolsListResult{
				Tools: []mcpTool{
					{
						Name:        "markedup_search",
						Description: "Search the knowledge base for pages matching a query",
						InputSchema: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"query": map[string]interface{}{
									"type":        "string",
									"description": "Search query string",
								},
							},
							"required": []string{"query"},
						},
					},
					{
						Name:        "markedup_get_page",
						Description: "Get a specific page by ID with its frontmatter and body",
						InputSchema: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"id": map[string]interface{}{
									"type":        "string",
									"description": "Page ID to retrieve",
								},
							},
							"required": []string{"id"},
						},
					},
					{
						Name:        "markedup_traverse",
						Description: "Traverse the knowledge graph from a starting node",
						InputSchema: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"from": map[string]interface{}{
									"type":        "string",
									"description": "Starting node ID",
								},
								"depth": map[string]interface{}{
									"type":        "integer",
									"description": "Maximum traversal depth (default 2)",
								},
								"direction": map[string]interface{}{
									"type":        "string",
									"description": "Traversal direction: forward, reverse, or both (default forward)",
									"enum":        []string{"forward", "reverse", "both"},
								},
							},
							"required": []string{"from"},
						},
					},
				},
			},
		}

	case "tools/call":
		return s.handleToolCall(req)

	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonRPCError{
				Code:    -32601,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			},
		}
	}
}

func (s *mcpServer) handleToolCall(req jsonRPCRequest) jsonRPCResponse {
	var params mcpToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonRPCError{
				Code:    -32602,
				Message: "Invalid params",
				Data:    err.Error(),
			},
		}
	}

	switch params.Name {
	case "markedup_search":
		return s.toolSearch(req.ID, params.Arguments)
	case "markedup_get_page":
		return s.toolGetPage(req.ID, params.Arguments)
	case "markedup_traverse":
		return s.toolTraverse(req.ID, params.Arguments)
	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpToolCallResult{
				Content: []mcpContentItem{{Type: "text", Text: fmt.Sprintf("Unknown tool: %s", params.Name)}},
				IsError: true,
			},
		}
	}
}

func (s *mcpServer) toolSearch(id json.RawMessage, args json.RawMessage) jsonRPCResponse {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return errorResponse(id, -32602, "Invalid arguments", err.Error())
	}

	results := index.Search(s.idx, params.Query)
	text := formatResults(results, FormatJSON)

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: mcpToolCallResult{
			Content: []mcpContentItem{{Type: "text", Text: text}},
		},
	}
}

func (s *mcpServer) toolGetPage(id json.RawMessage, args json.RawMessage) jsonRPCResponse {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return errorResponse(id, -32602, "Invalid arguments", err.Error())
	}

	page, ok := s.idx.Get(params.ID)
	if !ok {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result: mcpToolCallResult{
				Content: []mcpContentItem{{Type: "text", Text: fmt.Sprintf("Page %q not found", params.ID)}},
				IsError: true,
			},
		}
	}

	text := formatPage(page, FormatJSON)
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: mcpToolCallResult{
			Content: []mcpContentItem{{Type: "text", Text: text}},
		},
	}
}

func (s *mcpServer) toolTraverse(id json.RawMessage, args json.RawMessage) jsonRPCResponse {
	var params struct {
		From      string `json:"from"`
		Depth     int    `json:"depth"`
		Direction string `json:"direction"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return errorResponse(id, -32602, "Invalid arguments", err.Error())
	}

	if params.Depth <= 0 {
		params.Depth = 2
	}

	var opts []index.TraverseOption
	opts = append(opts, index.WithDepth(params.Depth))

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
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result: mcpToolCallResult{
				Content: []mcpContentItem{{Type: "text", Text: err.Error()}},
				IsError: true,
			},
		}
	}

	text := formatTraversal(result, FormatJSON)
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: mcpToolCallResult{
			Content: []mcpContentItem{{Type: "text", Text: text}},
		},
	}
}

func errorResponse(id json.RawMessage, code int, message, data string) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jsonRPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
}
