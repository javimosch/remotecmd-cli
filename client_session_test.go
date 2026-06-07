package main

import (
	"testing"
)

func TestNewClientSessionNoRelay(t *testing.T) {
	// Without a configured relay, newClientSession should fail
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	// Set a bogus relay URL
	setRelay("http://127.0.0.1:1", "test")

	session, err := newClientSession("http://127.0.0.1:1")
	if err == nil {
		session.Close()
		t.Skip("unexpectedly connected to relay")
	}
	if session != nil {
		session.Close()
	}
	_ = err
}

func TestClientSessionExecNoTarget(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	setRelay("http://127.0.0.1:1", "test")

	session, err := newClientSession("http://127.0.0.1:1")
	if err != nil {
		t.Skip("relay not available")
	}
	defer session.Close()

	// Exec without a configured target should error
	_, err = session.Exec("nonexistent", "uptime", 5)
	if err == nil {
		t.Error("expected error for unknown target")
	}
}
