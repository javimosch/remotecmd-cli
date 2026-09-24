package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// clientTunnel manages a single client-side tunnel: a local TCP listener
// that forwards connections through the relay to a remote address on the
// target daemon.
//
// The relay connection is replaceable. When it drops (relay restart, network
// blip, a half-dead link caught by the keepalive) the tunnel reconnects with
// backoff while the local port stays bound: connections in flight are closed
// (they cannot survive the relay), new ones wait briefly for the relay to
// come back. Before this, a relay restart left every tunnel listening but
// dead — new connections hung forever waiting for tunnel_opened.
type clientTunnel struct {
	targetName string
	remoteAddr string
	localPort  string
	relayURL   string
	token      string

	mu      sync.Mutex
	conn    *websocket.Conn // nil while reconnecting
	writeMu sync.Mutex
	// pendingOpen tracks tunnels waiting for tunnel_opened confirmation
	pendingOpen sync.Map // tunnelID → chan *Message
	// activeConns tracks local TCP connections by tunnelID
	activeConns sync.Map // tunnelID → net.Conn
}

// Tunnel timing; vars so tests can shorten them.
var (
	tunnelReconnectMin = 1 * time.Second
	tunnelReconnectMax = 30 * time.Second
	tunnelOpenTimeout  = 30 * time.Second // relay/daemon must answer tunnel_open
	tunnelWaitForRelay = 10 * time.Second // a new local connection waits this long for a reconnect
	tunnelPingPeriod   = 30 * time.Second
	tunnelPongWait     = 60 * time.Second
)

var errRelayDown = errors.New("relay connection down")

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

	ct := &clientTunnel{
		targetName: relayName,
		remoteAddr: remoteAddr,
		localPort:  localPort,
		relayURL:   wsURL(cfg.Relay.URL),
		token:      tgt.Token,
	}

	// The first connection is synchronous so a wrong relay URL fails fast.
	conn, _, err := dialRelay(ct.relayURL)
	if err != nil {
		return fmt.Errorf("connect to relay: %w", err)
	}
	ct.setConn(conn)

	// Local TCP listener
	listener, err := net.Listen("tcp", "127.0.0.1:"+localPort)
	if err != nil {
		conn.Close()
		return fmt.Errorf("listen on :%s: %w", localPort, err)
	}
	defer listener.Close()

	go ct.maintain(conn)

	fmt.Printf("Tunnel: 127.0.0.1:%s → %s:%s (via %s)\n", localPort, target, remoteAddr, cfg.Relay.URL)
	fmt.Printf("Waiting for connections... (Ctrl+C to stop)\n")

	for {
		localConn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go ct.handleLocalConnection(localConn)
	}
}

// maintain serves conn until it drops, then reconnects forever with backoff.
func (ct *clientTunnel) maintain(conn *websocket.Conn) {
	for {
		ct.serve(conn)
		ct.dropConn(conn)
		backoff := tunnelReconnectMin
		for {
			log.Printf("Tunnel: relay connection lost; reconnecting in %v", backoff)
			time.Sleep(backoff)
			c, _, err := dialRelay(ct.relayURL)
			if err == nil {
				conn = c
				ct.setConn(conn)
				log.Printf("Tunnel: reconnected to relay")
				break
			}
			log.Printf("Tunnel: reconnect failed: %v", err)
			if backoff *= 2; backoff > tunnelReconnectMax {
				backoff = tunnelReconnectMax
			}
		}
	}
}

// serve runs the keepalive and the read loop on conn until it fails.
func (ct *clientTunnel) serve(conn *websocket.Conn) {
	conn.SetReadDeadline(time.Now().Add(tunnelPongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(tunnelPongWait))
		return nil
	})
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(tunnelPingPeriod)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
					return
				}
			case <-stop:
				return
			}
		}
	}()
	ct.readLoop(conn)
}

func (ct *clientTunnel) setConn(conn *websocket.Conn) {
	ct.mu.Lock()
	ct.conn = conn
	ct.mu.Unlock()
}

func (ct *clientTunnel) currentConn() *websocket.Conn {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.conn
}

// dropConn retires conn: local connections riding on it are closed and
// pending opens fail, since the relay forgot them with the connection.
func (ct *clientTunnel) dropConn(conn *websocket.Conn) {
	ct.mu.Lock()
	if ct.conn == conn {
		ct.conn = nil
	}
	ct.mu.Unlock()
	conn.Close()
	ct.activeConns.Range(func(id, c any) bool {
		c.(net.Conn).Close()
		ct.activeConns.Delete(id)
		return true
	})
	ct.pendingOpen.Range(func(_, ch any) bool {
		select {
		case ch.(chan *Message) <- &Message{Error: "relay connection lost"}:
		default:
		}
		return true
	})
}

// waitConn returns a live relay connection, waiting up to d for a reconnect.
func (ct *clientTunnel) waitConn(d time.Duration) *websocket.Conn {
	deadline := time.Now().Add(d)
	for {
		if c := ct.currentConn(); c != nil {
			return c
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (ct *clientTunnel) readLoop(conn *websocket.Conn) {
	for {
		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			log.Printf("Tunnel read error: %v", err)
			return
		}
		// Any message proves the link is alive.
		conn.SetReadDeadline(time.Now().Add(tunnelPongWait))

		switch msg.Type {
		case "tunnel_opened":
			// Notify the waiting goroutine
			if ch, ok := ct.pendingOpen.Load(msg.TunnelID); ok {
				select {
				case ch.(chan *Message) <- &msg:
				default:
				}
			}

		case "tunnel_data":
			// Write data to the local TCP connection
			if c, ok := ct.activeConns.Load(msg.TunnelID); ok {
				data, err := base64.StdEncoding.DecodeString(msg.Data)
				if err != nil {
					log.Printf("Tunnel data decode error: %v", err)
					continue
				}
				if _, err := c.(net.Conn).Write(data); err != nil {
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

func (ct *clientTunnel) handleLocalConnection(localConn net.Conn) {
	// A connection arriving during a reconnect waits for the relay briefly
	// rather than failing at once or hanging forever.
	conn := ct.waitConn(tunnelWaitForRelay)
	if conn == nil {
		log.Printf("Tunnel open failed: relay unreachable")
		localConn.Close()
		return
	}
	tunnelID := newID()

	// Wait for tunnel_opened confirmation
	openCh := make(chan *Message, 1)
	ct.pendingOpen.Store(tunnelID, openCh)
	defer ct.pendingOpen.Delete(tunnelID)

	if err := ct.sendOn(conn, &Message{
		Type:       "tunnel_open",
		Target:     ct.targetName,
		Token:      ct.token,
		TunnelID:   tunnelID,
		RemoteAddr: ct.remoteAddr,
	}); err != nil {
		log.Printf("Tunnel open failed: %v", err)
		localConn.Close()
		return
	}

	var resp *Message
	select {
	case resp = <-openCh:
	case <-time.After(tunnelOpenTimeout):
		resp = &Message{Error: "no answer from relay"}
	}
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
			if serr := ct.sendOn(conn, &Message{
				Type:     "tunnel_data",
				TunnelID: tunnelID,
				Data:     base64.StdEncoding.EncodeToString(buf[:n]),
			}); serr != nil {
				return // relay gone: dropConn closes localConn
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("Tunnel local read error: %v", err)
			}
			ct.sendOn(conn, &Message{Type: "tunnel_close", TunnelID: tunnelID})
			return
		}
	}
}

func (ct *clientTunnel) closeTunnel(tunnelID string) {
	if c, ok := ct.activeConns.LoadAndDelete(tunnelID); ok {
		c.(net.Conn).Close()
	}
}

// sendOn writes msg on conn, the relay connection this local connection was
// opened on; a replaced connection must not carry data for the old one.
func (ct *clientTunnel) sendOn(conn *websocket.Conn, msg *Message) error {
	if ct.currentConn() != conn {
		return errRelayDown
	}
	ct.writeMu.Lock()
	defer ct.writeMu.Unlock()
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteJSON(msg)
}
