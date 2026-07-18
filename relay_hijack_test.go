package main

import (
	"fmt"
	"testing"
	"time"
)

// TestRegisterNameHijackRejected verifies that a registered target name cannot
// be taken over by a connection presenting a different token. Without this
// guard any client that knows only the target NAME could evict the real target
// and hijack (or simply deny) its command routing.
func TestRegisterNameHijackRejected(t *testing.T) {
	_, port := startTestRelay(t)
	relayURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Real target registers and stays reachable.
	real := testDaemon(t, relayURL, "box", "correct-token")
	defer real.Close()
	testDaemonResponder(t, real, 0, "still-alive")
	time.Sleep(50 * time.Millisecond)

	// Attacker knows the name but not the token.
	attacker := dialClient(t, port)
	defer attacker.Close()
	attacker.WriteJSON(&Message{Type: "register", Name: "box", Token: "guessed-token"})
	resp := readMsg(t, attacker)
	if resp.Type != "error" {
		t.Fatalf("expected error rejecting hijack, got %s (%+v)", resp.Type, resp)
	}
	if resp.Error != "name already registered with a different token" {
		t.Errorf("unexpected error: %q", resp.Error)
	}

	// The real target must still own the route: a client with the correct
	// token can still reach it after the hijack attempt.
	client := dialClient(t, port)
	defer client.Close()
	client.WriteJSON(&Message{
		Type: "execute", ID: newID(), Target: "box",
		Token: "correct-token", Cmd: "hostname", Timeout: 5,
	})
	res := readMsg(t, client)
	if res.OK == nil || !*res.OK {
		t.Fatalf("real target should stay routable after hijack attempt: %+v", res)
	}
	if res.Stdout != "still-alive" {
		t.Errorf("unexpected stdout from real target: %q", res.Stdout)
	}
}

// TestRegisterReconnectSameTokenAllowed verifies a legitimate reconnect using
// the same token is still permitted — the hijack guard must not break normal
// daemon reconnection after a network blip (the newest connection wins).
func TestRegisterReconnectSameTokenAllowed(t *testing.T) {
	_, port := startTestRelay(t)
	relayURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	first := testDaemon(t, relayURL, "box", "shared-token")
	defer first.Close()

	// Reconnect with the SAME token — testDaemon asserts it gets a "registered"
	// ack, so a failure here means legit reconnects were broken.
	second := testDaemon(t, relayURL, "box", "shared-token")
	defer second.Close()
	testDaemonResponder(t, second, 0, "reconnected")
	time.Sleep(50 * time.Millisecond)

	client := dialClient(t, port)
	defer client.Close()
	client.WriteJSON(&Message{
		Type: "execute", ID: newID(), Target: "box",
		Token: "shared-token", Cmd: "hostname", Timeout: 5,
	})
	res := readMsg(t, client)
	if res.OK == nil || !*res.OK {
		t.Fatalf("reconnected target should be routable: %+v", res)
	}
	if res.Stdout != "reconnected" {
		t.Errorf("newest connection should win, got stdout %q", res.Stdout)
	}
}
