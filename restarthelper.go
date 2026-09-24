package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The restart helper replaces a running daemon or relay that can neither
// re-exec itself (Windows; builds before 2.6.0) nor be restarted by a
// supervisor. `daemon update` spawns it detached — outside the process tree
// that runs the update, which is usually the daemon being replaced — and
// returns at once so its result reaches the caller. The helper then:
//
//  1. waits a few seconds (the update's result is delivered first),
//  2. stops the old process and starts the same command line again,
//  3. waits for the new process to log "Registered as" (daemon) or keep
//     running (relay); if it doesn't, restores <exe>.bak and starts that.
//
// Nodes reachable only through remotecmd (radioalto, rfs_dev_ecobox) must
// never be left without a running daemon, hence the automatic rollback.

// helperExecutable is the binary the helper runs as; tests point it at a
// real build (os.Executable is the test binary there).
var helperExecutable = os.Executable

// spawnRestartHelper starts `<exe> daemon restart-helper` detached for p.
func spawnRestartHelper(p runningProc, kind string) (logPath string, err error) {
	self, err := helperExecutable()
	if err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-150405")
	logPath = filepath.Join(os.TempDir(), fmt.Sprintf("rcmd-restart-%d-%s.log", p.PID, stamp))
	args := []string{"daemon", "restart-helper",
		"--pid", strconv.Itoa(p.PID), "--exe", p.Exe, "--kind", kind, "--log", logPath, "--"}
	args = append(args, p.Args...)
	return logPath, spawnDetached(self, args, logPath)
}

func handleRestartHelper(args []string) {
	fs := flag.NewFlagSet("daemon restart-helper", flag.ExitOnError)
	pid := fs.Int("pid", 0, "process to replace")
	exe := fs.String("exe", "", "binary path (its .bak is the rollback)")
	kind := fs.String("kind", "daemon", "daemon or relay")
	logPath := fs.String("log", "", "helper log file")
	delay := fs.Duration("delay", 3*time.Second, "wait before stopping the old process")
	wait := fs.Duration("wait", 25*time.Second, "how long the new process has to come up")
	fs.Parse(args)
	argv := fs.Args()
	if *pid == 0 || *exe == "" || len(argv) == 0 {
		fail(ExitConfigError, "missing_argument", "restart-helper needs --pid, --exe and the command line after --")
	}
	if f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		log.SetOutput(f)
	}
	// The original process's environment and working directory, when the
	// OS exposes them (Linux); otherwise ours, which the update command
	// inherited from the daemon that ran it.
	env, dir := processEnvDir(*pid)
	if dir == "" {
		dir = filepath.Dir(*exe)
	}
	if env == nil {
		env = os.Environ()
	}

	log.Printf("helper: replacing %s pid %d with: %s", *kind, *pid, strings.Join(argv, " "))
	time.Sleep(*delay)
	stopProcess(*pid)

	if newPID, ok := startAndConfirm(argv, env, dir, *kind, *logPath, *wait); ok {
		log.Printf("RESULT: new %s running, pid %d", *kind, newPID)
		return
	} else if newPID > 0 {
		stopProcess(newPID)
	}

	bak := *exe + ".bak"
	if _, err := os.Stat(bak); err != nil {
		log.Printf("RESULT: FAILED and no %s to roll back to", bak)
		return
	}
	log.Printf("rolling back: restoring %s", bak)
	os.Rename(*exe, *exe+".failed")
	if err := copyFile(bak, *exe); err != nil {
		log.Printf("RESULT: ROLLBACK FAILED: restore: %v", err)
		return
	}
	if newPID, ok := startAndConfirm(argv, env, dir, *kind, *logPath, *wait); ok {
		log.Printf("RESULT: rolled back, previous binary running, pid %d", newPID)
	} else {
		log.Printf("RESULT: ROLLBACK FAILED: previous binary did not come up")
	}
}

// startAndConfirm starts argv detached with its output in <helperlog>.out
// and waits until it has registered (daemon) or stayed up (relay).
func startAndConfirm(argv, env []string, dir, kind, helperLog string, wait time.Duration) (int, bool) {
	out := helperLog + ".out"
	os.Remove(out)
	pid, err := startDetached(argv, env, dir, out)
	if err != nil {
		log.Printf("start failed: %v", err)
		return 0, false
	}
	log.Printf("started pid %d (output: %s)", pid, out)
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if !processAlive(pid) {
			log.Printf("pid %d exited early", pid)
			return pid, false
		}
		if kind == "daemon" {
			if b, _ := os.ReadFile(out); strings.Contains(string(b), "Registered as") {
				return pid, true
			}
		}
	}
	// A relay has no registration to wait for: still running is success.
	return pid, kind == "relay" && processAlive(pid)
}

// stopProcess ends pid and waits (up to 10s) until it is gone.
func stopProcess(pid int) {
	terminateProcess(pid)
	for i := 0; i < 100 && processAlive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
}

// processEnvDir returns pid's environment and working directory where the
// OS exposes them (/proc on Linux).
func processEnvDir(pid int) ([]string, string) {
	if runtime.GOOS != "linux" {
		return nil, ""
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return nil, ""
	}
	var env []string
	for _, kv := range strings.Split(string(raw), "\x00") {
		if kv != "" {
			env = append(env, kv)
		}
	}
	dir, _ := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	return env, dir
}

// zombie reports whether a /proc/<pid>/stat line is in state Z.
func zombie(stat string) bool {
	if i := strings.LastIndex(stat, ")"); i >= 0 && i+2 < len(stat) {
		return stat[i+2] == 'Z'
	}
	return false
}
