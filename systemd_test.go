package main

import (
	"strings"
	"testing"
)

func TestDaemonUnitContent(t *testing.T) {
	content := daemonUnitContent("/usr/bin/remotecmd-cli")

	if err := validateUnitContent(content); err != nil {
		t.Fatalf("invalid unit content: %v", err)
	}

	if !strings.Contains(content, "remotecmd-cli daemon start") {
		t.Errorf("should contain daemon start command")
	}
	if !strings.Contains(content, "default.target") {
		t.Errorf("user service should target default.target")
	}
	if !strings.Contains(content, "Restart=always") {
		t.Errorf("should have Restart=always")
	}
	if !strings.Contains(content, "network-online.target") {
		t.Errorf("should depend on network-online.target")
	}
}

func TestRelayUnitContent(t *testing.T) {
	content := relayUnitContent("/usr/bin/remotecmd-cli", 3032)

	if err := validateUnitContent(content); err != nil {
		t.Fatalf("invalid unit content: %v", err)
	}

	if !strings.Contains(content, "remotecmd-cli relay daemon start --port 3032") {
		t.Errorf("should contain relay start command with port")
	}
	if !strings.Contains(content, "multi-user.target") {
		t.Errorf("system service should target multi-user.target")
	}
	if !strings.Contains(content, "Restart=always") {
		t.Errorf("should have Restart=always")
	}
}

func TestRelayUnitContentCustomPort(t *testing.T) {
	content := relayUnitContent("/opt/bin/rc", 8080)

	if !strings.Contains(content, "/opt/bin/rc") {
		t.Errorf("should use custom binary path")
	}
	if !strings.Contains(content, "--port 8080") {
		t.Errorf("should use custom port")
	}
}

func TestValidateUnitContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{"valid unit", "[Unit]\n[Service]\n[Install]\nExecStart=/bin/true", false},
		{"missing Unit", "[Service]\n[Install]\nExecStart=/bin/true", true},
		{"missing Service", "[Unit]\n[Install]\nExecStart=/bin/true", true},
		{"missing Install", "[Unit]\n[Service]\nExecStart=/bin/true", true},
		{"missing ExecStart", "[Unit]\n[Service]\n[Install]", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUnitContent(tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateUnitContent() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestDaemonUnitContentGenerated(t *testing.T) {
	// Test with findBinaryPath to ensure the function works
	content := DaemonUnitContent()
	if err := validateUnitContent(content); err != nil {
		t.Fatalf("invalid generated daemon unit: %v", err)
	}
}

func TestRelayUnitContentGenerated(t *testing.T) {
	content := RelayUnitContent(3032)
	if err := validateUnitContent(content); err != nil {
		t.Fatalf("invalid generated relay unit: %v", err)
	}
}

func TestIsRoot(t *testing.T) {
	// We can't easily change euid in tests, so just check the function doesn't crash
	_ = isRoot()
}
