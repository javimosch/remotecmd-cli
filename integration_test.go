package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// startTestRelay creates a RelayServer on a random port and returns it.
func startTestRelay(t *testing.T) (*RelayServer, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	port := addr.Port
	ln.Close()

	rs := NewRelayServer()
	go func() {
		if err := rs.Serve(port); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Logf("relay serve error: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)
	return rs, port
}

// testDaemon connects to a relay as a target daemon and registers.
func testDaemon(t *testing.T, relayURL string, name, token string) *websocket.Conn {
	t.Helper()
	u := wsURL(relayURL)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("daemon dial: %v", err)
	}

	// Register
	conn.WriteJSON(&Message{Type: "register", Name: name, Token: token})

	// Wait for registered response
	var resp Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("daemon read registered: %v", err)
	}
	if resp.Type != "registered" {
		t.Fatalf("expected registered, got %s", resp.Type)
	}
	return conn
}

// testDaemonResponder reads commands from the daemon connection and sends back results.
func testDaemonResponder(t *testing.T, conn *websocket.Conn, exitCode int, stdout string) {
	t.Helper()
	go func() {
		for {
			var msg Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "command" {
				conn.WriteJSON(&Message{
					Type:       "result",
					ID:         msg.ID,
					OK:         boolPtr(exitCode == 0),
					Stdout:     stdout,
					ExitCode:   exitCode,
					DurationMs: 1,
				})
			}
		}
	}()
}

func TestIntegrationSingleExec(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	// Start daemon
	daemon := testDaemon(t, "http://127.0.0.1:"+fmt.Sprintf("%d", port), "testbox", "tok123")
	defer daemon.Close()
	testDaemonResponder(t, daemon, 0, "hello-world")

	time.Sleep(50 * time.Millisecond)

	// Client connects and sends execute
	u := wsURL("http://127.0.0.1:" + fmt.Sprintf("%d", port))
	client, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer client.Close()

	id := newID()
	client.WriteJSON(&Message{
		Type:    "execute",
		ID:      id,
		Target:  "testbox",
		Token:   "tok123",
		Cmd:     "echo hello",
		Timeout: 5,
	})

	var result Message
	if err := client.ReadJSON(&result); err != nil {
		t.Fatalf("client read: %v", err)
	}

	if result.Type != "result" {
		t.Errorf("expected result, got %s", result.Type)
	}
	if result.OK == nil || !*result.OK {
		t.Error("expected OK=true")
	}
	if result.Stdout != "hello-world" {
		t.Errorf("Stdout = %q, want %q", result.Stdout, "hello-world")
	}
}

func TestIntegrationUnknownTarget(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	u := wsURL("http://127.0.0.1:" + fmt.Sprintf("%d", port))
	client, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer client.Close()

	client.WriteJSON(&Message{
		Type:   "execute",
		ID:     newID(),
		Target: "nonexistent",
		Token:  "tok",
		Cmd:    "echo hi",
	})

	var result Message
	if err := client.ReadJSON(&result); err != nil {
		t.Fatalf("client read: %v", err)
	}

	if result.OK != nil && *result.OK {
		t.Error("expected OK=false for unknown target")
	}
	if !strings.Contains(result.Error, "not connected") {
		t.Errorf("expected 'not connected' error, got %q", result.Error)
	}
}

func TestIntegrationInvalidToken(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+fmt.Sprintf("%d", port), "testbox", "correct-token")
	defer daemon.Close()

	u := wsURL("http://127.0.0.1:" + fmt.Sprintf("%d", port))
	client, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer client.Close()

	client.WriteJSON(&Message{
		Type:   "execute",
		ID:     newID(),
		Target: "testbox",
		Token:  "wrong-token",
		Cmd:    "echo hi",
	})

	var result Message
	if err := client.ReadJSON(&result); err != nil {
		t.Fatalf("client read: %v", err)
	}

	if result.OK != nil && *result.OK {
		t.Error("expected OK=false for wrong token")
	}
	if !strings.Contains(result.Error, "invalid token") {
		t.Errorf("expected 'invalid token' error, got %q", result.Error)
	}
}

func TestIntegrationMultiTarget(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	// Start 2 daemons
	d1 := testDaemon(t, "http://127.0.0.1:"+fmt.Sprintf("%d", port), "node-a", "ta")
	defer d1.Close()
	testDaemonResponder(t, d1, 0, "host-a")

	d2 := testDaemon(t, "http://127.0.0.1:"+fmt.Sprintf("%d", port), "node-b", "tb")
	defer d2.Close()
	testDaemonResponder(t, d2, 0, "host-b")

	time.Sleep(50 * time.Millisecond)

	u := wsURL("http://127.0.0.1:" + fmt.Sprintf("%d", port))
	client, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer client.Close()

	id := newID()
	client.WriteJSON(&Message{
		Type:    "execute_multi",
		ID:      id,
		Targets: []string{"node-a", "node-b"},
		Tokens:  map[string]string{"node-a": "ta", "node-b": "tb"},
		Cmd:     "hostname",
		Timeout: 5,
	})

	var result Message
	if err := client.ReadJSON(&result); err != nil {
		t.Fatalf("client read: %v", err)
	}

	if result.Type != "multi_result" {
		t.Errorf("expected multi_result, got %s", result.Type)
	}
	if result.Results == nil {
		t.Fatal("expected Results map")
	}
	if result.Results["node-a"] == nil || result.Results["node-a"].Stdout != "host-a" {
		t.Errorf("node-a result: %+v", result.Results["node-a"])
	}
	if result.Results["node-b"] == nil || result.Results["node-b"].Stdout != "host-b" {
		t.Errorf("node-b result: %+v", result.Results["node-b"])
	}
}

func TestIntegrationMultiTargetPartialFailure(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	// Only one daemon connected
	d1 := testDaemon(t, "http://127.0.0.1:"+fmt.Sprintf("%d", port), "node-a", "ta")
	defer d1.Close()
	testDaemonResponder(t, d1, 0, "host-a")

	time.Sleep(50 * time.Millisecond)

	u := wsURL("http://127.0.0.1:" + fmt.Sprintf("%d", port))
	client, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer client.Close()

	id := newID()
	client.WriteJSON(&Message{
		Type:    "execute_multi",
		ID:      id,
		Targets: []string{"node-a", "node-b"},
		Tokens:  map[string]string{"node-a": "ta", "node-b": "tb"},
		Cmd:     "hostname",
		Timeout: 5,
	})

	var result Message
	if err := client.ReadJSON(&result); err != nil {
		t.Fatalf("client read: %v", err)
	}

	if result.Type != "multi_result" {
		t.Errorf("expected multi_result, got %s", result.Type)
	}
	if result.Results["node-a"] == nil || !*result.Results["node-a"].OK {
		t.Error("node-a should have succeeded")
	}
	if result.Results["node-b"] == nil || *result.Results["node-b"].OK {
		t.Error("node-b should have failed")
	}
	if !strings.Contains(result.Results["node-b"].Error, "not connected") {
		t.Errorf("node-b error should mention 'not connected', got %q", result.Results["node-b"].Error)
	}
}

func TestIntegrationRelayHealth(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	// Direct HTTP health check
	body, err := httpGet("http://127.0.0.1:" + fmt.Sprintf("%d", port) + "/health")
	if err != nil {
		t.Fatalf("health check: %v", err)
	}

	var health map[string]string
	if err := json.Unmarshal([]byte(body), &health); err != nil {
		t.Fatalf("health JSON: %v", err)
	}
	if health["status"] != "healthy" {
		t.Errorf("health status = %q, want healthy", health["status"])
	}
}

func TestIntegrationDaemonNonZeroExit(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+fmt.Sprintf("%d", port), "testbox", "tok")
	defer daemon.Close()
	testDaemonResponder(t, daemon, 1, "error-msg")

	time.Sleep(50 * time.Millisecond)

	u := wsURL("http://127.0.0.1:" + fmt.Sprintf("%d", port))
	client, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer client.Close()

	client.WriteJSON(&Message{
		Type:   "execute",
		ID:     newID(),
		Target: "testbox",
		Token:  "tok",
		Cmd:    "false",
	})

	var result Message
	if err := client.ReadJSON(&result); err != nil {
		t.Fatalf("client read: %v", err)
	}

	if result.OK == nil || *result.OK {
		t.Error("expected OK=false for non-zero exit")
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
}

func TestIntegrationRegisterReplacesOld(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	// First daemon connects
	d1 := testDaemon(t, "http://127.0.0.1:"+fmt.Sprintf("%d", port), "testbox", "tok1")
	defer d1.Close()

	// Second daemon connects with same name but different token
	d2 := testDaemon(t, "http://127.0.0.1:"+fmt.Sprintf("%d", port), "testbox", "tok2")
	defer d2.Close()

	time.Sleep(50 * time.Millisecond)

	// Client tries to execute with old token — should fail
	u := wsURL("http://127.0.0.1:" + fmt.Sprintf("%d", port))
	client, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer client.Close()

	client.WriteJSON(&Message{
		Type:   "execute",
		ID:     newID(),
		Target: "testbox",
		Token:  "tok1",
		Cmd:    "echo hi",
	})

	var result Message
	client.ReadJSON(&result)
	if result.OK != nil && *result.OK {
		t.Error("expected failure with old token")
	}
}

// httpGet is a helper for HTTP GET requests in tests.
func httpGet(url string) (string, error) {
	resp, err := http.DefaultClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var buf [4096]byte
	n, _ := resp.Body.Read(buf[:])
	return string(buf[:n]), nil
}
