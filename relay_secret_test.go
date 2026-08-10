package main

import (
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
func TestRelaySecretHelperFunctions(t *testing.T) {
	// relayAuthHeaders returns nil when no secret configured
	t.Setenv("RELAY_SECRET", "")
	h := relayAuthHeaders()
	if h != nil {
		t.Error("expected nil headers when no secret configured")
	}

	// relayAuthHeaders returns Bearer header when env var is set
	t.Setenv("RELAY_SECRET", "env-secret")
	h = relayAuthHeaders()
	if h == nil {
		t.Fatal("expected non-nil headers when secret configured")
	}
	if h.Get("Authorization") != "Bearer env-secret" {
		t.Errorf("expected 'Bearer env-secret', got %q", h.Get("Authorization"))
	}
}
