//go:build !windows

package main

// handleDaemonPersistenceSubcommand dispatches the platform-specific
// persistence subcommand (systemd on Unix, schtasks on Windows).
func handleDaemonPersistenceSubcommand(args []string) {
	handleDaemonSystemdSubcommand(args)
}

// persistenceSubcommandName returns the name shown in help text.
func persistenceSubcommandName() string {
	return "systemd"
}
