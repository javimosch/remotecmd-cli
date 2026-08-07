package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// activationKeysPath returns the path to the activation keys file.
// Lives in ~/.remotecmd/activation-keys.json alongside the relay config.
func activationKeysPath() string {
	if override := os.Getenv("RCMD_TEST_CONFIG_DIR"); override != "" {
		return filepath.Join(override, "activation-keys.json")
	}
	return filepath.Join(configDir(), "activation-keys.json")
}

// activationKeyStore provides hot-read access to activation keys.
// The file is re-read on every validation call so keys can be
// added/removed without restarting the relay.
type activationKeyStore struct {
	mu       sync.RWMutex
	cached   []string
	modTime  int64
	fileSize int64
}

var activationKeys = &activationKeyStore{}

// isValid checks whether a key is in the activation keys file.
// Re-reads the file if it has changed (stat-based, no watcher).
func (s *activationKeyStore) isValid(key string) bool {
	if key == "" {
		return false
	}
	s.reload()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.cached {
		if k == key {
			return true
		}
	}
	return false
}

// reload re-reads the file only if it changed (stat check).
func (s *activationKeyStore) reload() {
	info, err := os.Stat(activationKeysPath())
	if err != nil {
		// File doesn't exist — clear cache
		s.mu.Lock()
		s.cached = nil
		s.mu.Unlock()
		return
	}

	// Skip if unchanged since last read
	if info.ModTime().UnixNano() == s.modTime && info.Size() == s.fileSize {
		return
	}

	data, err := os.ReadFile(activationKeysPath())
	if err != nil {
		return
	}

	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		// Malformed file — treat as empty
		keys = nil
	}

	s.mu.Lock()
	s.cached = keys
	s.modTime = info.ModTime().UnixNano()
	s.fileSize = info.Size()
	s.mu.Unlock()
}

// list returns the current keys (for the list-keys CLI command).
func (s *activationKeyStore) list() []string {
	s.reload()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.cached))
	copy(out, s.cached)
	return out
}

// addKey appends a key to the file (CLI: relay add-key).
func addActivationKey(key string) error {
	keys := activationKeys.list()
	for _, k := range keys {
		if k == key {
			return nil // already exists, idempotent
		}
	}
	keys = append(keys, key)
	return saveActivationKeys(keys)
}

// removeKey deletes a key from the file (CLI: relay remove-key).
func removeActivationKey(key string) error {
	keys := activationKeys.list()
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k != key {
			out = append(out, k)
		}
	}
	return saveActivationKeys(out)
}

func saveActivationKeys(keys []string) error {
	if err := ensureConfigDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(activationKeysPath(), data, 0600)
}
