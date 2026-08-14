package main

import (
	"encoding/json"
	"log"

	"github.com/gorilla/websocket"
)

// wsContext holds shared state for a single WebSocket connection's read loop.
// Sub-handlers are methods on this struct so they can access the relay server,
// the client, the raw connection (for binary frame reads), and the auth state
// without passing 4 parameters to each one.
type wsContext struct {
	rs           *RelayServer
	rc           *relayClient
	conn         *websocket.Conn
	authenticated bool
}

// handleRegister processes a "register" message.
// Returns true if the connection should be closed (auth failure).
func (ctx *wsContext) handleRegister(msg *Message) bool {
	if msg.Name == "" || msg.Token == "" {
		ctx.rc.send(&Message{Type: "error", Error: "name and token required"})
		return false
	}
	// If secret is enforced and this connection is unauthenticated,
	// check if the target name is in the exempt list.
	if ctx.rs.secret != "" && !ctx.authenticated {
		if !ctx.rs.secretExempt[msg.Name] {
			ctx.rc.send(&Message{Type: "error", Error: "authentication required: relay secret not provided"})
			log.Printf("Rejected registration: %s not in exempt list and no secret provided", msg.Name)
			return true
		}
		log.Printf("Exempt connection: %s (no secret, whitelisted)", msg.Name)
	}
	ctx.rs.mu.Lock()
	if existing, ok := ctx.rs.clients[msg.Name]; ok {
		existing.send(&Message{Type: "error", Error: "replaced by new connection"})
		delete(ctx.rs.clients, msg.Name)
	}
	ctx.rc.name = msg.Name
	ctx.rc.token = msg.Token
	ctx.rs.clients[msg.Name] = ctx.rc
	ctx.rs.mu.Unlock()
	ctx.rc.send(&Message{Type: "registered", Name: msg.Name})
	log.Printf("Target registered: %s", msg.Name)
	return false
}

// handleExecute processes an "execute" message — forwards a command to a target.
func (ctx *wsContext) handleExecute(msg *Message) {
	if msg.Target == "" {
		ctx.rc.send(errResult(msg.ID, "target is required"))
		return
	}
	ctx.rs.mu.RLock()
	target, ok := ctx.rs.clients[msg.Target]
	ctx.rs.mu.RUnlock()
	if !ok {
		ctx.rc.send(errResult(msg.ID, "target not connected: "+msg.Target))
		return
	}
	if target.token != msg.Token {
		ctx.rc.send(errResult(msg.ID, "invalid token for target: "+msg.Target))
		return
	}

	reqID := newID()
	log.Printf("Forwarding command %s -> %s (id=%s, stream=%v)", ctx.rc.name, msg.Target, reqID, msg.Stream)

	ctx.rs.mu.Lock()
	ctx.rs.pending[reqID] = &pendingRequest{
		serverID:   msg.ID,
		clientConn: ctx.rc,
	}
	ctx.rs.mu.Unlock()

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
		ctx.rs.cleanupPending(reqID)
		ctx.rc.send(errResult(msg.ID, "failed to forward command: "+err.Error()))
	}
}

// handleFileTransfer processes a "file_transfer" message — forwards a file
// transfer request to a target and sets up chunk routing for chunked transfers.
func (ctx *wsContext) handleFileTransfer(msg *Message) {
	if msg.Target == "" {
		ctx.rc.send(errResult(msg.ID, "target is required"))
		return
	}
	ctx.rs.mu.RLock()
	target, ok := ctx.rs.clients[msg.Target]
	ctx.rs.mu.RUnlock()
	if !ok {
		ctx.rc.send(errResult(msg.ID, "target not connected: "+msg.Target))
		return
	}
	if target.token != msg.Token {
		ctx.rc.send(errResult(msg.ID, "invalid token for target: "+msg.Target))
		return
	}

	reqID := newID()
	log.Printf("Forwarding file transfer %s -> %s (id=%s, mode=%s)", ctx.rc.name, msg.Target, reqID, msg.Mode)

	ctx.rs.mu.Lock()
	ctx.rs.pending[reqID] = &pendingRequest{
		serverID:   msg.ID,
		clientConn: ctx.rc,
	}
	ctx.rs.mu.Unlock()

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
		ctx.rs.cleanupPending(reqID)
		ctx.rc.send(errResult(msg.ID, "failed to forward file transfer: "+err.Error()))
		return
	}
	// For chunked transfers, remember where the follow-up chunks go.
	if msg.Chunked {
		ctx.rc.chunkRoutes[msg.ID] = &chunkRoute{target: target, reqID: reqID}
		// Start async writer on the target so the relay read loop
		// can immediately read the next chunk from the client while
		// the previous one is still being written to the target.
		target.startWriter()
	}
}

// handleFileChunk processes a "file_chunk" message — forwards chunk data to
// the target identified by the chunk route. Handles both binary chunks
// (reads the next frame as binary payload) and legacy base64 chunks.
func (ctx *wsContext) handleFileChunk(msg *Message) {
	route, ok := ctx.rc.chunkRoutes[msg.ID]
	if !ok {
		log.Printf("Dropping file_chunk for unknown transfer id=%s", msg.ID)
		return
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
			return
		}
		// Read the next frame — it must be the binary payload
		binType, binData, err := ctx.conn.ReadMessage()
		if err != nil {
			log.Printf("Read binary chunk payload: %v", err)
			delete(ctx.rc.chunkRoutes, msg.ID)
			ctx.rs.cleanupPending(route.reqID)
			ctx.rc.send(errResult(msg.ID, "failed to read binary chunk payload: "+err.Error()))
			return
		}
		if binType != websocket.BinaryMessage {
			log.Printf("Expected binary frame after chunk header, got text")
			return
		}
		// Forward header+binary atomically through the write queue
		if err := route.target.sendRawHeaderAndBinary(headerBytes, binData); err != nil {
			log.Printf("Forward binary chunk to %s failed: %v", route.target.name, err)
			delete(ctx.rc.chunkRoutes, msg.ID)
			ctx.rs.cleanupPending(route.reqID)
			ctx.rc.send(errResult(msg.ID, "failed to forward file chunk: "+err.Error()))
			return
		}
		if msg.Final {
			delete(ctx.rc.chunkRoutes, msg.ID)
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
			delete(ctx.rc.chunkRoutes, msg.ID)
			ctx.rs.cleanupPending(route.reqID)
			ctx.rc.send(errResult(msg.ID, "failed to forward file chunk: "+err.Error()))
			return
		}
		if msg.Final {
			delete(ctx.rc.chunkRoutes, msg.ID)
		}
	}
}

// handleStreamChunk processes a "stream_chunk" message — forwards streaming
// output chunks from a target back to the requesting client.
func (ctx *wsContext) handleStreamChunk(msg *Message) {
	ctx.rs.mu.RLock()
	pr, ok := ctx.rs.pending[msg.ID]
	ctx.rs.mu.RUnlock()
	if !ok {
		return
	}
	msg.ID = pr.serverID
	pr.clientConn.send(msg)
}

// handleStreamEnd processes a "stream_end" message — forwards the final
// stream output and cleans up the pending request.
func (ctx *wsContext) handleStreamEnd(msg *Message) {
	ctx.rs.mu.Lock()
	pr, ok := ctx.rs.pending[msg.ID]
	if ok {
		delete(ctx.rs.pending, msg.ID)
	}
	ctx.rs.mu.Unlock()
	if !ok {
		return
	}
	msg.ID = pr.serverID
	pr.clientConn.send(msg)
	log.Printf("Stream end relayed for id=%s (ok=%v)", msg.ID, msg.OK)
}

// handleResult processes a "result" message — handles both multi-target
// sub-results and normal single-target results.
func (ctx *wsContext) handleResult(msg *Message) {
	ctx.rs.mu.Lock()

	// Check multi-target sub-result first
	if info, isMulti := ctx.rs.subToMulti[msg.ID]; isMulti {
		delete(ctx.rs.subToMulti, msg.ID)
		multiEntry, hasEntry := ctx.rs.multiPending[info.multiID]
		if !hasEntry {
			ctx.rs.mu.Unlock()
			return
		}
		// Store this result for the target
		multiEntry.results[info.targetName] = msg
		multiEntry.remaining--

		if multiEntry.remaining <= 0 {
			// All results received — send aggregated response
			delete(ctx.rs.multiPending, info.multiID)
			ctx.rs.mu.Unlock()
			// Stop the timeout timer
			if multiEntry.timer != nil {
				multiEntry.timer.Stop()
			}
			ctx.rs.sendMultiResult(multiEntry.clientConn, multiEntry.clientID, multiEntry.results, multiEntry.targetOrder)
		} else {
			ctx.rs.mu.Unlock()
		}
		return
	}

	// Normal single-target result
	pr, ok := ctx.rs.pending[msg.ID]
	if ok {
		delete(ctx.rs.pending, msg.ID)
	}
	ctx.rs.mu.Unlock()
	if !ok {
		return
	}
	msg.ID = pr.serverID
	pr.clientConn.send(msg)
	log.Printf("Result relayed for id=%s (ok=%v)", msg.ID, msg.OK)
}

// handleFileTransferResult processes a "file_transfer_result" message —
// forwards the result and closes the target's async write queue.
func (ctx *wsContext) handleFileTransferResult(msg *Message) {
	ctx.rs.mu.Lock()
	pr, ok := ctx.rs.pending[msg.ID]
	if ok {
		delete(ctx.rs.pending, msg.ID)
	}
	ctx.rs.mu.Unlock()
	if !ok {
		return
	}
	msg.ID = pr.serverID
	msg.Type = "result"
	pr.clientConn.send(msg)
	log.Printf("File transfer result relayed for id=%s (ok=%v)", msg.ID, msg.OK)
	// Close the target's async write queue — all chunks have been
	// received and the daemon has replied.
	ctx.rc.closeWriter()
}

// handlePairListen processes a "pair_listen" message — registers a pairing
// listener waiting for a daemon to connect with the same code.
func (ctx *wsContext) handlePairListen(msg *Message) {
	if msg.Code == "" {
		ctx.rc.send(&Message{Type: "error", Error: "pair_listen requires code"})
		return
	}
	ctx.rs.mu.Lock()
	ctx.rs.pairListeners[msg.Code] = &pairListener{
		conn:                 ctx.rc,
		requireActivationKey: msg.RequireActivationKey,
	}
	ctx.rs.mu.Unlock()
	log.Printf("Pair listener registered for code %s (requireActivationKey=%v)", msg.Code, msg.RequireActivationKey)
}

// handlePair processes a "pair" message — connects a daemon to a waiting
// listener, validating activation keys if required.
func (ctx *wsContext) handlePair(msg *Message) {
	if msg.Code == "" || msg.Token == "" {
		ctx.rc.send(&Message{Type: "error", Error: "pair requires code and token"})
		return
	}
	ctx.rs.mu.Lock()
	listener, ok := ctx.rs.pairListeners[msg.Code]
	if ok {
		delete(ctx.rs.pairListeners, msg.Code)
	}
	ctx.rs.mu.Unlock()
	if !ok {
		log.Printf("Pair code %s not found or already used (daemon will retry)", msg.Code)
		return
	}
	// Validate activation key if the listener requires one
	if listener.requireActivationKey {
		if msg.ActivationKey == "" || !activationKeys.isValid(msg.ActivationKey) {
			log.Printf("Pair code %s rejected: invalid or missing activation key", msg.Code)
			ctx.rc.send(&Message{Type: "error", Error: "pair rejected: activation key required but not provided or invalid"})
			// Re-register the listener so the real peer can still pair
			ctx.rs.mu.Lock()
			ctx.rs.pairListeners[msg.Code] = listener
			ctx.rs.mu.Unlock()
			return
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
	ctx.rc.send(&Message{
		Type: "pair_confirmed",
		Code: msg.Code,
	})
}

// handleDisconnect processes a "disconnect" message — forwards a disconnect
// request to the target daemon.
func (ctx *wsContext) handleDisconnect(msg *Message) {
	if msg.Target == "" {
		ctx.rc.send(&Message{Type: "error", Error: "disconnect requires target"})
		return
	}
	ctx.rs.mu.RLock()
	target, ok := ctx.rs.clients[msg.Target]
	ctx.rs.mu.RUnlock()
	if !ok {
		ctx.rc.send(&Message{Type: "error", Error: "target not connected: " + msg.Target})
		return
	}
	if target.token != msg.Token {
		ctx.rc.send(&Message{Type: "error", Error: "invalid token for target: " + msg.Target})
		return
	}
	// Forward disconnect to the daemon
	if err := target.send(&Message{Type: "disconnect"}); err != nil {
		ctx.rc.send(&Message{Type: "error", Error: "failed to forward disconnect: " + err.Error()})
		return
	}
	log.Printf("Disconnect forwarded to %s", msg.Target)
	ctx.rc.send(&Message{Type: "disconnect_confirmed", Target: msg.Target})
}
