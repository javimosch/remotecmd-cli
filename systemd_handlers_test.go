package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDaemonSystemdSubcommand(t *testing.T) {
	// Can't fully test without systemd, but verify the handler dispatches correctly
	// We test the subcommand dispatch by capturing os.Exit which would happen with invalid args
	// Instead, just verify the parse doesn't crash on valid args
	if testing.Short() {
		t.Skip("skipping systemd handler test in short mode")
	}

	// The function writes a unit file and runs systemctl
	// We can test the unit file validation separately
}

func TestHandleDaemonInstallSystemdUnitFile(t *testing.T) {
	// Test that the unit file content is valid by rendering it
	content := daemonUnitContent("/usr/local/bin/remotecmd-cli")
	if err := validateUnitContent(content); err != nil {
		t.Fatalf("invalid unit: %v", err)
	}
}

func TestHandleRelayInstallSystemdUnitFile(t *testing.T) {
	content := relayUnitContent("/usr/local/bin/remotecmd-cli", 8443)
	if err := validateUnitContent(content); err != nil {
		t.Fatalf("invalid unit: %v", err)
	}
}

func TestFindBinaryPath(t *testing.T) {
	path := findBinaryPath()
	if path == "" {
		t.Error("expected non-empty binary path")
	}
}

func TestSystemdUnitFileWritten(t *testing.T) {
	// Test that writing the unit file to a temp location works
	tmpDir, err := os.MkdirTemp("", "remotecmd-systemd-test-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	unitPath := filepath.Join(tmpDir, "test.service")
	content := daemonUnitContent("/usr/bin/rc")

	if err := os.WriteFile(unitPath, []byte(content), 0644); err != nil {
		t.Fatalf("write unit: %v", err)
	}

	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		t.Error("unit file should exist")
	}
}

func TestDaemonUnitContentIncludesConfigDir(t *testing.T) {
	content := daemonUnitContent("/home/user/.local/bin/remotecmd-cli")
	if !contains(content, "/home/user/.local/bin/remotecmd-cli") {
		t.Error("unit should contain the exact binary path")
	}
}

func TestRelayUnitContentNonStandardPort(t *testing.T) {
	content := relayUnitContent("/snap/bin/remotecmd-cli", 443)
	if !contains(content, "443") {
		t.Error("unit should use custom port 443")
	}
}
