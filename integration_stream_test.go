package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestIntegrationStreamingExec(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "stream-box", "stok")
	defer daemon.Close()

	// Daemon that sends stream_chunks then stream_end
	go func() {
		for {
			var msg Message
			if err := daemon.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "command" && msg.Stream {
				daemon.WriteJSON(&Message{Type: "stream_chunk", ID: msg.ID, StreamName: "stdout", Data: "line1\n"})
				daemon.WriteJSON(&Message{Type: "stream_chunk", ID: msg.ID, StreamName: "stdout", Data: "line2\n"})
				daemon.WriteJSON(&Message{Type: "stream_chunk", ID: msg.ID, StreamName: "stderr", Data: "err-line\n"})
				daemon.WriteJSON(&Message{Type: "stream_end", ID: msg.ID, OK: boolPtr(true), ExitCode: 0, DurationMs: 5})
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	client := testClient(t, port)
	defer client.Close()

	id := newID()
	client.WriteJSON(&Message{
		Type: "execute", ID: id, Target: "stream-box", Token: "stok",
		Cmd: "echo test", Timeout: 5, Stream: true,
	})

	// Read stream_chunks
	chunks := 0
	for chunks < 3 {
		var msg Message
		if err := client.ReadJSON(&msg); err != nil {
			t.Fatalf("read chunk: %v", err)
		}
		if msg.Type == "stream_chunk" {
			chunks++
			switch chunks {
			case 1:
				if msg.StreamName != "stdout" || msg.Data != "line1\n" {
					t.Errorf("chunk1: %+v", msg)
				}
			case 2:
				if msg.StreamName != "stdout" || msg.Data != "line2\n" {
					t.Errorf("chunk2: %+v", msg)
				}
			case 3:
				if msg.StreamName != "stderr" || msg.Data != "err-line\n" {
					t.Errorf("chunk3: %+v", msg)
				}
			}
		}
		if msg.Type == "stream_end" {
			break
		}
	}

	if chunks != 3 {
		t.Errorf("expected 3 chunks, got %d", chunks)
	}
}

func TestIntegrationStreamingNoChunks(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "empty-box", "eok")
	defer daemon.Close()

	go func() {
		for {
			var msg Message
			if err := daemon.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "command" && msg.Stream {
				daemon.WriteJSON(&Message{Type: "stream_end", ID: msg.ID, OK: boolPtr(true), ExitCode: 0, DurationMs: 1})
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	client := testClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{
		Type: "execute", ID: newID(), Target: "empty-box", Token: "eok",
		Cmd: "echo test", Timeout: 5, Stream: true,
	})

	var result Message
	if err := client.ReadJSON(&result); err != nil {
		t.Fatalf("read: %v", err)
	}
	if result.Type != "stream_end" {
		t.Errorf("expected stream_end, got %s", result.Type)
	}
	if result.OK == nil || !*result.OK {
		t.Error("expected OK=true")
	}
}

func TestIntegrationFileTransferViaRelay(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "file-box", "ftok")
	defer daemon.Close()

	go func() {
		for {
			var msg Message
			if err := daemon.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "file_transfer" {
				if msg.Mode == "scp" && strings.Contains(msg.Content, "base64data") {
					daemon.WriteJSON(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(true)})
				} else {
					daemon.WriteJSON(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: "unknown mode"})
				}
			}
			if msg.Type == "command" {
				daemon.WriteJSON(&Message{Type: "result", ID: msg.ID, OK: boolPtr(true), Stdout: "ok", ExitCode: 0, DurationMs: 1})
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	client := testClient(t, port)
	defer client.Close()

	id := newID()
	client.WriteJSON(&Message{
		Type: "file_transfer", ID: id, Target: "file-box", Token: "ftok",
		Mode: "scp", SrcPath: "/src/file", DstPath: "/dst/file",
		Content: "base64data",
	})

	var result Message
	if err := client.ReadJSON(&result); err != nil {
		t.Fatalf("read: %v", err)
	}
	if result.Type != "result" {
		t.Errorf("expected result type (converted by relay), got %s", result.Type)
	}
	if result.OK == nil || !*result.OK {
		t.Error("expected OK=true")
	}
}

func TestIntegrationFileTransferUnknownMode(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "file-box", "ftok")
	defer daemon.Close()

	go func() {
		for {
			var msg Message
			if err := daemon.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "file_transfer" {
				daemon.WriteJSON(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: "unknown mode: " + msg.Mode})
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	client := testClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{
		Type: "file_transfer", ID: newID(), Target: "file-box", Token: "ftok",
		Mode: "invalid", SrcPath: "/s", DstPath: "/d",
		Content: "data",
	})

	var result Message
	if err := client.ReadJSON(&result); err != nil {
		t.Fatalf("read: %v", err)
	}
	if result.OK != nil && *result.OK {
		t.Error("expected OK=false")
	}
}

func TestIntegrationMultipleSequentialExec(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	callCount := 0
	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "seq-box", "stok")
	defer daemon.Close()

	go func() {
		for {
			var msg Message
			if err := daemon.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "command" {
				callCount++
				daemon.WriteJSON(&Message{
					Type: "result", ID: msg.ID, OK: boolPtr(true),
					Stdout: "call-" + itoa(callCount), ExitCode: 0, DurationMs: 1,
				})
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	client := testClient(t, port)
	defer client.Close()

	for i := 1; i <= 3; i++ {
		client.WriteJSON(&Message{
			Type: "execute", ID: newID(), Target: "seq-box", Token: "stok",
			Cmd: "echo test", Timeout: 5,
		})
		var result Message
		if err := client.ReadJSON(&result); err != nil {
			t.Fatalf("read #%d: %v", i, err)
		}
		if result.Stdout != "call-" + itoa(i) {
			t.Errorf("#%d: stdout = %q", i, result.Stdout)
		}
	}
}

func TestIntegrationPairListenNoPeer(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	client := testClient(t, port)
	defer client.Close()

	// Send pair_listen
	client.WriteJSON(&Message{
		Type: "pair_listen", Code: "testcode123",
	})

	// Should get no immediate response — listener is waiting for a peer
	// Instead, test that registering with the same code works
	client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var msg Message
	err := client.ReadJSON(&msg)
	if err == nil {
		t.Errorf("expected timeout (no peer), got %+v", msg)
	}
	client.SetReadDeadline(time.Time{})
}

func TestIntegrationPairFullFlow(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	// Listener (client A)
	listener := testClient(t, port)
	defer listener.Close()

	listener.WriteJSON(&Message{
		Type: "pair_listen", Code: "pair-me",
	})

	time.Sleep(50 * time.Millisecond)

	// Peer daemon (client B)
	peer := testClient(t, port)
	defer peer.Close()

	peer.WriteJSON(&Message{
		Type: "pair", Code: "pair-me", Token: "peer-token", Hostname: "peer-host",
	})

	// Listener should get pair_done
	var pairDone Message
	if err := listener.ReadJSON(&pairDone); err != nil {
		t.Fatalf("listener read: %v", err)
	}
	if pairDone.Type != "pair_done" {
		t.Errorf("expected pair_done, got %s", pairDone.Type)
	}
	if pairDone.Code != "pair-me" {
		t.Errorf("code = %q", pairDone.Code)
	}
	if pairDone.Token != "peer-token" {
		t.Errorf("token = %q", pairDone.Token)
	}
	if pairDone.Hostname != "peer-host" {
		t.Errorf("hostname = %q", pairDone.Hostname)
	}

	// Peer should get pair_confirmed
	var confirmed Message
	if err := peer.ReadJSON(&confirmed); err != nil {
		t.Fatalf("peer read: %v", err)
	}
	if confirmed.Type != "pair_confirmed" {
		t.Errorf("expected pair_confirmed, got %s", confirmed.Type)
	}
}

func TestIntegrationRegisterMissingFields(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	client := testClient(t, port)
	defer client.Close()

	// Register with empty name
	client.WriteJSON(&Message{Type: "register", Name: "", Token: "tok"})

	var resp Message
	if err := client.ReadJSON(&resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Type != "error" {
		t.Errorf("expected error, got %s", resp.Type)
	}

	// Register with empty token
	client2 := testClient(t, port)
	defer client2.Close()
	client2.WriteJSON(&Message{Type: "register", Name: "x", Token: ""})
	if err := client2.ReadJSON(&resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Type != "error" {
		t.Errorf("expected error, got %s", resp.Type)
	}
}

func TestIntegrationUnknownMessageType(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	client := testClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{Type: "some_random_type"})

	var resp Message
	if err := client.ReadJSON(&resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Type != "error" {
		t.Errorf("expected error, got %s", resp.Type)
	}
	if !strings.Contains(resp.Error, "unknown message type") {
		t.Errorf("expected 'unknown message type', got %q", resp.Error)
	}
}

func TestIntegrationExecMissingTarget(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	client := testClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{
		Type: "execute", ID: newID(), Target: "", Token: "t", Cmd: "h",
	})

	var resp Message
	if err := client.ReadJSON(&resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Type != "result" {
		t.Errorf("expected result, got %s", resp.Type)
	}
	if resp.OK != nil && *resp.OK {
		t.Error("expected failure")
	}
}

func TestIntegrationFileTransferMissingTarget(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	client := testClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{
		Type: "file_transfer", ID: newID(), Target: "", Token: "t",
		Mode: "scp", SrcPath: "/s", DstPath: "/d",
	})

	var resp Message
	if err := client.ReadJSON(&resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.OK != nil && *resp.OK {
		t.Error("expected failure")
	}
}

func TestIntegrationMultiMissingTargets(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	client := testClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{
		Type: "execute_multi", ID: newID(), Cmd: "hi",
		Targets: []string{}, Tokens: map[string]string{},
	})

	var resp Message
	if err := client.ReadJSON(&resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.OK != nil && *resp.OK {
		t.Error("expected failure")
	}
}

// testClient is a helper that connects to the relay at the given port.
func testClient(t *testing.T, port int) *websocket.Conn {
	t.Helper()
	u := wsURL("http://127.0.0.1:" + itoa(port))
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	return conn
}

// itoa converts an int to a string (no fmt import needed in test files with this helper).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
