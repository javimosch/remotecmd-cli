package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// set-relay --secret-stdin alone changes only the secret.
func TestSetRelaySecretStdinOnly(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	setRelay("http://relay.example:3032", "node-a")

	r, w, _ := os.Pipe()
	w.WriteString("s3cret-value\n")
	w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	captureStdout(t, func() { handleSetRelay([]string{"--secret-stdin"}) })

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Relay.URL != "http://relay.example:3032" || cfg.Relay.Name != "node-a" {
		t.Errorf("relay settings changed: %+v", cfg.Relay)
	}
	if relaySecret() != "s3cret-value" {
		t.Errorf("secret = %q", relaySecret())
	}
}

// With RELAY_SECRET_EXEMPT, an unauthenticated connection may register as
// an exempt daemon but must not act as a client.
func TestExemptConnectionsCannotExecute(t *testing.T) {
	rs := NewRelayServer()
	rs.secret = "S"
	rs.secretExempt["legacy-box"] = true
	srv := httptest.NewServer(rs.mux())
	defer srv.Close()
	u := "ws" + strings.TrimPrefix(srv.URL, "http")
	dial := func(auth bool) *websocket.Conn {
		h := http.Header{}
		if auth {
			h.Set("Authorization", "Bearer S")
		}
		c, _, err := websocket.DefaultDialer.Dial(u, h)
		if err != nil {
			t.Fatalf("dial (auth=%v): %v", auth, err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	read := func(c *websocket.Conn) Message {
		var m Message
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		c.ReadJSON(&m)
		return m
	}

	// The exempt daemon registers without the secret.
	daemon := dial(false)
	daemon.WriteJSON(&Message{Type: "register", Name: "legacy-box", Token: "tok"})
	if m := read(daemon); m.Type != "registered" {
		t.Fatalf("exempt daemon register: %+v", m)
	}

	// An unauthenticated client cannot execute on it...
	anon := dial(false)
	anon.WriteJSON(&Message{Type: "execute", ID: "1", Target: "legacy-box", Token: "tok", Cmd: "id"})
	if m := read(anon); !strings.Contains(m.Error, "authentication required") {
		t.Errorf("unauthenticated execute: %+v", m)
	}
	// ...and neither can the exempt daemon's own connection.
	daemon.WriteJSON(&Message{Type: "execute", ID: "2", Target: "legacy-box", Token: "tok", Cmd: "id"})
	if m := read(daemon); !strings.Contains(m.Error, "authentication required") {
		t.Errorf("execute from exempt daemon connection: %+v", m)
	}

	// An authenticated client can (the command reaches the daemon).
	daemon2 := dial(false)
	daemon2.WriteJSON(&Message{Type: "register", Name: "legacy-box", Token: "tok"})
	read(daemon2)
	client := dial(true)
	client.WriteJSON(&Message{Type: "execute", ID: "3", Target: "legacy-box", Token: "tok", Cmd: "id"})
	if m := read(daemon2); m.Type != "command" || m.Cmd != "id" {
		t.Errorf("authenticated execute should reach the daemon, daemon got %+v", m)
	}
}
