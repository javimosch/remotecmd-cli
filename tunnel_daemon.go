package main

import (
	"encoding/base64"
	"io"
	"log"
	"net"
	"time"
)

// activeTunnels tracks tunnels by ID so we can clean them up on disconnect.
var activeTunnels = make(map[string]net.Conn)

// handleTunnelOpen is called when the daemon receives a tunnel_open message
// from the relay. It dials the remote address, sends tunnel_opened back,
// and pipes data bidirectionally.
func (td *TargetDaemon) handleTunnelOpen(msg *Message) {
	log.Printf("Tunnel open request (tunnel=%s, remote=%s)", msg.TunnelID, msg.RemoteAddr)

	conn, err := net.DialTimeout("tcp", msg.RemoteAddr, 10*time.Second)
	if err != nil {
		log.Printf("Tunnel dial failed (tunnel=%s): %v", msg.TunnelID, err)
		td.send(&Message{Type: "tunnel_opened", TunnelID: msg.TunnelID, Error: "dial failed: " + err.Error()})
		return
	}

	log.Printf("Tunnel connected (tunnel=%s, remote=%s)", msg.TunnelID, msg.RemoteAddr)
	td.send(&Message{Type: "tunnel_opened", TunnelID: msg.TunnelID, OK: boolPtr(true)})

	// Register the tunnel
	td.registerTunnel(msg.TunnelID, conn)

	// Pipe: remote TCP → relay → client
	go td.tunnelReadLoop(msg.TunnelID, conn)

	// Note: the write loop (relay → client → daemon → remote TCP) is driven
	// by tunnel_data messages arriving in the daemon's main read loop,
	// which calls handleTunnelData.
}

// handleTunnelData writes data from the client to the remote TCP connection.
func (td *TargetDaemon) handleTunnelData(msg *Message) {
	conn := td.getTunnel(msg.TunnelID)
	if conn == nil {
		return
	}
	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		log.Printf("Tunnel data decode failed (tunnel=%s): %v", msg.TunnelID, err)
		return
	}
	if _, err := conn.Write(data); err != nil {
		log.Printf("Tunnel write failed (tunnel=%s): %v", msg.TunnelID, err)
		td.closeTunnel(msg.TunnelID)
		td.send(&Message{Type: "tunnel_close", TunnelID: msg.TunnelID})
	}
}

// handleTunnelClose cleans up a tunnel on the daemon side.
func (td *TargetDaemon) handleTunnelClose(msg *Message) {
	td.closeTunnel(msg.TunnelID)
}

// tunnelReadLoop reads from the remote TCP connection and sends tunnel_data
// messages back through the relay to the client.
func (td *TargetDaemon) tunnelReadLoop(tunnelID string, conn net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			td.send(&Message{
				Type:     "tunnel_data",
				TunnelID: tunnelID,
				Data:     base64.StdEncoding.EncodeToString(buf[:n]),
			})
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("Tunnel read error (tunnel=%s): %v", tunnelID, err)
			}
			td.closeTunnel(tunnelID)
			td.send(&Message{Type: "tunnel_close", TunnelID: tunnelID})
			return
		}
	}
}

// registerTunnel stores a tunnel connection for later lookup.
func (td *TargetDaemon) registerTunnel(id string, conn net.Conn) {
	td.writeMu.Lock()
	defer td.writeMu.Unlock()
	activeTunnels[id] = conn
}

// getTunnel retrieves a tunnel connection by ID.
func (td *TargetDaemon) getTunnel(id string) net.Conn {
	td.writeMu.Lock()
	defer td.writeMu.Unlock()
	return activeTunnels[id]
}

// closeTunnel closes and removes a tunnel connection.
func (td *TargetDaemon) closeTunnel(id string) {
	td.writeMu.Lock()
	defer td.writeMu.Unlock()
	if conn, ok := activeTunnels[id]; ok {
		conn.Close()
		delete(activeTunnels, id)
	}
}
