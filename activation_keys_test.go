package main

import (
	"os"
	"path/filepath"
	"testing"
)

func resetActivationKeyCache() {
	activationKeys.mu.Lock()
	activationKeys.cached = nil
	activationKeys.modTime = 0
	activationKeys.fileSize = 0
	activationKeys.mu.Unlock()
}

func TestActivationKeysValid(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("RCMD_TEST_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("RCMD_TEST_CONFIG_DIR")
	resetActivationKeyCache()

	// No file — no keys valid
	if activationKeys.isValid("test-key") {
		t.Error("expected no keys to be valid when file doesn't exist")
	}

	// Write a keys file
	data := `["key-one","key-two","key-three"]`
	if err := os.WriteFile(filepath.Join(tmpDir, "activation-keys.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	resetActivationKeyCache()

	if !activationKeys.isValid("key-one") {
		t.Error("key-one should be valid")
	}
	if !activationKeys.isValid("key-two") {
		t.Error("key-two should be valid")
	}
	if !activationKeys.isValid("key-three") {
		t.Error("key-three should be valid")
	}
	if activationKeys.isValid("key-four") {
		t.Error("key-four should not be valid")
	}
	if activationKeys.isValid("") {
		t.Error("empty key should not be valid")
	}
}

func TestActivationKeysHotReload(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("RCMD_TEST_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("RCMD_TEST_CONFIG_DIR")
	resetActivationKeyCache()

	keysPath := filepath.Join(tmpDir, "activation-keys.json")

	// Write initial keys
	os.WriteFile(keysPath, []byte(`["old-key"]`), 0600)
	resetActivationKeyCache()

	if !activationKeys.isValid("old-key") {
		t.Error("old-key should be valid")
	}

	// Rewrite file with new key (change content so stat differs)
	os.WriteFile(keysPath, []byte(`["new-key-extended"]`), 0600)

	if !activationKeys.isValid("new-key-extended") {
		t.Error("new-key-extended should be valid after hot reload")
	}
	if activationKeys.isValid("old-key") {
		t.Error("old-key should no longer be valid after hot reload")
	}
}

func TestAddRemoveActivationKey(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("RCMD_TEST_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("RCMD_TEST_CONFIG_DIR")
	resetActivationKeyCache()

	// Add a key
	if err := addActivationKey("test-add-key"); err != nil {
		t.Fatal(err)
	}

	resetActivationKeyCache()

	if !activationKeys.isValid("test-add-key") {
		t.Error("test-add-key should be valid after addActivationKey")
	}

	// Add same key (idempotent)
	if err := addActivationKey("test-add-key"); err != nil {
		t.Fatal(err)
	}

	keys := activationKeys.list()
	count := 0
	for _, k := range keys {
		if k == "test-add-key" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 instance of key, got %d", count)
	}

	// Remove the key
	if err := removeActivationKey("test-add-key"); err != nil {
		t.Fatal(err)
	}

	resetActivationKeyCache()

	if activationKeys.isValid("test-add-key") {
		t.Error("test-add-key should not be valid after removeActivationKey")
	}
}

func TestActivationKeyDaemonSideStorage(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("RCMD_TEST_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("RCMD_TEST_CONFIG_DIR")

	// Save and load
	if err := saveActivationKey("daemon-key-123"); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadActivationKey()
	if err != nil {
		t.Fatal(err)
	}
	if loaded != "daemon-key-123" {
		t.Errorf("expected 'daemon-key-123', got '%s'", loaded)
	}

	// Delete
	deleteActivationKey()

	_, err = loadActivationKey()
	if err == nil {
		t.Error("expected error after deleteActivationKey")
	}
}
