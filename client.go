package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func handleExec(target, cmd string, timeout int, stream bool) error {
	return handleExecWithStdin(target, cmd, timeout, stream, nil)
}

// handleExecWithStdin is like handleExec but pipes stdinData to the remote command.
func handleExecWithStdin(target, cmd string, timeout int, stream bool, stdinData []byte) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Relay.URL == "" {
		return fmt.Errorf("relay not configured. Run: remotecmd-cli set-relay --url <url> --name <name>")
	}

	tgt, ok := cfg.Targets[target]
	if !ok {
		return fmt.Errorf("unknown target %q. Run: remotecmd-cli add-target --name %s --token <token>", target, target)
	}

	u := wsURL(cfg.Relay.URL)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return fmt.Errorf("connect to relay: %w", err)
	}
	defer conn.Close()

	relayTarget := target
	if tgt.RelayName != "" {
		relayTarget = tgt.RelayName
	}

	id := newID()
	req := &Message{
		Type:    "execute",
		ID:      id,
		Target:  relayTarget,
		Token:   tgt.Token,
		Cmd:     cmd,
		Timeout: timeout,
		Stream:  stream,
	}
	if len(stdinData) > 0 {
		req.StdinData = base64.StdEncoding.EncodeToString(stdinData)
	}

	if err := conn.WriteJSON(req); err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	resultCh := make(chan *Message, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			var msg Message
			if err := conn.ReadJSON(&msg); err != nil {
				if stream {
					emitProgress("error", map[string]interface{}{
						"message": err.Error(),
					})
				}
				errCh <- fmt.Errorf("read response: %w", err)
				return
			}
			if msg.ID != id {
				continue
			}
			switch msg.Type {
			case "stream_chunk":
				if stream {
					emitProgress("chunk", map[string]interface{}{
						"stream": msg.StreamName,
						"data":   msg.Data,
					})
				} else {
					if msg.StreamName == "stderr" {
						fmt.Fprint(os.Stderr, msg.Data)
					} else {
						fmt.Fprint(os.Stdout, msg.Data)
					}
				}
			case "stream_end", "result":
				if stream {
					emitProgress("complete", map[string]interface{}{
						"ok":         msg.OK,
						"exit_code":  msg.ExitCode,
						"duration":  msg.DurationMs,
					})
				}
				resultCh <- &msg
				return
			}
		}
	}()

	select {
	case result := <-resultCh:
		if result.Type == "result" || !stream {
			out, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(out))
		}
		return nil
	case err := <-errCh:
		return err
	case <-time.After(time.Duration(timeout+5) * time.Second):
		if stream {
			emitProgress("timeout", map[string]interface{}{})
		}
		return fmt.Errorf("timed out waiting for response from %q", target)
	}
}

// sendToRelay connects to the relay, sends a single message, and returns the result.
// Used by the MCP server and other programmatic callers that need the raw result
// without stdout printing.
func sendToRelay(cfg *Config, msg *Message) (*Message, error) {
	if cfg.Relay.URL == "" {
		return nil, fmt.Errorf("relay not configured")
	}

	u := wsURL(cfg.Relay.URL)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to relay: %w", err)
	}
	defer conn.Close()

	if msg.ID == "" {
		msg.ID = newID()
	}

	if err := conn.WriteJSON(msg); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	timeout := msg.Timeout
	if timeout <= 0 {
		timeout = 30
	}

	resultCh := make(chan *Message, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			var resp Message
			if err := conn.ReadJSON(&resp); err != nil {
				errCh <- fmt.Errorf("read response: %w", err)
				return
			}
			if resp.ID != msg.ID {
				continue
			}
			resultCh <- &resp
			return
		}
	}()

	select {
	case result := <-resultCh:
		return result, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(time.Duration(timeout+5) * time.Second):
		return nil, fmt.Errorf("timed out waiting for response")
	}
}

// resolveRelayTargets maps local target alias names to their relay-registered
// names and builds the token map required by the relay's execute_multi path.
func resolveRelayTargets(targetAliases []string) (resolved []string, tokens map[string]string, err error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	if cfg.Relay.URL == "" {
		return nil, nil, fmt.Errorf("relay not configured. Run: remotecmd-cli set-relay --url <url> --name <name>")
	}
	resolved = make([]string, len(targetAliases))
	tokens = make(map[string]string)
	for i, alias := range targetAliases {
		tgt, ok := cfg.Targets[alias]
		if !ok {
			return nil, nil, fmt.Errorf("unknown target %q", alias)
		}
		relayTarget := alias
		if tgt.RelayName != "" {
			relayTarget = tgt.RelayName
		}
		resolved[i] = relayTarget
		tokens[relayTarget] = tgt.Token
	}
	return resolved, tokens, nil
}

// multiExecRaw sends a single command to multiple targets via the relay and
// returns the raw multi_result message. It performs no printing — callers
// (handleMultiExec, pingTargets) decide how to present results.
func multiExecRaw(resolvedTargets []string, tokens map[string]string, cmd string, timeout int) (*Message, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if cfg.Relay.URL == "" {
		return nil, fmt.Errorf("relay not configured. Run: remotecmd-cli set-relay --url <url> --name <name>")
	}

	u := wsURL(cfg.Relay.URL)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to relay: %w", err)
	}
	defer conn.Close()

	id := newID()
	req := &Message{
		Type:    "execute_multi",
		ID:      id,
		Targets: resolvedTargets,
		Tokens:  tokens,
		Cmd:     cmd,
		Timeout: timeout,
	}

	if err := conn.WriteJSON(req); err != nil {
		return nil, fmt.Errorf("send multi-exec request: %w", err)
	}

	resultCh := make(chan *Message, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			var msg Message
			if err := conn.ReadJSON(&msg); err != nil {
				errCh <- fmt.Errorf("read response: %w", err)
				return
			}
			if msg.Type == "multi_result" && msg.ID == id {
				resultCh <- &msg
				return
			}
		}
	}()

	select {
	case result := <-resultCh:
		return result, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(time.Duration(timeout+10) * time.Second):
		return nil, fmt.Errorf("timed out waiting for multi-target results")
	}
}

func handleMultiExec(targets []string, cmd string, timeout int, format string) error {
	resolvedTargets, tokens, err := resolveRelayTargets(targets)
	if err != nil {
		return err
	}

	result, err := multiExecRaw(resolvedTargets, tokens, cmd, timeout)
	if err != nil {
		return err
	}

	hasFailure := false
	if format == "json" {
		out, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Printf("%-20s | %-6s | %s\n", "TARGET", "STATUS", "OUTPUT/ERROR")
		fmt.Println("---------------------|--------|----------------------------------------")
		for _, target := range resolvedTargets {
			r, ok := result.Results[target]
			if !ok {
				fmt.Printf("%-20s | %-6s | %s\n", target, "N/A", "no result")
				hasFailure = true
				continue
			}
			if r.OK != nil && *r.OK {
				out := r.Stdout
				if len(out) > 60 {
					out = out[:60] + "..."
				}
				fmt.Printf("%-20s | %-6s | %s\n", target, "OK", out)
			} else {
				errMsg := r.Error
				if errMsg == "" {
					errMsg = "unknown error"
				}
				fmt.Printf("%-20s | %-6s | %s\n", target, "FAIL", errMsg)
				hasFailure = true
			}
		}
	}

	if hasFailure {
		return fmt.Errorf("one or more targets failed")
	}
	return nil
}
