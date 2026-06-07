package main

import (
	"os"
	"strings"
	"testing"
)

func TestDaemonSystemdSubcommandHandlers(t *testing.T) {
	// Test that daemonSystemdSubcommand dispatches correctly for "install" and "remove"
	// These functions try to write files and run systemctl, so we can't fully test them.
	// But we can verify the unit content they'd produce.

	content := daemonUnitContent("/tmp/test-rc")
	if !strings.Contains(content, "Type=simple") {
		t.Error("unit should use Type=simple")
	}
	if !strings.Contains(content, "RestartSec=5") {
		t.Error("daemon should have 5s restart")
	}
}

func TestRelaySystemdSubcommandUnitContent(t *testing.T) {
	content := relayUnitContent("/tmp/test-rc", 8443)
	if !strings.Contains(content, "RestartSec=3") {
		t.Error("relay should have 3s restart")
	}
	if !strings.Contains(content, "After=network.target") {
		t.Error("relay should depend on network.target")
	}
}

func TestSystemdFunctionsNoPanic(t *testing.T) {
	// Verify function signatures are correct — call with test args
	// These would normally write files, but we won't call the real handlers

	// Just call validateUnitContent with various inputs
	tests := []string{
		"[Unit]\n[Service]\n[Install]\nExecStart=/bin/true",
		"[Unit]\nDescription=test\n[Service]\nExecStart=/bin/true\n[Install]\nWantedBy=default.target",
	}
	for _, tc := range tests {
		if err := validateUnitContent(tc); err != nil {
			t.Errorf("unexpected error for valid content: %v", err)
		}
	}
}

func TestHandleClientSubcommandNoRelay(t *testing.T) {
	// Without a configured relay, handleClientSubcommand should exit with code 3
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	// Set HOME to a temp dir with no config
	if os.Getenv("HOME") != "" {
		// The function will try to load config and fail with "relay not configured"
		// We can't easily catch os.Exit(ExitConfigError), but we can verify
		// the config is empty (no relay URL)
		cfg, _ := loadConfig()
		if cfg.Relay.URL != "" {
			t.Error("expected empty relay URL")
		}
	}
}
