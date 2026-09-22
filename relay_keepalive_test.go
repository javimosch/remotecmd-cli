package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func startKeepaliveRelay(t *testing.T) string {
	t.Helper()
	rs := NewRelayServer()
	rs.pingPeriod = 50 * time.Millisecond
	rs.pongWait = 200 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(rs.handleWS))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func registerOn(t *testing.T, u, name, token string) (*websocket.Conn, Message) {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.WriteJSON(&Message{Type: "register", Name: name, Token: token})
	var resp Message
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read register response: %v", err)
	}
	conn.SetReadDeadline(time.Time{})
	return conn, resp
}

// A peer that stops answering pings is dropped, releasing its name for a
// reinstalled daemon that comes back with a new token.
func TestRelayKeepaliveReapsDeadPeer(t *testing.T) {
	u := startKeepaliveRelay(t)

	// Never reads again, so gorilla never answers the relay's pings.
	registerOn(t, u, "testbox", "old-token")

	if _, resp := registerOn(t, u, "testbox", "new-token"); resp.Type != "error" {
		t.Fatalf("expected rejection while old peer is live, got %+v", resp)
	}

	time.Sleep(500 * time.Millisecond)

	if _, resp := registerOn(t, u, "testbox", "new-token"); resp.Type != "registered" {
		t.Fatalf("expected dead peer to be reaped, got %+v", resp)
	}
}

// A peer that keeps reading answers pings and outlives pongWait.
func TestRelayKeepaliveKeepsLivePeer(t *testing.T) {
	u := startKeepaliveRelay(t)

	conn, _ := registerOn(t, u, "testbox", "tok")
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	time.Sleep(500 * time.Millisecond)

	if _, resp := registerOn(t, u, "testbox", "other"); resp.Type != "error" {
		t.Fatalf("live peer was dropped: got %+v", resp)
	}
}
