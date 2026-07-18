package main

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialClient opens a raw client connection to a test relay.
func dialClient(t *testing.T, port int) *websocket.Conn {
	t.Helper()
	u := wsURL(fmt.Sprintf("http://127.0.0.1:%d", port))
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	return conn
}

// readMsg reads one message with a deadline so a hung relay fails fast.
func readMsg(t *testing.T, conn *websocket.Conn) Message {
	t.Helper()
	conn.SetReadDeadline(timeNowPlus(5 * time.Second))
	var msg Message
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	conn.SetReadDeadline(zeroTime())
	return msg
}

func timeNowPlus(d time.Duration) time.Time { return time.Now().Add(d) }
func zeroTime() time.Time                   { return time.Time{} }

// --- tokenEqual (auth primitive) ---------------------------------------------

func TestTokenEqual(t *testing.T) {
	cases := []struct {
		expected, presented string
		want                bool
	}{
		{"secret", "secret", true},
		{"secret", "Secret", false},
		{"secret", "secre", false},
		{"secret", "secrett", false},
		{"secret", "", false},
		{"", "secret", false},
		{"", "", false}, // empty expected never matches — targets must have a token
	}
	for _, c := range cases {
		if got := tokenEqual(c.expected, c.presented); got != c.want {
			t.Errorf("tokenEqual(%q,%q)=%v want %v", c.expected, c.presented, got, c.want)
		}
	}
}

// --- register auth -----------------------------------------------------------

func TestRegisterRequiresNameAndToken(t *testing.T) {
	_, port := startTestRelay(t)

	cases := []Message{
		{Type: "register", Name: "", Token: "tok"},
		{Type: "register", Name: "node", Token: ""},
		{Type: "register", Name: "", Token: ""},
	}
	for _, reg := range cases {
		conn := dialClient(t, port)
		conn.WriteJSON(&reg)
		resp := readMsg(t, conn)
		if resp.Type != "error" {
			t.Errorf("register %+v: expected error, got %s", reg, resp.Type)
		}
		if resp.Error != "name and token required" {
			t.Errorf("register %+v: unexpected error %q", reg, resp.Error)
		}
		conn.Close()
	}
}

// --- execute auth / routing --------------------------------------------------

func TestExecuteTargetNotConnected(t *testing.T) {
	_, port := startTestRelay(t)
	client := dialClient(t, port)
	defer client.Close()

	id := newID()
	client.WriteJSON(&Message{Type: "execute", ID: id, Target: "ghost", Token: "t", Cmd: "x", Timeout: 5})
	resp := readMsg(t, client)
	if resp.OK == nil || *resp.OK {
		t.Fatal("expected failure result")
	}
	if resp.Error != "target not connected: ghost" {
		t.Errorf("unexpected error: %q", resp.Error)
	}
}

func TestExecuteInvalidToken(t *testing.T) {
	rs, port := startTestRelay(t)
	_ = rs
	daemon := testDaemon(t, fmt.Sprintf("http://127.0.0.1:%d", port), "box", "correct-token")
	defer daemon.Close()
	testDaemonResponder(t, daemon, 0, "should-not-run")
	time.Sleep(50 * time.Millisecond)

	client := dialClient(t, port)
	defer client.Close()

	id := newID()
	client.WriteJSON(&Message{Type: "execute", ID: id, Target: "box", Token: "wrong-token", Cmd: "whoami", Timeout: 5})
	resp := readMsg(t, client)
	if resp.OK == nil || *resp.OK {
		t.Fatal("expected failure result for invalid token")
	}
	if resp.Error != "invalid token for target: box" {
		t.Errorf("unexpected error: %q", resp.Error)
	}
}

func TestExecuteMissingTarget(t *testing.T) {
	_, port := startTestRelay(t)
	client := dialClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{Type: "execute", ID: newID(), Cmd: "x", Timeout: 5})
	resp := readMsg(t, client)
	if resp.Error != "target is required" {
		t.Errorf("unexpected error: %q", resp.Error)
	}
}

// --- file_transfer auth ------------------------------------------------------

func TestFileTransferInvalidToken(t *testing.T) {
	_, port := startTestRelay(t)
	daemon := testDaemon(t, fmt.Sprintf("http://127.0.0.1:%d", port), "fbox", "real")
	defer daemon.Close()
	time.Sleep(50 * time.Millisecond)

	client := dialClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{Type: "file_transfer", ID: newID(), Target: "fbox", Token: "fake", Mode: "file", SrcPath: "a", DstPath: "b"})
	resp := readMsg(t, client)
	if resp.OK == nil || *resp.OK {
		t.Fatal("expected failure for invalid file_transfer token")
	}
	if resp.Error != "invalid token for target: fbox" {
		t.Errorf("unexpected error: %q", resp.Error)
	}
}

func TestFileTransferTargetNotConnected(t *testing.T) {
	_, port := startTestRelay(t)
	client := dialClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{Type: "file_transfer", ID: newID(), Target: "nope", Token: "t", Mode: "file"})
	resp := readMsg(t, client)
	if resp.Error != "target not connected: nope" {
		t.Errorf("unexpected error: %q", resp.Error)
	}
}

// --- unknown message type ----------------------------------------------------

func TestUnknownMessageType(t *testing.T) {
	_, port := startTestRelay(t)
	client := dialClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{Type: "frobnicate"})
	resp := readMsg(t, client)
	if resp.Type != "error" || resp.Error != "unknown message type: frobnicate" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// --- multi-target auth / routing --------------------------------------------

func TestMultiExecMissingArgs(t *testing.T) {
	_, port := startTestRelay(t)
	client := dialClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{Type: "execute_multi", ID: newID(), Targets: nil, Cmd: "x"})
	resp := readMsg(t, client)
	if resp.Error != "targets and cmd are required" {
		t.Errorf("unexpected error: %q", resp.Error)
	}
}

// TestMultiExecMixedAuth verifies each target is authenticated independently:
// a connected+valid target runs, a bad-token target and an absent target each
// fail with their own error, and every requested target appears in the result.
func TestMultiExecMixedAuth(t *testing.T) {
	_, port := startTestRelay(t)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	good := testDaemon(t, url, "good", "gtok")
	defer good.Close()
	testDaemonResponder(t, good, 0, "ran-ok")

	bad := testDaemon(t, url, "bad", "realtok")
	defer bad.Close()
	testDaemonResponder(t, bad, 0, "should-not-run")

	time.Sleep(50 * time.Millisecond)

	client := dialClient(t, port)
	defer client.Close()

	id := newID()
	client.WriteJSON(&Message{
		Type:    "execute_multi",
		ID:      id,
		Targets: []string{"good", "bad", "absent"},
		Tokens:  map[string]string{"good": "gtok", "bad": "wrongtok"},
		Cmd:     "hostname",
		Timeout: 5,
	})

	resp := readMsg(t, client)
	if resp.Type != "multi_result" {
		t.Fatalf("expected multi_result, got %s", resp.Type)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 results, got %d: %+v", len(resp.Results), resp.Results)
	}
	if r := resp.Results["good"]; r == nil || r.OK == nil || !*r.OK {
		t.Errorf("good target should succeed: %+v", r)
	}
	if r := resp.Results["bad"]; r == nil || r.Error != "invalid token" {
		t.Errorf("bad target should report invalid token: %+v", r)
	}
	if r := resp.Results["absent"]; r == nil || r.Error != "target not connected" {
		t.Errorf("absent target should report not connected: %+v", r)
	}
}

// TestMultiExecConcurrentNoRace hammers execute_multi from many clients against
// shared targets. Before the routing fix the fan-out mutated the shared
// subToMulti map under a read lock, so concurrent batches could trigger a
// concurrent-map-write panic (caught here by `go test -race`). It also guards
// the "remaining set before unlock" fix: every batch must return all results.
func TestMultiExecConcurrentNoRace(t *testing.T) {
	_, port := startTestRelay(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	for _, name := range []string{"n1", "n2", "n3"} {
		d := testDaemon(t, base, name, name+"-tok")
		defer d.Close()
		testDaemonResponder(t, d, 0, name+"-out")
	}
	time.Sleep(50 * time.Millisecond)

	const clients = 12
	var wg sync.WaitGroup
	errs := make(chan error, clients)
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := dialClient(t, port)
			defer c.Close()
			id := newID()
			c.WriteJSON(&Message{
				Type:    "execute_multi",
				ID:      id,
				Targets: []string{"n1", "n2", "n3"},
				Tokens:  map[string]string{"n1": "n1-tok", "n2": "n2-tok", "n3": "n3-tok"},
				Cmd:     "hostname",
				Timeout: 5,
			})
			c.SetReadDeadline(timeNowPlus(8 * time.Second))
			var resp Message
			if err := c.ReadJSON(&resp); err != nil {
				errs <- fmt.Errorf("read: %w", err)
				return
			}
			if resp.Type != "multi_result" || resp.ID != id {
				errs <- fmt.Errorf("bad response type=%s id=%s", resp.Type, resp.ID)
				return
			}
			if len(resp.Results) != 3 {
				errs <- fmt.Errorf("expected 3 results, got %d", len(resp.Results))
				return
			}
			for _, n := range []string{"n1", "n2", "n3"} {
				r := resp.Results[n]
				if r == nil || r.OK == nil || !*r.OK {
					errs <- fmt.Errorf("target %s missing/failed: %+v", n, r)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
