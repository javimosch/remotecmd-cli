package main

import (
	"log"
	"time"
)

// handleExecuteMulti processes an "execute_multi" message — fans out a
// command to multiple targets and aggregates results with a timeout.
func (ctx *wsContext) handleExecuteMulti(msg *Message) {
	if len(msg.Targets) == 0 || msg.Cmd == "" {
		ctx.rc.send(errResult(msg.ID, "targets and cmd are required"))
		return
	}
	if msg.Tokens == nil {
		msg.Tokens = make(map[string]string)
	}

	multiID := newID()
	entry := &multiPendingEntry{
		clientConn:  ctx.rc,
		clientID:    msg.ID,
		results:     make(map[string]*Message),
		targetOrder: msg.Targets,
		remaining:   0,
	}

	ctx.rs.mu.Lock()
	ctx.rs.multiPending[multiID] = entry
	ctx.rs.mu.Unlock()

	log.Printf("Multi-target execute: targets=%v, cmd=%s", msg.Targets, msg.Cmd)

	batchTimeout := msg.Timeout + 5
	if batchTimeout <= 0 {
		batchTimeout = 35
	}

	pendingCount := 0
	ctx.rs.mu.RLock()
	for _, targetName := range msg.Targets {
		tgt, ok := ctx.rs.clients[targetName]
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
		ctx.rs.subToMulti[subID] = &subTargetInfo{
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
			delete(ctx.rs.subToMulti, subID)
			b := false
			entry.results[targetName] = &Message{
				Type:  "result",
				OK:    &b,
				Error: "forward failed: " + err.Error(),
			}
			continue
		}
	}
	ctx.rs.mu.RUnlock()

	entry.remaining = pendingCount

	if pendingCount == 0 {
		ctx.rs.mu.Lock()
		delete(ctx.rs.multiPending, multiID)
		ctx.rs.mu.Unlock()
		ctx.rs.sendMultiResult(ctx.rc, msg.ID, entry.results, entry.targetOrder)
		return
	}

	entry.timer = time.AfterFunc(time.Duration(batchTimeout)*time.Second, func() {
		ctx.rs.mu.Lock()
		e, ok := ctx.rs.multiPending[multiID]
		if !ok {
			ctx.rs.mu.Unlock()
			return
		}
		delete(ctx.rs.multiPending, multiID)
		for subID, info := range ctx.rs.subToMulti {
			if info.multiID == multiID {
				delete(ctx.rs.subToMulti, subID)
			}
		}
		ctx.rs.mu.Unlock()

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
		ctx.rs.sendMultiResult(e.clientConn, e.clientID, e.results, e.targetOrder)
	})
}
