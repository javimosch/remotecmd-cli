//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const schtasksName = "remotecmd-daemon"

// handleDaemonInstallSchtasks creates a Windows scheduled task that starts
// the daemon at system boot as SYSTEM. This is the Windows equivalent of
// "daemon systemd install".
//
// The task runs a wrapper batch file because schtasks.exe cannot handle
// spaces in command arguments directly.
//
// If RCMD_CONFIG_DIR is set in the current environment, it is embedded in
// the wrapper batch file so the SYSTEM daemon reads config from the same
// location as the user who installed it.
func handleDaemonInstallSchtasks() {
	binPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot get executable path: %v\n", err)
		osExit(ExitInternal)
	}

	binDir := filepath.Dir(binPath)
	wrapperPath := filepath.Join(binDir, "start-rcmd-daemon.bat")

	// Write the wrapper batch file.
	// If RCMD_CONFIG_DIR is set, embed it so the SYSTEM daemon finds the config.
	wrapperContent := "@echo off\r\n"
	if configDir := os.Getenv("RCMD_CONFIG_DIR"); configDir != "" {
		wrapperContent += fmt.Sprintf("set RCMD_CONFIG_DIR=%s\r\n", configDir)
	}
	wrapperContent += fmt.Sprintf("\"%s\" daemon start\r\n", binPath)

	if err := os.WriteFile(wrapperPath, []byte(wrapperContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: writing wrapper batch file: %v\n", err)
		osExit(ExitInternal)
	}

	fmt.Printf("Wrapper batch file: %s\n", wrapperPath)

	// Delete existing task if present (ignore errors)
	exec.Command("schtasks", "/delete", "/tn", schtasksName, "/f").Run()

	// Create the scheduled task: start at boot, run as SYSTEM, highest privileges
	cmd := exec.Command("schtasks", "/create",
		"/tn", schtasksName,
		"/tr", wrapperPath,
		"/sc", "onstart",
		"/ru", "SYSTEM",
		"/rl", "highest",
		"/f",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating scheduled task: %v\n", err)
		osExit(ExitInternal)
	}

	// Disable battery restrictions and time limit via PowerShell
	psScript := fmt.Sprintf(
		"$t = Get-ScheduledTask -TaskName '%s'; "+
			"$s = $t.Settings; "+
			"$s.DisallowStartIfOnBatteries = $false; "+
			"$s.StopIfGoingOnBatteries = $false; "+
			"$s.ExecutionTimeLimit = 'PT0S'; "+
			"Set-ScheduledTask -TaskName '%s' -Settings $s",
		schtasksName, schtasksName,
	)
	psCmd := exec.Command("powershell", "-Command", psScript)
	psCmd.Stdout = os.Stdout
	psCmd.Stderr = os.Stderr
	if err := psCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not disable battery/time restrictions: %v\n", err)
		fmt.Println("Run manually to fix:")
		fmt.Printf("  powershell -Command \"$t=Get-ScheduledTask '%s'; $t.Settings.DisallowStartIfOnBatteries=$false; $t.Settings.StopIfGoingOnBatteries=$false; $t.Settings.ExecutionTimeLimit='PT0S'; Set-ScheduledTask -TaskName '%s' -Settings $t.Settings\"\n", schtasksName, schtasksName)
	}

	fmt.Printf("Scheduled task '%s' created (starts at boot as SYSTEM)\n", schtasksName)

	// Start it now
	startCmd := exec.Command("schtasks", "/run", "/tn", schtasksName)
	startCmd.Stdout = os.Stdout
	startCmd.Stderr = os.Stderr
	if err := startCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not start task now: %v\n", err)
		fmt.Printf("Run manually: schtasks /run /tn %s\n", schtasksName)
	} else {
		fmt.Printf("Task started. Check with: schtasks /query /tn %s /v /fo list\n", schtasksName)
	}

	// Print config dir info
	if os.Getenv("RCMD_CONFIG_DIR") != "" {
		fmt.Printf("\nConfig dir (RCMD_CONFIG_DIR): %s\n", os.Getenv("RCMD_CONFIG_DIR"))
	} else {
		fmt.Printf("\nNOTE: When running as SYSTEM, the daemon reads config from:\n")
		fmt.Printf("  C:\\Windows\\System32\\config\\systemprofile\\.remotecmd\\\n")
		fmt.Printf("To use a different config dir, set RCMD_CONFIG_DIR before installing:\n")
		fmt.Printf("  set RCMD_CONFIG_DIR=C:\\Users\\Benevoles\\.remotecmd\n")
		fmt.Printf("  remotecmd-cli daemon schtasks install\n")
	}
}

// handleDaemonRemoveSchtasks removes the Windows scheduled task.
func handleDaemonRemoveSchtasks() {
	// Stop the task first
	exec.Command("schtasks", "/end", "/tn", schtasksName).Run()

	cmd := exec.Command("schtasks", "/delete", "/tn", schtasksName, "/f")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: removing scheduled task: %v\n", err)
		osExit(ExitInternal)
	}

	// Remove the wrapper batch file
	binPath, err := os.Executable()
	if err == nil {
		wrapperPath := filepath.Join(filepath.Dir(binPath), "start-rcmd-daemon.bat")
		os.Remove(wrapperPath)
	}

	fmt.Printf("Scheduled task '%s' removed\n", schtasksName)
}

// handleDaemonSchtasksSubcommand handles "daemon schtasks install|remove" on Windows.
func handleDaemonSchtasksSubcommand(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli daemon schtasks install|remove")
		osExit(ExitConfigError)
	}
	switch args[0] {
	case "install":
		handleDaemonInstallSchtasks()
	case "remove":
		handleDaemonRemoveSchtasks()
	default:
		fmt.Fprintf(os.Stderr, "Unknown schtasks command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli daemon schtasks install|remove")
		osExit(ExitConfigError)
	}
}
