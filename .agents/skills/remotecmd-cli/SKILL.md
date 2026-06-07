---
name: remotecmd-cli
description: Use this skill when the user or an agent wants to execute shell commands on remote machines via the remotecmd WebSocket relay, or manage the remotecmd daemon/relay infrastructure.
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

Use `remotecmd-cli` directly on any machine where it's installed:

### Execute commands
```
remotecmd-cli --target <name> --cmd '<command>' --timeout 30 [--stream]
```

**Streaming mode**: Add `--stream` flag to see output in real-time as it's produced (useful for long-running commands, logs, etc.). Without `--stream`, output is buffered and returned after command completion.

### Target daemon management (on remote machine)
```
remotecmd-cli daemon start [--token <t>] [-daemon]     # start daemon (--daemon = background)
remotecmd-cli daemon stop                               # stop daemon
remotecmd-cli daemon status                             # check if running
```

### Relay management (on relay host)
```
remotecmd-cli relay daemon start --port <port> [-daemon]  # start relay hub
remotecmd-cli relay daemon stop                            # stop relay hub
remotecmd-cli relay daemon status                          # check relay hub status
```

### Configuration
```
remotecmd-cli set-relay --url <url> --name <n>          # configure relay connection
remotecmd-cli add-target --name <n> --token <t>         # add remote target
remotecmd-cli remove-target --name <n>                  # remove target
remotecmd-cli list-targets                              # list configured targets
remotecmd-cli version                                    # show version
```

**CRITICAL**: The `--name` in `set-relay` must match the target name you use in `add-target` and when executing commands. The daemon registers with the relay using this name, and clients look up targets by this name. If they don't match, commands will fail with "target not connected" even if the daemon is running.

### Alias Management
```
remotecmd-cli alias install                             # install convenience aliases (rc, rcx, rcl, rcs)
remotecmd-cli alias uninstall                           # remove installed aliases
```

**Convenience Aliases** (after running `remotecmd-cli alias install`):
- `rc` - Direct alias to remotecmd-cli (full access to all commands)
- `rcx <target> <cmd> [timeout]` - Execute command on target (default 10s timeout)
- `rcl` - List all configured targets
- `rcs <target>` - Check remotecmd daemon status on target

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

## Configuration

- **Config file**: `~/.remotecmd/config.json`
- **Daemon token**: `~/.remotecmd/token` (auto-generated on daemon start)
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

## Common Workflow

1. Start relay hub on a central machine
2. Start daemon on target machine with token
3. Configure client with relay URL and target token
4. **(Optional)** Install convenience aliases: `remotecmd-cli alias install`
5. Execute commands remotely (use `rcx <target> <cmd>` for shorter syntax if aliases installed)

## Troubleshooting

### Daemon not starting
- Check if port is already in use
- Remove stale PID files: `rm /tmp/remotecmd-*.pid`
- Verify token file exists at `~/.remotecmd/token`

### Relay connectivity issues
- Test relay health: `curl -s http://<relay-host>:<port>/health`
- Expected response: `{"status":"healthy"}`
- Verify firewall allows WebSocket traffic

### Command execution failures
- Test full path with simple command: `remotecmd-cli --target <name> --cmd 'echo test' --timeout 10`
- Check daemon status on target machine
- Verify target configuration in `~/.remotecmd/config.json`
- **CRITICAL**: Ensure the daemon's `set-relay --name` matches the target name used in commands. The relay uses this name to look up connected daemons.
