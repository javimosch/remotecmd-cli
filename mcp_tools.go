package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// mcpTools returns the list of tools exposed by the rcmd MCP server.
func mcpTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "rcmd_list_targets",
			Description: "List all configured remote targets (servers) available via rcmd. Returns target names and their connection status.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "rcmd_exec",
			Description: "Execute a shell command on a remote target. Returns stdout, stderr, exit code, and duration. Use this to run commands on remote servers without SSH.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "The target server name (from rcmd_list_targets).",
					},
					"command": map[string]any{
						"type":        "string",
						"description": "The shell command to execute on the remote server.",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Timeout in seconds (default 30).",
						"default":     30,
					},
				},
				"required": []string{"target", "command"},
			},
		},
		{
			Name:        "rcmd_cp",
			Description: "Copy a local file to a remote target. The file is sent through the rcmd relay — no SSH or open ports needed.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "The target server name.",
					},
					"source": map[string]any{
						"type":        "string",
						"description": "Local file path to copy.",
					},
					"destination": map[string]any{
						"type":        "string",
						"description": "Remote destination path on the target.",
					},
				},
				"required": []string{"target", "source", "destination"},
			},
		},
		{
			Name:        "rcmd_health",
			Description: "Check if a remote target is reachable and connected to the rcmd relay. Returns connection status and latency.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "The target server name to check.",
					},
				},
				"required": []string{"target"},
			},
		},
	}
}

// executeMCPTool dispatches a tool call to the appropriate handler.
func executeMCPTool(name string, args json.RawMessage) (MCPToolResult, error) {
	switch name {
	case "rcmd_list_targets":
		return mcpListTargets()
	case "rcmd_exec":
		return mcpExec(args)
	case "rcmd_cp":
		return mcpCp(args)
	case "rcmd_health":
		return mcpHealth(args)
	default:
		return MCPToolResult{}, fmt.Errorf("unknown tool: %s", name)
	}
}

// mcpListTargets lists all configured targets.
func mcpListTargets() (MCPToolResult, error) {
	cfg, err := loadConfig()
	if err != nil {
		return errorResultMCP("Failed to load config: " + err.Error()), nil
	}

	type targetInfo struct {
		Name      string `json:"name"`
		Relay     string `json:"relay"`
		HasToken  bool   `json:"connected"`
	}

	var targets []targetInfo
	for name, t := range cfg.Targets {
		targets = append(targets, targetInfo{
			Name:     name,
			Relay:    cfg.Relay.URL,
			HasToken: t.Token != "",
		})
	}

	if len(targets) == 0 {
		return textResult("No targets configured. Add one with: rcmd add-target --name <n> --token <t>"), nil
	}

	return jsonResult(targets), nil
}

// mcpExec executes a command on a remote target.
func mcpExec(args json.RawMessage) (MCPToolResult, error) {
	var params struct {
		Target  string `json:"target"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return MCPToolResult{}, fmt.Errorf("invalid arguments: %v", err)
	}
	if params.Target == "" || params.Command == "" {
		return MCPToolResult{}, fmt.Errorf("target and command are required")
	}
	if params.Timeout == 0 {
		params.Timeout = 30
	}

	cfg, err := loadConfig()
	if err != nil {
		return errorResultMCP("Failed to load config: " + err.Error()), nil
	}

	tgt, ok := cfg.Targets[params.Target]
	if !ok {
		return errorResultMCP(fmt.Sprintf("Target %q not found. Use rcmd_list_targets to see available targets.", params.Target)), nil
	}

	relayTarget := params.Target
	if tgt.RelayName != "" {
		relayTarget = tgt.RelayName
	}

	resp, err := sendToRelay(cfg, &Message{
		Type:    "execute",
		Target:  relayTarget,
		Token:   tgt.Token,
		Cmd:     params.Command,
		Timeout: params.Timeout,
	})
	if err != nil {
		return errorResultMCP(fmt.Sprintf("Failed to execute: %v", err)), nil
	}
	if resp.Error != "" {
		return errorResultMCP(resp.Error), nil
	}

	type execResult struct {
		Target   string `json:"target"`
		Command  string `json:"command"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
		Duration string `json:"duration"`
		Success  bool   `json:"success"`
	}

	return jsonResult(execResult{
		Target:   params.Target,
		Command:  params.Command,
		Stdout:   resp.Stdout,
		Stderr:   resp.Stderr,
		ExitCode: resp.ExitCode,
		Duration: fmt.Sprintf("%dms", resp.DurationMs),
		Success:  resp.ExitCode == 0,
	}), nil
}

// mcpCp copies a file to a remote target.
func mcpCp(args json.RawMessage) (MCPToolResult, error) {
	var params struct {
		Target      string `json:"target"`
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return MCPToolResult{}, fmt.Errorf("invalid arguments: %v", err)
	}
	if params.Target == "" || params.Source == "" || params.Destination == "" {
		return MCPToolResult{}, fmt.Errorf("target, source, and destination are required")
	}

	if _, err := os.Stat(params.Source); err != nil {
		return errorResultMCP(fmt.Sprintf("Source file not found: %s", params.Source)), nil
	}

	cfg, err := loadConfig()
	if err != nil {
		return errorResultMCP("Failed to load config: " + err.Error()), nil
	}

	_, ok := cfg.Targets[params.Target]
	if !ok {
		return errorResultMCP(fmt.Sprintf("Target %q not found", params.Target)), nil
	}

	if err := handleFileTransfer(params.Target, params.Source, params.Destination, false); err != nil {
		return errorResultMCP(fmt.Sprintf("Transfer failed: %v", err)), nil
	}

	return textResult(fmt.Sprintf("File copied: %s → %s:%s", params.Source, params.Target, params.Destination)), nil
}

// mcpHealth checks if a target is reachable.
func mcpHealth(args json.RawMessage) (MCPToolResult, error) {
	var params struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return MCPToolResult{}, fmt.Errorf("invalid arguments: %v", err)
	}
	if params.Target == "" {
		return MCPToolResult{}, fmt.Errorf("target is required")
	}

	cfg, err := loadConfig()
	if err != nil {
		return errorResultMCP("Failed to load config: " + err.Error()), nil
	}

	tgt, ok := cfg.Targets[params.Target]
	if !ok {
		return errorResultMCP(fmt.Sprintf("Target %q not found", params.Target)), nil
	}

	relayTarget := params.Target
	if tgt.RelayName != "" {
		relayTarget = tgt.RelayName
	}

	resp, err := sendToRelay(cfg, &Message{
		Type:    "execute",
		Target:  relayTarget,
		Token:   tgt.Token,
		Cmd:     "echo ok",
		Timeout: 5,
	})
	if err != nil {
		type healthResult struct {
			Target  string `json:"target"`
			Healthy bool   `json:"healthy"`
			Error   string `json:"error"`
		}
		return jsonResult(healthResult{
			Target:  params.Target,
			Healthy: false,
			Error:   err.Error(),
		}), nil
	}

	type healthResult struct {
		Target   string `json:"target"`
		Healthy  bool   `json:"healthy"`
		Latency  string `json:"latency"`
		ExitCode int    `json:"exit_code"`
	}

	return jsonResult(healthResult{
		Target:   params.Target,
		Healthy:  resp.ExitCode == 0,
		Latency:  fmt.Sprintf("%dms", resp.DurationMs),
		ExitCode: resp.ExitCode,
	}), nil
}
