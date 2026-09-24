package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// `daemon update` / `relay daemon update`: update the binary that the
// *running* daemon or relay executes — not whatever remotecmd-cli is first
// on PATH (on rbm4 that was a stale /usr/local/bin copy) — then restart the
// process on it.
//
// Restart strategy, per process:
//   - reexec:  the running binary supports SIGUSR2 (its help-json lists
//     "daemon update"): it re-executes itself in place, same PID, after
//     in-flight commands finish. Works under systemd, user units and nohup.
//   - systemd: an older binary under a systemd (user) unit: a transient
//     timer restarts the unit 2s later, outside the process being replaced,
//     so this command's own result still gets back through the relay.
//   - manual:  an older binary with no supervisor: the new binary is on
//     disk, but the process must be restarted by hand this one last time.

type runningProc struct {
	PID       int      `json:"pid"`
	Exe       string   `json:"exe"`
	Args      []string `json:"-"`
	Unit      string   `json:"unit,omitempty"`
	UserUnit  bool     `json:"user_unit,omitempty"`
	Version   string   `json:"version"`
	CanReexec bool     `json:"can_reexec"`
}

// matchesProc reports whether argv is a `daemon start` (kind "daemon") or
// `relay daemon start` (kind "relay") invocation, optionally with --name.
func matchesProc(argv []string, kind, name string) bool {
	want := []string{"daemon", "start"}
	if kind == "relay" {
		want = []string{"relay", "daemon", "start"}
	}
	if len(argv) < len(want)+1 {
		return false
	}
	for i, w := range want {
		if argv[i+1] != w {
			return false
		}
	}
	if kind == "daemon" && name != "" {
		rest := argv[len(want)+1:]
		for i, a := range rest {
			if (a == "-name" || a == "--name") && i+1 < len(rest) && rest[i+1] == name {
				return true
			}
			if a == "-name="+name || a == "--name="+name {
				return true
			}
		}
		return false
	}
	return true
}

// unitFromCgroup extracts the systemd unit owning a process from the
// contents of /proc/<pid>/cgroup, and whether it is a user unit.
//
//	0::/system.slice/remotecmd-daemon.service                → remotecmd-daemon.service, system
//	0::/user.slice/user-0.slice/user@0.service/app.slice/remotecmd.service → remotecmd.service, user
//	0::/user.slice/user-0.slice/session-12.scope             → "" (plain login session)
func unitFromCgroup(cgroup string) (unit string, user bool) {
	for _, line := range strings.Split(cgroup, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		segs := strings.Split(parts[2], "/")
		for i := len(segs) - 1; i >= 0; i-- {
			s := segs[i]
			if strings.HasSuffix(s, ".service") && !strings.HasPrefix(s, "user@") {
				for _, p := range segs[:i] {
					if strings.HasPrefix(p, "user@") {
						return s, true
					}
				}
				return s, false
			}
		}
	}
	return "", false
}

// findRunning lists our running daemons (or relays). On Linux it scans
// /proc, which also finds systemd-managed processes that have no PID
// file; elsewhere it falls back to the -daemon PID file.
func findRunning(kind, name string) []runningProc {
	if entries, err := os.ReadDir("/proc"); err == nil {
		var out []runningProc
		self := os.Getpid()
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil || pid == self {
				continue
			}
			raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
			if err != nil || len(raw) == 0 {
				continue
			}
			argv := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
			if !matchesProc(argv, kind, name) || !inOurNamespaces(pid) {
				continue
			}
			exe, err := os.Readlink(filepath.Join("/proc", e.Name(), "exe"))
			if err != nil {
				continue // not ours to manage (other user)
			}
			p := runningProc{PID: pid, Exe: strings.TrimSuffix(exe, " (deleted)"), Args: argv}
			if cg, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cgroup")); err == nil {
				p.Unit, p.UserUnit = supervisingUnit(pid, string(cg))
			}
			probeRunning(&p, filepath.Join("/proc", e.Name(), "exe"))
			out = append(out, p)
		}
		return out
	}
	pidFile := relayPidFile
	if kind == "daemon" {
		pidFile = namedDaemonPidFile(name)
	}
	if ok, pid := isRunning(pidFile); ok {
		exe, _ := os.Executable()
		p := runningProc{PID: pid, Exe: exe}
		probeRunning(&p, exe)
		return []runningProc{p}
	}
	return nil
}

// readNamespace returns the namespace identity of a process, e.g.
// "pid:[4026531836]". A var so tests can fake container processes.
var readNamespace = func(pid, ns string) (string, error) {
	return os.Readlink(filepath.Join("/proc", pid, "ns", ns))
}

// inOurNamespaces reports whether pid shares our PID and mount namespaces.
// A Proxmox/LXC host sees its containers' processes in /proc (pve2 listed
// rbm20's and rbm21's daemons as its own); updating or signalling those
// would reach into another machine. Unreadable namespaces count as foreign.
func inOurNamespaces(pid int) bool {
	for _, ns := range []string{"pid", "mnt"} {
		ours, err1 := readNamespace("self", ns)
		theirs, err2 := readNamespace(strconv.Itoa(pid), ns)
		if err1 != nil || err2 != nil || ours != theirs {
			return false
		}
	}
	return true
}

// probeRunning asks the binary the process actually runs (/proc/<pid>/exe
// works even if the file was replaced since) for its version, and whether it
// knows the SIGUSR2 in-place restart (added together with `daemon update`).
func probeRunning(p *runningProc, runningBinary string) {
	p.Version = parseDaemonVersion(runQuiet(runningBinary, "version"))
	p.CanReexec = strings.Contains(runQuiet(runningBinary, "help-json"), `"daemon update"`)
}

// supervisingUnit returns the systemd unit that supervises pid, if any. A
// process launched from inside another unit (e.g. a relay started through
// the node's daemon) sits in that unit's cgroup without being its main
// process; restarting that unit would kill it, not restart it.
func supervisingUnit(pid int, cgroup string) (string, bool) {
	unit, user := unitFromCgroup(cgroup)
	if unit == "" || unitMainPID(unit, user) != pid {
		return "", false
	}
	return unit, user
}

// unitMainPID returns the main PID systemd tracks for unit (0 if unknown).
// A var so tests can stub systemd.
var unitMainPID = func(unit string, user bool) int {
	args := []string{"show", "-p", "MainPID", "--value", unit}
	if user {
		args = append([]string{"--user"}, args...)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(runQuiet("systemctl", args...)))
	return pid
}

func runQuiet(bin string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = &out
	cmd.Env = append(os.Environ(), "RCMD_NO_NUDGE=1")
	_ = cmd.Run()
	return out.String()
}

type restartResult struct {
	PID    int    `json:"pid"`
	Method string `json:"method"` // reexec | systemd | manual
	OK     bool   `json:"ok"`
	Note   string `json:"note,omitempty"`
}

// restartProc restarts p onto the binary now on disk.
func restartProc(p runningProc) restartResult {
	switch {
	case p.CanReexec:
		if err := sendRestartSignal(p.PID); err != nil {
			return restartResult{p.PID, "reexec", false, err.Error()}
		}
		return restartResult{p.PID, "reexec", true, "re-executes in place once in-flight commands finish"}
	case p.Unit != "":
		out, err := exec.Command("systemd-run", restartTimerArgs(p)...).CombinedOutput()
		if err != nil {
			return restartResult{p.PID, "systemd", false, fmt.Sprintf("%v: %s", err, strings.TrimSpace(string(out)))}
		}
		return restartResult{p.PID, "systemd", true, "unit " + p.Unit + " restarts in 2s"}
	}
	return restartResult{p.PID, "manual", false,
		"this process predates in-place restart and has no supervisor: restart it once by hand; later updates restart it automatically"}
}

// restartTimerArgs builds the systemd-run call that restarts p's unit 2s
// from now. The transient timer runs outside the unit being restarted, so
// this command finishes and its result is delivered first. AccuracySec is
// essential: timers default to 1min accuracy, so "2s" fired 32s late on
// mikavm3.
func restartTimerArgs(p runningProc) []string {
	args := []string{"--on-active=2", "--timer-property=AccuracySec=100ms",
		"--quiet", "--collect", "--unit=remotecmd-restart-" + strconv.Itoa(p.PID)}
	ctl := []string{"systemctl", "restart", p.Unit}
	if p.UserUnit {
		args = append([]string{"--user"}, args...)
		ctl = []string{"systemctl", "--user", "restart", p.Unit}
	}
	return append(args, ctl...)
}

func handleDaemonUpdate(args []string) { handleProcUpdate("daemon", args) }

func handleRelayDaemonUpdate(args []string) { handleProcUpdate("relay", args) }

func handleProcUpdate(kind string, args []string) {
	fsName := "daemon update"
	if kind == "relay" {
		fsName = "relay daemon update"
	}
	fs := flag.NewFlagSet(fsName, flag.ExitOnError)
	check := fs.Bool("check", false, "report what would change; exit 5 if an update is available")
	force := fs.Bool("force", false, "reinstall and restart even if already up to date")
	var name *string
	if kind == "daemon" {
		name = fs.String("name", "", "only the instance started with this --name")
	} else {
		empty := ""
		name = &empty
	}
	fs.Parse(args)

	procs := findRunning(kind, *name)
	if len(procs) == 0 {
		fail(ExitConfigError, "not_found", "no running "+kind+" found on this machine",
			"remotecmd-cli "+map[string]string{"daemon": "daemon", "relay": "relay daemon"}[kind]+" start", "remotecmd-cli update   (update this CLI binary only)")
	}

	rel, err := latestReleaseInfo()
	if err != nil {
		failErrCode(exitUpdateFail, fmt.Errorf("cannot reach GitHub: %w", err))
	}
	latest := strings.TrimPrefix(rel.TagName, "v")

	// One swap per binary: several processes may share one (rbm4 runs a
	// dk1 and a dk3 daemon from the same file).
	byExe := map[string][]runningProc{}
	var order []string
	for _, p := range procs {
		if _, seen := byExe[p.Exe]; !seen {
			order = append(order, p.Exe)
		}
		byExe[p.Exe] = append(byExe[p.Exe], p)
	}

	type binaryResult struct {
		Exe       string          `json:"exe"`
		OnDisk    string          `json:"on_disk"`
		Installed bool            `json:"installed"`
		Backup    string          `json:"backup,omitempty"`
		Processes []runningProc   `json:"processes"`
		Restarts  []restartResult `json:"restarts,omitempty"`
	}
	var results []binaryResult
	pending := false
	allOK := true

	for _, exe := range order {
		br := binaryResult{Exe: exe, OnDisk: parseDaemonVersion(runQuiet(exe, "version")), Processes: byExe[exe]}
		needInstall := *force || versionLess(br.OnDisk, latest)
		target := br.OnDisk
		if needInstall {
			target = latest
		}
		var toRestart []runningProc
		for _, p := range br.Processes {
			if *force || needInstall || p.Version != target {
				toRestart = append(toRestart, p)
			}
		}
		if needInstall || len(toRestart) > 0 {
			pending = true
		}
		if *check {
			results = append(results, br)
			continue
		}
		if needInstall {
			fmt.Fprintf(os.Stderr, "[update] %s: %s → %s\n", exe, br.OnDisk, latest)
			bak, err := installRelease(rel, exe)
			if err != nil {
				failInstall(err)
			}
			br.Installed, br.Backup = true, bak
		}
		for _, p := range toRestart {
			r := restartProc(p)
			allOK = allOK && r.OK
			br.Restarts = append(br.Restarts, r)
		}
		results = append(results, br)
	}

	out := map[string]any{"ok": allOK, "kind": kind, "latest": latest, "binaries": results}
	if *check {
		out["ok"] = true
		out["up_to_date"] = !pending
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	switch {
	case *check && pending:
		osExit(exitUpdateAvail)
	case !*check && !allOK:
		osExit(ExitConfigError) // installed, but a restart needs a human
	}
}
