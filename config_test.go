package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestConfig(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "remotecmd-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)

	cleanup := func() {
		os.Setenv("HOME", origHome)
		os.RemoveAll(tmpDir)
	}
	return tmpDir, cleanup
}

func writeConfig(t *testing.T, data string) {
	t.Helper()
	dir := configDir()
	os.MkdirAll(dir, 0700)
	if err := os.WriteFile(configPath(), []byte(data), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
}

func TestConfigDir(t *testing.T) {
	tmp, cleanup := setupTestConfig(t)
	defer cleanup()

	dir := configDir()
	expected := filepath.Join(tmp, ".remotecmd")
	if dir != expected {
		t.Errorf("configDir() = %q, want %q", dir, expected)
	}
}

func TestLoadConfigEmpty(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() on empty state: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Targets == nil {
		t.Error("expected non-nil Targets map")
	}
	if cfg.Groups == nil {
		t.Error("expected non-nil Groups map")
	}
	if cfg.Relay.URL != "" {
		t.Errorf("expected empty Relay URL, got %q", cfg.Relay.URL)
	}
}

func TestLoadConfigInvalidJSON(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	writeConfig(t, "invalid json")

	_, err := loadConfig()
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	err := setRelay("http://relay:3032", "test-node")
	if err != nil {
		t.Fatalf("setRelay: %v", err)
	}

	err = addTarget("web1", "token123")
	if err != nil {
		t.Fatalf("addTarget: %v", err)
	}

	err = addTargetWithRelayName("web2", "token456", "web2-relay")
	if err != nil {
		t.Fatalf("addTargetWithRelayName: %v", err)
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.Relay.URL != "http://relay:3032" {
		t.Errorf("Relay URL = %q", cfg.Relay.URL)
	}
	if cfg.Relay.Name != "test-node" {
		t.Errorf("Relay Name = %q", cfg.Relay.Name)
	}

	t1, ok := cfg.Targets["web1"]
	if !ok {
		t.Fatal("target web1 not found")
	}
	if t1.Token != "token123" {
		t.Errorf("web1 token = %q", t1.Token)
	}
	if t1.RelayName != "" {
		t.Errorf("web1 RelayName = %q, want empty", t1.RelayName)
	}

	t2, ok := cfg.Targets["web2"]
	if !ok {
		t.Fatal("target web2 not found")
	}
	if t2.Token != "token456" {
		t.Errorf("web2 token = %q", t2.Token)
	}
	if t2.RelayName != "web2-relay" {
		t.Errorf("web2 RelayName = %q", t2.RelayName)
	}
}

func TestRemoveTarget(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	addTarget("web2", "t2")

	err := removeTarget("web1")
	if err != nil {
		t.Fatalf("removeTarget: %v", err)
	}

	cfg, _ := loadConfig()
	if _, ok := cfg.Targets["web1"]; ok {
		t.Error("web1 should be removed")
	}
	if _, ok := cfg.Targets["web2"]; !ok {
		t.Error("web2 should still exist")
	}

	err = removeTarget("nonexistent")
	if err != nil {
		t.Errorf("removeTarget(nonexistent) should not error: %v", err)
	}
}

func TestListTargets(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "abc12345")
	addTargetWithRelayName("web2", "def67890", "web2-relay")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := listTargets()
	if err != nil {
		t.Fatalf("listTargets: %v", err)
	}

	w.Close()
	os.Stdout = old

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "web1") {
		t.Errorf("output should contain web1: %s", output)
	}
	if !strings.Contains(output, "abc1...") {
		t.Errorf("output should contain truncated token: %s", output)
	}
	if !strings.Contains(output, "web2-relay") {
		t.Errorf("output should mention relay name: %s", output)
	}
}

func TestListTargetsEmpty(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	listTargets()

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	if !strings.Contains(string(buf[:n]), "No targets") {
		t.Errorf("expected 'No targets' message: %s", string(buf[:n]))
	}
}

func TestGroupCreate(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	addTarget("web2", "t2")

	err := groupCreate("prod-web", []string{"web1", "web2"})
	if err != nil {
		t.Fatalf("groupCreate: %v", err)
	}

	cfg, _ := loadConfig()
	group, ok := cfg.Groups["prod-web"]
	if !ok {
		t.Fatal("group prod-web not found")
	}
	if len(group) != 2 || group[0] != "web1" || group[1] != "web2" {
		t.Errorf("unexpected group members: %v", group)
	}

	err = groupCreate("prod-web", []string{"web1"})
	if err == nil {
		t.Error("expected error for duplicate group")
	}

	err = groupCreate("bad", []string{"nonexistent"})
	if err == nil {
		t.Error("expected error for unknown target")
	}
}

func TestGroupDelete(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	groupCreate("test", []string{"web1"})

	err := groupDelete("test")
	if err != nil {
		t.Fatalf("groupDelete: %v", err)
	}

	cfg, _ := loadConfig()
	if _, ok := cfg.Groups["test"]; ok {
		t.Error("group should be deleted")
	}

	err = groupDelete("nonexistent")
	if err == nil {
		t.Error("expected error for deleting nonexistent group")
	}
}

func TestGroupAddTargets(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	addTarget("web2", "t2")
	addTarget("web3", "t3")

	groupCreate("web", []string{"web1"})

	err := groupAddTargets("web", []string{"web2", "web3"})
	if err != nil {
		t.Fatalf("groupAddTargets: %v", err)
	}

	cfg, _ := loadConfig()
	group := cfg.Groups["web"]
	if len(group) != 3 {
		t.Errorf("expected 3 members, got %v", group)
	}

	err = groupAddTargets("web", []string{"web1"})
	if err != nil {
		t.Fatalf("groupAddTargets duplicate: %v", err)
	}
	cfg, _ = loadConfig()
	if len(cfg.Groups["web"]) != 3 {
		t.Errorf("expected still 3 members after duplicate add, got %v", cfg.Groups["web"])
	}

	err = groupAddTargets("nonexistent", []string{"web1"})
	if err == nil {
		t.Error("expected error for nonexistent group")
	}
}

func TestGroupRemoveTargets(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	addTarget("web2", "t2")
	addTarget("web3", "t3")

	groupCreate("web", []string{"web1", "web2", "web3"})

	err := groupRemoveTargets("web", []string{"web1", "web3"})
	if err != nil {
		t.Fatalf("groupRemoveTargets: %v", err)
	}

	cfg, _ := loadConfig()
	group := cfg.Groups["web"]
	if len(group) != 1 || group[0] != "web2" {
		t.Errorf("expected [web2], got %v", group)
	}

	err = groupRemoveTargets("nonexistent", []string{"web1"})
	if err == nil {
		t.Error("expected error for nonexistent group")
	}
}

func TestGroupList(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	addTarget("web2", "t2")
	groupCreate("web", []string{"web1", "web2"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := groupList()
	if err != nil {
		t.Fatalf("groupList: %v", err)
	}

	w.Close()
	os.Stdout = old

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "web:") {
		t.Errorf("expected group listing: %s", output)
	}
}

func TestGroupListEmpty(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	groupList()
	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	if !strings.Contains(string(buf[:n]), "No groups") {
		t.Errorf("expected 'No groups' message: %s", string(buf[:n]))
	}
}

func TestResolveTargets(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	addTarget("web2", "t2")
	groupCreate("web", []string{"web1", "web2"})

	targets, err := resolveTargets("web", true)
	if err != nil {
		t.Fatalf("resolveTargets group: %v", err)
	}
	if len(targets) != 2 || targets[0] != "web1" || targets[1] != "web2" {
		t.Errorf("unexpected targets: %v", targets)
	}

	targets, err = resolveTargets("web1,web2", false)
	if err != nil {
		t.Fatalf("resolveTargets comma: %v", err)
	}
	if len(targets) != 2 {
		t.Errorf("expected 2 targets, got %v", targets)
	}

	_, err = resolveTargets("unknown", true)
	if err == nil {
		t.Error("expected error for unknown group")
	}

	_, err = resolveTargets("web1,unknown", false)
	if err == nil {
		t.Error("expected error for unknown target")
	}

	targets, err = resolveTargets("web1", false)
	if err != nil {
		t.Fatalf("resolveTargets single: %v", err)
	}
	if len(targets) != 1 || targets[0] != "web1" {
		t.Errorf("expected [web1], got %v", targets)
	}
}

func TestTokenManagement(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	token := "test-token-value"
	err := saveToken(token)
	if err != nil {
		t.Fatalf("saveToken: %v", err)
	}

	loaded, err := loadToken()
	if err != nil {
		t.Fatalf("loadToken: %v", err)
	}
	if loaded != token {
		t.Errorf("loaded = %q, want %q", loaded, token)
	}

	os.Remove(tokenPath())
	_, err = loadToken()
	if err == nil {
		t.Error("expected error loading non-existent token")
	}
}

func TestGenerateToken(t *testing.T) {
	t1 := generateToken()
	t2 := generateToken()

	if len(t1) != 64 {
		t.Errorf("expected 64 hex chars, got %d: %s", len(t1), t1)
	}
	if t1 == t2 {
		t.Error("two tokens should be different")
	}
}

func TestPairCode(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	code := "abc123"
	err := savePairCode(code)
	if err != nil {
		t.Fatalf("savePairCode: %v", err)
	}

	loaded, err := loadPairCode()
	if err != nil {
		t.Fatalf("loadPairCode: %v", err)
	}
	if loaded != code {
		t.Errorf("loaded = %q, want %q", loaded, code)
	}

	deletePairCode()
	_, err = loadPairCode()
	if err == nil {
		t.Error("expected error after deletion")
	}
}

func TestEnsureConfigDir(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	err := ensureConfigDir()
	if err != nil {
		t.Fatalf("ensureConfigDir: %v", err)
	}

	if _, err := os.Stat(configDir()); os.IsNotExist(err) {
		t.Error("config dir should exist")
	}
}
