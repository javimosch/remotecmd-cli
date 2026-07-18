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
	port          int
	clients       map[string]*relayClient
	pending       map[string]*pendingRequest
	pairListeners map[string]*relayClient
	multiPending  map[string]*multiPendingEntry
	subToMulti    map[string]*subTargetInfo
	mu            sync.RWMutex
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
				// A registered name may only be taken over by a connection that
				// presents the same token. Otherwise any client knowing just the
				// target NAME could evict the real target and hijack (or deny)
				// its command routing.
				if !tokenEqual(existing.token, msg.Token) {
					rs.mu.Unlock()
					rc.send(&Message{Type: "error", Error: "name already registered with a different token"})
					log.Printf("Rejected register for %s: token mismatch", msg.Name)
					continue
				}
				// Legitimate reconnect (matching token): replace the stale conn.
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
			if !tokenEqual(target.token, msg.Token) {
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
			if !tokenEqual(target.token, msg.Token) {
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
				Type:    "file_transfer",
				ID:      reqID,
				Mode:    msg.Mode,
				SrcPath: msg.SrcPath,
				DstPath: msg.DstPath,
				Content: msg.Content,
			}
			if err := target.send(forward); err != nil {
				log.Printf("Forward to %s failed: %v", msg.Target, err)
				rs.cleanupPending(reqID)
				rc.send(errResult(msg.ID, "failed to forward file transfer: "+err.Error()))
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
			// Multi-target sub-results flow through the shared completion path.
			if rs.completeSubResult(msg.ID, &msg) {
				continue
			}

			// Normal single-target result
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

			log.Printf("Multi-target execute: targets=%v, cmd=%s", msg.Targets, msg.Cmd)

			batchTimeout := msg.Timeout + 5
			if batchTimeout <= 0 {
				batchTimeout = 35
			}

			// Resolve targets and register the routing table under a single write
			// lock. subToMulti and entry.results are mutated here, so a read lock
			// is unsafe: two concurrent execute_multi requests would race on the
			// subToMulti map. The actual sends are deferred until after the lock
			// is released so a slow/blocked target can't stall the whole relay.
			type forwardJob struct {
				target     *relayClient
				subID      string
				targetName string
			}
			var jobs []forwardJob

			rs.mu.Lock()
			rs.multiPending[multiID] = entry
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
				if !hasToken || !tokenEqual(tgt.token, token) {
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
				jobs = append(jobs, forwardJob{target: tgt, subID: subID, targetName: targetName})
			}
			// Set remaining before unlocking so an early result can't observe a
			// stale zero and complete the batch prematurely.
			entry.remaining = len(jobs)
			noJobs := len(jobs) == 0
			// Arm the timeout while still holding rs.mu, so the write to
			// entry.timer is ordered (via the lock) before any concurrent read
			// in completeSubResult — the target's result goroutine has no other
			// happens-before edge to this assignment. The AfterFunc callback
			// itself takes rs.mu, so it blocks until this Unlock and cannot fire
			// against a half-built batch.
			if !noJobs {
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
			}
			rs.mu.Unlock()

			if noJobs {
				// Every target failed validation (unconnected or bad token);
				// nothing was dispatched, so respond immediately.
				rs.mu.Lock()
				delete(rs.multiPending, multiID)
				rs.mu.Unlock()
				rs.sendMultiResult(rc, msg.ID, entry.results, entry.targetOrder)
				continue
			}

			// Dispatch commands outside the lock so a slow or blocked target
			// cannot stall the relay. A forward failure is finalized through the
			// shared completion path, which also fires the aggregated response if
			// it was the last outstanding target.
			for _, job := range jobs {
				forward := &Message{
					Type:    "command",
					ID:      job.subID,
					Cmd:     msg.Cmd,
					Timeout: msg.Timeout,
				}
				if err := job.target.send(forward); err != nil {
					log.Printf("Forward to %s failed: %v", job.targetName, err)
					b := false
					rs.completeSubResult(job.subID, &Message{
						Type:  "result",
						OK:    &b,
						Error: "forward failed: " + err.Error(),
					})
				}
			}

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

		default:
			rc.send(&Message{Type: "error", Error: "unknown message type: " + msg.Type})
		}
	}
}

// completeSubResult records a single sub-result for a multi-target batch. When
// the batch has no outstanding targets left it stops the timeout timer and
// dispatches the aggregated response to the originating client. It is the one
// completion path shared by real daemon results and forward failures, so the
// "all results in" decision is made under a single lock and cannot fire twice
// or observe a stale remaining count. It returns true if subID belonged to a
// multi-target batch (so callers can distinguish it from a single-target
// result); unknown or already-completed sub-IDs return false.
func (rs *RelayServer) completeSubResult(subID string, result *Message) bool {
	rs.mu.Lock()
	info, ok := rs.subToMulti[subID]
	if !ok {
		rs.mu.Unlock()
		return false
	}
	delete(rs.subToMulti, subID)
	entry, hasEntry := rs.multiPending[info.multiID]
	if !hasEntry {
		rs.mu.Unlock()
		return true
	}
	entry.results[info.targetName] = result
	entry.remaining--
	if entry.remaining > 0 {
		rs.mu.Unlock()
		return true
	}
	delete(rs.multiPending, info.multiID)
	// Read the timer under the lock; it is written under rs.mu during batch
	// setup, so grabbing it here (rather than after Unlock) preserves the
	// happens-before edge and keeps -race and the memory model happy.
	timer := entry.timer
	rs.mu.Unlock()

	if timer != nil {
		timer.Stop()
	}
	rs.sendMultiResult(entry.clientConn, entry.clientID, entry.results, entry.targetOrder)
	return true
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

	log.Printf("Target disconnected: %s", rc.name)
}

func (rs *RelayServer) cleanupPending(reqID string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.pending, reqID)
}
