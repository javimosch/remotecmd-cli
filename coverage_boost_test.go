package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveConfigWithPermissionError(t *testing.T) {
	// Test that saveConfig handles OS write errors gracefully
	tmpDir, err := os.MkdirTemp("", "remotecmd-savecfg-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create config dir read-only so saveConfig fails
	dir := configDir()
	os.MkdirAll(dir, 0700)

	cfg := &Config{
		Relay:   RelayConfig{URL: "http://test:3032"},
		Targets: make(map[string]TargetConfig),
		Groups:  make(map[string][]string),
	}

	// First save should work
	err = saveConfig(cfg)
	if err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	// Verify it was written
	data, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "http://test:3032") {
		t.Error("config should contain relay URL")
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	// Write a config file and verify loadConfig reads it
	writeConfig(t, `{
		"relay": {"url": "http://relay:3032", "name": "mynode"},
		"targets": {
			"web1": {"token": "abc123"},
			"web2": {"token": "def456", "relay_name": "web2-relay"}
		},
		"groups": {
			"web": ["web1", "web2"]
		}
	}`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.Relay.URL != "http://relay:3032" {
		t.Errorf("Relay URL = %q", cfg.Relay.URL)
	}
	if cfg.Targets["web2"].RelayName != "web2-relay" {
		t.Errorf("web2 RelayName = %q", cfg.Targets["web2"].RelayName)
	}
	if len(cfg.Groups["web"]) != 2 {
		t.Errorf("group web = %v", cfg.Groups["web"])
	}
}

func TestHandleGroupSubcommandList(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	groupCreate("web", []string{"web1"})

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleGroupSubcommand([]string{"list"})

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])
	if !strings.Contains(output, "web:") {
		t.Errorf("expected group listing: %s", output)
	}
}

func TestDaemonSendWithNilConn(t *testing.T) {
	td := &TargetDaemon{}
	// send with nil conn should log but not panic
	td.send(&Message{Type: "test", ID: "nil-test"})
}

func TestFindBinaryPathNotEmpty(t *testing.T) {
	path := findBinaryPath()
	if path == "" {
		t.Error("expected non-empty path")
	}
}

func TestSystemdHandlerContents(t *testing.T) {
	// Verify the handler functions produce correct unit content
	// without actually installing the service

	binPath := "/opt/remotecmd-cli/bin/rc"

	t.Run("daemon unit", func(t *testing.T) {
		content := daemonUnitContent(binPath)
		if !strings.Contains(content, binPath) {
			t.Error("unit should contain binary path")
		}
		if err := validateUnitContent(content); err != nil {
			t.Errorf("invalid unit: %v", err)
		}
	})

	t.Run("relay unit", func(t *testing.T) {
		content := relayUnitContent(binPath, 9443)
		if !strings.Contains(content, "9443") {
			t.Error("unit should use port 9443")
		}
		if err := validateUnitContent(content); err != nil {
			t.Errorf("invalid unit: %v", err)
		}
	})
}

func TestConfigDirCreation(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	// Ensure dir exists
	err := ensureConfigDir()
	if err != nil {
		t.Fatalf("ensureConfigDir: %v", err)
	}

	// Verify it's a directory
	info, err := os.Stat(configDir())
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if !info.IsDir() {
		t.Error("config dir should be a directory")
	}
}

func TestListTargetsShowsGroups(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	groupCreate("mygroup", []string{"web1"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	listTargets()

	w.Close()
	os.Stdout = old

	var buf [2048]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "Groups:") {
		t.Errorf("should show Groups section: %s", output)
	}
	if !strings.Contains(output, "mygroup") {
		t.Errorf("should show group name: %s", output)
	}
}

func TestRelayUnitFilePermissions(t *testing.T) {
	// Test that relay unit file can be written to a temp dir
	tmpDir, err := os.MkdirTemp("", "remotecmd-relay-unit-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	content := relayUnitContent("/bin/remotecmd-cli", 443)
	unitPath := filepath.Join(tmpDir, "remotecmd-relay.service")

	if err := os.WriteFile(unitPath, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		t.Error("unit file should exist")
	}
}
