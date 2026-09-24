package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Fleet version visibility. Nodes drift: a daemon installed months ago keeps
// running while clients move on, and a newer client then fails in confusing
// ways against it (stdin silently dropped, cp hanging). list-targets records
// each daemon's version so the drift is visible, and the commands that depend
// on newer daemons warn before they misbehave.

// daemonVersionProbe asks the running daemon binary for its version. On a
// POSIX shell $PPID is the daemon that spawned `sh -c`, and /proc/<pid>/exe
// is the exact binary it runs (even if the file on disk was replaced). There
// are deliberately no redirections: under Windows cmd this just fails with
// "not recognized" and touches nothing; the node then shows no version.
const daemonVersionProbe = `/proc/$PPID/exe version`

// minDaemonVersion is the oldest daemon that supports each feature.
var minDaemonVersion = map[string]string{
	"stdin": "1.8.0", // cd97f46: stdin forwarding for exec
	"cp":    "1.8.0", // cd97f46: binary WebSocket frames for cp
}

// parseDaemonVersion extracts the version from `version` output such as
// "remotecmd-cli version 2.5.0" or "rcmd version 1.0.0-cloud".
func parseDaemonVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "version" && versionNumbers(f[i+1]) != nil {
				return f[i+1]
			}
		}
	}
	return ""
}

// versionNumbers returns the numeric major/minor/patch of "v2.5.0-cloud",
// or nil if s does not start with a number.
func versionNumbers(s string) []int {
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+ "); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	nums := make([]int, 3)
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return nil
		}
		nums[i] = n
	}
	return nums
}

// versionLess reports a < b with semver ordering: numbers first, then a
// pre-release ("2.6.0-dev+abc") sorts before its release ("2.6.0"). Build
// metadata after "+" is ignored. Unparseable versions compare as not-less,
// so an unknown version never triggers a warning or an update.
func versionLess(a, b string) bool {
	x, y := versionNumbers(a), versionNumbers(b)
	if x == nil || y == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return isPrerelease(a) && !isPrerelease(b)
}

func isPrerelease(v string) bool {
	if i := strings.Index(v, "+"); i >= 0 {
		v = v[:i]
	}
	return strings.Contains(v, "-")
}

// knownDaemonVersion returns the last version list-targets recorded for a
// target alias, or "" if it was never seen.
func knownDaemonVersion(alias string) string {
	cache, err := loadHealthCache()
	if err != nil {
		return ""
	}
	return cache.Targets[alias].Version
}

// stampVersion marks a daemon-built message with this daemon's version.
func stampVersion(m *Message) *Message {
	m.DaemonVersion = Version
	return m
}

// noteDaemonVersion records a version a target reported on a normal result,
// so the cache (and the feature warnings that read it) follow upgrades
// without waiting for the next health probe. Writes only on change.
func noteDaemonVersion(alias, v string) {
	if alias == "" || v == "" {
		return
	}
	cache, err := loadHealthCache()
	if err != nil || cache.Targets[alias].Version == v {
		return
	}
	h := cache.Targets[alias]
	h.Version = v
	cache.Targets[alias] = h
	_ = saveHealthCache(cache)
}

// warnOut is where feature warnings go; tests swap it.
var warnOut io.Writer = os.Stderr

// warnIfDaemonTooOld prints a warning when the target's recorded daemon
// version predates feature. It never blocks: the cache may be stale.
func warnIfDaemonTooOld(alias, feature, consequence string) {
	min := minDaemonVersion[feature]
	v := knownDaemonVersion(alias)
	if v == "" || min == "" || !versionLess(v, min) {
		return
	}
	fmt.Fprintf(warnOut, "Warning: %s runs daemon v%s; %s needs v%s or newer, so %s. Upgrade the daemon on %s.\n",
		alias, v, feature, min, consequence, alias)
}
