package main

import (
	"os"
	"testing"
)

// assertExitCode runs a function that calls osExit (which panics in tests)
// and verifies the exit code.
func assertExitCode(t *testing.T, want int, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("expected osExit(%d), but function did not exit", want)
			return
		}
		code, ok := r.(exitCodePanic)
		if !ok {
			t.Errorf("expected exitCodePanic, got %T: %v", r, r)
			return
		}
		if int(code) != want {
			t.Errorf("expected exit code %d, got %d", want, int(code))
		}
	}()

	old := osExit
	osExit = func(code int) { panic(exitCodePanic(code)) }
	defer func() { osExit = old }()

	fn()
}

// assertExitCodeSuccess runs a function that should NOT call osExit (exit 0).
func assertExitCodeSuccess(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r != nil {
			t.Errorf("function should not call osExit, but got panic: %v", r)
		}
	}()

	old := osExit
	osExit = func(code int) { panic(exitCodePanic(code)) }
	defer func() { osExit = old }()

	fn()
}

func setupTestHome(t *testing.T) func() {
	t.Helper()
	tmpDir, cleanup := setupTestConfig(t)
	_ = tmpDir
	return cleanup
}

func TestMainUnknownCommand(t *testing.T) {
	oldArgs := os.Args
	os.Args = []string{"remotecmd-cli", "nonexistent-command"}
	defer func() { os.Args = oldArgs }()

	assertExitCode(t, ExitConfigError, func() { main() })
}

func TestMainNoArgs(t *testing.T) {
	oldArgs := os.Args
	os.Args = []string{"remotecmd-cli"}
	defer func() { os.Args = oldArgs }()

	assertExitCode(t, ExitConfigError, func() { main() })
}

func TestMainVersion(t *testing.T) {
	oldArgs := os.Args
	os.Args = []string{"remotecmd-cli", "version"}
	defer func() { os.Args = oldArgs }()

	// Version just prints, does not call osExit — no panic means success (exit 0)
	assertExitCodeSuccess(t, func() { main() })
}

func TestMainHelp(t *testing.T) {
	oldArgs := os.Args
	os.Args = []string{"remotecmd-cli", "help"}
	defer func() { os.Args = oldArgs }()

	assertExitCodeSuccess(t, func() { main() })
}

func TestHandleAddTargetMissingArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleAddTarget([]string{})
	})
}

func TestHandleAddTargetMissingName(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleAddTarget([]string{"--token", "abc"})
	})
}

func TestHandleRemoveTargetMissingName(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleRemoveTarget([]string{})
	})
}

func TestHandleSetRelayMissingArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleSetRelay([]string{"--url", "http://r"})
	})
}

func TestHandleExecFlagsMissingArgs(t *testing.T) {
	// Legacy exec: --target and --cmd are required
	assertExitCode(t, ExitConfigError, func() {
		handleExecFlags([]string{})
	})
}

func TestHandleExecFlagsMissingTarget(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleExecFlags([]string{"--cmd", "uptime"})
	})
}

func TestHandleExecSubcommandMissingCmd(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleExecSubcommand([]string{"--target", "box"})
	})
}

func TestHandleGroupCreateMissingArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleGroupCreate([]string{})
	})
}

func TestHandleGroupCreateMissingTargets(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleGroupCreate([]string{"--name", "g"})
	})
}

func TestHandleGroupDeleteMissingName(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleGroupDelete([]string{})
	})
}

func TestHandleGroupAddMissingArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleGroupAdd([]string{})
	})
}

func TestHandleGroupRemoveMissingArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleGroupRemove([]string{})
	})
}

func TestHandleRelaySubcommandNoArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleRelaySubcommand([]string{})
	})
}

func TestHandleRelaySubcommandInvalid(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleRelaySubcommand([]string{"invalid"})
	})
}

func TestHandleDaemonSubcommandNoArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleDaemonSubcommand([]string{})
	})
}

func TestHandleDaemonSubcommandInvalid(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleDaemonSubcommand([]string{"invalid"})
	})
}

func TestHandleRelayDaemonSubcommandNoArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleRelayDaemon([]string{})
	})
}

func TestHandleRelayDaemonSubcommandInvalid(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleRelayDaemon([]string{"invalid"})
	})
}

func TestHandleDaemonSystemdSubcommandNoArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleDaemonSystemdSubcommand([]string{})
	})
}

func TestHandleDaemonSystemdSubcommandInvalid(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleDaemonSystemdSubcommand([]string{"invalid"})
	})
}

func TestHandleRelaySystemdSubcommandNoArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleRelaySystemdSubcommand([]string{})
	})
}

func TestHandleRelaySystemdSubcommandInvalid(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleRelaySystemdSubcommand([]string{"invalid"})
	})
}

func TestHandleClientSubcommandNoRelay(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	assertExitCode(t, ExitConfigError, func() {
		handleClientSubcommand([]string{})
	})
}

func TestHandleAliasSubcommandNoArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleAliasSubcommand([]string{})
	})
}

func TestHandleAliasSubcommandInvalid(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleAliasSubcommand([]string{"invalid"})
	})
}

func TestHandleGroupSubcommandNoArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleGroupSubcommand([]string{})
	})
}

func TestHandleGroupSubcommandInvalid(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleGroupSubcommand([]string{"invalid"})
	})
}

func TestHandlePairSubcommandNoArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handlePairSubcommand([]string{})
	})
}

func TestHandlePairSubcommandInvalid(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handlePairSubcommand([]string{"invalid"})
	})
}

func TestHandleCPMissingArgs(t *testing.T) {
	assertExitCode(t, ExitConfigError, func() {
		handleCP([]string{})
	})
}

func TestHandleRelayInstallSystemdNotRoot(t *testing.T) {
	// Without root, this should tell the user to use sudo
	assertExitCode(t, ExitConfigError, func() {
		handleRelayInstallSystemd()
	})
}
