package main

import (
	"log"
)

// tunnelSession tracks a single tunnel connection so the relay can route
// bidirectional data between the client and the target daemon.
type tunnelSession struct {
	clientConn  *relayClient
	daemonConn  *relayClient
}

// handleTunnelOpen is called when a client wants to open a tunnel to a
// remote address on a target daemon. It validates the target+token,
// creates a tunnel session, and forwards the request to the daemon.
func (rs *RelayServer) handleTunnelOpen(rc *relayClient, msg *Message) {
	if msg.Target == "" || msg.TunnelID == "" || msg.RemoteAddr == "" {
		rc.send(&Message{Type: "tunnel_opened", TunnelID: msg.TunnelID, Error: "target, tunnel_id, and remote_addr are required"})
		return
	}

	rs.mu.RLock()
	target, ok := rs.clients[msg.Target]
	rs.mu.RUnlock()
	if !ok {
		rc.send(&Message{Type: "tunnel_opened", TunnelID: msg.TunnelID, Error: "target not connected: " + msg.Target})
		return
	}
	if target.token != msg.Token {
		rc.send(&Message{Type: "tunnel_opened", TunnelID: msg.TunnelID, Error: "invalid token for target: " + msg.Target})
		return
	}

	// Register the tunnel session
	rs.mu.Lock()
	rs.tunnels[msg.TunnelID] = &tunnelSession{
		clientConn: rc,
		daemonConn: target,
	}
	rs.mu.Unlock()

	log.Printf("Tunnel open: %s -> %s (tunnel=%s, remote=%s)", rc.name, msg.Target, msg.TunnelID, msg.RemoteAddr)

	// Forward to daemon
	forward := &Message{
		Type:       "tunnel_open",
		TunnelID:   msg.TunnelID,
		RemoteAddr: msg.RemoteAddr,
	}
	if err := target.send(forward); err != nil {
		log.Printf("Tunnel forward to %s failed: %v", msg.Target, err)
		rs.mu.Lock()
		delete(rs.tunnels, msg.TunnelID)
		rs.mu.Unlock()
		rc.send(&Message{Type: "tunnel_opened", TunnelID: msg.TunnelID, Error: "failed to forward tunnel request: " + err.Error()})
	}
}

// handleTunnelRelay routes tunnel_opened and tunnel_data messages to the
// other side of the tunnel. It looks up the session by TunnelID and
// forwards to whichever connection is NOT the sender.
func (rs *RelayServer) handleTunnelRelay(rc *relayClient, msg *Message) {
	rs.mu.RLock()
	ts, ok := rs.tunnels[msg.TunnelID]
	rs.mu.RUnlock()
	if !ok {
		return
	}

	var dest *relayClient
	if ts.clientConn == rc {
		dest = ts.daemonConn
	} else {
		dest = ts.clientConn
	}
	if dest == nil {
		return
	}
	dest.send(msg)
}

// handleTunnelClose cleans up a tunnel session and notifies the other side.
func (rs *RelayServer) handleTunnelClose(rc *relayClient, msg *Message) {
	rs.mu.Lock()
	ts, ok := rs.tunnels[msg.TunnelID]
	if ok {
		delete(rs.tunnels, msg.TunnelID)
	}
	rs.mu.Unlock()
	if !ok {
		return
	}

	var dest *relayClient
	if ts.clientConn == rc {
		dest = ts.daemonConn
	} else {
		dest = ts.clientConn
	}
	if dest != nil {
		dest.send(&Message{Type: "tunnel_close", TunnelID: msg.TunnelID})
	}
	log.Printf("Tunnel closed: %s", msg.TunnelID)
}
