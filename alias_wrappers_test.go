package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateRcgWrapper(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-rcg-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execPath := "/usr/local/bin/remotecmd-cli"
	aliasPath := filepath.Join(tmpDir, "rcg")

	if err := createRcgWrapper(aliasPath, execPath); err != nil {
		t.Fatalf("createRcgWrapper: %v", err)
	}

	data, err := os.ReadFile(aliasPath)
	if err != nil {
		t.Fatalf("read alias: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, execPath) {
		t.Errorf("should contain exec path")
	}
	if !strings.Contains(content, "group list") {
		t.Errorf("should route list to 'group list'")
	}
	if !strings.Contains(content, "group \"$SUB\"") {
		t.Errorf("should route create/delete/add/remove to 'group $SUB'")
	}
	if !strings.Contains(content, "--help") {
		t.Errorf("should have help text")
	}
}

func TestCreateRcdWrapper(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-rcd-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execPath := "/usr/local/bin/remotecmd-cli"
	aliasPath := filepath.Join(tmpDir, "rcd")

	if err := createRcdWrapper(aliasPath, execPath); err != nil {
		t.Fatalf("createRcdWrapper: %v", err)
	}

	data, err := os.ReadFile(aliasPath)
	if err != nil {
		t.Fatalf("read alias: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, execPath) {
		t.Errorf("should contain exec path")
	}
	if !strings.Contains(content, "daemon \"$@\"") {
		t.Errorf("should pass args through to 'daemon'")
	}
	if !strings.Contains(content, "systemd") {
		t.Errorf("should mention systemd subcommand")
	}
}

func TestCreateRcrWrapper(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-rcr-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execPath := "/usr/local/bin/remotecmd-cli"
	aliasPath := filepath.Join(tmpDir, "rcr")

	if err := createRcrWrapper(aliasPath, execPath); err != nil {
		t.Fatalf("createRcrWrapper: %v", err)
	}

	data, err := os.ReadFile(aliasPath)
	if err != nil {
		t.Fatalf("read alias: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, execPath) {
		t.Errorf("should contain exec path")
	}
	if !strings.Contains(content, "relay daemon") {
		t.Errorf("should route to 'relay daemon'")
	}
	if !strings.Contains(content, "relay daemon systemd") {
		t.Errorf("should route systemd to 'relay daemon systemd'")
	}
}

func TestRcxWrapperSupportsMultiTarget(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-rcx-multi-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execPath := "/usr/local/bin/remotecmd-cli"
	aliasPath := filepath.Join(tmpDir, "rcx")

	if err := createRcxWrapper(aliasPath, execPath); err != nil {
		t.Fatalf("createRcxWrapper: %v", err)
	}

	data, err := os.ReadFile(aliasPath)
	if err != nil {
		t.Fatalf("read alias: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "--targets") {
		t.Errorf("rcx should support --targets multi-target path")
	}
	if !strings.Contains(content, "--group") {
		t.Errorf("rcx should support --group path")
	}
	if !strings.Contains(content, "exec \"$MODE\"") {
		t.Errorf("rcx multi path should call 'exec' subcommand")
	}
	// Single-target path must still be present
	if !strings.Contains(content, "--target \"$TARGET\"") {
		t.Errorf("rcx should still support single-target --target path")
	}
}

func TestRclWrapperPassesFlags(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-rcl-flags-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execPath := "/usr/local/bin/remotecmd-cli"
	aliasPath := filepath.Join(tmpDir, "rcl")

	if err := createRclWrapper(aliasPath, execPath); err != nil {
		t.Fatalf("createRclWrapper: %v", err)
	}

	data, err := os.ReadFile(aliasPath)
	if err != nil {
		t.Fatalf("read alias: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "list-targets \"$@\"") {
		t.Errorf("rcl should forward args to list-targets")
	}
	if !strings.Contains(content, "--refresh") {
		t.Errorf("rcl help should mention --refresh")
	}
	if !strings.Contains(content, "--no-health") {
		t.Errorf("rcl help should mention --no-health")
	}
}
