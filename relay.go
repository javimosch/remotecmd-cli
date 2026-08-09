package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
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
	lastBinaryRoute *chunkRoute // route for the next binary frame (BinaryChunk=true)
	// Async write queue for file chunk forwarding. When non-nil, binary
	// frames and chunk headers are sent through the queue instead of
	// synchronously, allowing the relay read loop to immediately read
	// the next frame from the client while the previous one is still
	// being written to the target.
	writeQueueMu sync.Mutex
	writeQueue   chan []byte
	writeErr     error // set by the writer goroutine on failure
	writeOnce    sync.Once
}

// startWriter launches a background goroutine that drains the write queue
// and writes frames to the connection in order. This decouples the relay's
// read loop from the target's write speed.
func (c *relayClient) startWriter() {
	c.writeOnce.Do(func() {
		c.writeQueue = make(chan []byte, 64) // buffer up to 64 frames
		go func() {
			for frame := range c.writeQueue {
				// First byte indicates frame type:
				// 0 = text (JSON), 1 = binary, 2 = header+binary pair
				c.mu.Lock()
				var err error
				if len(frame) > 0 && frame[0] == 2 {
					// Header+binary pair: [2][4-byte hl][header][binary]
					hl := int(frame[1])<<24 | int(frame[2])<<16 | int(frame[3])<<8 | int(frame[4])
					header := frame[5 : 5+hl]
					binary := frame[5+hl:]
					if err = c.conn.WriteMessage(websocket.TextMessage, header); err == nil {
						err = c.conn.WriteMessage(websocket.BinaryMessage, binary)
					}
				} else {
					msgType := websocket.TextMessage
					data := frame
					if len(frame) > 0 && frame[0] == 1 {
						msgType = websocket.BinaryMessage
						data = frame[1:]
					} else if len(frame) > 0 && frame[0] == 0 {
						data = frame[1:]
					}
					err = c.conn.WriteMessage(msgType, data)
				}
				c.mu.Unlock()
				if err != nil {
					c.writeErr = err
					// Drain remaining frames
					for range c.writeQueue {
					}
					return
				}
			}
		}()
	})
}

// closeWriter shuts down the async write queue. Called after the transfer
// result is received, meaning all chunks have been forwarded successfully.
func (c *relayClient) closeWriter() {
	c.writeQueueMu.Lock()
	q := c.writeQueue
	c.writeQueue = nil
	c.writeQueueMu.Unlock()
	if q != nil {
		close(q)
	}
}

func (c *relayClient) send(msg *Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(msg)
}

// sendRawBytes sends a raw binary frame to the client (no JSON wrapping).
// Used for forwarding binary file chunk data. If an async write queue is
// active, enqueues the frame instead of blocking.
func (c *relayClient) sendRawBytes(data []byte) error {
	c.writeQueueMu.Lock()
	q := c.writeQueue
	c.writeQueueMu.Unlock()
	if q != nil {
		// Prepend 1 to indicate binary frame
		frame := make([]byte, len(data)+1)
		frame[0] = 1
		copy(frame[1:], data)
		select {
		case q <- frame:
			return c.writeErr
		default:
			// Queue full — fall back to synchronous send
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.BinaryMessage, data)
}

// sendRawJSON sends pre-marshaled JSON bytes as a text frame.
// Used for forwarding file chunk headers without re-serialization.
func (c *relayClient) sendRawJSON(data []byte) error {
	c.writeQueueMu.Lock()
	q := c.writeQueue
	c.writeQueueMu.Unlock()
	if q != nil {
		// Prepend 0 to indicate text frame
		frame := make([]byte, len(data)+1)
		frame[0] = 0
		copy(frame[1:], data)
		select {
		case q <- frame:
			return c.writeErr
		default:
			// Queue full — fall back to synchronous send
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// sendRawHeaderAndBinary atomically enqueues a JSON header followed by a
// binary frame. This prevents interleaving with other streams' chunks when
// multiple parallel streams share the same target's async write queue.
func (c *relayClient) sendRawHeaderAndBinary(header []byte, binary []byte) error {
	c.writeQueueMu.Lock()
	q := c.writeQueue
	c.writeQueueMu.Unlock()
	if q != nil {
		// Format: [2][4-byte header length][header...][binary...]
		hl := len(header)
		frame := make([]byte, 1+4+hl+len(binary))
		frame[0] = 2
		frame[1] = byte(hl >> 24)
		frame[2] = byte(hl >> 16)
		frame[3] = byte(hl >> 8)
		frame[4] = byte(hl)
		copy(frame[5:], header)
		copy(frame[5+hl:], binary)
		select {
		case q <- frame:
			return c.writeErr
		default:
			// Queue full — fall back to synchronous send
		}
	}
	// Synchronous: send header then binary
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.WriteMessage(websocket.TextMessage, header); err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.BinaryMessage, binary)
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

// pairListener tracks a pending pair listener and whether it requires
// an activation key from the joining daemon.
type pairListener struct {
	conn                 *relayClient
	requireActivationKey bool
}

type RelayServer struct {
	port         int
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
				Type:      "command",
				ID:        reqID,
				Cmd:       msg.Cmd,
				Timeout:   msg.Timeout,
				Stream:    msg.Stream,
				StdinData: msg.StdinData,
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
				Type:            "file_transfer",
				ID:              reqID,
				Mode:            msg.Mode,
				SrcPath:         msg.SrcPath,
				DstPath:         msg.DstPath,
				Content:         msg.Content,
				Chunked:         msg.Chunked,
				TotalChunks:     msg.TotalChunks,
				TotalSize:       msg.TotalSize,
				ChunkSizeBytes:  msg.ChunkSizeBytes,
				ParallelStreams: msg.ParallelStreams,
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
				// Start async writer on the target so the relay read loop
				// can immediately read the next chunk from the client while
				// the previous one is still being written to the target.
				target.startWriter()
			}

		case "file_chunk":
			route, ok := rc.chunkRoutes[msg.ID]
			if !ok {
				log.Printf("Dropping file_chunk for unknown transfer id=%s", msg.ID)
				continue
			}
			if msg.BinaryChunk {
				// Binary chunk: the next frame on this connection is the binary
				// payload. Read it immediately and forward header+binary as a
				// single atomic unit to prevent interleaving with other parallel
				// streams sharing the same target's async write queue.
				forwardHeader := &Message{
					Type:        "file_chunk",
					ID:          route.reqID,
					Seq:         msg.Seq,
					Final:       msg.Final,
					BinaryChunk: true,
					Compressed:  msg.Compressed,
				}
				headerBytes, err := json.Marshal(forwardHeader)
				if err != nil {
					log.Printf("Marshal binary chunk header: %v", err)
					continue
				}
				// Read the next frame — it must be the binary payload
				binType, binData, err := conn.ReadMessage()
				if err != nil {
					log.Printf("Read binary chunk payload: %v", err)
					delete(rc.chunkRoutes, msg.ID)
					rs.cleanupPending(route.reqID)
					rc.send(errResult(msg.ID, "failed to read binary chunk payload: "+err.Error()))
					continue
				}
				if binType != websocket.BinaryMessage {
					log.Printf("Expected binary frame after chunk header, got text")
					continue
				}
				// Forward header+binary atomically through the write queue
				if err := route.target.sendRawHeaderAndBinary(headerBytes, binData); err != nil {
					log.Printf("Forward binary chunk to %s failed: %v", route.target.name, err)
					delete(rc.chunkRoutes, msg.ID)
					rs.cleanupPending(route.reqID)
					rc.send(errResult(msg.ID, "failed to forward file chunk: "+err.Error()))
					continue
				}
				if msg.Final {
					delete(rc.chunkRoutes, msg.ID)
				}
			} else {
				// Legacy base64 chunk (backwards compat with old clients)
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
			// Close the target's async write queue — all chunks have been
			// received and the daemon has replied.
			rc.closeWriter()

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
			rs.pairListeners[msg.Code] = &pairListener{
				conn:                 rc,
				requireActivationKey: msg.RequireActivationKey,
			}
			rs.mu.Unlock()
			log.Printf("Pair listener registered for code %s (requireActivationKey=%v)", msg.Code, msg.RequireActivationKey)

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
			// Validate activation key if the listener requires one
			if listener.requireActivationKey {
				if msg.ActivationKey == "" || !activationKeys.isValid(msg.ActivationKey) {
					log.Printf("Pair code %s rejected: invalid or missing activation key", msg.Code)
					rc.send(&Message{Type: "error", Error: "pair rejected: activation key required but not provided or invalid"})
					// Re-register the listener so the real peer can still pair
					rs.mu.Lock()
					rs.pairListeners[msg.Code] = listener
					rs.mu.Unlock()
					continue
				}
				log.Printf("Pair code %s: activation key validated", msg.Code)
			}
			log.Printf("Pair code %s matched, notifying listener (hostname=%s)", msg.Code, msg.Hostname)
			listener.conn.send(&Message{
				Type:     "pair_done",
				Code:     msg.Code,
				Token:    msg.Token,
				Hostname: msg.Hostname,
			})
			rc.send(&Message{
				Type: "pair_confirmed",
				Code: msg.Code,
			})

		case "disconnect":
			if msg.Target == "" {
				rc.send(&Message{Type: "error", Error: "disconnect requires target"})
				continue
			}
			rs.mu.RLock()
			target, ok := rs.clients[msg.Target]
			rs.mu.RUnlock()
			if !ok {
				rc.send(&Message{Type: "error", Error: "target not connected: " + msg.Target})
				continue
			}
			if target.token != msg.Token {
				rc.send(&Message{Type: "error", Error: "invalid token for target: " + msg.Target})
				continue
			}
			// Forward disconnect to the daemon
			if err := target.send(&Message{Type: "disconnect"}); err != nil {
				rc.send(&Message{Type: "error", Error: "failed to forward disconnect: " + err.Error()})
				continue
			}
			log.Printf("Disconnect forwarded to %s", msg.Target)
			rc.send(&Message{Type: "disconnect_confirmed", Target: msg.Target})

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
