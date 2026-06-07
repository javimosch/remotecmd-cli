package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

const (
	daemonServiceName = "remotecmd-daemon"
	relayServiceName  = "remotecmd-relay"
)

func daemonUnitContent(binPath string) string {
	return fmt.Sprintf(`[Unit]
Description=remotecmd-cli Target Daemon
Documentation=https://github.com/javimosch/remotecmd-cli
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s daemon start
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`, binPath)
}

func relayUnitContent(binPath string, port int) string {
	return fmt.Sprintf(`[Unit]
Description=remotecmd-cli Relay Hub
Documentation=https://github.com/javimosch/remotecmd-cli
After=network.target

[Service]
Type=simple
ExecStart=%s relay daemon start --port %d
Restart=always
RestartSec=3
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`, binPath, port)
}

func handleDaemonInstallSystemd() {
	binPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot get executable path: %v\n", err)
		os.Exit(ExitInternal)
	}

	usr, err := user.Current()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine user: %v\n", err)
		os.Exit(ExitInternal)
	}

	unitDir := filepath.Join(usr.HomeDir, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot create systemd user directory: %v\n", err)
		os.Exit(ExitInternal)
	}

	unitPath := filepath.Join(unitDir, daemonServiceName+".service")
	content := daemonUnitContent(binPath)

	if err := os.WriteFile(unitPath, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: writing unit file: %v\n", err)
		os.Exit(ExitInternal)
	}

	fmt.Printf("Unit file written: %s\n", unitPath)

	// Reload and enable
	runSystemctl := func(args ...string) error {
		cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	if err := runSystemctl("daemon-reload"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: systemctl daemon-reload failed: %v\n", err)
		fmt.Println("Run manually: systemctl --user daemon-reload")
	} else {
		fmt.Println("Systemd daemon-reload: OK")
	}

	if err := runSystemctl("enable", "--now", daemonServiceName+".service"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not enable/start service: %v\n", err)
		fmt.Printf("Run manually: systemctl --user enable --now %s\n", daemonServiceName)
	} else {
		fmt.Printf("Service %s enabled and started\n", daemonServiceName)
	}

	if err := runSystemctl("status", "--no-pager", daemonServiceName+".service"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not show status: %v\n", err)
	}

	fmt.Printf("\nManage the service:\n")
	fmt.Printf("  systemctl --user status %s\n", daemonServiceName)
	fmt.Printf("  systemctl --user restart %s\n", daemonServiceName)
	fmt.Printf("  journalctl --user -u %s -f\n", daemonServiceName)
}

func handleRelayInstallSystemd() {
	binPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot get executable path: %v\n", err)
		os.Exit(ExitInternal)
	}

	// Check if running as root for system-wide service
	if !isRoot() {
		fmt.Fprintln(os.Stderr, "Warning: relay systemd service requires root for system-wide installation.")
		fmt.Fprintln(os.Stderr, "Run with sudo or as root.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "For a user-scope relay (no root), use:")
		fmt.Fprintln(os.Stderr, "  remotecmd-cli daemon install-systemd")
		fmt.Fprintln(os.Stderr, "  (the relay also runs as a daemon -- it can be a user service)")
		os.Exit(ExitConfigError)
	}

	unitDir := "/etc/systemd/system"
	unitPath := filepath.Join(unitDir, relayServiceName+".service")

	port := 3032
	content := relayUnitContent(binPath, port)

	if err := os.WriteFile(unitPath, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: writing unit file: %v\n", err)
		os.Exit(ExitInternal)
	}

	fmt.Printf("Unit file written: %s\n", unitPath)

	runSystemctl := func(args ...string) error {
		cmd := exec.Command("systemctl", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	if err := runSystemctl("daemon-reload"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: systemctl daemon-reload failed: %v\n", err)
		fmt.Println("Run manually: systemctl daemon-reload")
	} else {
		fmt.Println("Systemd daemon-reload: OK")
	}

	if err := runSystemctl("enable", "--now", relayServiceName+".service"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not enable/start service: %v\n", err)
		fmt.Printf("Run manually: systemctl enable --now %s\n", relayServiceName)
	} else {
		fmt.Printf("Service %s enabled and started\n", relayServiceName)
	}

	if err := runSystemctl("status", "--no-pager", relayServiceName+".service"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not show status: %v\n", err)
	}

	fmt.Printf("\nManage the service:\n")
	fmt.Printf("  systemctl status %s\n", relayServiceName)
	fmt.Printf("  systemctl restart %s\n", relayServiceName)
	fmt.Printf("  journalctl -u %s -f\n", relayServiceName)
}

func handleDaemonRemoveSystemd() {
	usr, err := user.Current()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine user: %v\n", err)
		os.Exit(ExitInternal)
	}

	unitPath := filepath.Join(usr.HomeDir, ".config", "systemd", "user", daemonServiceName+".service")

	runSystemctl := func(args ...string) error {
		cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	runSystemctl("stop", daemonServiceName+".service")
	runSystemctl("disable", daemonServiceName+".service")

	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Warning: could not remove unit file: %v\n", err)
	}

	runSystemctl("daemon-reload")
	fmt.Printf("Service %s removed\n", daemonServiceName)
}

func handleRelayRemoveSystemd() {
	unitPath := filepath.Join("/etc/systemd/system", relayServiceName+".service")

	runSystemctl := func(args ...string) error {
		cmd := exec.Command("systemctl", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	runSystemctl("stop", relayServiceName+".service")
	runSystemctl("disable", relayServiceName+".service")

	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Warning: could not remove unit file: %v\n", err)
	}

	runSystemctl("daemon-reload")
	fmt.Printf("Service %s removed\n", relayServiceName)
}

func isRoot() bool {
	return os.Geteuid() == 0
}

// Add systemd subcommand handlers to existing daemon/relay handlers
func handleDaemonSystemdSubcommand(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli daemon systemd install|remove")
		os.Exit(ExitConfigError)
	}
	switch args[0] {
	case "install":
		handleDaemonInstallSystemd()
	case "remove":
		handleDaemonRemoveSystemd()
	default:
		fmt.Fprintf(os.Stderr, "Unknown systemd command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli daemon systemd install|remove")
		os.Exit(ExitConfigError)
	}
}

func handleRelaySystemdSubcommand(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli relay systemd install|remove")
		os.Exit(ExitConfigError)
	}
	switch args[0] {
	case "install":
		handleRelayInstallSystemd()
	case "remove":
		handleRelayRemoveSystemd()
	default:
		fmt.Fprintf(os.Stderr, "Unknown systemd command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli relay systemd install|remove")
		os.Exit(ExitConfigError)
	}
}

// Helper to find the binary for unit template rendering
func findBinaryPath() string {
	p, err := os.Executable()
	if err != nil {
		return "remotecmd-cli"
	}
	return p
}

// Unit content generators exported for testing
func DaemonUnitContent() string {
	return daemonUnitContent(findBinaryPath())
}

func RelayUnitContent(port int) string {
	return relayUnitContent(findBinaryPath(), port)
}

// Validate the unit file content (basic structure check)
func validateUnitContent(content string) error {
	if !strings.Contains(content, "[Unit]") {
		return fmt.Errorf("missing [Unit] section")
	}
	if !strings.Contains(content, "[Service]") {
		return fmt.Errorf("missing [Service] section")
	}
	if !strings.Contains(content, "[Install]") {
		return fmt.Errorf("missing [Install] section")
	}
	if !strings.Contains(content, "ExecStart=") {
		return fmt.Errorf("missing ExecStart directive")
	}
	return nil
}
