package main

import "fmt"

func printHelp() {
	fmt.Println(`remotecmd-cli — remote command execution via WebSocket relay

EXECUTE (single target):
  remotecmd-cli --target <name> --cmd <command> [--timeout <s>] [--stream]    Single target (legacy)
  remotecmd-cli exec --target <name> --cmd <command> [--timeout <s>] [--stream]  Single target (new)

EXECUTE (multi-target):
  remotecmd-cli exec --targets <t1,t2,...> --cmd <command> [--timeout <s>] [--format json|table]
  remotecmd-cli exec --group <name> --cmd <command> [--timeout <s>] [--format json|table]

FILE TRANSFER:
  remotecmd-cli cp --target <name> --src <path> --dst <path>  Copy file or directory to remote target

TARGET CONFIGURATION:
  remotecmd-cli add-target --name <n> --token <t>    Add a known target
  remotecmd-cli remove-target --name <n>              Remove a target
  remotecmd-cli list-targets                          List configured targets and groups
  remotecmd-cli set-relay --url <u> --name <n>        Configure relay connection

GROUP MANAGEMENT:
  remotecmd-cli group create --name <n> --targets <t1,t2,...>  Create a target group
  remotecmd-cli group add --name <n> --targets <t1,t2,...>     Add targets to a group
  remotecmd-cli group remove --name <n> --targets <t1,t2,...>  Remove targets from a group
  remotecmd-cli group delete --name <n>                        Delete a group
  remotecmd-cli group list                                     List all groups

ALIAS:
  remotecmd-cli alias install                         Install convenience aliases (rc, rcx, rcl, rcs, rcc)
  remotecmd-cli alias uninstall                       Remove installed aliases

RELAY (run on relay hub machine):
  remotecmd-cli relay daemon start [--port 3032]     Start relay hub (foreground)
  remotecmd-cli relay daemon start --port 3032 -daemon  Start relay hub (background)
  remotecmd-cli relay daemon stop                    Stop relay hub
  remotecmd-cli relay daemon status                  Check relay hub status

DAEMON (run on target machine):
  remotecmd-cli daemon start [--token <t>]            Start target daemon (foreground)
  remotecmd-cli daemon start --token <t> -daemon       Start target daemon (background)
  remotecmd-cli daemon stop                           Stop target daemon
  remotecmd-cli daemon status                         Check target daemon status

PERSISTENT CLIENT:
  remotecmd-cli client                                Interactive session (one JSON command per line)
  echo '{"target":"<n>","cmd":"<cmd>"}' | remotecmd-cli client    Batch mode via stdin

PAIRING:
  remotecmd-cli pair listen [--name <n>] [--timeout <s>] [--code <c>]  Wait for peer; prints one-liner
  remotecmd-cli pair accept --code <c>                                   Accept a pair code

OTHER:
  remotecmd-cli version    Show version
  remotecmd-cli help       Show this help`)
}

func printRelayHelp() {
	fmt.Println(`Usage: remotecmd-cli relay <command>

Commands:
  daemon    Manage relay daemon (start/stop/status)`)
}

func printRelayDaemonHelp() {
	fmt.Println(`Usage: remotecmd-cli relay daemon <command>

Commands:
  start [--port <n>] [-daemon] [--tls-cert <file>] [--tls-key <file>]  Start relay hub
  stop                                                                  Stop relay hub
  status                                                                Check relay hub status
  systemd install|remove                                                Install/remove systemd service`)
}

func printDaemonHelp() {
	fmt.Println(`Usage: remotecmd-cli daemon <command>

Commands:
  start [--token <t>] [-daemon]     Start target daemon
  stop                               Stop target daemon
  status                             Check target daemon status
  systemd install|remove             Install/remove systemd user service`)
}

func printGroupHelp() {
	fmt.Println(`Usage: remotecmd-cli group <command>

Commands:
  create --name <n> --targets <t1,t2,...>   Create a target group
  delete --name <n>                          Delete a group
  add --name <n> --targets <t1,t2,...>       Add targets to a group
  remove --name <n> --targets <t1,t2,...>    Remove targets from a group
  list                                        List all groups`)
}
