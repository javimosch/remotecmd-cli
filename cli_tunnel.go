package main

import (
	"flag"
	"fmt"
	"os"
)

// handleTunnel handles the `remotecmd-cli tunnel` command.
//
// Usage:
//   remotecmd-cli tunnel --target <name> --local <port> --remote <host:port>
//
// Forwards local TCP connections to a remote address on the target machine
// through the WebSocket relay — no SSH, no VPN, no open ports.
func handleTunnel(args []string) {
	fs := flag.NewFlagSet("tunnel", flag.ExitOnError)
	target := fs.String("target", "", "target machine name")
	local := fs.String("local", "", "local port to listen on (e.g. 5432)")
	remote := fs.String("remote", "", "remote address on target to forward to (e.g. localhost:5432)")
	fs.Parse(args)

	if *target == "" || *local == "" || *remote == "" {
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli tunnel --target <name> --local <port> --remote <host:port>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example:")
		fmt.Fprintln(os.Stderr, "  remotecmd-cli tunnel --target prod --local 5432 --remote localhost:5432")
		fmt.Fprintln(os.Stderr, "  # → localhost:5432 on your machine now tunnels to prod:5432")
		osExit(ExitConfigError)
	}

	if err := runTunnel(*target, *local, *remote); err != nil {
		fmt.Fprintf(os.Stderr, "Tunnel error: %v\n", err)
		osExit(ExitInternal)
	}
}
