<p align="center">
  <img src="https://img.shields.io/github/v/release/javimosch/remotecmd-cli" alt="Release">
  <img src="https://img.shields.io/badge/language-Go-00ADD8" alt="Go">
  <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  <img src="https://img.shields.io/github/stars/javimosch/remotecmd-cli?style=social" alt="Stars">
</p>

<h1 align="center">remotecmd-cli — Execute commands on any machine, anywhere</h1>

<p align="center">
  <b>No VPN. No open ports. No SSH keys.</b><br>
  Connect a machine with one curl command. Run commands from anywhere.
</p>

> WebSocket relay + token auth. Works over Tailscale, public internet, NAT, firewalls.

## TL;DR

```bash
# 1. Start relay on any reachable VPS
remotecmd-cli relay daemon start --port 3032 -daemon

# 2. Add a remote machine in 10 seconds — share one line with your peer:
remotecmd-cli pair listen --name myserver

# 3. Run commands on it
remotecmd-cli --target myserver --cmd 'uptime'

# 4. Stream real-time output
remotecmd-cli --target myserver --cmd 'tail -f /var/log/syslog' --stream --timeout 60
```

---

## The Problem

Executing a command on a remote machine shouldn't require:
- Setting up SSH key pairs and managing `authorized_keys`
- Opening firewall ports or configuring VPNs
- Dealing with NAT traversal or dynamic IPs
- Installing heavyweight remote management tools

**For agents**, the situation is worse — they need a clean, scriptable interface that returns structured output, handles timeouts, and works reliably over unstable connections.

## The Solution

remotecmd-cli uses a **WebSocket relay** as the routing hub. Machines connect *out* to the relay (no inbound ports needed) and the relay routes commands between them by token-authenticated target name.

```
Client  ──ws──►  Relay Hub  ──ws──►  Target Daemon  ──►  shell
                    ▲
              (VPS or any
              reachable host)
```

- **Zero inbound ports** on target machines — daemons connect out
- **Token auth** — each target has a unique secret token
- **Streaming** — real-time stdout/stderr, line by line
- **Pair in seconds** — one curl one-liner to add any machine

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

---

## Quick Start

### Step 1 — Start a relay

On any VPS or machine reachable by both sides:

```bash
remotecmd-cli relay daemon start --port 3032 -daemon
```

### Step 2 — Add a target machine

**Option A: Pair (easiest — works on any machine)**

```bash
# On your machine:
remotecmd-cli set-relay --url http://<relay-host>:3032 --name myclient
remotecmd-cli pair listen --name myserver
```

It prints a one-liner. Send it to the remote machine — they paste it and you're connected:

```
curl -sSL https://raw.githubusercontent.com/javimosch/remotecmd-cli/master/install.sh \
  | sh -s -- --relay http://<relay-host>:3032 --code a1b2c3d4
```

The remote machine installs the binary, starts the daemon as a persistent systemd service, and sends the pair code. You see:

```
Peer connected! Target "myserver" added (relay name: myhostname)
Run: remotecmd-cli --target myserver --cmd 'hostname'
```

**Option B: Manual (if you already have shell access)**

```bash
# On the target machine:
remotecmd-cli set-relay --url http://<relay-host>:3032 --name myserver
remotecmd-cli daemon start -daemon
# copy the printed token

# On your client:
remotecmd-cli set-relay --url http://<relay-host>:3032 --name myclient
remotecmd-cli add-target --name myserver --token <token>
```

### Step 3 — Execute commands

```bash
# Buffered (returns JSON after completion)
remotecmd-cli --target myserver --cmd 'df -h'

# Streaming (real-time output, line by line)
remotecmd-cli --target myserver --cmd 'npm run build' --stream --timeout 120

# With shorter aliases
rcx myserver 'uptime'
rcx myserver 'docker ps' 15
```

---

## Pairing Flow

The `pair` command is the fastest way to add a machine you don't yet have shell access to:

```
Your machine                           Remote machine
─────────────────                      ─────────────────────────────────────
remotecmd-cli pair                     (receives one-liner from you)
  listen --name vps1
       │                               curl ... | sh -s -- --relay ... --code abc123
       │ registers code "abc123"                │
       │ on relay                               │ installs binary
       │                                        │ sets relay config (name = hostname)
       │                                        │ saves pair code to disk
       │                                        │ starts daemon (systemd or nohup)
       │                                        │
       │◄──────── pair message ─────────────────┘
       │          (code + token + hostname)
       │
  adds target "vps1" → config.json
  prints: Peer connected!
```

- Pair codes are **one-time use** — deleted from relay and disk after match
- The daemon auto-starts on boot via systemd user service (falls back to nohup)
- Aliases save both the hostname (relay-routed) and your custom name

---

## Streaming

Without `--stream`, output is buffered and returned as a JSON object after the command exits.

With `--stream`, stdout and stderr are forwarded **line by line** in real time:

```bash
# Watch a build in real time
remotecmd-cli --target myserver --cmd 'make all' --stream --timeout 300

# Follow logs
remotecmd-cli --target myserver --cmd 'journalctl -f -u nginx' --stream --timeout 3600

# Pipe streaming output locally
remotecmd-cli --target myserver --cmd 'cat /var/log/app.log' --stream | grep ERROR
```

Streaming stdout goes to `stdout` (pipeable). The final summary goes to `stderr`.

---

## Output Format

Buffered execution returns JSON:

```json
{
  "ok": true,
  "stdout": "myserver\n",
  "stderr": "",
  "exit_code": 0,
  "duration_ms": 8
}
```

Streaming execution prints lines directly, then a summary on `stderr`:

```
myserver
{"ok":true,"exit_code":0,"duration_ms":8}
```

---

## Commands Reference

```
EXECUTE:
  remotecmd-cli --target <n> --cmd <cmd> [--timeout <s>] [--stream]

PAIRING:
  remotecmd-cli pair listen [--name <n>] [--timeout <s>]

CONFIGURATION:
  remotecmd-cli set-relay --url <u> --name <n>
  remotecmd-cli add-target --name <n> --token <t>
  remotecmd-cli remove-target --name <n>
  remotecmd-cli list-targets

ALIASES:
  remotecmd-cli alias install      Install rc / rcx / rcl / rcs shortcuts
  remotecmd-cli alias uninstall

RELAY:
  remotecmd-cli relay daemon start [--port 3032] [-daemon]
  remotecmd-cli relay daemon stop
  remotecmd-cli relay daemon status

DAEMON:
  remotecmd-cli daemon start [-daemon]
  remotecmd-cli daemon stop
  remotecmd-cli daemon status
```

### Convenience aliases (after `alias install`)

| Alias | Equivalent | Description |
|-------|-----------|-------------|
| `rc` | `remotecmd-cli` | Full CLI shortcut |
| `rcx <target> <cmd> [timeout]` | `--target <t> --cmd <c>` | Execute command (default 10s) |
| `rcl` | `list-targets` | List configured targets |
| `rcs <target>` | `--target <t> --cmd 'remotecmd status'` | Check daemon status |

---

## Architecture

```
┌────────────┐   WebSocket   ┌─────────────┐   WebSocket   ┌──────────────────┐
│   Client   │──────────────►│  Relay Hub  │◄──────────────│  Target Daemon   │
│ (one-shot) │               │  (always on)│               │  (persistent bg) │
└────────────┘               └─────────────┘               └──────────────────┘
                                    │
                         routes by target name
                         + token verification
```

**Relay** — stateless hub. Accepts WebSocket connections from both daemons and clients. Routes `execute` messages by target name, verifies tokens, forwards `result`/`stream_chunk`/`stream_end` back to the waiting client. Also handles `pair_listen` / `pair` for the pairing flow.

**Daemon** — runs on each target machine. Connects out to relay (no inbound ports). Registers itself by name + token. On `command` message: forks a shell, streams or buffers output, sends result back. On first connect after pairing: sends pair message then deletes the code.

**Client** — one-shot. Connects to relay, sends `execute`, waits for `result` or streams `stream_chunk` to stdout until `stream_end`.

---

## Use Cases

| Scenario | Command |
|----------|---------|
| Quick health check | `rcx myserver 'uptime && df -h'` |
| Deploy to remote | `rcx myserver 'cd /app && git pull && pm2 restart all' 60` |
| Stream build logs | `remotecmd-cli --target myserver --cmd 'make' --stream --timeout 300` |
| Follow app logs | `remotecmd-cli --target myserver --cmd 'tail -f /var/log/app.log' --stream` |
| Run on 3 servers | `for t in web1 web2 web3; do rcx $t 'systemctl status nginx'; done` |
| Add friend's machine | `remotecmd-cli pair listen --name friend` → share one-liner |

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `target not connected` | Daemon not running or wrong relay URL | Check `daemon status` on target; verify relay URL matches |
| `pair code not found` | Code already used or listener timed out | Run `pair listen` again for a fresh code |
| `curl: (23) Failure writing output` | Binary busy (systemd running it) | install.sh now stops service first; re-run the one-liner |
| Token mismatch | Target re-started with new token | Re-add target: `add-target --name <n> --token <new>` |
| Streaming stops early | Default timeout hit | Add `--timeout <seconds>` |

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Language | Go — single static binary, no runtime deps |
| Transport | WebSocket (`gorilla/websocket`) |
| Auth | HMAC token per target |
| Persistence | `~/.remotecmd/config.json` |
| Daemon | PID file + systemd user service (pair install) |
| Streaming | `StdoutPipe` + `bufio.Scanner`, line-by-line forwarding |
| Releases | GitHub Actions → multi-arch binaries (linux/darwin, amd64/arm64) |

---

## License

MIT — [Javier Leandro Arancibia](https://github.com/javimosch)
