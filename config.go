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
	Groups  map[string][]string      `json:"groups,omitempty"`
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
			return &Config{Targets: make(map[string]TargetConfig), Groups: make(map[string][]string)}, nil
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
	if cfg.Groups == nil {
		cfg.Groups = make(map[string][]string)
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
	} else {
		for name, tgt := range cfg.Targets {
			masked := tgt.Token
			if len(masked) > 4 {
				masked = masked[:4] + "..."
			}
			if tgt.RelayName != "" && tgt.RelayName != name {
				fmt.Printf("%s → %s (token: %s)\n", name, tgt.RelayName, masked)
			} else {
				fmt.Printf("%s (token: %s)\n", name, masked)
			}
		}
	}

	if len(cfg.Groups) > 0 {
		fmt.Println()
		fmt.Println("Groups:")
		for name, members := range cfg.Groups {
			fmt.Printf("  %s: %s\n", name, strings.Join(members, ", "))
		}
	}
	return nil
}

// Group management
func groupCreate(name string, targets []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, exists := cfg.Groups[name]; exists {
		return fmt.Errorf("group %q already exists", name)
	}
	// Validate all targets exist
	for _, t := range targets {
		if _, ok := cfg.Targets[t]; !ok {
			return fmt.Errorf("unknown target %q (add it first via add-target)", t)
		}
	}
	cfg.Groups[name] = targets
	return saveConfig(cfg)
}

func groupDelete(name string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, exists := cfg.Groups[name]; !exists {
		return fmt.Errorf("group %q not found", name)
	}
	delete(cfg.Groups, name)
	return saveConfig(cfg)
}

func groupAddTargets(name string, targets []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	members, exists := cfg.Groups[name]
	if !exists {
		return fmt.Errorf("group %q not found", name)
	}
	// Validate all targets exist
	for _, t := range targets {
		if _, ok := cfg.Targets[t]; !ok {
			return fmt.Errorf("unknown target %q", t)
		}
	}
	// Avoid duplicates
	memberSet := make(map[string]bool)
	for _, m := range members {
		memberSet[m] = true
	}
	for _, t := range targets {
		if !memberSet[t] {
			members = append(members, t)
		}
	}
	cfg.Groups[name] = members
	return saveConfig(cfg)
}

func groupRemoveTargets(name string, targets []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	members, exists := cfg.Groups[name]
	if !exists {
		return fmt.Errorf("group %q not found", name)
	}
	keep := make([]string, 0, len(members))
	exclude := make(map[string]bool)
	for _, t := range targets {
		exclude[t] = true
	}
	for _, m := range members {
		if !exclude[m] {
			keep = append(keep, m)
		}
	}
	cfg.Groups[name] = keep
	return saveConfig(cfg)
}

func groupList() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Groups) == 0 {
		fmt.Println("No groups configured")
		return nil
	}
	for name, members := range cfg.Groups {
		fmt.Printf("%s: %s\n", name, strings.Join(members, ", "))
	}
	return nil
}

func resolveTargets(groupOrTargets string, isGroup bool) ([]string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	if isGroup {
		members, ok := cfg.Groups[groupOrTargets]
		if !ok {
			return nil, fmt.Errorf("group %q not found", groupOrTargets)
		}
		return members, nil
	}
	// Comma-separated list of targets
	targets := strings.Split(groupOrTargets, ",")
	for i, t := range targets {
		targets[i] = strings.TrimSpace(t)
		if _, ok := cfg.Targets[targets[i]]; !ok {
			return nil, fmt.Errorf("unknown target %q (add it first via add-target)", targets[i])
		}
	}
	return targets, nil
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
