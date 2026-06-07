package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleAliasInstall(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-alias-install-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Redirect HOME to temp dir
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create a fake shell config
	shellConfig := filepath.Join(tmpDir, ".zshrc")
	os.WriteFile(shellConfig, []byte("export PATH=/usr/bin:$PATH\n"), 0644)

	// Set SHELL to zsh so getShellConfigPath returns .zshrc
	origShell := os.Getenv("SHELL")
	os.Setenv("SHELL", "/bin/zsh")
	defer os.Setenv("SHELL", origShell)

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// This creates alias files in ~/.local/bin and updates shell config
	handleAliasInstall()

	w.Close()
	os.Stdout = old

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "Aliases installed") {
		t.Errorf("expected success message: %s", output)
	}

	// Verify alias files created
	binDir := filepath.Join(tmpDir, ".local", "bin")
	for _, name := range []string{"rc", "rcx", "rcl", "rcs", "rcc"} {
		path := filepath.Join(binDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("alias %s not created at %s", name, path)
		}
	}

	// Verify shell config was updated
	data, _ := os.ReadFile(shellConfig)
	content := string(data)
	if !strings.Contains(content, "remotecmd-cli aliases") {
		t.Errorf("shell config should have alias comment:\n%s", content)
	}
	if !strings.Contains(content, binDir) {
		t.Errorf("shell config should contain bin dir:\n%s", content)
	}
}

func TestHandleAliasUninstall(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-alias-uninstall-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	origShell := os.Getenv("SHELL")
	os.Setenv("SHELL", "/bin/zsh")
	defer os.Setenv("SHELL", origShell)

	// First install aliases
	handleAliasInstall()

	// Verify they exist
	binDir := filepath.Join(tmpDir, ".local", "bin")
	for _, name := range []string{"rc", "rcx", "rcl"} {
		if _, err := os.Stat(filepath.Join(binDir, name)); os.IsNotExist(err) {
			t.Fatalf("alias %s should exist before uninstall", name)
		}
	}

	// Capture output
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleAliasUninstall()

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "Aliases uninstalled") {
		t.Errorf("expected uninstall message: %s", output)
	}

	// Verify files are gone
	for _, name := range []string{"rc", "rcx", "rcl", "rcs", "rcc"} {
		path := filepath.Join(binDir, name)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("alias %s should be removed", name)
		}
	}

	// Uninstall again should be harmless
	handleAliasUninstall()
}

func TestPrintAliasHelp(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printAliasHelp()

	w.Close()
	os.Stdout = old

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "install") {
		t.Errorf("help should mention install: %s", output)
	}
	if !strings.Contains(output, "uninstall") {
		t.Errorf("help should mention uninstall: %s", output)
	}
}

func TestPrintPairHelp(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printPairHelp()

	w.Close()
	os.Stdout = old

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "listen") {
		t.Errorf("help should mention listen: %s", output)
	}
	if !strings.Contains(output, "accept") {
		t.Errorf("help should mention accept: %s", output)
	}
}

func TestPrintGroupHelp(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printGroupHelp()

	w.Close()
	os.Stdout = old

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "create") {
		t.Errorf("help should mention create: %s", output)
	}
	if !strings.Contains(output, "delete") {
		t.Errorf("help should mention delete: %s", output)
	}
	if !strings.Contains(output, "add") {
		t.Errorf("help should mention add: %s", output)
	}
	if !strings.Contains(output, "remove") {
		t.Errorf("help should mention remove: %s", output)
	}
	if !strings.Contains(output, "list") {
		t.Errorf("help should mention list: %s", output)
	}
}

func TestPrintDaemonHelp(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printDaemonHelp()

	w.Close()
	os.Stdout = old

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "start") {
		t.Errorf("should mention start: %s", output)
	}
	if !strings.Contains(output, "stop") {
		t.Errorf("should mention stop: %s", output)
	}
	if !strings.Contains(output, "status") {
		t.Errorf("should mention status: %s", output)
	}
}

func TestPrintRelayHelp(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printRelayHelp()

	w.Close()
	os.Stdout = old

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "daemon") {
		t.Errorf("should mention daemon: %s", output)
	}
}

func TestPrintRelayDaemonHelp(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printRelayDaemonHelp()

	w.Close()
	os.Stdout = old

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "start") {
		t.Errorf("should mention start: %s", output)
	}
	if !strings.Contains(output, "stop") {
		t.Errorf("should mention stop: %s", output)
	}
	if !strings.Contains(output, "status") {
		t.Errorf("should mention status: %s", output)
	}
}
