package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Relay   RelayConfig              `json:"relay"`
	Targets map[string]TargetConfig  `json:"targets"`
}

type RelayConfig struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

type TargetConfig struct {
	Token    string `json:"token"`
	RelayName string `json:"relay_name,omitempty"` // actual registered name on relay (if different from map key)
}

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".remotecmd")
}

func configPath() string {
	return filepath.Join(configDir(), "config.json")
}

func tokenPath() string {
	return filepath.Join(configDir(), "token")
}

func ensureConfigDir() error {
	return os.MkdirAll(configDir(), 0700)
}

func loadConfig() (*Config, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Targets: make(map[string]TargetConfig)}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Targets == nil {
		cfg.Targets = make(map[string]TargetConfig)
	}
	return &cfg, nil
}

func saveConfig(cfg *Config) error {
	if err := ensureConfigDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0600)
}

func addTarget(name, token string) error {
	return addTargetWithRelayName(name, token, "")
}

func addTargetWithRelayName(name, token, relayName string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	cfg.Targets[name] = TargetConfig{Token: token, RelayName: relayName}
	return saveConfig(cfg)
}

func removeTarget(name string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	delete(cfg.Targets, name)
	return saveConfig(cfg)
}

func listTargets() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Targets) == 0 {
		fmt.Println("No targets configured")
		return nil
	}
	for name, tgt := range cfg.Targets {
		masked := tgt.Token
		if len(masked) > 4 {
			masked = masked[:4] + "..."
		}
		fmt.Printf("%s (token: %s)\n", name, masked)
	}
	return nil
}

func setRelay(url, name string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	cfg.Relay = RelayConfig{URL: url, Name: name}
	return saveConfig(cfg)
}

func pairCodePath() string {
	return filepath.Join(configDir(), "pair_code")
}

func savePairCode(code string) error {
	if err := ensureConfigDir(); err != nil {
		return err
	}
	return os.WriteFile(pairCodePath(), []byte(code), 0600)
}

func loadPairCode() (string, error) {
	data, err := os.ReadFile(pairCodePath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func deletePairCode() {
	os.Remove(pairCodePath())
}

func loadToken() (string, error) {
	data, err := os.ReadFile(tokenPath())
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func saveToken(token string) error {
	if err := ensureConfigDir(); err != nil {
		return err
	}
	return os.WriteFile(tokenPath(), []byte(token), 0600)
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
