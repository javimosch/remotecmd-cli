package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestRelaySecretRejectsNoAuth verifies that when RELAY_SECRET is set,
// connections without the Authorization header are rejected.
func TestRelaySecretRejectsNoAuth(t *testing.T) {
	rs := NewRelayServer()
	rs.secret = "test-secret-123"

	mux := http.NewServeMux()
	mux.HandleFunc("/", rs.handleWS)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	// No auth header → should fail
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected error when no auth header provided")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// TestRelaySecretAcceptsValidAuth verifies that a valid Bearer token is accepted.
func TestRelaySecretAcceptsValidAuth(t *testing.T) {
	rs := NewRelayServer()
	rs.secret = "test-secret-123"

	mux := http.NewServeMux()
	mux.HandleFunc("/", rs.handleWS)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	h := http.Header{}
	h.Set("Authorization", "Bearer test-secret-123")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, h)
	if err != nil {
		t.Fatalf("expected connection to succeed with valid secret: %v", err)
	}
	defer conn.Close()

	// Verify we can send a message (relay should accept it)
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteJSON(&Message{Type: "register", Name: "test", Token: "tok"}); err != nil {
		t.Fatalf("failed to send register: %v", err)
	}
}

// TestRelaySecretRejectsWrongAuth verifies that a wrong Bearer token is rejected.
func TestRelaySecretRejectsWrongAuth(t *testing.T) {
	rs := NewRelayServer()
	rs.secret = "correct-secret"

	mux := http.NewServeMux()
	mux.HandleFunc("/", rs.handleWS)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	h := http.Header{}
	h.Set("Authorization", "Bearer wrong-secret")

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, h)
	if err == nil {
		t.Fatal("expected error with wrong secret")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// TestRelayNoSecretAllowsAll verifies that when no secret is configured,
// connections without auth are accepted (backward compatibility).
func TestRelayNoSecretAllowsAll(t *testing.T) {
	rs := NewRelayServer()
	// rs.secret is empty (zero value) — no auth required

	mux := http.NewServeMux()
	mux.HandleFunc("/", rs.handleWS)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("expected connection without auth when no secret configured: %v", err)
	}
	defer conn.Close()
}

// TestRelaySecretHelperFunctions tests relaySecret() and relayAuthHeaders().
// relaySecret() reads from config.json first, then falls back to RELAY_SECRET env var.
// We just verify that when a secret is available, the header is a Bearer token.
func TestRelaySecretHelperFunctions(t *testing.T) {
	t.Setenv("RELAY_SECRET", "env-secret-test")
	h := relayAuthHeaders()
	if h == nil {
		t.Fatal("expected non-nil headers when secret is available")
	}
	auth := h.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		t.Errorf("expected 'Bearer <token>', got %q", auth)
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" {
		t.Error("expected non-empty token after 'Bearer '")
	}
}

// TestRelaySecretExemptAllowsWhitelisted verifies that a target in the exempt
// list can register without providing the secret.
func TestRelaySecretExemptAllowsWhitelisted(t *testing.T) {
	rs := NewRelayServer()
	rs.secret = "test-secret-123"
	rs.secretExempt["supergato"] = true
	rs.secretExempt["74ac167fc6df"] = true

	mux := http.NewServeMux()
	mux.HandleFunc("/", rs.handleWS)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	// No auth header, but target is exempt — should succeed
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("expected connection to succeed with exempt list: %v", err)
	}
	defer conn.Close()

	// Register as exempt target
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteJSON(&Message{Type: "register", Name: "supergato", Token: "tok"}); err != nil {
		t.Fatalf("failed to send register: %v", err)
	}

	// Should receive "registered" response
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	var resp Message
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Type != "registered" {
		t.Errorf("expected 'registered', got %q: %s", resp.Type, resp.Error)
	}
}

// TestRelaySecretExemptRejectsNonWhitelisted verifies that a target NOT in the
// exempt list is rejected on register when no secret is provided.
func TestRelaySecretExemptRejectsNonWhitelisted(t *testing.T) {
	rs := NewRelayServer()
	rs.secret = "test-secret-123"
	rs.secretExempt["supergato"] = true

	mux := http.NewServeMux()
	mux.HandleFunc("/", rs.handleWS)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	// No auth header, target is NOT exempt — connection allowed but register rejected
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("expected upgrade to succeed with exempt list present: %v", err)
	}
	defer conn.Close()

	// Register as non-exempt target
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteJSON(&Message{Type: "register", Name: "rogue-node", Token: "tok"}); err != nil {
		t.Fatalf("failed to send register: %v", err)
	}

	// Should receive error response
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		// Connection closed by server — also acceptable
		return
	}
	var resp Message
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Type != "error" {
		t.Errorf("expected 'error', got %q", resp.Type)
	}
	if !strings.Contains(resp.Error, "authentication required") {
		t.Errorf("expected auth error, got %q", resp.Error)
	}
}

// TestSplitCSV tests the splitCSV helper.
func TestSplitCSV(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}},
		{"", nil},
		{"single", []string{"single"}},
		{"a,,b", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := splitCSV(c.input)
		if len(got) != len(c.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", c.input, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", c.input, i, got[i], c.want[i])
			}
		}
	}
}
