package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

type relayClient struct {
	conn  *websocket.Conn
	name  string
	token string
	mu    sync.Mutex
}

func (c *relayClient) send(msg *Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(msg)
}

type pendingRequest struct {
	serverID   string
	clientConn *relayClient
}

type RelayServer struct {
	port         int
	clients      map[string]*relayClient
	pending      map[string]*pendingRequest
	pairListeners map[string]*relayClient // code -> waiting client
	mu           sync.RWMutex
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func startRelay(port int) {
	rs := &RelayServer{
		port:          port,
		clients:       make(map[string]*relayClient),
		pending:       make(map[string]*pendingRequest),
		pairListeners: make(map[string]*relayClient),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"healthy"}`))
	})
	mux.HandleFunc("/", rs.handleWS)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("Relay listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Relay failed: %v", err)
	}
}

func (rs *RelayServer) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Upgrade error: %v", err)
		return
	}
	defer conn.Close()

	rc := &relayClient{conn: conn}
	registered := false

	defer func() {
		if registered {
			rs.unregister(rc)
		}
	}()

	for {
		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("Read error: %v", err)
			}
			return
		}

		switch msg.Type {
		case "register":
			if msg.Name == "" || msg.Token == "" {
				rc.send(&Message{Type: "error", Error: "name and token required"})
				continue
			}
			rs.mu.Lock()
			if existing, ok := rs.clients[msg.Name]; ok {
				existing.send(&Message{Type: "error", Error: "replaced by new connection"})
				delete(rs.clients, msg.Name)
			}
			rc.name = msg.Name
			rc.token = msg.Token
			rs.clients[msg.Name] = rc
			rs.mu.Unlock()
			registered = true
			rc.send(&Message{Type: "registered", Name: msg.Name})
			log.Printf("Target registered: %s", msg.Name)

		case "execute":
			if msg.Target == "" {
				rc.send(errResult(msg.ID, "target is required"))
				continue
			}
			rs.mu.RLock()
			target, ok := rs.clients[msg.Target]
			rs.mu.RUnlock()
			if !ok {
				rc.send(errResult(msg.ID, "target not connected: "+msg.Target))
				continue
			}
			if target.token != msg.Token {
				rc.send(errResult(msg.ID, "invalid token for target: "+msg.Target))
				continue
			}

			reqID := newID()
			log.Printf("Forwarding command %s -> %s (id=%s, stream=%v)", rc.name, msg.Target, reqID, msg.Stream)

			rs.mu.Lock()
			rs.pending[reqID] = &pendingRequest{
				serverID:   msg.ID,
				clientConn: rc,
			}
			rs.mu.Unlock()

			forward := &Message{
				Type:    "command",
				ID:      reqID,
				Cmd:     msg.Cmd,
				Timeout: msg.Timeout,
				Stream:  msg.Stream,
			}
			if err := target.send(forward); err != nil {
				log.Printf("Forward to %s failed: %v", msg.Target, err)
				rs.cleanupPending(reqID)
				rc.send(errResult(msg.ID, "failed to forward command: "+err.Error()))
			}

		case "stream_chunk":
			rs.mu.RLock()
			pr, ok := rs.pending[msg.ID]
			rs.mu.RUnlock()
			if !ok {
				continue
			}
			msg.ID = pr.serverID
			pr.clientConn.send(&msg)

		case "stream_end":
			rs.mu.Lock()
			pr, ok := rs.pending[msg.ID]
			if ok {
				delete(rs.pending, msg.ID)
			}
			rs.mu.Unlock()
			if !ok {
				continue
			}
			msg.ID = pr.serverID
			pr.clientConn.send(&msg)
			log.Printf("Stream end relayed for id=%s (ok=%v)", msg.ID, msg.OK)

		case "result":
			rs.mu.Lock()
			pr, ok := rs.pending[msg.ID]
			if ok {
				delete(rs.pending, msg.ID)
			}
			rs.mu.Unlock()
			if !ok {
				continue
			}
			msg.ID = pr.serverID
			pr.clientConn.send(&msg)
			log.Printf("Result relayed for id=%s (ok=%v)", msg.ID, msg.OK)

		case "pair_listen":
			if msg.Code == "" {
				rc.send(&Message{Type: "error", Error: "pair_listen requires code"})
				continue
			}
			rs.mu.Lock()
			rs.pairListeners[msg.Code] = rc
			rs.mu.Unlock()
			log.Printf("Pair listener registered for code %s", msg.Code)

		case "pair":
			if msg.Code == "" || msg.Token == "" {
				rc.send(&Message{Type: "error", Error: "pair requires code and token"})
				continue
			}
			rs.mu.Lock()
			listener, ok := rs.pairListeners[msg.Code]
			if ok {
				delete(rs.pairListeners, msg.Code)
			}
			rs.mu.Unlock()
			if !ok {
				log.Printf("Pair code %s not found or already used", msg.Code)
				rc.send(&Message{Type: "error", Error: "pair code not found or already used"})
				continue
			}
			log.Printf("Pair code %s matched, notifying listener (hostname=%s)", msg.Code, msg.Hostname)
			listener.send(&Message{
				Type:     "pair_done",
				Code:     msg.Code,
				Token:    msg.Token,
				Hostname: msg.Hostname,
			})

		default:
			rc.send(&Message{Type: "error", Error: "unknown message type: " + msg.Type})
		}
	}
}

func (rs *RelayServer) unregister(rc *relayClient) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if existing, ok := rs.clients[rc.name]; ok && existing == rc {
		delete(rs.clients, rc.name)
	}

	for id, pr := range rs.pending {
		if pr.clientConn == rc {
			delete(rs.pending, id)
		}
	}

	for code, listener := range rs.pairListeners {
		if listener == rc {
			delete(rs.pairListeners, code)
		}
	}

	log.Printf("Target disconnected: %s", rc.name)
}

func (rs *RelayServer) cleanupPending(reqID string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.pending, reqID)
}
