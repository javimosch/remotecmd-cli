package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// resetActivationKeyCacheForTest clears the activation key cache.
func resetActivationKeyCacheForTest() {
	activationKeys.mu.Lock()
	activationKeys.cached = nil
	activationKeys.modTime = 0
	activationKeys.fileSize = 0
	activationKeys.mu.Unlock()
}

// --- Pair with activation key ---

func TestIntegrationPairWithActivationKey(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("RCMD_TEST_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("RCMD_TEST_CONFIG_DIR")
	resetActivationKeyCacheForTest()

	// Write a valid activation key to the relay's key file
	os.WriteFile(filepath.Join(tmpDir, "activation-keys.json"), []byte(`["secret-key-123"]`), 0600)
	resetActivationKeyCacheForTest()

	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	// Listener requests activation key
	listener := testClient(t, port)
	defer listener.Close()

	listener.WriteJSON(&Message{
		Type: "pair_listen", Code: "secure-pair", RequireActivationKey: true,
	})

	time.Sleep(50 * time.Millisecond)

	// Peer with correct activation key
	peer := testClient(t, port)
	defer peer.Close()

	peer.WriteJSON(&Message{
		Type: "pair", Code: "secure-pair", Token: "peer-tok",
		Hostname: "peer-host", ActivationKey: "secret-key-123",
	})

	// Listener should get pair_done
	var pairDone Message
	if err := listener.ReadJSON(&pairDone); err != nil {
		t.Fatalf("listener read: %v", err)
	}
	if pairDone.Type != "pair_done" {
		t.Errorf("expected pair_done, got %s: %s", pairDone.Type, pairDone.Error)
	}
	if pairDone.Token != "peer-tok" {
		t.Errorf("token = %q", pairDone.Token)
	}

	// Peer should get pair_confirmed
	var confirmed Message
	if err := peer.ReadJSON(&confirmed); err != nil {
		t.Fatalf("peer read: %v", err)
	}
	if confirmed.Type != "pair_confirmed" {
		t.Errorf("expected pair_confirmed, got %s", confirmed.Type)
	}
}

func TestIntegrationPairActivationKeyRejected(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("RCMD_TEST_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("RCMD_TEST_CONFIG_DIR")
	resetActivationKeyCacheForTest()

	// Write a valid activation key
	os.WriteFile(filepath.Join(tmpDir, "activation-keys.json"), []byte(`["real-key"]`), 0600)
	resetActivationKeyCacheForTest()

	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	// Listener requests activation key
	listener := testClient(t, port)
	defer listener.Close()

	listener.WriteJSON(&Message{
		Type: "pair_listen", Code: "reject-pair", RequireActivationKey: true,
	})

	time.Sleep(50 * time.Millisecond)

	// Peer with WRONG activation key
	peer := testClient(t, port)
	defer peer.Close()

	peer.WriteJSON(&Message{
		Type: "pair", Code: "reject-pair", Token: "peer-tok",
		Hostname: "peer-host", ActivationKey: "wrong-key",
	})

	// Peer should get an error
	var resp Message
	if err := peer.ReadJSON(&resp); err != nil {
		t.Fatalf("peer read: %v", err)
	}
	if resp.Type != "error" {
		t.Errorf("expected error, got %s", resp.Type)
	}
	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}

	// Listener should still be registered (re-registered after rejection)
	// so a valid peer can still pair
	time.Sleep(50 * time.Millisecond)

	peer2 := testClient(t, port)
	defer peer2.Close()

	peer2.WriteJSON(&Message{
		Type: "pair", Code: "reject-pair", Token: "peer-tok-2",
		Hostname: "peer-host-2", ActivationKey: "real-key",
	})

	var pairDone Message
	if err := listener.ReadJSON(&pairDone); err != nil {
		t.Fatalf("listener should still get pair_done after rejected attempt: %v", err)
	}
	if pairDone.Type != "pair_done" {
		t.Errorf("expected pair_done after valid retry, got %s", pairDone.Type)
	}
	if pairDone.Token != "peer-tok-2" {
		t.Errorf("token = %q", pairDone.Token)
	}
}

func TestIntegrationPairActivationKeyMissing(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("RCMD_TEST_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("RCMD_TEST_CONFIG_DIR")
	resetActivationKeyCacheForTest()

	os.WriteFile(filepath.Join(tmpDir, "activation-keys.json"), []byte(`["key-x"]`), 0600)
	resetActivationKeyCacheForTest()

	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	listener := testClient(t, port)
	defer listener.Close()

	listener.WriteJSON(&Message{
		Type: "pair_listen", Code: "missing-key-pair", RequireActivationKey: true,
	})

	time.Sleep(50 * time.Millisecond)

	// Peer sends NO activation key
	peer := testClient(t, port)
	defer peer.Close()

	peer.WriteJSON(&Message{
		Type: "pair", Code: "missing-key-pair", Token: "tok", Hostname: "host",
	})

	var resp Message
	if err := peer.ReadJSON(&resp); err != nil {
		t.Fatalf("peer read: %v", err)
	}
	if resp.Type != "error" {
		t.Errorf("expected error for missing activation key, got %s", resp.Type)
	}
}

func TestIntegrationPairNoActivationKeyRequired(t *testing.T) {
	// When listener does NOT set RequireActivationKey, pair works without a key
	// (backward compatibility)
	tmpDir := t.TempDir()
	os.Setenv("RCMD_TEST_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("RCMD_TEST_CONFIG_DIR")
	resetActivationKeyCacheForTest()

	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	listener := testClient(t, port)
	defer listener.Close()

	listener.WriteJSON(&Message{
		Type: "pair_listen", Code: "no-key-pair",
	})

	time.Sleep(50 * time.Millisecond)

	peer := testClient(t, port)
	defer peer.Close()

	peer.WriteJSON(&Message{
		Type: "pair", Code: "no-key-pair", Token: "tok", Hostname: "host",
	})

	var pairDone Message
	if err := listener.ReadJSON(&pairDone); err != nil {
		t.Fatalf("listener read: %v", err)
	}
	if pairDone.Type != "pair_done" {
		t.Errorf("expected pair_done, got %s", pairDone.Type)
	}
}

// --- Disconnect ---

func TestIntegrationDisconnectForwarded(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	// Register a daemon
	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "disc-target", "dtok")
	defer daemon.Close()

	time.Sleep(50 * time.Millisecond)

	// Client sends disconnect
	client := testClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{
		Type: "disconnect", Target: "disc-target", Token: "dtok",
	})

	// Client should get disconnect_confirmed
	var confirmed Message
	if err := client.ReadJSON(&confirmed); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if confirmed.Type != "disconnect_confirmed" {
		t.Errorf("expected disconnect_confirmed, got %s", confirmed.Type)
	}
	if confirmed.Target != "disc-target" {
		t.Errorf("target = %q", confirmed.Target)
	}

	// Daemon should receive the disconnect message
	var discMsg Message
	daemon.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := daemon.ReadJSON(&discMsg); err != nil {
		t.Fatalf("daemon should receive disconnect: %v", err)
	}
	if discMsg.Type != "disconnect" {
		t.Errorf("expected disconnect, got %s", discMsg.Type)
	}
}

func TestIntegrationDisconnectWrongToken(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "disc-target-2", "real-tok")
	defer daemon.Close()

	time.Sleep(50 * time.Millisecond)

	client := testClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{
		Type: "disconnect", Target: "disc-target-2", Token: "wrong-tok",
	})

	var resp Message
	if err := client.ReadJSON(&resp); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if resp.Type != "error" {
		t.Errorf("expected error for wrong token, got %s", resp.Type)
	}
}

func TestIntegrationDisconnectTargetNotFound(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	client := testClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{
		Type: "disconnect", Target: "nonexistent", Token: "tok",
	})

	var resp Message
	if err := client.ReadJSON(&resp); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if resp.Type != "error" {
		t.Errorf("expected error for nonexistent target, got %s", resp.Type)
	}
}

// --- Sidecar CLI helpers ---

func TestEndsWith(t *testing.T) {
	if !endsWith("https://app.fr/__rcmd/pair", "/__rcmd/pair") {
		t.Error("should match suffix")
	}
	if endsWith("https://app.fr/api", "/__rcmd/pair") {
		t.Error("should not match suffix")
	}
	if !endsWith("short", "short") {
		t.Error("should match exact string")
	}
	if endsWith("short", "longer-than-input") {
		t.Error("should not match when suffix is longer")
	}
}

func TestTrimSuffix(t *testing.T) {
	result := trimSuffix("https://app.fr/", "/")
	if result != "https://app.fr" {
		t.Errorf("expected 'https://app.fr', got '%s'", result)
	}
	result = trimSuffix("https://app.fr", "/")
	if result != "https://app.fr" {
		t.Errorf("expected 'https://app.fr', got '%s'", result)
	}
}

// --- Pair listener struct ---

func TestPairListenerStruct(t *testing.T) {
	rc := &relayClient{name: "test"}
	pl := &pairListener{
		conn:                 rc,
		requireActivationKey: true,
	}
	if pl.conn != rc {
		t.Error("conn should be set")
	}
	if !pl.requireActivationKey {
		t.Error("requireActivationKey should be true")
	}

	pl2 := &pairListener{conn: rc, requireActivationKey: false}
	if pl2.requireActivationKey {
		t.Error("requireActivationKey should be false")
	}
}

// --- Protocol fields ---

func TestMessageActivationKeyFields(t *testing.T) {
	msg := &Message{
		Type:                 "pair_listen",
		Code:                 "test-code",
		RequireActivationKey: true,
		ActivationKey:        "test-key",
	}
	if !msg.RequireActivationKey {
		t.Error("RequireActivationKey should be true")
	}
	if msg.ActivationKey != "test-key" {
		t.Errorf("ActivationKey = %q", msg.ActivationKey)
	}

	// Verify JSON round-trip preserves the fields
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.RequireActivationKey {
		t.Error("RequireActivationKey lost in JSON round-trip")
	}
	if decoded.ActivationKey != "test-key" {
		t.Errorf("ActivationKey lost in round-trip: %q", decoded.ActivationKey)
	}
}

func TestMessageActivationKeyOmitEmpty(t *testing.T) {
	// When fields are zero-value, they should be omitted from JSON
	msg := &Message{Type: "pair_listen", Code: "test"}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	str := string(data)
	if containsStr(str, "require_activation_key") {
		t.Error("RequireActivationKey should be omitted when false")
	}
	if containsStr(str, "activation_key") {
		t.Error("ActivationKey should be omitted when empty")
	}
}

// --- Relay key management CLI ---

func TestRelayKeyManagementFlow(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("RCMD_TEST_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("RCMD_TEST_CONFIG_DIR")
	resetActivationKeyCacheForTest()

	// Initially no keys
	keys := activationKeys.list()
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}

	// Add keys
	addActivationKey("key-alpha")
	addActivationKey("key-beta")
	resetActivationKeyCacheForTest()

	keys = activationKeys.list()
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}

	// Remove one
	removeActivationKey("key-alpha")
	resetActivationKeyCacheForTest()

	keys = activationKeys.list()
	if len(keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(keys))
	}
	if keys[0] != "key-beta" {
		t.Errorf("expected key-beta, got %s", keys[0])
	}
}

func TestRelayKeyFilePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("RCMD_TEST_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("RCMD_TEST_CONFIG_DIR")
	resetActivationKeyCacheForTest()

	// Add a key via the CLI function
	addActivationKey("persisted-key")
	resetActivationKeyCacheForTest()

	// Verify the file exists and contains the key
	data, err := os.ReadFile(filepath.Join(tmpDir, "activation-keys.json"))
	if err != nil {
		t.Fatalf("file should exist: %v", err)
	}

	var keys []string
	json.Unmarshal(data, &keys)

	found := false
	for _, k := range keys {
		if k == "persisted-key" {
			found = true
		}
	}
	if !found {
		t.Error("persisted-key not found in file")
	}
}

// --- Daemon activation key cleanup on pair_confirmed ---

func TestDaemonActivationKeyCleanupOnPairConfirmed(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("RCMD_TEST_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("RCMD_TEST_CONFIG_DIR")

	// Save an activation key
	saveActivationKey("temp-key")
	loaded, err := loadActivationKey()
	if err != nil {
		t.Fatal(err)
	}
	if loaded != "temp-key" {
		t.Fatalf("expected temp-key, got %s", loaded)
	}

	// Simulate pair_confirmed cleanup
	deleteActivationKey()
	deletePairCode()

	// Key should be gone
	_, err = loadActivationKey()
	if err == nil {
		t.Error("activation key should be deleted after pair_confirmed")
	}
}

// --- Helpers ---

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
