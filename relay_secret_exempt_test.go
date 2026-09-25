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

// A client without the secret gets the relay's reason, not "unexpected
// EOF" — both when the relay lets it in (exempt list set) and rejects its
// command, and when the relay refuses the handshake (401).
func TestClientWithoutSecretSeesWhy(t *testing.T) {
	for _, exempt := range []bool{true, false} {
		_, cleanup := setupTestConfig(t)
		rs := NewRelayServer()
		rs.secret = "S"
		if exempt {
			rs.secretExempt["legacy-box"] = true
		}
		srv := httptest.NewServer(rs.mux())
		setRelay(srv.URL, "client-without-secret")
		addTarget("legacy-box", "tok")

		err := handleExecWithStdin("legacy-box", "id", 5, false, nil)
		if err == nil {
			t.Fatalf("exempt=%v: expected an error", exempt)
		}
		want := "authentication required"
		if !exempt {
			want = "relay requires a secret (no relay secret is configured)"
		}
		if !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "unexpected EOF") {
			t.Errorf("exempt=%v: error %q, want it to explain %q", exempt, err, want)
		}
		if code := classifyError(err); code != ExitConfigError {
			t.Errorf("exempt=%v: exit %d, want %d (config, not a retryable network error)", exempt, code, ExitConfigError)
		}
		if typ := errorType(classifyError(err), err.Error()); typ != "auth_required" {
			t.Errorf("exempt=%v: type %q, want auth_required", exempt, typ)
		}
		srv.Close()
		cleanup()
	}
}

// Older clients only understand the reply they wait for: an execute is
// rejected with a failed result, not an "error" message they would skip.
func TestAuthRequiredReplyShapes(t *testing.T) {
	cases := map[string]string{"execute": "result", "file_transfer": "result", "execute_multi": "multi_result", "tunnel_open": "tunnel_opened", "pair_listen": "error"}
	for in, want := range cases {
		r := authRequiredReply(&Message{Type: in, ID: "x", TunnelID: "t"})
		if r.Type != want || !strings.Contains(r.Error, "authentication required") {
			t.Errorf("%s -> %+v, want type %s with the reason", in, r, want)
		}
		if want == "result" && (r.OK == nil || *r.OK) {
			t.Errorf("%s: result must be ok:false", in)
		}
	}
}
