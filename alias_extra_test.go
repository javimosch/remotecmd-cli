package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasBinPathInConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-haspath-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configFile := filepath.Join(tmpDir, ".zshrc")
	binDir := "/home/user/.local/bin"

	// File doesn't exist
	if hasBinPathInConfig(configFile, binDir) {
		t.Error("should return false for non-existent file")
	}

	// File exists but doesn't contain path
	os.WriteFile(configFile, []byte("export PATH=/usr/bin:$PATH\n"), 0644)
	if hasBinPathInConfig(configFile, binDir) {
		t.Error("should return false when path not in config")
	}

	// File contains path (exported)
	os.WriteFile(configFile, []byte("export PATH=\"/home/user/.local/bin:$PATH\"\n"), 0644)
	if !hasBinPathInConfig(configFile, binDir) {
		t.Error("should return true when path is in config")
	}

	// File contains path (simple assignment)
	os.WriteFile(configFile, []byte("PATH=\"/home/user/.local/bin:$PATH\"\n"), 0644)
	if !hasBinPathInConfig(configFile, binDir) {
		t.Error("should detect simple PATH assignment")
	}
}

func TestAddBinPathToConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-addpath-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configFile := filepath.Join(tmpDir, ".bashrc")
	binDir := "/home/user/.local/bin"

	// Create empty config
	os.WriteFile(configFile, []byte(""), 0644)

	err = addBinPathToConfig(configFile, binDir)
	if err != nil {
		t.Fatalf("addBinPathToConfig: %v", err)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	content := string(data)
	if !contains(content, binDir) {
		t.Errorf("config should contain bin dir:\n%s", content)
	}
	if !contains(content, "remotecmd-cli aliases") {
		t.Errorf("config should contain comment:\n%s", content)
	}
}
