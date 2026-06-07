<p align="center">
  <img src="https://img.shields.io/github/v/release/javimosch/remotecmd-cli" alt="Release">
  <img src="https://img.shields.io/badge/language-Go-00ADD8" alt="Go">
  <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  <img src="https://img.shields.io/github/stars/javimosch/remotecmd-cli?style=social" alt="Stars">
</p>

<h1 align="center">remotecmd-cli — Remote execution for your entire fleet</h1>

<p align="center">
  <b>One binary. Zero open ports. JSON output. Multi-target.</b><br>
  Execute commands on 1 or 100 machines with a single command.
</p>

> WebSocket relay + token auth. Works over Tailscale, public internet, NAT, firewalls.
> SSH was built for one machine. remotecmd-cli was built for ten, a hundred, a thousand.

## TL;DR

```bash
# 1. Start relay on any reachable VPS
remotecmd-cli relay daemon start --port 3032 -daemon

# 2. Add a machine in 10 seconds
remotecmd-cli pair listen --name myserver
# Share the printed one-liner with the remote machine

# 3. Run on a single target
remotecmd-cli --target myserver --cmd 'uptime'

# 4. Run on multiple targets at once
remotecmd-cli exec --targets web1,web2,web3 --cmd 'systemctl restart nginx'

# 5. Organize into groups and blast commands
remotecmd-cli group create prod-web --targets web1,web2,web3
remotecmd-cli exec --group prod-web --cmd 'df -h /data'

# 6. Output as table or JSON
remotecmd-cli exec --group prod-web --cmd 'hostname' --format table
remotecmd-cli exec --group prod-web --cmd 'hostname' --format json
```

---

## The Problem

Executing a command on a remote machine shouldn't require:
- Setting up SSH key pairs and managing `authorized_keys`
- Opening firewall ports or configuring VPNs
- Dealing with NAT traversal or dynamic IPs
- Installing heavyweight remote management tools

**For multiple machines**, the problem compounds. You loop in bash. You write ad-hoc scripts. You pray you don't hit the wrong one.

**For AI agents**, the situation is worse — they need a clean, scriptable interface that:
- Returns structured JSON (stdout, stderr, exit code, duration) every time
- Handles timeouts deterministically
- Works reliably over unstable connections
- Requires zero parsing or regex extraction

Most tools output human-readable text or inconsistent formats. Agents waste tokens parsing, guessing, and retrying. remotecmd-cli is built the other way around: **JSON by default, human-readable on request**.

## The Solution

remotecmd-cli uses a **WebSocket relay** as the routing hub. Machines connect *out* to the relay (no inbound ports needed) and the relay routes commands between them by token-authenticated target name.

```
Client  ──ws──►  Relay Hub  ──ws──►  Target Daemon 1
                    │             └──  Target Daemon 2
                    │             └──  Target Daemon N
                    ▲
              (VPS or any
              reachable host)
```

- **Zero inbound ports** on target machines — daemons connect out
- **Token auth** — each target has a unique secret token
- **Streaming** — real-time stdout/stderr, line by line
- **Multi-target** — fan-out to any number of connected targets simultaneously
- **Groups** — organize targets by role, environment, or project
- **Pair in seconds** — one curl one-liner to add any machine
- **Agent-first** — JSON output by default, no parsing required

---

## Install

```bash
# Linux/amd64
curl -sSL https://github.com/javimosch/remotecmd-cli/releases/latest/download/remotecmd-cli-linux-amd64 \
  -o ~/.local/bin/remotecmd-cli && chmod +x ~/.local/bin/remotecmd-cli

# Linux/arm64
curl -sSL https://github.com/javimosch/remotecmd-cli/releases/latest/download/remotecmd-cli-linux-arm64 \
  -o ~/.local/bin/remotecmd-cli && chmod +x ~/.local/bin/remotecmd-cli

# macOS (Apple Silicon)
curl -sSL https://github.com/javimosch/remotecmd-cli/releases/latest/download/remotecmd-cli-darwin-arm64 \
  -o /usr/local/bin/remotecmd-cli && chmod +x /usr/local/bin/remotecmd-cli
```

Or build from source:

```bash
git clone https://github.com/javimosch/remotecmd-cli.git
cd remotecmd-cli && go build -o remotecmd-cli .
```

### Convenience aliases

```bash
remotecmd-cli alias install
# Installs: rc (full CLI), rcx (execute), rcl (list), rcs (status), rcc (copy)
```

---

## Quick Start

### Step 1 — Start a relay

On any VPS or machine reachable by both sides:

```bash
remotecmd-cli relay daemon start --port 3032 -daemon
```

### Step 2 — Add a target machine

**Option A: Pair (easiest — send one line to any machine)**

```bash
remotecmd-cli set-relay --url http://<relay-host>:3032 --name myclient
remotecmd-cli pair listen --name myserver
```

It prints a one-liner. Send it to the remote machine — they paste it:

```
curl -sSL https://raw.githubusercontent.com/javimosch/remotecmd-cli/master/install.sh \
  | sh -s -- --relay http://<relay-host>:3032 --code a1b2c3d4
```

**Option B: Manual (if you already have shell access)**

```bash
# On the target:
remotecmd-cli set-relay --url http://<relay-host>:3032 --name myserver
remotecmd-cli daemon start -daemon
# copy the printed token

# On your client:
remotecmd-cli set-relay --url http://<relay-host>:3032 --name myclient
remotecmd-cli add-target --name myserver --token <token>
```

### Step 3 — Execute commands

```bash
# Single target (legacy syntax)
remotecmd-cli --target myserver --cmd 'df -h'

# Single target (new syntax)
remotecmd-cli exec --target myserver --cmd 'hostname'

# Streaming mode
remotecmd-cli exec --target myserver --cmd 'journalctl -f' --stream --timeout 60

# With aliases
rcx myserver 'uptime'
rcx myserver 'docker ps' 15
```

---

## Multi-Target Execution

This is where remotecmd-cli really shines. Run commands across your fleet in one shot.

### By comma-separated targets

```bash
remotecmd-cli exec --targets web1,web2,db1 --cmd 'uptime'
```

### By named group

```bash
# Create groups
remotecmd-cli group create prod-web --targets web1,web2,web3
remotecmd-cli group create prod-db --targets db1,db2

# Execute on a group
remotecmd-cli exec --group prod-web --cmd 'systemctl reload nginx'

# Execute on all prod machines (target can be in multiple groups)
remotecmd-cli exec --targets web1,web2,db1,db2 --cmd 'date'
```

### Output formats

**Table (default, for humans):**

```
$ remotecmd-cli exec --targets web1,db1 --cmd 'hostname' --format table
TARGET               | STATUS | OUTPUT/ERROR
---------------------|--------|----------------------------------------
web1                 | OK     | web1.example.com
db1                  | OK     | db1.internal
```

**JSON (for agents and scripts):**

```json
{
  "type": "multi_result",
  "results": {
    "web1": {"ok": true, "stdout": "web1.example.com", "exit_code": 0, "duration_ms": 5},
    "db1":  {"ok": true, "stdout": "db1.internal", "exit_code": 0, "duration_ms": 4}
  }
}
```

### Group management

```bash
remotecmd-cli group create --name <n> --targets <t1,t2,...>    # Create group
remotecmd-cli group add --name <n> --targets <t1,t2,...>       # Add targets to group
remotecmd-cli group remove --name <n> --targets <t1,t2,...>    # Remove targets from group
remotecmd-cli group delete --name <n>                          # Delete group
remotecmd-cli group list                                        # List all groups
remotecmd-cli list-targets                                      # List targets + groups
```

---

## File Transfer

Copy files and directories to remote targets:

```bash
# Single file
remotecmd-cli cp --target myserver --src ./config.yaml --dst /etc/app/config.yaml

# Directory (auto-detected, uses tar archive + base64)
remotecmd-cli cp --target myserver --src ./dist --dst /var/www/app

# With streaming progress
remotecmd-cli cp --target myserver --src ./large-file --dst /tmp/large-file --stream

# Using the rcc alias
rcc myserver ~/.ssh/config ~/.ssh/config
rcc myserver /app/dist /app/dist --stream
```

---

## Pairing Flow

The `pair` command is the fastest way to add a machine you don't yet have shell access to:

```
You (client)                      Remote machine
─────────────────                 ─────────────────────────────────────
remotecmd-cli pair                (receives one-liner from you)
  listen --name vps1
     │                             curl ... | sh -s -- --relay ... --code abc123
     │ registers code "abc123"              │
     │ on relay                             │ installs binary
     │                                      │ sets relay config
     │                                      │ saves pair code
     │                                      │ starts daemon
     │                                      │
     │◄──────── pair message ──────────────┘
     │          (code + token + hostname)
     │
  adds target "vps1"
  prints: Peer connected!
```

- Pair codes are **one-time use**
- Daemon auto-starts on boot via systemd user service (falls back to nohup)

---

## Streaming

Without `--stream`, output is buffered and returned as JSON after the command exits.

With `--stream`, stdout and stderr are forwarded **line by line** in real time:

```bash
# Watch a build in real time
remotecmd-cli exec --target myserver --cmd 'make all' --stream --timeout 300

# Follow logs
remotecmd-cli exec --target myserver --cmd 'journalctl -f -u nginx' --stream --timeout 3600
```

**JSONL Streaming (for agents):**

```bash
rcx myserver 'long-cmd' --stream
# Output: {"event":"chunk","data":{"stream":"stdout","data":"line"}}
#         {"event":"complete","data":{"ok":true,"exit_code":0,"duration":123}}
```

---

## Commands Reference

```
EXECUTE (single):
  remotecmd-cli --target <n> --cmd <cmd> [--timeout <s>] [--stream]    Legacy syntax
  remotecmd-cli exec --target <n> --cmd <cmd> [--timeout <s>] [--stream]  New syntax

EXECUTE (multi-target):
  remotecmd-cli exec --targets <t1,t2,...> --cmd <cmd> [--timeout <s>] [--format json|table]
  remotecmd-cli exec --group <name> --cmd <cmd> [--timeout <s>] [--format json|table]

FILE TRANSFER:
  remotecmd-cli cp --target <n> --src <path> --dst <path> [--stream]

GROUPS:
  remotecmd-cli group create --name <n> --targets <t1,t2,...>
  remotecmd-cli group add --name <n> --targets <t1,t2,...>
  remotecmd-cli group remove --name <n> --targets <t1,t2,...>
  remotecmd-cli group delete --name <n>
  remotecmd-cli group list

PAIRING:
  remotecmd-cli pair listen [--name <n>] [--timeout <s>] [--code <c>]
  remotecmd-cli pair accept --code <c>

CONFIGURATION:
  remotecmd-cli set-relay --url <u> --name <n>
  remotecmd-cli add-target --name <n> --token <t>
  remotecmd-cli remove-target --name <n>
  remotecmd-cli list-targets

ALIASES:
  remotecmd-cli alias install       Install rc / rcx / rcl / rcs / rcc
  remotecmd-cli alias uninstall     Remove installed aliases

RELAY:
  remotecmd-cli relay daemon start [--port 3032] [-daemon]
  remotecmd-cli relay daemon stop
  remotecmd-cli relay daemon status

DAEMON:
  remotecmd-cli daemon start [-daemon]
  remotecmd-cli daemon stop
  remotecmd-cli daemon status
```

### Convenience aliases

| Alias | Equivalent | Description |
|-------|-----------|-------------|
| `rc` | `remotecmd-cli` | Full CLI shortcut |
| `rcx <target> <cmd> [--stream] [timeout]` | `exec --target <t> --cmd <c>` | Execute command (default 10s) |
| `rcl` | `list-targets` | List configured targets + groups |
| `rcs <target>` | `exec --target <t> --cmd 'status check'` | Check daemon status via PID file |
| `rcc <target> <src> <dst> [--stream]` | `cp --target <t> --src <s> --dst <d>` | Copy files/directories |

---

## Output Format

**Single-target (buffered):**

```json
{
  "ok": true,
  "stdout": "myserver\n",
  "stderr": "",
  "exit_code": 0,
  "duration_ms": 8
}
```

**Multi-target (JSON):**

```json
{
  "type": "multi_result",
  "results": {
    "web1": {"ok": true, "stdout": "OK", "exit_code": 0, "duration_ms": 5},
    "web2": {"ok": false, "error": "target not connected"}
  }
}
```

---

## Architecture

```
┌────────────┐   WebSocket   ┌─────────────┐   WebSocket   ┌──────────────────┐
│   Client   │──────────────►│  Relay Hub  │◄──────────────│  Target Daemon 1 │
│ (one-shot)  │              │  (always on)│              │  (persistent bg) │
└────────────┘               │              │              └──────────────────┘
                             │              │              ┌──────────────────┐
                             │              │◄──────────────│  Target Daemon 2 │
                             │              │              └──────────────────┘
                             │              │              ┌──────────────────┐
                             │              │◄──────────────│  Target Daemon N │
                             │              │              └──────────────────┘
                             └─────────────┘
                                    │
                         routes by target name
                         + token verification
                         + multi-target fan-out
```

**Relay** — stateless hub. Routes single commands by target name. For multi-target, fans out to N daemons, collects results, and returns an aggregated response.

**Daemon** — runs on each target. Connects out to relay (no inbound ports). Forks a shell on command, returns structured JSON.

**Client** — one-shot WebSocket connection. Sends command, waits for result.

---

## Use Cases

| Scenario | Command |
|----------|---------|
| Quick health check | `rcx myserver 'uptime && df -h'` |
| Fleet health check | `remotecmd-cli exec --group all --cmd 'uptime' --format table` |
| Rolling restart | `remotecmd-cli exec --group web --cmd 'systemctl restart nginx'` |
| Deploy to remote | `remotecmd-cli exec --target app1 --cmd 'cd /app && git pull && pm2 restart all' 60` |
| Stream build logs | `remotecmd-cli exec --target build --cmd 'make' --stream --timeout 300` |
| Follow app logs | `remotecmd-cli exec --target prod --cmd 'tail -f /var/log/app.log' --stream` |
| Copy config to all | `for t in web1 web2 web3; do rcc "$t" ./app.conf /etc/app/app.conf; done` |
| Add friend's machine | `remotecmd-cli pair listen --name friend` → share one-liner |

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `target not connected` | Daemon not running or wrong relay URL | Check `daemon status` on target; verify relay URL matches |
| `pair code not found` | Code already used or listener timed out | Run `pair listen` again for a fresh code |
| `curl: (23) Failure writing output` | Binary busy (systemd running it) | install.sh stops service first; re-run one-liner |
| Token mismatch | Target re-started with new token | Re-add target: `add-target --name <n> --token <new>` |
| Streaming stops early | Default timeout hit | Add `--timeout <seconds>` |
| Group target not resolved | Target not in config | Add target first with `add-target`, then add to group |

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Language | Go — single static binary, no runtime deps |
| Transport | WebSocket (`gorilla/websocket`) |
| Auth | Token per target (auto-generated) |
| Persistence | `~/.remotecmd/config.json` |
| Daemon | PID file + nohup (fallback) |
| Streaming | `StdoutPipe` + `bufio.Scanner`, line-by-line forwarding |
| Multi-target | Relay-level fan-out with result aggregation |
| Releases | GitHub Actions → multi-arch binaries (linux/darwin, amd64/arm64) |

---

## Roadmap

See [docs/vision.md](docs/vision.md) for the full vision and roadmap.

Upcoming priorities:
- **v1.3**: Script-friendly exit codes, systemd unit generation
- **v1.4**: Persistent client connections for faster sequential commands
- **v1.5**: Optional TLS for relay encryption

---

## License

MIT — [Javier Leandro Arancibia](https://github.com/javimosch)

## Support

[![Support me on Ko-fi](https://storage.ko-fi.com/cdn/brandasset/v2/support_me_on_kofi_badge_beige.png)](https://ko-fi.com/javimosch)
