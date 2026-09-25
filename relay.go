package main

import (
	"crypto/subtle"
	_ "embed"
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

//go:embed docs/index.html
var landingHTML []byte

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

	// Keepalive: the relay pings every connection and drops any that
	// miss a pong within pongWait, so half-dead sockets (peer rebooted,
	// NAT expired) free their target name instead of lingering until
	// the OS notices.
	pingPeriod time.Duration
	pongWait   time.Duration
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
		pingPeriod:    30 * time.Second,
		pongWait:      60 * time.Second,
	}
}

// Serve listens on all interfaces; see ServeOn.
func (rs *RelayServer) Serve(port int) error {
	return rs.ServeOn("", port)
}

// ServeOn listens on host:port. An empty host means all interfaces, which
// is the relay's default: unlike a local daemon (cli-daemon-spec's loopback
// default), a relay only works if remote daemons and clients can reach it.
// Pass a specific address to restrict it (e.g. a Tailscale IP behind a proxy).
func (rs *RelayServer) ServeOn(host string, port int) error {
	rs.port = port
	return http.ListenAndServe(relayListenAddr(host, port), rs.mux())
}

// mux routes the relay's HTTP surface: health probes and the WebSocket.
func (rs *RelayServer) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"healthy"}`))
	})
	// cli-daemon-spec §2: open, cheap, identifies the process.
	mux.HandleFunc("/_health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"service":"remotecmd-relay","pid":%d,"version":%q}`, os.Getpid(), Version)
	})
	mux.HandleFunc("/", rs.handleWS)
	return mux
}

func relayListenAddr(host string, port int) string {
	return net.JoinHostPort(host, fmt.Sprintf("%d", port))
}

// newRelayFromEnv builds a relay configured from RELAY_SECRET and
// RELAY_SECRET_EXEMPT (comma-separated target names allowed without secret).
func newRelayFromEnv() *RelayServer {
	rs := NewRelayServer()
	rs.secret = os.Getenv("RELAY_SECRET")
	if rs.secret != "" {
		log.Printf("Relay secret enabled (RELAY_SECRET)")
	}
	if exempt := os.Getenv("RELAY_SECRET_EXEMPT"); exempt != "" {
		for _, name := range splitCSV(exempt) {
			rs.secretExempt[name] = true
		}
		if len(rs.secretExempt) > 0 {
			log.Printf("Relay secret exempt: %d target(s)", len(rs.secretExempt))
		}
	}
	return rs
}

func startRelay(host string, port int) {
	go listenForRestartSignal(nil)
	if err := newRelayFromEnv().ServeOn(host, port); err != nil {
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
	// Non-WebSocket requests get the landing page (rcmd.intrane.fr)
	if r.Header.Get("Upgrade") != "websocket" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(landingHTML)
		return
	}
	// If relay secret is configured, check the Bearer token.
	// Connections without a valid secret are still allowed to upgrade
	// if there is an exempt list — they will be rejected on register
	// unless their target name is exempt.
	authenticated := true
	if rs.secret != "" {
		auth := r.Header.Get("Authorization")
		if !tokenEqual(auth, "Bearer "+rs.secret) {
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

	conn.SetReadDeadline(time.Now().Add(rs.pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(rs.pongWait))
		return nil
	})
	pingStop := make(chan struct{})
	defer close(pingStop)
	go func() {
		ticker := time.NewTicker(rs.pingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// WriteControl is concurrency-safe — no write lock needed.
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			case <-pingStop:
				return
			}
		}
	}()

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
		// Any inbound frame proves liveness — a client busy uploading
		// may not be reading, so it can't answer pings until it's done.
		conn.SetReadDeadline(time.Now().Add(rs.pongWait))

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

		// A connection without the relay secret (allowed in only because
		// RELAY_SECRET_EXEMPT is set) may act as an exempt daemon, never as a
		// client: otherwise the exempt list would reopen command execution
		// to anyone who can reach the relay.
		if !ctx.authenticated && clientOnlyMessages[msg.Type] {
			rc.send(authRequiredReply(&msg))
			log.Printf("Rejected %s from unauthenticated connection", msg.Type)
			return
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

	// Close the async write queue if active. Without this, an interrupted
	// file transfer leaves the writer goroutine blocked on
	// `for range c.writeQueue` forever — the channel is never closed and
	// the goroutine + its 128-frame buffered channel leak (one per
	// interrupted rcc file transfer).
	rc.closeWriter()

	log.Printf("Target disconnected: %s", rc.name)
}

func (rs *RelayServer) cleanupPending(reqID string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.pending, reqID)
}

// tokenEqual compares secrets in constant time so response timing does not
// leak how many leading bytes of a guessed token were correct.
func tokenEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// clientOnlyMessages are what a client (not a daemon) sends. They need the
// relay secret whenever one is configured, even when RELAY_SECRET_EXEMPT
// lets some daemons register without it.
var clientOnlyMessages = map[string]bool{
	"execute":       true,
	"execute_multi": true,
	"file_transfer": true,
	"file_chunk":    true,
	"pair_listen":   true,
	"disconnect":    true,
	"tunnel_open":   true,
}

const errAuthRequired = "authentication required: relay secret not provided"

// authRequiredReply answers a rejected client message in the shape that
// client is waiting for, so every CLI version (not only ones that know an
// "error" message) shows the reason instead of "unexpected EOF".
func authRequiredReply(msg *Message) *Message {
	switch msg.Type {
	case "execute_multi":
		return &Message{Type: "multi_result", ID: msg.ID, Error: errAuthRequired}
	case "tunnel_open":
		return &Message{Type: "tunnel_opened", TunnelID: msg.TunnelID, Error: errAuthRequired}
	case "pair_listen", "disconnect":
		return &Message{Type: "error", ID: msg.ID, Error: errAuthRequired}
	}
	return errResult(msg.ID, errAuthRequired) // execute, file_transfer, file_chunk
}
