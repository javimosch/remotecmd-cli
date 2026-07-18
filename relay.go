package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type relayClient struct {
	conn      *websocket.Conn
	name      string
	token     string
	mu        sync.Mutex
	chunkRoutes map[string]*chunkRoute // client transfer ID -> forwarding route
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

type RelayServer struct {
	port         int
	clients      map[string]*relayClient
	pending      map[string]*pendingRequest
	pairListeners map[string]*relayClient
	multiPending  map[string]*multiPendingEntry
	subToMulti    map[string]*subTargetInfo
	tunnels       map[string]*tunnelSession
	mu           sync.RWMutex
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// NewRelayServer creates a new relay server ready to serve requests.
func NewRelayServer() *RelayServer {
	return &RelayServer{
		clients:       make(map[string]*relayClient),
		pending:       make(map[string]*pendingRequest),
		pairListeners: make(map[string]*relayClient),
		multiPending:  make(map[string]*multiPendingEntry),
		subToMulti:    make(map[string]*subTargetInfo),
		tunnels:       make(map[string]*tunnelSession),
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
	if err := rs.Serve(port); err != nil {
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

		case "file_transfer":
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
			log.Printf("Forwarding file transfer %s -> %s (id=%s, mode=%s)", rc.name, msg.Target, reqID, msg.Mode)

			rs.mu.Lock()
			rs.pending[reqID] = &pendingRequest{
				serverID:   msg.ID,
				clientConn: rc,
			}
			rs.mu.Unlock()

			forward := &Message{
				Type:        "file_transfer",
				ID:          reqID,
				Mode:        msg.Mode,
				SrcPath:     msg.SrcPath,
				DstPath:     msg.DstPath,
				Content:     msg.Content,
				Chunked:     msg.Chunked,
				TotalChunks: msg.TotalChunks,
				TotalSize:   msg.TotalSize,
			}
			if err := target.send(forward); err != nil {
				log.Printf("Forward to %s failed: %v", msg.Target, err)
				rs.cleanupPending(reqID)
				rc.send(errResult(msg.ID, "failed to forward file transfer: "+err.Error()))
				continue
			}
			// For chunked transfers, remember where the follow-up chunks go.
			if msg.Chunked {
				rc.chunkRoutes[msg.ID] = &chunkRoute{target: target, reqID: reqID}
			}

		case "file_chunk":
			route, ok := rc.chunkRoutes[msg.ID]
			if !ok {
				log.Printf("Dropping file_chunk for unknown transfer id=%s", msg.ID)
				continue
			}
			forward := &Message{
				Type:  "file_chunk",
				ID:    route.reqID,
				Seq:   msg.Seq,
				Data:  msg.Data,
				Final: msg.Final,
			}
			if err := route.target.send(forward); err != nil {
				log.Printf("Forward chunk to %s failed: %v", route.target.name, err)
				delete(rc.chunkRoutes, msg.ID)
				rs.cleanupPending(route.reqID)
				rc.send(errResult(msg.ID, "failed to forward file chunk: "+err.Error()))
				continue
			}
			if msg.Final {
				delete(rc.chunkRoutes, msg.ID)
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

			// Check multi-target sub-result first
			if info, isMulti := rs.subToMulti[msg.ID]; isMulti {
				delete(rs.subToMulti, msg.ID)
				multiEntry, hasEntry := rs.multiPending[info.multiID]
				if !hasEntry {
					rs.mu.Unlock()
					continue
				}
				// Store this result for the target
				multiEntry.results[info.targetName] = &msg
				multiEntry.remaining--

				if multiEntry.remaining <= 0 {
					// All results received — send aggregated response
					delete(rs.multiPending, info.multiID)
					rs.mu.Unlock()
					// Stop the timeout timer
					if multiEntry.timer != nil {
						multiEntry.timer.Stop()
					}
					rs.sendMultiResult(multiEntry.clientConn, multiEntry.clientID, multiEntry.results, multiEntry.targetOrder)
				} else {
					rs.mu.Unlock()
				}
				continue
			}

			// Normal single-target result
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

		case "file_transfer_result":
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
			msg.Type = "result"
			pr.clientConn.send(&msg)
			log.Printf("File transfer result relayed for id=%s (ok=%v)", msg.ID, msg.OK)

		case "execute_multi":
			if len(msg.Targets) == 0 || msg.Cmd == "" {
				rc.send(errResult(msg.ID, "targets and cmd are required"))
				continue
			}
			if msg.Tokens == nil {
				msg.Tokens = make(map[string]string)
			}

			multiID := newID()
			entry := &multiPendingEntry{
				clientConn:  rc,
				clientID:    msg.ID,
				results:     make(map[string]*Message),
				targetOrder: msg.Targets,
				remaining:   0,
			}

			rs.mu.Lock()
			rs.multiPending[multiID] = entry
			rs.mu.Unlock()

			log.Printf("Multi-target execute: targets=%v, cmd=%s", msg.Targets, msg.Cmd)

			batchTimeout := msg.Timeout + 5
			if batchTimeout <= 0 {
				batchTimeout = 35
			}

			pendingCount := 0
			rs.mu.RLock()
			for _, targetName := range msg.Targets {
				tgt, ok := rs.clients[targetName]
				if !ok {
					b := false
					entry.results[targetName] = &Message{
						Type:  "result",
						OK:    &b,
						Error: "target not connected",
					}
					continue
				}
				token, hasToken := msg.Tokens[targetName]
				if !hasToken || tgt.token != token {
					b := false
					entry.results[targetName] = &Message{
						Type:  "result",
						OK:    &b,
						Error: "invalid token",
					}
					continue
				}

				subID := newID()
				rs.subToMulti[subID] = &subTargetInfo{
					multiID:    multiID,
					targetName: targetName,
				}
				pendingCount++

				forward := &Message{
					Type:    "command",
					ID:      subID,
					Cmd:     msg.Cmd,
					Timeout: msg.Timeout,
				}
				if err := tgt.send(forward); err != nil {
					log.Printf("Forward to %s failed: %v", targetName, err)
					delete(rs.subToMulti, subID)
					b := false
					entry.results[targetName] = &Message{
						Type:  "result",
						OK:    &b,
						Error: "forward failed: " + err.Error(),
					}
					continue
				}
			}
			rs.mu.RUnlock()

			entry.remaining = pendingCount

			if pendingCount == 0 {
				rs.mu.Lock()
				delete(rs.multiPending, multiID)
				rs.mu.Unlock()
				rs.sendMultiResult(rc, msg.ID, entry.results, entry.targetOrder)
				continue
			}

			entry.timer = time.AfterFunc(time.Duration(batchTimeout)*time.Second, func() {
				rs.mu.Lock()
				e, ok := rs.multiPending[multiID]
				if !ok {
					rs.mu.Unlock()
					return
				}
				delete(rs.multiPending, multiID)
				for subID, info := range rs.subToMulti {
					if info.multiID == multiID {
						delete(rs.subToMulti, subID)
					}
				}
				rs.mu.Unlock()

				for _, t := range e.targetOrder {
					if _, done := e.results[t]; !done {
						b := false
						e.results[t] = &Message{
							Type:  "result",
							OK:    &b,
							Error: "timed out waiting for result",
						}
					}
				}
				rs.sendMultiResult(e.clientConn, e.clientID, e.results, e.targetOrder)
			})

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
				log.Printf("Pair code %s not found or already used (daemon will retry)", msg.Code)
				continue
			}
			log.Printf("Pair code %s matched, notifying listener (hostname=%s)", msg.Code, msg.Hostname)
			listener.send(&Message{
				Type:     "pair_done",
				Code:     msg.Code,
				Token:    msg.Token,
				Hostname: msg.Hostname,
			})
			rc.send(&Message{
				Type: "pair_confirmed",
				Code: msg.Code,
			})

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
		if listener == rc {
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
