package main

import (
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// flakyProxy forwards TCP to a relay and can drop every connection at once,
// which is what a tunnel client sees when the relay restarts.
type flakyProxy struct {
	t      *testing.T
	addr   string
	target string
	mu     sync.Mutex
	ln     net.Listener
	conns  []net.Conn
}

func newFlakyProxy(t *testing.T, target string) *flakyProxy {
	p := &flakyProxy{t: t, target: target}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.addr = ln.Addr().String()
	p.serve(ln)
	t.Cleanup(func() { p.drop(0) })
	return p
}

func (p *flakyProxy) serve(ln net.Listener) {
	p.mu.Lock()
	p.ln = ln
	p.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", p.target)
			if err != nil {
				c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, up)
			p.mu.Unlock()
			go func() { io.Copy(up, c); up.Close() }()
			go func() { io.Copy(c, up); c.Close() }()
		}
	}()
}

// drop kills all proxied connections and refuses new ones for down,
// then listens again on the same address (down=0: stay down).
func (p *flakyProxy) drop(down time.Duration) {
	p.mu.Lock()
	p.ln.Close()
	for _, c := range p.conns {
		c.Close()
	}
	p.conns = nil
	p.mu.Unlock()
	if down == 0 {
		return
	}
	time.Sleep(down)
	ln, err := net.Listen("tcp", p.addr)
	if err != nil {
		p.t.Fatalf("proxy relisten: %v", err)
	}
	p.serve(ln)
}

// echoDaemon registers as a target and echoes tunnel data back.
func echoDaemon(t *testing.T, relay, name, token string) {
	conn := testDaemon(t, relay, name, token)
	t.Cleanup(func() { conn.Close() })
	go func() {
		for {
			var m Message
			if err := conn.ReadJSON(&m); err != nil {
				return
			}
			switch m.Type {
			case "tunnel_open":
				conn.WriteJSON(&Message{Type: "tunnel_opened", TunnelID: m.TunnelID})
			case "tunnel_data":
				conn.WriteJSON(&Message{Type: "tunnel_data", TunnelID: m.TunnelID, Data: m.Data})
			}
		}
	}()
}

// echoThrough sends msg through the local tunnel port and expects it back.
func echoThrough(port, msg string, timeout time.Duration) error {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, timeout)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(timeout))
	if _, err := c.Write([]byte(msg)); err != nil {
		return err
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil {
		return err
	}
	if string(buf) != msg {
		return fmt.Errorf("echo %q, want %q", buf, msg)
	}
	return nil
}

// A relay restart must not kill the tunnel: in-flight connections close,
// the local port stays bound, and new connections work once it's back.
func TestTunnelReconnectsAfterRelayDrop(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	for _, v := range []*time.Duration{&tunnelReconnectMin, &tunnelReconnectMax, &tunnelWaitForRelay, &tunnelOpenTimeout} {
		old := *v
		defer func(v *time.Duration, old time.Duration) { *v = old }(v, old)
	}
	tunnelReconnectMin, tunnelReconnectMax = 100*time.Millisecond, 400*time.Millisecond
	tunnelWaitForRelay, tunnelOpenTimeout = 3*time.Second, 3*time.Second

	_, relayPort := startTestRelay(t)
	relay := fmt.Sprintf("http://127.0.0.1:%d", relayPort)
	echoDaemon(t, relay, "box", "tok")
	proxy := newFlakyProxy(t, fmt.Sprintf("127.0.0.1:%d", relayPort))
	setRelay("http://"+proxy.addr, "tester")
	addTarget("box", "tok")

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	local := fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	go runTunnel("box", local, "echo:1")
	waitFor(t, "tunnel up", func() bool { return echoThrough(local, "hello", time.Second) == nil })

	// An open connection when the relay goes away is closed, not left hanging.
	held, err := net.Dial("tcp", "127.0.0.1:"+local)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	held.Write([]byte("x"))
	io.ReadFull(held, make([]byte, 1))

	proxy.drop(800 * time.Millisecond)

	held.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := held.Read(make([]byte, 1)); err == nil {
		t.Error("connection in flight during the drop should have been closed")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Error("connection in flight during the drop was left hanging")
	}

	// The local port never went away, and the tunnel recovers by itself.
	waitFor(t, "tunnel back after relay restart", func() bool {
		return echoThrough(local, "after-restart", 2*time.Second) == nil
	})
}

// With the relay gone for good, a new connection fails in bounded time
// instead of hanging forever (the old behaviour).
func TestTunnelFailsFastWhileRelayDown(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	for _, v := range []*time.Duration{&tunnelReconnectMin, &tunnelReconnectMax, &tunnelWaitForRelay, &tunnelOpenTimeout} {
		old := *v
		defer func(v *time.Duration, old time.Duration) { *v = old }(v, old)
	}
	tunnelReconnectMin, tunnelReconnectMax = 100*time.Millisecond, 400*time.Millisecond
	tunnelWaitForRelay, tunnelOpenTimeout = time.Second, time.Second

	_, relayPort := startTestRelay(t)
	relay := fmt.Sprintf("http://127.0.0.1:%d", relayPort)
	echoDaemon(t, relay, "box", "tok")
	proxy := newFlakyProxy(t, fmt.Sprintf("127.0.0.1:%d", relayPort))
	setRelay("http://"+proxy.addr, "tester")
	addTarget("box", "tok")

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	local := fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	go runTunnel("box", local, "echo:1")
	waitFor(t, "tunnel up", func() bool { return echoThrough(local, "hello", time.Second) == nil })

	proxy.drop(0) // relay gone for good
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	if err := echoThrough(local, "nope", 10*time.Second); err == nil {
		t.Fatal("expected failure while the relay is down")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("connection took %v to fail; must not hang", d)
	}
}
