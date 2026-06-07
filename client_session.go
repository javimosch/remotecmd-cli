package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ClientSession holds a persistent WebSocket connection for multiple commands.
type ClientSession struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	pending  map[string]chan *Message
	pendingMu sync.Mutex
	closed   bool
}

func newClientSession(relayURL string) (*ClientSession, error) {
	u := wsURL(relayURL)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to relay: %w", err)
	}

	s := &ClientSession{
		conn:    conn,
		pending: make(map[string]chan *Message),
	}

	// Start response reader goroutine
	go s.readLoop()

	return s, nil
}

func (s *ClientSession) readLoop() {
	for {
		var msg Message
		if err := s.conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				// Connection lost
			}
			s.pendingMu.Lock()
			for id, ch := range s.pending {
				s.pendingMu.Unlock()
				// Don't send on closed channel
				select {
				case ch <- &Message{Type: "result", OK: boolPtr(false), Error: "connection closed"}:
				default:
				}
				s.pendingMu.Lock()
				delete(s.pending, id)
			}
			s.pendingMu.Unlock()
			return
		}

		// Route response to waiting caller by ID
		s.pendingMu.Lock()
		ch, ok := s.pending[msg.ID]
		if ok {
			delete(s.pending, msg.ID)
		}
		s.pendingMu.Unlock()
		if ok && ch != nil {
			select {
			case ch <- &msg:
			default:
			}
		}
	}
}

func (s *ClientSession) Exec(target, cmd string, timeout int) (*Message, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	tgt, ok := cfg.Targets[target]
	if !ok {
		return nil, fmt.Errorf("unknown target %q", target)
	}

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
	}

	ch := make(chan *Message, 1)
	s.pendingMu.Lock()
	s.pending[id] = ch
	s.pendingMu.Unlock()

	s.writeMu.Lock()
	err = s.conn.WriteJSON(req)
	s.writeMu.Unlock()
	if err != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("send request: %w", err)
	}

	select {
	case result := <-ch:
		return result, nil
	case <-time.After(time.Duration(timeout+5) * time.Second):
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("timed out waiting for response from %q", target)
	}
}

func (s *ClientSession) Close() {
	s.closed = true
	s.conn.Close()
}

// handleClientSubcommand implements the "client" subcommand.
// It creates a persistent connection and reads commands from stdin.
func handleClientSubcommand(args []string) {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitConfigError)
	}
	if cfg.Relay.URL == "" {
		fmt.Fprintln(os.Stderr, "Error: relay not configured")
		osExit(ExitConfigError)
	}

	session, err := newClientSession(cfg.Relay.URL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(classifyError(err))
	}
	defer session.Close()

	// Interactive mode: read from stdin, one JSON command per line
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Fprintf(os.Stderr, "Connected to relay at %s\n", cfg.Relay.URL)
	fmt.Fprintf(os.Stderr, "Enter JSON commands, one per line:\n")
	fmt.Fprintf(os.Stderr, "  {\"target\":\"<name>\",\"cmd\":\"<command>\",\"timeout\":<s>}\n")
	fmt.Fprintf(os.Stderr, "Press Ctrl+D to exit.\n")

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' || line[0] == '/' {
			continue
		}

		var req struct {
			Target  string `json:"target"`
			Cmd     string `json:"cmd"`
			Timeout int    `json:"timeout"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid JSON: %v\n", err)
			continue
		}
		if req.Target == "" || req.Cmd == "" {
			fmt.Fprintln(os.Stderr, "Error: target and cmd are required")
			continue
		}
		if req.Timeout <= 0 {
			req.Timeout = 30
		}

		result, err := session.Exec(req.Target, req.Cmd, req.Timeout)
		if err != nil {
			errMsg := map[string]interface{}{
				"ok":    false,
				"error": err.Error(),
			}
			out, _ := json.Marshal(errMsg)
			fmt.Println(string(out))
			continue
		}
		out, _ := json.Marshal(result)
		fmt.Println(string(out))
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
	}
}
