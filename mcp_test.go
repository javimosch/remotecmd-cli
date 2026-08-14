package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Protocol conformance for MCP 2026-07-28, which removed the initialize
// handshake, made requests stateless, and requires server/discover.

func callMCP(t *testing.T, method string, params any) *JSONRPCResponse {
	t.Helper()
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		raw = b
	}
	return handleMCPRequest(&JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  method,
		Params:  raw,
	})
}

// modernParams builds params carrying the per-request protocol fields.
func modernParams(extra map[string]any) map[string]any {
	p := map[string]any{
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    mcpProtocolVersion,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			"io.modelcontextprotocol/clientInfo": map[string]any{
				"name": "test-client", "version": "1.0.0",
			},
		},
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func resultMap(t *testing.T, resp *JSONRPCResponse) map[string]any {
	t.Helper()
	if resp == nil {
		t.Fatal("nil response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want map", resp.Result)
	}
	return m
}

func resetMCPSession(t *testing.T) {
	t.Helper()
	mcpCurrentSession = &mcpSession{}
	t.Cleanup(func() { mcpCurrentSession = &mcpSession{} })
}

// server/discover is mandatory in this revision.
func TestMCPServerDiscover(t *testing.T) {
	resetMCPSession(t)

	res := resultMap(t, callMCP(t, "server/discover", modernParams(nil)))

	if res["resultType"] != "complete" {
		t.Errorf("resultType = %v, want complete", res["resultType"])
	}
	versions, ok := res["supportedVersions"].([]string)
	if !ok || len(versions) == 0 {
		t.Fatalf("supportedVersions = %v, want a non-empty list", res["supportedVersions"])
	}
	if versions[0] != mcpProtocolVersion {
		t.Errorf("supportedVersions[0] = %q, want %q", versions[0], mcpProtocolVersion)
	}
	if _, ok := res["capabilities"]; !ok {
		t.Error("discover result declares no capabilities")
	}
	meta, ok := res["_meta"].(map[string]any)
	if !ok {
		t.Fatal("discover result carries no _meta")
	}
	if _, ok := meta["io.modelcontextprotocol/serverInfo"]; !ok {
		t.Error("_meta is missing io.modelcontextprotocol/serverInfo")
	}
	// server/discover is cacheable.
	if res["ttlMs"] == nil || res["cacheScope"] == nil {
		t.Error("discover result is missing ttlMs/cacheScope")
	}
}

// The discover probe must answer even without _meta: on stdio it is how a
// dual-era client decides which era the server speaks.
func TestMCPServerDiscoverWithoutMeta(t *testing.T) {
	resetMCPSession(t)
	res := resultMap(t, callMCP(t, "server/discover", nil))
	if res["resultType"] != "complete" {
		t.Errorf("resultType = %v, want complete", res["resultType"])
	}
}

func TestMCPToolsListModern(t *testing.T) {
	resetMCPSession(t)

	res := resultMap(t, callMCP(t, "tools/list", modernParams(nil)))

	if res["resultType"] != "complete" {
		t.Errorf("resultType = %v, want complete", res["resultType"])
	}
	if res["ttlMs"] == nil || res["cacheScope"] == nil {
		t.Error("tools/list must carry ttlMs and cacheScope (CacheableResult)")
	}
	tools, ok := res["tools"].([]MCPTool)
	if !ok {
		t.Fatalf("tools is %T", res["tools"])
	}
	if len(tools) != 4 {
		t.Fatalf("tool count = %d, want 4", len(tools))
	}
	want := []string{"rcmd_list_targets", "rcmd_exec", "rcmd_cp", "rcmd_health"}
	for i, n := range want {
		if tools[i].Name != n {
			t.Errorf("tool[%d] = %q, want %q (order must be deterministic)", i, tools[i].Name, n)
		}
	}
}

// Tool order must be stable across calls so clients can cache it.
func TestMCPToolsListOrderIsStable(t *testing.T) {
	resetMCPSession(t)
	first := mcpTools()
	for i := 0; i < 5; i++ {
		next := mcpTools()
		for j := range first {
			if first[j].Name != next[j].Name {
				t.Fatalf("tool order changed between calls at %d", j)
			}
		}
	}
}

// A modern request without the required protocol fields is malformed.
func TestMCPModernRequestRequiresProtocolFields(t *testing.T) {
	resetMCPSession(t)

	resp := callMCP(t, "tools/list", map[string]any{})
	if resp.Error == nil {
		t.Fatal("expected an error for a request with no _meta")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("code = %d, want -32602", resp.Error.Code)
	}

	// Version present, capabilities missing.
	resp = callMCP(t, "tools/list", map[string]any{
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion": mcpProtocolVersion,
		},
	})
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Errorf("missing clientCapabilities: got %+v, want -32602", resp.Error)
	}
}

// An unknown version must produce UnsupportedProtocolVersionError listing what
// we do serve, so the client can retry instead of guessing.
func TestMCPUnsupportedProtocolVersion(t *testing.T) {
	resetMCPSession(t)

	resp := callMCP(t, "tools/list", map[string]any{
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    "1900-01-01",
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		},
	})
	if resp.Error == nil {
		t.Fatal("expected an error")
	}
	if resp.Error.Code != mcpErrUnsupportedProtocolVersion {
		t.Errorf("code = %d, want %d", resp.Error.Code, mcpErrUnsupportedProtocolVersion)
	}
	data, ok := resp.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("error data is %T, want map", resp.Error.Data)
	}
	if data["requested"] != "1900-01-01" {
		t.Errorf("data.requested = %v", data["requested"])
	}
	supported, ok := data["supported"].([]string)
	if !ok || len(supported) == 0 || supported[0] != mcpProtocolVersion {
		t.Errorf("data.supported = %v, want [%s]", data["supported"], mcpProtocolVersion)
	}
}

// Legacy clients still open with initialize; that must keep working, and it
// must not require the modern per-request fields afterwards.
func TestMCPLegacyInitializeStillWorks(t *testing.T) {
	resetMCPSession(t)

	resp := callMCP(t, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "legacy", "version": "0.1"},
	})
	res := resultMap(t, resp)

	if res["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want the version the client asked for", res["protocolVersion"])
	}
	if _, ok := res["serverInfo"]; !ok {
		t.Error("legacy initialize result has no serverInfo")
	}
	// Legacy results must not carry modern-only fields.
	if _, ok := res["resultType"]; ok {
		t.Error("legacy initialize result should not carry resultType")
	}

	// After initialize, a bare tools/list must be served, not rejected.
	listResp := callMCP(t, "tools/list", nil)
	if listResp.Error != nil {
		t.Fatalf("legacy tools/list rejected: %+v", listResp.Error)
	}
	listRes := resultMap(t, listResp)
	if _, ok := listRes["resultType"]; ok {
		t.Error("legacy tools/list result should not carry resultType")
	}
	if _, ok := listRes["tools"]; !ok {
		t.Error("legacy tools/list returned no tools")
	}
}

// A modern request must still be served statelessly even after a legacy client
// has opened the process, since the era is chosen per how the client opens.
func TestMCPModernRequestAfterLegacyInitialize(t *testing.T) {
	resetMCPSession(t)

	callMCP(t, "initialize", map[string]any{"protocolVersion": "2025-06-18"})

	res := resultMap(t, callMCP(t, "tools/list", modernParams(nil)))
	if res["resultType"] != "complete" {
		t.Error("a request carrying modern _meta must be served with modern semantics")
	}
}

func TestMCPNotificationsGetNoResponse(t *testing.T) {
	resetMCPSession(t)
	if resp := callMCP(t, "notifications/initialized", nil); resp != nil {
		t.Errorf("notification produced a response: %+v", resp)
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	resetMCPSession(t)
	resp := callMCP(t, "does/not/exist", modernParams(nil))
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Errorf("got %+v, want -32601", resp.Error)
	}
}

// Unknown tools must come back as a tool error, not a transport error: the
// model should see the message and correct itself.
func TestMCPUnknownToolIsToolError(t *testing.T) {
	resetMCPSession(t)

	resp := callMCP(t, "tools/call", modernParams(map[string]any{
		"name":      "rcmd_not_a_tool",
		"arguments": map[string]any{},
	}))
	res := resultMap(t, resp)

	if res["isError"] != true {
		t.Errorf("isError = %v, want true", res["isError"])
	}
	if res["resultType"] != "complete" {
		t.Errorf("resultType = %v, want complete", res["resultType"])
	}
}

func TestParseMeta(t *testing.T) {
	if parseMeta(nil) != nil {
		t.Error("nil params should yield nil meta")
	}
	if parseMeta(json.RawMessage(`{}`)) != nil {
		t.Error("params without _meta should yield nil meta")
	}
	m := parseMeta(json.RawMessage(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`))
	if m == nil || m.ProtocolVersion != "2026-07-28" {
		t.Errorf("parseMeta = %+v", m)
	}
}

// --- tool-layer tests carried over from the previous suite ---

func TestMCPToolSchemaHasRequired(t *testing.T) {
	tools := mcpTools()
	for _, tool := range tools {
		schema := tool.InputSchema
		if schema["type"] != "object" {
			t.Errorf("tool %s: schema type = %v, want object", tool.Name, schema["type"])
		}
	}
}

func TestMCPExecToolSchema(t *testing.T) {
	tools := mcpTools()
	var execTool MCPTool
	for _, tool := range tools {
		if tool.Name == "rcmd_exec" {
			execTool = tool
		}
	}
	if execTool.Name == "" {
		t.Fatal("rcmd_exec tool not found")
	}

	required, ok := execTool.InputSchema["required"].([]string)
	if !ok {
		t.Fatal("required field not found or wrong type")
	}
	hasTarget := false
	hasCommand := false
	for _, r := range required {
		if r == "target" {
			hasTarget = true
		}
		if r == "command" {
			hasCommand = true
		}
	}
	if !hasTarget || !hasCommand {
		t.Errorf("required = %v, want target and command", required)
	}
}

func TestMCPTextResult(t *testing.T) {
	r := textResult("hello world")
	if len(r.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(r.Content))
	}
	if r.Content[0].Text != "hello world" {
		t.Errorf("text = %s", r.Content[0].Text)
	}
	if r.IsError {
		t.Error("should not be error")
	}
}

func TestMCPErrorResult(t *testing.T) {
	r := errorResultMCP("something went wrong")
	if !r.IsError {
		t.Error("should be error")
	}
	if !strings.Contains(r.Content[0].Text, "something went wrong") {
		t.Errorf("text = %s", r.Content[0].Text)
	}
}

func TestMCPJSONResult(t *testing.T) {
	r := jsonResult(map[string]string{"key": "value"})
	if len(r.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(r.Content))
	}
	if !strings.Contains(r.Content[0].Text, "key") {
		t.Errorf("text doesn't contain key: %s", r.Content[0].Text)
	}
}

func TestMCPParseID(t *testing.T) {
	// String ID
	id := parseID(json.RawMessage(`"abc"`))
	if id != "abc" {
		t.Errorf("id = %v, want abc", id)
	}

	// Numeric ID
	id = parseID(json.RawMessage("42"))
	if id != float64(42) {
		t.Errorf("id = %v, want 42", id)
	}

	// Nil ID
	id = parseID(json.RawMessage(""))
	if id != nil {
		t.Errorf("id = %v, want nil", id)
	}
}
