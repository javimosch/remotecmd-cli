package main

import (
	"fmt"
	"os"
)

// This file holds the alias wrapper generators for the management aliases
// (rcg/rcd/rcr). The core exec/list/copy wrappers live in alias.go.

func createRcgWrapper(path, execPath string) error {
	content := fmt.Sprintf(`#!/bin/sh
# rcg - Manage remotecmd target groups
# Usage: rcg <list|create|add|remove|delete> [args...]

if [ "$1" = "--help" ] || [ "$1" = "-h" ] || [ $# -lt 1 ]; then
    echo "rcg - Manage remotecmd target groups"
    echo ""
    echo "Usage:"
    echo "  rcg list"
    echo "  rcg create  --name <n> --targets <t1,t2,...>"
    echo "  rcg add     --name <n> --targets <t1,t2,...>"
    echo "  rcg remove  --name <n> --targets <t1,t2,...>"
    echo "  rcg delete  --name <n>"
    echo ""
    echo "Groups let you run one command across many targets:"
    echo "  rcx --group <name> <command>"
    exit 0
fi

SUB="$1"
shift
case "$SUB" in
    list)
        exec %s group list
        ;;
    create|delete|add|remove)
        exec %s group "$SUB" "$@"
        ;;
    *)
        echo "Error: unknown rcg subcommand: $SUB"
        echo "Use 'rcg --help' for usage"
        exit 1
        ;;
esac
`, execPath, execPath)
	return os.WriteFile(path, []byte(content), 0755)
}

func createRcdWrapper(path, execPath string) error {
	content := fmt.Sprintf(`#!/bin/sh
# rcd - Manage the local remotecmd daemon (run on a target machine)
# Usage: rcd <start|stop|status|systemd> [args...]

if [ "$1" = "--help" ] || [ "$1" = "-h" ] || [ $# -lt 1 ]; then
    echo "rcd - Manage the local remotecmd daemon"
    echo ""
    echo "Usage:"
    echo "  rcd start [--token <t>] [-daemon]    Start daemon (foreground or background)"
    echo "  rcd stop                              Stop daemon"
    echo "  rcd status                            Check daemon status"
    echo "  rcd systemd install|remove            Install/remove systemd user service"
    echo ""
    echo "Run this on the target machine you want to make remotely executable."
    exit 0
fi

exec %s daemon "$@"
`, execPath)
	return os.WriteFile(path, []byte(content), 0755)
}

func createRcrWrapper(path, execPath string) error {
	content := fmt.Sprintf(`#!/bin/sh
# rcr - Manage the local remotecmd relay hub
# Usage: rcr <start|stop|status|systemd> [args...]

if [ "$1" = "--help" ] || [ "$1" = "-h" ] || [ $# -lt 1 ]; then
    echo "rcr - Manage the local remotecmd relay hub"
    echo ""
    echo "Usage:"
    echo "  rcr start [--port <n>] [-daemon] [--tls-cert <f> --tls-key <f>]"
    echo "  rcr stop"
    echo "  rcr status"
    echo "  rcr systemd install|remove"
    echo ""
    echo "The relay hub brokers WebSocket connections between clients and daemons."
    echo "Run this on a centrally-reachable machine."
    exit 0
fi

SUB="$1"
shift
case "$SUB" in
    start|stop|status)
        exec %s relay daemon "$SUB" "$@"
        ;;
    systemd)
        exec %s relay daemon systemd "$@"
        ;;
    *)
        echo "Error: unknown rcr subcommand: $SUB"
        echo "Use 'rcr --help' for usage"
        exit 1
        ;;
esac
`, execPath, execPath)
	return os.WriteFile(path, []byte(content), 0755)
}
