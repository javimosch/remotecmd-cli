package main

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

// rcmd speaks two eras of MCP.
//
// The 2026-07-28 revision removed the initialize/notifications-initialized
// handshake and made the protocol stateless: every request carries its
// protocol version and client capabilities in _meta, every result carries a
// resultType, and servers MUST implement server/discover.
//
// Most clients in the wild still open with `initialize`. A dual-era server
// picks its behaviour from how the client opens: a request carrying modern
// _meta is served statelessly; an `initialize` request selects legacy
// semantics for the lifetime of this stdio process.
const (
	mcpProtocolVersion = "2026-07-28"

	// mcpLegacyFallbackVersion is echoed to a legacy client that opens with
	// `initialize` without naming a version it wants.
	mcpLegacyFallbackVersion = "2024-11-05"

	// mcpToolsCacheTTLMs is the freshness hint on cacheable list results. The
	// tool set is compiled in, so it only changes when the binary does.
	mcpToolsCacheTTLMs = 3600000
)

// MCP error codes from the 2026-07-28 allocation policy. The -32000..-32019
// range is legacy and must not be used for new codes.
const (
	mcpErrUnsupportedProtocolVersion = -32022
)

// --- JSON-RPC types ---

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
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
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

type MCPServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// mcpSession tracks which era this process is serving. It is not conversation
// state — the modern protocol is stateless — only a record of how the client
// opened, which the spec scopes to the stdio process.
type mcpSession struct {
	mu     sync.RWMutex
	legacy bool
}

func (s *mcpSession) setLegacy() {
	s.mu.Lock()
	s.legacy = true
	s.mu.Unlock()
}

func (s *mcpSession) isLegacy() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.legacy
}

var mcpCurrentSession = &mcpSession{}

// --- server ---

func runMCPServer() {
	suppressLog()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 10 MiB max line

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
		// Notifications carry no ID and get no response.
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

// mcpRequestMeta is the per-request protocol metadata the modern revision
// requires in _meta.
type mcpRequestMeta struct {
	ProtocolVersion    string          `json:"io.modelcontextprotocol/protocolVersion"`
	ClientInfo         json.RawMessage `json:"io.modelcontextprotocol/clientInfo"`
	ClientCapabilities json.RawMessage `json:"io.modelcontextprotocol/clientCapabilities"`
}

// parseMeta pulls _meta out of a request's params.
func parseMeta(params json.RawMessage) *mcpRequestMeta {
	if len(params) == 0 {
		return nil
	}
	var wrapper struct {
		Meta json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(params, &wrapper); err != nil || len(wrapper.Meta) == 0 {
		return nil
	}
	var meta mcpRequestMeta
	if err := json.Unmarshal(wrapper.Meta, &meta); err != nil {
		return nil
	}
	return &meta
}

// serverInfoMeta is the _meta block servers should attach to every result.
func serverInfoMeta() map[string]any {
	return map[string]any{
		"io.modelcontextprotocol/serverInfo": MCPServerInfo{Name: "rcmd", Version: Version},
	}
}

// completeResult wraps a modern result with the required resultType and the
// server identity. Legacy results skip both, since legacy clients validate
// against the older schemas.
func completeResult(fields map[string]any, legacy bool) map[string]any {
	if fields == nil {
		fields = map[string]any{}
	}
	if legacy {
		return fields
	}
	fields["resultType"] = "complete"
	fields["_meta"] = serverInfoMeta()
	return fields
}

func handleMCPRequest(req *JSONRPCRequest) *JSONRPCResponse {
	meta := parseMeta(req.Params)
	modern := meta != nil && meta.ProtocolVersion != ""

	switch req.Method {
	case "server/discover":
		// Also the stdio backward-compatibility probe, so it is answered
		// whether or not the caller supplied _meta.
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      parseID(req.ID),
			Result: completeResult(map[string]any{
				"supportedVersions": []string{mcpProtocolVersion},
				"capabilities":      map[string]any{"tools": map[string]any{}},
				"instructions": "Execute commands, copy files and check health on remote " +
					"machines through the rcmd relay — no SSH and no inbound ports. " +
					"Access is scoped by the relay token: a token may be limited to " +
					"specific targets and to read-only operations, and every action is " +
					"recorded in the account's audit log.",
				"ttlMs":      mcpToolsCacheTTLMs,
				"cacheScope": "public",
			}, false),
		}

	case "initialize":
		// Legacy handshake. Serving it puts this process in legacy mode.
		mcpCurrentSession.setLegacy()
		version := mcpLegacyFallbackVersion
		if req.Params != nil {
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			if json.Unmarshal(req.Params, &p) == nil && p.ProtocolVersion != "" {
				version = p.ProtocolVersion
			}
		}
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      parseID(req.ID),
			Result: map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      MCPServerInfo{Name: "rcmd", Version: Version},
			},
		}

	case "notifications/initialized":
		return nil

	case "ping":
		// Removed in 2026-07-28, still answered for legacy clients.
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      parseID(req.ID),
			Result:  map[string]any{},
		}
	}

	// Everything below is a real operation. In modern mode the per-request
	// protocol fields are mandatory and the version must be one we serve.
	legacy := mcpCurrentSession.isLegacy() && !modern
	if !legacy {
		if err := validateModernRequest(meta); err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: parseID(req.ID), Error: err}
		}
	}

	switch req.Method {
	case "tools/list":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      parseID(req.ID),
			Result: completeResult(map[string]any{
				// Deterministic order: clients cache on it.
				"tools":      mcpTools(),
				"ttlMs":      mcpToolsCacheTTLMs,
				"cacheScope": "public",
			}, legacy),
		}

	case "tools/call":
		return handleToolCall(req, legacy)

	default:
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      parseID(req.ID),
			Error:   &RPCError{Code: -32601, Message: "Method not found: " + req.Method},
		}
	}
}

// validateModernRequest enforces the per-request protocol fields. A request
// missing a required field is malformed (-32602); an unknown version gets
// UnsupportedProtocolVersionError with the versions we do serve, so the client
// can retry rather than guess.
func validateModernRequest(meta *mcpRequestMeta) *RPCError {
	if meta == nil || meta.ProtocolVersion == "" {
		return &RPCError{
			Code:    -32602,
			Message: "Invalid params: _meta['io.modelcontextprotocol/protocolVersion'] is required",
		}
	}
	if meta.ProtocolVersion != mcpProtocolVersion {
		return &RPCError{
			Code:    mcpErrUnsupportedProtocolVersion,
			Message: "Unsupported protocol version",
			Data: map[string]any{
				"supported": []string{mcpProtocolVersion},
				"requested": meta.ProtocolVersion,
			},
		}
	}
	if len(meta.ClientCapabilities) == 0 {
		return &RPCError{
			Code:    -32602,
			Message: "Invalid params: _meta['io.modelcontextprotocol/clientCapabilities'] is required",
		}
	}
	return nil
}

func handleToolCall(req *JSONRPCRequest, legacy bool) *JSONRPCResponse {
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
		result = errorResultMCP(err.Error())
	}

	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      parseID(req.ID),
		Result: completeResult(map[string]any{
			"content": result.Content,
			"isError": result.IsError,
		}, legacy),
	}
}

// parseID extracts a JSON-RPC request ID.
func parseID(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	var id interface{}
	json.Unmarshal(raw, &id)
	return id
}

func textResult(text string) MCPToolResult {
	return MCPToolResult{Content: []MCPContent{{Type: "text", Text: text}}}
}

func errorResultMCP(msg string) MCPToolResult {
	return MCPToolResult{
		Content: []MCPContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}

func jsonResult(data interface{}) MCPToolResult {
	b, _ := json.MarshalIndent(data, "", "  ")
	return textResult(string(b))
}

// suppressLog keeps stdout clean for the JSON-RPC stream. The 2026-07-28
// revision deprecates the logging feature and points stdio servers at stderr.
func suppressLog() {
	log.SetOutput(os.Stderr)
}
