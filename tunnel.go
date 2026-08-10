package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"github.com/gorilla/websocket"
)

// clientTunnel manages a single client-side tunnel: a local TCP listener
// that forwards connections through the relay to a remote address on the
// target daemon.
type clientTunnel struct {
	targetName  string
	remoteAddr  string
	localPort   string
	conn        *websocket.Conn
	writeMu     sync.Mutex
	// pendingOpen tracks tunnels waiting for tunnel_opened confirmation
	pendingOpen sync.Map // tunnelID → chan *Message
	// activeConns tracks local TCP connections by tunnelID
	activeConns sync.Map // tunnelID → net.Conn
}

// runTunnel starts a local TCP listener and forwards every connection
// through the relay to the remote address on the target daemon.
func runTunnel(target, localPort, remoteAddr string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Relay.URL == "" {
		return fmt.Errorf("relay not configured. Run: remotecmd-cli set-relay --url <url> --name <name>")
	}

	tgt, ok := cfg.Targets[target]
	if !ok {
		return fmt.Errorf("unknown target %q", target)
	}

	// Resolve to relay-registered name (alias → relay name)
	relayName := tgt.RelayName
	if relayName == "" {
		relayName = target
	}

	// Connect to relay
	wsURL := wsURL(cfg.Relay.URL)
	conn, _, err := dialRelay(wsURL)
	if err != nil {
		return fmt.Errorf("connect to relay: %w", err)
	}
	defer conn.Close()

	ct := &clientTunnel{
		targetName: relayName,
		remoteAddr: remoteAddr,
		localPort:  localPort,
		conn:       conn,
	}

	// Read loop: handle tunnel_opened, tunnel_data, tunnel_close
	go ct.readLoop()

	// Local TCP listener
	listener, err := net.Listen("tcp", "127.0.0.1:"+localPort)
	if err != nil {
		return fmt.Errorf("listen on :%s: %w", localPort, err)
	}
	defer listener.Close()

	fmt.Printf("Tunnel: 127.0.0.1:%s → %s:%s (via %s)\n", localPort, target, remoteAddr, cfg.Relay.URL)
	fmt.Printf("Waiting for connections... (Ctrl+C to stop)\n")

	for {
		localConn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go ct.handleLocalConnection(localConn, tgt.Token)
	}
}

func (ct *clientTunnel) readLoop() {
	for {
		var msg Message
		if err := ct.conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("Tunnel read error: %v", err)
			}
			return
		}

		switch msg.Type {
		case "tunnel_opened":
			// Notify the waiting goroutine
			if ch, ok := ct.pendingOpen.Load(msg.TunnelID); ok {
				ch.(chan *Message) <- &msg
			}

		case "tunnel_data":
			// Write data to the local TCP connection
			if conn, ok := ct.activeConns.Load(msg.TunnelID); ok {
				data, err := base64.StdEncoding.DecodeString(msg.Data)
				if err != nil {
					log.Printf("Tunnel data decode error: %v", err)
					continue
				}
				if _, err := conn.(net.Conn).Write(data); err != nil {
					log.Printf("Tunnel local write error: %v", err)
					ct.closeTunnel(msg.TunnelID)
				}
			}

		case "tunnel_close":
			ct.closeTunnel(msg.TunnelID)

		case "error":
			log.Printf("Relay error: %s", msg.Error)
		}
	}
}

func (ct *clientTunnel) handleLocalConnection(localConn net.Conn, token string) {
	tunnelID := newID()

	// Wait for tunnel_opened confirmation
	openCh := make(chan *Message, 1)
	ct.pendingOpen.Store(tunnelID, openCh)
	defer ct.pendingOpen.Delete(tunnelID)

	// Send tunnel_open
	ct.send(&Message{
		Type:       "tunnel_open",
		Target:     ct.targetName,
		Token:      token,
		TunnelID:   tunnelID,
		RemoteAddr: ct.remoteAddr,
	})

	// Wait for opened confirmation
	resp := <-openCh
	if resp.Error != "" {
		log.Printf("Tunnel open failed: %s", resp.Error)
		localConn.Close()
		return
	}

	// Register the active connection
	ct.activeConns.Store(tunnelID, localConn)
	defer ct.closeTunnel(tunnelID)

	// Pipe: local TCP → relay → daemon → remote TCP
	buf := make([]byte, 32*1024)
	for {
		n, err := localConn.Read(buf)
		if n > 0 {
			ct.send(&Message{
				Type:     "tunnel_data",
				TunnelID: tunnelID,
				Data:     base64.StdEncoding.EncodeToString(buf[:n]),
			})
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("Tunnel local read error: %v", err)
			}
			ct.send(&Message{Type: "tunnel_close", TunnelID: tunnelID})
			return
		}
	}
}

func (ct *clientTunnel) closeTunnel(tunnelID string) {
	if conn, ok := ct.activeConns.LoadAndDelete(tunnelID); ok {
		conn.(net.Conn).Close()
	}
}

func (ct *clientTunnel) send(msg *Message) {
	ct.writeMu.Lock()
	defer ct.writeMu.Unlock()
	ct.conn.WriteJSON(msg)
}
