package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAliasWrapper(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-alias-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execPath := "/usr/local/bin/remotecmd-cli"
	aliasPath := filepath.Join(tmpDir, "rc")

	err = createAliasWrapper(aliasPath, execPath)
	if err != nil {
		t.Fatalf("createAliasWrapper: %v", err)
	}

	data, err := os.ReadFile(aliasPath)
	if err != nil {
		t.Fatalf("read alias: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, execPath) {
		t.Errorf("wrapper should contain exec path: %s", content)
	}
	if !strings.HasPrefix(content, "#!/bin/sh") {
		t.Errorf("should start with shebang: %s", content)
	}
}

func TestCreateRcxWrapper(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-rcx-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execPath := "/usr/local/bin/remotecmd-cli"
	aliasPath := filepath.Join(tmpDir, "rcx")

	err = createRcxWrapper(aliasPath, execPath)
	if err != nil {
		t.Fatalf("createRcxWrapper: %v", err)
	}

	data, err := os.ReadFile(aliasPath)
	if err != nil {
		t.Fatalf("read alias: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, execPath) {
		t.Errorf("should contain exec path")
	}
	if !strings.Contains(content, "--stream") {
		t.Errorf("should handle --stream flag")
	}
	if !strings.Contains(content, "TIMEOUT=\"10\"") {
		t.Errorf("should have default timeout of 10")
	}
	if !strings.Contains(content, "--help") {
		t.Errorf("should have help text")
	}
}

func TestCreateRclWrapper(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-rcl-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execPath := "/usr/local/bin/remotecmd-cli"
	aliasPath := filepath.Join(tmpDir, "rcl")

	err = createRclWrapper(aliasPath, execPath)
	if err != nil {
		t.Fatalf("createRclWrapper: %v", err)
	}

	data, err := os.ReadFile(aliasPath)
	if err != nil {
		t.Fatalf("read alias: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, execPath) {
		t.Errorf("should contain exec path")
	}
	if !strings.Contains(content, "list-targets") {
		t.Errorf("should call list-targets")
	}
	if !strings.Contains(content, "relay name") {
		t.Errorf("should mention relay name in help")
	}
}

func TestCreateRcsWrapper(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-rcs-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execPath := "/usr/local/bin/remotecmd-cli"
	aliasPath := filepath.Join(tmpDir, "rcs")

	err = createRcsWrapper(aliasPath, execPath)
	if err != nil {
		t.Fatalf("createRcsWrapper: %v", err)
	}

	data, err := os.ReadFile(aliasPath)
	if err != nil {
		t.Fatalf("read alias: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, execPath) {
		t.Errorf("should contain exec path")
	}
	if !strings.Contains(content, "remotecmd-daemon.pid") {
		t.Errorf("should check PID file")
	}
	if !strings.Contains(content, "Daemon running") {
		t.Errorf("should show daemon running message")
	}
}

func TestCreateRccWrapper(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-rcc-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execPath := "/usr/local/bin/remotecmd-cli"
	aliasPath := filepath.Join(tmpDir, "rcc")

	err = createRccWrapper(aliasPath, execPath)
	if err != nil {
		t.Fatalf("createRccWrapper: %v", err)
	}

	data, err := os.ReadFile(aliasPath)
	if err != nil {
		t.Fatalf("read alias: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, execPath) {
		t.Errorf("should contain exec path")
	}
	if !strings.Contains(content, "--stream") {
		t.Errorf("should handle --stream flag")
	}
	if !strings.Contains(content, "target, src, and dst are required") {
		t.Errorf("should validate required args")
	}
}

func TestGetShellConfigPath(t *testing.T) {
	// Save and restore
	oldShell := os.Getenv("SHELL")
	defer os.Setenv("SHELL", oldShell)

	os.Setenv("SHELL", "/bin/zsh")
	path := getShellConfigPath()
	if path == "" {
		t.Error("expected non-empty path for zsh")
	}
	if !strings.HasSuffix(path, ".zshrc") {
		t.Errorf("expected .zshrc, got %s", path)
	}

	os.Setenv("SHELL", "/bin/bash")
	path = getShellConfigPath()
	if path == "" {
		t.Error("expected non-empty path for bash")
	}
	if !strings.HasSuffix(path, ".bashrc") {
		t.Errorf("expected .bashrc, got %s", path)
	}

	// Empty shell
	os.Setenv("SHELL", "")
	path = getShellConfigPath()
	if path != "" {
		t.Errorf("expected empty path for empty SHELL, got %s", path)
	}
}

func TestContainsPath(t *testing.T) {
	tests := []struct {
		content string
		path    string
		want    bool
	}{
		{`export PATH="/home/user/.local/bin:$PATH"`, "/home/user/.local/bin", true},
		{`export PATH='/home/user/.local/bin:$PATH'`, "/home/user/.local/bin", true},
		{`export PATH=/home/user/.local/bin:$PATH`, "/home/user/.local/bin", true},
		{`PATH="/home/user/.local/bin:$PATH"`, "/home/user/.local/bin", true},
		{`PATH=/usr/bin:$PATH`, "/home/user/.local/bin", false},
		{"", "/some/path", false},
	}

	for _, tt := range tests {
		got := containsPath(tt.content, tt.path)
		if got != tt.want {
			t.Errorf("containsPath(%q, %q) = %v, want %v", tt.content, tt.path, got, tt.want)
		}
	}
}
