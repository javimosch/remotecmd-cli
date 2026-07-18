package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPInitialize(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
	}
	resp := handleMCPRequest(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	result, ok := resp.Result.(MCPInitializeResult)
	if !ok {
		t.Fatal("expected MCPInitializeResult")
	}
	if result.ProtocolVersion == "" {
		t.Error("empty protocol version")
	}
	if result.ServerInfo.Name != "rcmd" {
		t.Errorf("server name = %s, want rcmd", result.ServerInfo.Name)
	}
	if result.Capabilities["tools"] == nil {
		t.Error("tools capability not declared")
	}
}

func TestMCPToolsList(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "tools/list",
	}
	resp := handleMCPRequest(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	result, ok := resp.Result.(MCPToolsListResult)
	if !ok {
		t.Fatal("expected MCPToolsListResult")
	}
	if len(result.Tools) < 4 {
		t.Errorf("tools count = %d, want >= 4", len(result.Tools))
	}

	names := make(map[string]bool)
	for _, tool := range result.Tools {
		names[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %s has empty description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %s has nil input schema", tool.Name)
		}
	}

	expected := []string{"rcmd_list_targets", "rcmd_exec", "rcmd_cp", "rcmd_health"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("3"),
		Method:  "unknown/method",
	}
	resp := handleMCPRequest(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code = %d, want -32601", resp.Error.Code)
	}
}

func TestMCPNotificationsInitialized(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	resp := handleMCPRequest(req)
	if resp != nil {
		t.Error("expected nil response for notification")
	}
}

func TestMCPToolCallUnknownTool(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("4"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"unknown_tool","arguments":{}}`),
	}
	resp := handleMCPRequest(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error == nil {
		// Tool errors come back as results with isError=true, not as RPC errors
		result, ok := resp.Result.(MCPToolResult)
		if !ok {
			t.Fatal("expected MCPToolResult")
		}
		if !result.IsError {
			t.Error("expected isError=true for unknown tool")
		}
	}
}

func TestMCPToolCallExecMissingArgs(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("5"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"rcmd_exec","arguments":{}}`),
	}
	resp := handleMCPRequest(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	// Missing args returns a tool error result (IsError=true), not an RPC error
	result, ok := resp.Result.(MCPToolResult)
	if !ok {
		t.Fatal("expected MCPToolResult")
	}
	if !result.IsError {
		t.Error("expected isError=true for missing args")
	}
}

func TestMCPToolCallExecMissingTarget(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("6"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"rcmd_exec","arguments":{"command":"ls"}}`),
	}
	resp := handleMCPRequest(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	result, ok := resp.Result.(MCPToolResult)
	if !ok {
		t.Fatal("expected MCPToolResult")
	}
	if !result.IsError {
		t.Error("expected isError=true for missing target")
	}
}

func TestMCPPing(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("7"),
		Method:  "ping",
	}
	resp := handleMCPRequest(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Result == nil {
		t.Error("expected result for ping")
	}
}

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
