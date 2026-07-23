---
name: remotecmd-cli
description: Use this skill when the user or an agent wants to execute shell commands on remote machines via the remotecmd WebSocket relay, manage the remotecmd daemon/relay infrastructure, or check fleet health.
---

# remotecmd-cli Skill

Remote command execution via WebSocket relay. Execute shell commands on remote machines over any network.

## Architecture

```
Client → remotecmd-cli → WebSocket → Relay Hub → WebSocket → Target Daemon → sh -c
```

Three components:
- **Target Daemon** — runs on remote machine, executes commands
- **Relay Hub** — WebSocket relay that connects clients to daemons
- **Client** — triggers commands on remote targets

## Channel — Direct CLI

### Execute commands (single target)
```
remotecmd-cli --target <name> --cmd '<command>' --timeout 30 [--stream]
remotecmd-cli exec --target <name> --cmd '<command>' [--timeout <s>] [--stream]
```

**Streaming mode**: Add `--stream` flag to see output in real-time as it is produced. Without `--stream`, output is buffered and returned after command completion.

### Execute commands (multi-target)
```
remotecmd-cli exec --targets <t1,t2,...> --cmd '<command>' [--timeout <s>] [--format json|table]
remotecmd-cli exec --group <name> --cmd '<command>' [--timeout <s>] [--format json|table]
```

### Fleet health check
```
remotecmd-cli list-targets                              # smart: auto-probes stale nodes (>1h cache)
remotecmd-cli list-targets --refresh                    # force re-probe every target now
remotecmd-cli list-targets --json                       # machine-readable JSON output
remotecmd-cli list-targets --no-health                  # legacy config-only view (no probing)
```

`rcl` (default) probes each target by sending `hostname` via the relay in one multi-exec round trip. Results are cached per-target in `~/.remotecmd/health.json` for 1 hour. Stale or never-probed targets are auto-probed; fresh ones use the cache (instant). Output table:

```
TARGET                 STATUS  SEEN              HOSTNAME
----------------------------------------------------------------------
prod                   up      3min ago          prod-server-01
staging                down    checked 1h ago    target not connected
```

### Target daemon management (on remote machine)
```
remotecmd-cli daemon start [--token <t>] [-daemon]     # start daemon (--daemon = background)
remotecmd-cli daemon stop                               # stop daemon
remotecmd-cli daemon status                             # check if running
remotecmd-cli daemon systemd install|remove             # install/remove systemd service
```

### Relay management (on relay host)
```
remotecmd-cli relay daemon start --port <port> [-daemon]  # start relay hub
remotecmd-cli relay daemon stop                            # stop relay hub
remotecmd-cli relay daemon status                          # check relay hub status
remotecmd-cli relay daemon systemd install|remove          # install/remove systemd service
```

### Configuration
```
remotecmd-cli set-relay --url <url> --name <n>          # configure relay connection
remotecmd-cli add-target --name <n> --token <t>         # add remote target
remotecmd-cli remove-target --name <n>                  # remove target
remotecmd-cli list-targets [--refresh] [--json] [--no-health]  # list targets + health
remotecmd-cli version                                    # show version
```

### Port forwarding (tunnel — replaces ssh -L)
```
rcmd tunnel --target <name> --local <port> --remote <host:port>
```
Forwards a local port to a remote address through the target daemon.

### Scheduled commands (cron — cloud relay only)
```
rcmd cron add --target <name> --schedule "0 3 * * *" --cmd "docker restart app" [--timeout 30]
rcmd cron list [--json]
rcmd cron remove --id <job-id>
rcmd cron logs --id <job-id> [--limit 10]
rcmd cron enable --id <job-id>
rcmd cron disable --id <job-id>
```
5-field cron expressions. Relay runs a scheduler goroutine. Offline targets = "skipped" (no retry).

### Team access (cloud — scoped sub-tokens)
```
rcmd team token --role <admin|operator|viewer> [--targets prod,staging] [--label "Bob"] [--expires 24h]
rcmd team invite --email <e> --role <admin|operator|viewer> [--targets prod,staging] [--label "Bob"]
rcmd team accept <code>
rcmd team list [--json]
rcmd team revoke --token <sub-token>
rcmd team audit [--target <name>] [--since 7d] [--limit 50] [--json]
```
Roles: admin (all targets), operator (exec/cp/tunnel/cron on assigned targets), viewer (read-only).
All command executions are recorded in the audit log.

### Agent self-discovery
```
rcmd guide                                              # print the full operator reference
```
HTTP endpoints on the relay:
- `GET /llms.txt` — short agent breadcrumb (entry point for a lost AI assistant)
- `GET /guide` — full operator reference (all commands, flags, gotchas)

**CRITICAL**: The `--name` in `set-relay` must match the target name you use in `add-target` and when executing commands. The daemon registers with the relay using this name, and clients look up targets by this name. If they don't match, commands will fail with "target not connected" even if the daemon is running.

### Group management
```
remotecmd-cli group create --name <n> --targets <t1,t2,...>
remotecmd-cli group add --name <n> --targets <t1,t2,...>
remotecmd-cli group remove --name <n> --targets <t1,t2,...>
remotecmd-cli group delete --name <n>
remotecmd-cli group list
```

### File transfer
```
remotecmd-cli cp --target <name> --src <path> --dst <path> [--stream]
```

### Alias Management
```
remotecmd-cli alias install                             # install all 8 aliases
remotecmd-cli alias uninstall                           # remove installed aliases
```

**Convenience Aliases** (after running `remotecmd-cli alias install`):

| Alias | Usage | Description |
|-------|-------|-------------|
| `rc` | `rc <any-command>` | Full CLI shortcut |
| `rcx` | `rcx <target> <cmd> [--stream] [timeout]` | Execute on one target (default 10s) |
| `rcx` | `rcx --targets <t1,t2> <cmd> [timeout]` | Multi-target execute (table output, default 30s) |
| `rcx` | `rcx --group <name> <cmd> [timeout]` | Group execute (table output, default 30s) |
| `rcl` | `rcl [--refresh] [--json] [--no-health]` | List targets with health (auto-probes stale >1h) |
| `rcs` | `rcs <target>` | Check daemon status on target via PID file |
| `rcc` | `rcc <target> <src> <dst> [--stream]` | Copy files/directories to remote target |
| `rcg` | `rcg list\|create\|add\|remove\|delete` | Manage target groups |
| `rcd` | `rcd start\|stop\|status\|systemd` | Manage local daemon |
| `rcr` | `rcr start\|stop\|status\|systemd` | Manage local relay hub |

## Daemon Persistence (survive reboots)

**Daemons do NOT auto-start after reboot unless a systemd service is installed.**

### Standard Linux (system-level service, recommended)
```bash
# On the target machine — works for root, VMs, physical hosts:
remotecmd-cli daemon systemd install
```
This creates a `systemctl --user` service. **In LXC containers this fails** (no D-Bus user session — "Failed to connect to bus: No medium found").

### LXC containers (manual system service)
LXC containers need a system-level unit at `/etc/systemd/system/` with `Environment=HOME=/root` (without it, systemd doesn't set HOME, so the daemon can't find `~/.remotecmd/config.json` and fails with "Relay not configured"):

```bash
BIN=$(which remotecmd-cli)
cat > /etc/systemd/system/remotecmd-daemon.service << EOF
[Unit]
Description=remotecmd-cli Target Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=HOME=/root
ExecStart=$BIN daemon start
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload && systemctl enable remotecmd-daemon && systemctl start remotecmd-daemon
```

### Relay hub persistence
Same pattern but system-level (relay needs root for port binding):
```bash
remotecmd-cli relay daemon systemd install   # run as root
```

## Channel — Supercli Plugin

If the supercli remotecmd plugin is installed, use `sc remotecmd`:

```
sc remotecmd exec run --target <name> --cmd "<command>" --timeout <s> [--stream]
sc remotecmd daemon start [--token <t>] [--daemon]
sc remotecmd daemon stop
sc remotecmd daemon status
sc remotecmd relay start --port <port> [--daemon]
sc remotecmd relay stop
sc remotecmd relay status
sc remotecmd relay config --url <u> --name <n>
sc remotecmd target add --name <n> --token <t>
sc remotecmd target remove --name <n>
sc remotecmd target list
sc remotecmd self version
```

## Installation

```bash
curl -LO https://github.com/javimosch/remotecmd-cli/releases/latest/download/remotecmd-cli-linux-amd64
chmod +x remotecmd-cli-linux-amd64
sudo mv remotecmd-cli-linux-amd64 /usr/local/bin/remotecmd-cli
```

## File Locations

- **Config file**: `~/.remotecmd/config.json`
- **Daemon token**: `~/.remotecmd/token` (auto-generated on daemon start)
- **Health cache**: `~/.remotecmd/health.json` (per-target last-seen, status, hostname, latency)
- **Daemon PID file**: `/tmp/remotecmd-daemon.pid`
- **Relay PID file**: `/tmp/remotecmd-relay.pid`

## Output Format

All command execution returns JSON:
```json
{
  "ok": bool,
  "stdout": "...",
  "stderr": "...",
  "exit_code": int,
  "duration_ms": int
}
```

## Key Concepts

- **Timeout**: Commands support `--timeout <seconds>` to prevent hanging
- **Daemon mode**: Use `--daemon` flag to run processes in background
- **Auto-reconnect**: Daemons automatically reconnect every 5s if connection drops
- **Token authentication**: Daemons require tokens for security; tokens are generated on start
- **Health cache**: `rcl` caches probe results for 1h per target; `--refresh` forces a fresh probe

## Common Workflow

1. Start relay hub on a central machine
2. Start daemon on target machine with token
3. Configure client with relay URL and target token
4. Install convenience aliases: `remotecmd-cli alias install`
5. Execute commands remotely: `rcx <target> <cmd>`
6. Check fleet health: `rcl` (or `rcl --refresh` to force)
7. Install systemd service on targets so daemons survive reboots

## Troubleshooting

### "target not connected" — daemon not running on target
This is the #1 issue. The daemon simply stopped (not crashed — config is fine).
```bash
ssh <target> "remotecmd-cli daemon status"
ssh <target> "remotecmd-cli daemon start -daemon"
rcx <target> "hostname"   # verify
```
If this keeps happening after reboots, install the systemd service (see Daemon Persistence above).

### Daemon not starting
- Check if port is already in use
- Remove stale PID files: `rm /tmp/remotecmd-*.pid`
- Verify token file exists at `~/.remotecmd/token`
- If running under systemd: `journalctl -u remotecmd-daemon --no-pager -n 20`

### systemd service fails with "Relay not configured"
Missing `Environment=HOME=/root` in the unit file. systemd doesn't set HOME by default, so the daemon can't find `~/.remotecmd/config.json`. See the LXC container pattern above.

### Relay connectivity issues
- Test relay health: `curl -s http://<relay-host>:<port>/health`
- Expected response: `{"status":"healthy"}`
- Verify firewall allows WebSocket traffic

### Command execution failures
- Test with simple command: `remotecmd-cli --target <name> --cmd 'echo test' --timeout 10`
- Check daemon status on target machine
- Verify target configuration in `~/.remotecmd/config.json`
- **CRITICAL**: Ensure the daemon's `set-relay --name` matches the target name used in commands

### Stale health cache
If `rcl` shows outdated status, force a fresh probe: `rcl --refresh`
Or delete the cache: `rm ~/.remotecmd/health.json`
