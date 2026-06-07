package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func handleExec(target, cmd string, timeout int, stream bool) error {
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

	// Resolve relay-registered name (may differ from local alias)
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
