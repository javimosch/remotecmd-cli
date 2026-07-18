package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

// --- MCP protocol types ---

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type MCPToolResult struct {
	Content []MCPContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type MCPContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type MCPInitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      MCPServerInfo  `json:"serverInfo"`
}

type MCPServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type MCPToolsListResult struct {
	Tools []MCPTool `json:"tools"`
}

// --- MCP server ---

func runMCPServer() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 10MB max line

	enc := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			enc.Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &RPCError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		resp := handleMCPRequest(&req)
		// Notifications (no ID) don't get a response
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		if resp != nil {
			enc.Encode(resp)
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		log.Fatalf("MCP scanner error: %v", err)
	}
}

func handleMCPRequest(req *JSONRPCRequest) *JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      parseID(req.ID),
			Result: MCPInitializeResult{
				ProtocolVersion: "2024-11-05",
				Capabilities: map[string]any{
					"tools": map[string]any{},
				},
				ServerInfo: MCPServerInfo{
					Name:    "rcmd",
					Version: Version,
				},
			},
		}

	case "notifications/initialized":
		// Acknowledgment — no response needed
		return nil

	case "tools/list":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      parseID(req.ID),
			Result:  MCPToolsListResult{Tools: mcpTools()},
		}

	case "tools/call":
		return handleToolCall(req)

	case "ping":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      parseID(req.ID),
			Result:  map[string]any{},
		}

	default:
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      parseID(req.ID),
			Error:   &RPCError{Code: -32601, Message: "Method not found: " + req.Method},
		}
	}
}

func handleToolCall(req *JSONRPCRequest) *JSONRPCResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      parseID(req.ID),
			Error:   &RPCError{Code: -32602, Message: "Invalid params"},
		}
	}

	result, err := executeMCPTool(params.Name, params.Arguments)
	if err != nil {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      parseID(req.ID),
			Result: MCPToolResult{
				Content: []MCPContent{{Type: "text", Text: err.Error()}},
				IsError: true,
			},
		}
	}

	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      parseID(req.ID),
		Result:  result,
	}
}

// parseID extracts the ID from a JSON-RPC request as a generic interface.
func parseID(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	var id interface{}
	json.Unmarshal(raw, &id)
	return id
}

// textResult is a helper to create a simple text tool result.
func textResult(text string) MCPToolResult {
	return MCPToolResult{
		Content: []MCPContent{{Type: "text", Text: text}},
	}
}

// errorResultMCP creates an error tool result.
func errorResultMCP(msg string) MCPToolResult {
	return MCPToolResult{
		Content: []MCPContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}

// jsonResult marshals data as formatted JSON in a text result.
func jsonResult(data interface{}) MCPToolResult {
	b, _ := json.MarshalIndent(data, "", "  ")
	return textResult(string(b))
}

// suppressLog redirects log output to stderr so stdout stays clean for MCP.
func suppressLog() {
	log.SetOutput(os.Stderr)
	// Also suppress fmt.Println that might pollute stdout
	_ = fmt.Sprintf // keep import
}
