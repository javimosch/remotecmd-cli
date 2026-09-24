package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// healthCheckInterval is the minimum age after which a cached target health
// entry is considered stale and re-probed automatically by listTargetsSmart.
const healthCheckInterval = 1 * time.Hour

// pingTimeout is the per-target timeout used when probing with `hostname`.
const pingTimeout = 8

// pingCmd is the command sent to each target to confirm liveness and capture
// a stable identity string. `hostname` is universally available and cheap.
const pingCmd = "hostname"

// TargetHealth is the per-target cached health entry.
type TargetHealth struct {
	LastCheck time.Time `json:"last_check"` // when we last probed this target
	LastSeen  time.Time `json:"last_seen"`  // when the target last answered successfully
	Status    string    `json:"status"`     // "up" | "down" | "unknown"
	Hostname  string    `json:"hostname"`   // hostname reported by the target (when up)
	LatencyMs int64     `json:"latency_ms"` // round-trip latency of the last successful probe
	Error     string    `json:"error,omitempty"`
	// Version is the daemon build the target runs: self-reported on
	// results (daemon_version), else probed via /proc/$PPID/exe.
	Version string `json:"version,omitempty"`
}

// HealthCache is the on-disk cache of per-target health probes.
type HealthCache struct {
	Targets map[string]TargetHealth `json:"targets"`
}

func healthCachePath() string {
	return filepath.Join(configDir(), "health.json")
}

func loadHealthCache() (*HealthCache, error) {
	data, err := os.ReadFile(healthCachePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &HealthCache{Targets: make(map[string]TargetHealth)}, nil
		}
		return nil, err
	}
	var c HealthCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Targets == nil {
		c.Targets = make(map[string]TargetHealth)
	}
	return &c, nil
}

func saveHealthCache(c *HealthCache) error {
	if err := ensureConfigDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(healthCachePath(), data, 0600)
}

// pingTargets probes the given local target aliases in one multi-exec round
// trip and returns a map keyed by the local alias name. Targets that fail to
// resolve or that the relay reports as not connected are returned with
// Status=="down" rather than being dropped.
func pingTargets(targetAliases []string) map[string]TargetHealth {
	now := time.Now()
	results := make(map[string]TargetHealth, len(targetAliases))

	if len(targetAliases) == 0 {
		return results
	}

	resolved, tokens, err := resolveRelayTargets(targetAliases)
	if err != nil {
		for _, alias := range targetAliases {
			results[alias] = TargetHealth{LastCheck: now, Status: "down", Error: err.Error()}
		}
		return results
	}

	// aliasByRelay maps the relay-registered name back to the local alias so
	// we can attribute results correctly when RelayName differs from the key.
	aliasByRelay := make(map[string]string, len(targetAliases))
	for i, alias := range targetAliases {
		aliasByRelay[resolved[i]] = alias
	}

	start := time.Now()
	msg, err := multiExecRaw(resolved, tokens, pingCmd, pingTimeout)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		for _, alias := range targetAliases {
			results[alias] = TargetHealth{LastCheck: now, Status: "down", Error: err.Error()}
		}
		return results
	}

	for _, relayName := range resolved {
		alias := aliasByRelay[relayName]
		r, ok := msg.Results[relayName]
		if !ok || r == nil || r.OK == nil || !*r.OK {
			h := TargetHealth{
				LastCheck: now,
				Status:    "down",
				LatencyMs: elapsed,
			}
			if r != nil && r.Error != "" {
				h.Error = r.Error
			} else if !ok {
				h.Error = "no result from relay"
			}
			results[alias] = h
			continue
		}
		results[alias] = TargetHealth{
			LastCheck: now,
			LastSeen:  now,
			Status:    "up",
			Hostname:  strings.TrimSpace(r.Stdout),
			LatencyMs: elapsed,
			Version:   r.DaemonVersion,
		}
	}
	probeDaemonVersions(results, resolved, tokens, aliasByRelay)
	return results
}

// probeDaemonVersions fills in Version for targets that answered but did not
// self-report one — old daemons, or any daemon behind a relay that predates
// the daemon_version field. One extra round trip, only for those targets.
func probeDaemonVersions(results map[string]TargetHealth, resolved []string, tokens map[string]string, aliasByRelay map[string]string) {
	var need []string
	for _, relayName := range resolved {
		if h := results[aliasByRelay[relayName]]; h.Status == "up" && h.Version == "" {
			need = append(need, relayName)
		}
	}
	if len(need) == 0 {
		return
	}
	msg, err := multiExecRaw(need, tokens, daemonVersionProbe, pingTimeout)
	if err != nil || msg == nil {
		return
	}
	for _, relayName := range need {
		r := msg.Results[relayName]
		if r == nil || r.OK == nil || !*r.OK {
			continue
		}
		if v := parseDaemonVersion(r.Stdout); v != "" {
			alias := aliasByRelay[relayName]
			h := results[alias]
			h.Version = v
			results[alias] = h
		}
	}
}

// listTargetsSmart prints all configured targets with cached health info,
// auto-probing any target whose last check is older than healthCheckInterval
// (or that has never been probed). When refresh is true, every target is
// re-probed regardless of cache age. When noHealth is true (and not json),
// it falls back to the legacy headerless listTargets() view so existing
// scripts that grep the first token of each line keep working.
func listTargetsSmart(refresh, noHealth, jsonOut bool) error {
	if noHealth && !jsonOut {
		return listTargets()
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	cache, err := loadHealthCache()
	if err != nil {
		cache = &HealthCache{Targets: make(map[string]TargetHealth)}
	}

	// Determine which targets need probing.
	aliasNames := make([]string, 0, len(cfg.Targets))
	for name := range cfg.Targets {
		aliasNames = append(aliasNames, name)
	}
	sort.Strings(aliasNames)

	var toProbe []string
	if !noHealth {
		for _, alias := range aliasNames {
			h, has := cache.Targets[alias]
			stale := !has || h.LastCheck.IsZero() || time.Since(h.LastCheck) > healthCheckInterval
			if refresh || stale {
				toProbe = append(toProbe, alias)
			}
		}
	}

	if len(toProbe) > 0 {
		fresh := pingTargets(toProbe)
		for alias, h := range fresh {
			// Keep the last known version while a node is down or silent.
			if h.Version == "" {
				h.Version = cache.Targets[alias].Version
			}
			cache.Targets[alias] = h
		}
		_ = saveHealthCache(cache)
	}

	if jsonOut {
		return printHealthJSON(cfg, cache, aliasNames)
	}
	return printHealthTable(cfg, cache, aliasNames)
}

func printHealthJSON(cfg *Config, cache *HealthCache, aliasNames []string) error {
	type targetOut struct {
		Name      string `json:"name"`
		RelayName string `json:"relay_name,omitempty"`
		Status    string `json:"status"`
		Hostname  string `json:"hostname,omitempty"`
		SeenAgo   string `json:"seen_ago,omitempty"`
		LatencyMs int64  `json:"latency_ms,omitempty"`
		Error     string `json:"error,omitempty"`
		Version   string `json:"version,omitempty"`
		Outdated  bool   `json:"outdated,omitempty"`
	}
	out := struct {
		Targets []targetOut     `json:"targets"`
		Groups  map[string][]string `json:"groups,omitempty"`
	}{
		Groups: cfg.Groups,
	}
	for _, alias := range aliasNames {
		tgt := cfg.Targets[alias]
		h := cache.Targets[alias]
		entry := targetOut{
			Name:      alias,
			RelayName: tgt.RelayName,
			Status:    h.Status,
			Hostname:  h.Hostname,
			LatencyMs: h.LatencyMs,
			Error:     h.Error,
			Version:   h.Version,
			Outdated:  versionLess(h.Version, Version),
		}
		if !h.LastSeen.IsZero() {
			entry.SeenAgo = agoString(time.Since(h.LastSeen))
		}
		if entry.Status == "" {
			entry.Status = "unknown"
		}
		out.Targets = append(out.Targets, entry)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func printHealthTable(cfg *Config, cache *HealthCache, aliasNames []string) error {
	if len(aliasNames) == 0 {
		fmt.Println("No targets configured")
	} else {
		fmt.Printf("%-22s %-7s %-18s %-10s %s\n", "TARGET", "STATUS", "SEEN", "VERSION", "HOSTNAME")
		fmt.Printf("%s\n", strings.Repeat("-", 81))
		for _, alias := range aliasNames {
			tgt := cfg.Targets[alias]
			h := cache.Targets[alias]

			displayName := alias
			if tgt.RelayName != "" && tgt.RelayName != alias {
				displayName = alias + " → " + tgt.RelayName
			}

			status := h.Status
			if status == "" {
				status = "unknown"
			}

			seen := "never"
			if !h.LastSeen.IsZero() {
				seen = agoString(time.Since(h.LastSeen))
			} else if h.Status == "down" && !h.LastCheck.IsZero() {
				seen = "checked " + agoString(time.Since(h.LastCheck))
			}

			hostname := h.Hostname
			if h.Status == "down" && h.Error != "" {
				hostname = h.Error
				if len(hostname) > 30 {
					hostname = hostname[:30] + "..."
				}
			}

			version := h.Version
			if version == "" {
				version = "?"
			} else if versionLess(version, Version) {
				version += "*" // older than this client
			}

			fmt.Printf("%-22s %-7s %-18s %-10s %s\n", displayName, status, seen, version, hostname)
		}
	}

	if len(aliasNames) > 0 {
		fmt.Printf("\nVERSION: * = older than this client (v%s), ? = not reported (Windows/macOS node, or not probed yet)\n", Version)
	}

	if len(cfg.Groups) > 0 {
		fmt.Println()
		fmt.Println("Groups:")
		groupNames := make([]string, 0, len(cfg.Groups))
		for name := range cfg.Groups {
			groupNames = append(groupNames, name)
		}
		sort.Strings(groupNames)
		for _, name := range groupNames {
			fmt.Printf("  %s: %s\n", name, strings.Join(cfg.Groups[name], ", "))
		}
	}
	return nil
}

// humanDuration renders a duration as a compact human-readable string
// ("just now", "45s", "12min", "3h", "2d").
func humanDuration(d time.Duration) string {
	switch {
	case d < 30*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dmin", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// agoString renders a duration as a "X ago" phrase, collapsing the "just now"
// case so we never produce the awkward "just now ago".
func agoString(d time.Duration) string {
	h := humanDuration(d)
	if h == "just now" {
		return "just now"
	}
	return h + " ago"
}
