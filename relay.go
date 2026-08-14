package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type pendingRequest struct {
	serverID   string
	clientConn *relayClient
}

// chunkRoute records where the chunks of an in-flight chunked file transfer
// should be forwarded. It is keyed by the sending client's transfer ID and
// lives on that client's connection, so it is only touched by that
// connection's read loop and needs no extra locking.
type chunkRoute struct {
	target *relayClient
	reqID  string
}

type subTargetInfo struct {
	multiID    string
	targetName string
}

type multiPendingEntry struct {
	clientConn  *relayClient
	clientID    string
	results     map[string]*Message
	targetOrder []string
	remaining   int
	timer       *time.Timer
}

// pairListener tracks a pending pair listener and whether it requires
// an activation key from the joining daemon.
type pairListener struct {
	conn                 *relayClient
	requireActivationKey bool
}

type RelayServer struct {
	port         int
	secret       string // if non-empty, clients must send it as Bearer token
	secretExempt map[string]bool // target names allowed to connect without secret
	clients      map[string]*relayClient
	pending      map[string]*pendingRequest
	pairListeners map[string]*pairListener
	multiPending  map[string]*multiPendingEntry
	subToMulti    map[string]*subTargetInfo
	tunnels       map[string]*tunnelSession
	mu           sync.RWMutex
}

var upgrader = websocket.Upgrader{
	CheckOrigin:      func(r *http.Request) bool { return true },
	ReadBufferSize:  1 << 20, // 1 MiB
	WriteBufferSize: 1 << 20,
}

// NewRelayServer creates a new relay server ready to serve requests.
func NewRelayServer() *RelayServer {
	return &RelayServer{
		clients:       make(map[string]*relayClient),
		pending:       make(map[string]*pendingRequest),
		pairListeners: make(map[string]*pairListener),
		multiPending:  make(map[string]*multiPendingEntry),
		subToMulti:    make(map[string]*subTargetInfo),
		tunnels:       make(map[string]*tunnelSession),
		secretExempt:  make(map[string]bool),
	}
}

func (rs *RelayServer) Serve(port int) error {
	rs.port = port
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"healthy"}`))
	})
	mux.HandleFunc("/", rs.handleWS)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), mux)
}

func startRelay(port int) {
	rs := NewRelayServer()
	rs.secret = os.Getenv("RELAY_SECRET")
	if rs.secret != "" {
		log.Printf("Relay secret enabled (RELAY_SECRET)")
	}
	// Parse exempt list: comma-separated target names allowed without secret
	if exempt := os.Getenv("RELAY_SECRET_EXEMPT"); exempt != "" {
		for _, name := range splitCSV(exempt) {
			rs.secretExempt[name] = true
		}
		if len(rs.secretExempt) > 0 {
			log.Printf("Relay secret exempt: %d target(s)", len(rs.secretExempt))
		}
	}
	if err := rs.Serve(port); err != nil {
		log.Fatalf("Relay failed: %v", err)
	}
}

// splitCSV splits a comma-separated string into trimmed non-empty fields.
func splitCSV(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func (rs *RelayServer) handleWS(w http.ResponseWriter, r *http.Request) {
	// If relay secret is configured, check the Bearer token.
	// Connections without a valid secret are still allowed to upgrade
	// if there is an exempt list — they will be rejected on register
	// unless their target name is exempt.
	authenticated := true
	if rs.secret != "" {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+rs.secret {
			if len(rs.secretExempt) == 0 {
				// No exempt list — reject immediately
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				log.Printf("Rejected connection: invalid or missing relay secret")
				return
			}
			// Has exempt list — allow upgrade, check on register
			authenticated = false
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Set TCP_NODELAY on the underlying connection to reduce latency
	// for small JSON headers that precede binary chunks.
	if conn.UnderlyingConn() != nil {
		if tcpConn, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
			tcpConn.SetNoDelay(true)
		}
	}

	// Reject frames larger than the negotiated limit instead of buffering
	// unbounded data — clients chunk large file transfers to stay under it.
	conn.SetReadLimit(relayMaxFrameSize)

	rc := &relayClient{conn: conn, chunkRoutes: make(map[string]*chunkRoute)}
	registered := false

	defer func() {
		if registered {
			rs.unregister(rc)
		}
	}()

	ctx := &wsContext{rs: rs, rc: rc, conn: conn, authenticated: authenticated}

	for {
		// Read raw message to handle both text (JSON) and binary frames
		msgType, rawData, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("Read error: %v", err)
			}
			return
		}

		// Binary frames are file chunk payloads — forward to the target
		// identified by the last chunkRoute that has BinaryChunk=true.
		if msgType == websocket.BinaryMessage {
			if rc.lastBinaryRoute != nil {
				if err := rc.lastBinaryRoute.target.sendRawBytes(rawData); err != nil {
					log.Printf("Forward binary chunk to %s failed: %v", rc.lastBinaryRoute.target.name, err)
				}
			}
			continue
		}

		var msg Message
		if err := json.Unmarshal(rawData, &msg); err != nil {
			log.Printf("JSON unmarshal error: %v", err)
			continue
		}

		switch msg.Type {
		case "register":
			if ctx.handleRegister(&msg) {
				return
			}
			registered = true

		case "execute":
			ctx.handleExecute(&msg)

		case "file_transfer":
			ctx.handleFileTransfer(&msg)

		case "file_chunk":
			ctx.handleFileChunk(&msg)

		case "stream_chunk":
			ctx.handleStreamChunk(&msg)

		case "stream_end":
			ctx.handleStreamEnd(&msg)

		case "result":
			ctx.handleResult(&msg)

		case "file_transfer_result":
			ctx.handleFileTransferResult(&msg)

		case "execute_multi":
			ctx.handleExecuteMulti(&msg)

		case "pair_listen":
			ctx.handlePairListen(&msg)

		case "pair":
			ctx.handlePair(&msg)

		case "disconnect":
			ctx.handleDisconnect(&msg)

		case "tunnel_open":
			rs.handleTunnelOpen(rc, &msg)

		case "tunnel_opened":
			rs.handleTunnelRelay(rc, &msg)

		case "tunnel_data":
			rs.handleTunnelRelay(rc, &msg)

		case "tunnel_close":
			rs.handleTunnelClose(rc, &msg)

		default:
			rc.send(&Message{Type: "error", Error: "unknown message type: " + msg.Type})
		}
	}
}

func (rs *RelayServer) sendMultiResult(client *relayClient, clientID string, results map[string]*Message, order []string) {
	// Build results in the original target order
	ordered := make(map[string]*Message)
	for _, t := range order {
		if r, ok := results[t]; ok {
			ordered[t] = r
		}
	}

	resp := &Message{
		Type:    "multi_result",
		ID:      clientID,
		Results: ordered,
	}
	client.send(resp)
	log.Printf("Multi-target result sent for id=%s (%d results)", clientID, len(ordered))
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
		if listener.conn == rc {
			delete(rs.pairListeners, code)
		}
	}

	// Clean up tunnels associated with this connection
	for tid, ts := range rs.tunnels {
		if ts.clientConn == rc || ts.daemonConn == rc {
			// Notify the other side
			var other *relayClient
			if ts.clientConn == rc {
				other = ts.daemonConn
			} else {
				other = ts.clientConn
			}
			if other != nil {
				other.send(&Message{Type: "tunnel_close", TunnelID: tid})
			}
			delete(rs.tunnels, tid)
		}
	}

	log.Printf("Target disconnected: %s", rc.name)
}

func (rs *RelayServer) cleanupPending(reqID string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.pending, reqID)
}
