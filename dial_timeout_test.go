package main

import (
	"net"
	"testing"
	"time"
)

// A relay that accepts TCP but never answers the upgrade must not hang the
// daemon's reconnect loop — the dial has to give up and let it retry.
func TestDialRelayHandshakeTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // hold it open, say nothing
		}
	}()

	oldDial, oldHS := relayDialTimeout, relayHandshakeTimeout
	relayDialTimeout, relayHandshakeTimeout = 200*time.Millisecond, 300*time.Millisecond
	defer func() { relayDialTimeout, relayHandshakeTimeout = oldDial, oldHS }()

	start := time.Now()
	_, _, err = dialRelay("ws://" + ln.Addr().String())
	if err == nil {
		t.Fatal("expected dial to fail against a silent relay")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("dial took %v, want it bounded by the handshake timeout", elapsed)
	}
}

func TestWsDialerHasTimeouts(t *testing.T) {
	if d := wsDialer(); d.HandshakeTimeout <= 0 {
		t.Errorf("HandshakeTimeout = %v, want > 0", d.HandshakeTimeout)
	}
	if relayDialTimeout <= 0 {
		t.Errorf("relayDialTimeout = %v, want > 0", relayDialTimeout)
	}
}
