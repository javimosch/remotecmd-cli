# remotecmd-cli Vision

> **Remote execution for the fleet era.** SSH was built for one machine. remotecmd-cli was built for ten, a hundred, a thousand.

## Why remotecmd-cli?

SSH is the Unix standard for remote access — and it's showing its age. Every machine needs key management, open ports, and individual connection handling. For AI agents, SSH output is inconsistent, unparseable, and token-wasting.

remotecmd-cli replaces the SSH mental model with a **WebSocket relay**:

```
Client ──ws──► Relay Hub ──ws──► Daemon (target 1)
                                  └── Daemon (target 2)
                                  └── Daemon (target N)
```

- **Zero inbound ports** — daemons connect out to the relay
- **Zero key management** — token auth, auto-generated
- **JSON by default** — structured output, agent-first
- **Multi-target** — blast commands across your fleet in parallel
- **NAT-proof** — works over Tailscale, public internet, corporate proxies

## Target Audience

| Who | What remotecmd-cli gives them |
|-----|------------------------------|
| **Sysadmins** | Fleet-wide command execution without SSH keys, VPNs, or agent installation hell |
| **DevOps engineers** | Scriptable remote execution for deployment pipelines, health checks, incident response |
| **AI agents** | Deterministic JSON output, zero parsing, streaming, timeout-safe |
| **Linux hackers** | A hackable, single-binary tool that composes with jq, grep, and shell scripts |
| **Solo devs** | 10-second machine onboarding via pair codes; manage VPS, homelab, and dev boxes from one CLI |

## Roadmap

### v1.x — Foundation ✅

- [x] Single-target command execution
- [x] WebSocket relay architecture
- [x] Token-based auth
- [x] Pairing flow (add any machine in 10 seconds)
- [x] Streaming output
- [x] File transfer (file + directory via tar)
- [x] Convenience aliases (rc, rcx, rcl, rcs, rcc)
- [x] Config persistence (~/.remotecmd/config.json)

### v1.2 — Multi-Target & Groups ✅

- [x] `exec --targets <list>` — comma-separated target names
- [x] `exec --group <name>` — named target groups
- [x] `group create / add / remove / delete / list`
- [x] Table format output for human reading
- [x] JSON format output for agent/script consumption
- [x] Relay-level fan-out with result aggregation
- [x] Partial failure handling (per-target errors in results)

### v1.3 — Tier 2 (Script-Friendly Exit Codes & systemd) 🎯

- [ ] **Exit codes for multi-target execution**
  - Exit 0 if all targets OK
  - Exit 1 if any target failed
  - Exit 2 on connection/relay errors
- [ ] **rcs exit code improvement** — return daemon status as exit code (0=running, 1=not running)
- [ ] **systemd unit generation**
  - `remotecmd-cli daemon install-systemd` — creates + enables user service
  - `remotecmd-cli relay install-systemd` — creates + enables system service
  - Proper restart policy, logging, and dependency ordering
- [ ] **Daemon log management**
  - Log rotation support
  - `--log-file` flag for daemon/relay
  - Structured JSON logging option

### v1.4 — Persistent Connections 🔌

- [ ] **Client session mode** — single WebSocket, multiplexed requests
  - Avoids TLS handshake and WebSocket upgrade per command
  - 10-50x faster for sequential command bursts
- [ ] **Connection pool** — keep N connections open for parallel workloads
- [ ] **Auto-reconnect** with backoff (client-side)
- [ ] **Subscription mode** — `exec --subscribe` streams results as they complete
  - No need to wait for slowest target
  - Each target result emitted as JSONL as it arrives

### v1.5 — TLS Security 🔒

- [ ] **Relay TLS** — `--tls-cert`, `--tls-key` for relay daemon
- [ ] **Client WSS** — auto-detect `wss://` from `https://` relay URL
- [ ] **LetsEncrypt auto** — optional auto-cert via ACME
- [ ] **mTLS** — optional mutual TLS for daemon-to-relay auth
- [ ] **Connection metadata in relay logs** — peer IP, TLS cipher, connection duration

### Future Horizons

| Feature | Why |
|---------|-----|
| **Port forwarding** | Replace `ssh -L` for ad-hoc tunnels |
| **Interactive PTY** | Run `htop`, `less`, `top` remotely |
| **Cron-style scheduled exec** | `exec --at '0 2 * * *' --group prod --cmd 'apt update'` |
| **Web dashboard** | Real-time fleet view, command history, result browser |
| **Ansible-like playbooks** | YAML-free sequential/conditional command chains |
| **Prometheus exporter** | Relay exposes daemon health metrics |
| **Webhook triggers** | `exec --on-change --watch /etc/nginx/nginx.conf --cmd 'reload'` |

## Design Principles

1. **One binary, zero deps.** Static Go binary. No Python, no Ruby, no Node.
2. **JSON by default.** Every command returns structured JSON. Humans can use `--format table` or pipe to `jq`.
3. **Agent-first.** AI agents should be first-class users. Deterministic output, explicit timeouts, no parsing required.
4. **No lock-in.** Your config is a local JSON file. Your relay can be a $5 VPS. Your daemons can die and come back.
5. **Composable.** remotecmd-cli plays nice with shell pipes, `xargs`, `jq`, `curl`, and CI pipelines.
6. **Progressive complexity.** Start with `rcx myserver 'uptime'`. Graduate to groups. Then fleet-wide orchestration.

## Comparison

| Feature | SSH | Ansible | SaltStack | **remotecmd-cli** |
|---------|-----|---------|-----------|-------------------|
| Open ports required | Yes (22) | Yes (22) | Yes (4505/4506) | **No (outbound only)** |
| Key management | Manual | Manual | Manual | **Auto (tokens)** |
| NAT traversal | VPN needed | VPN needed | VPN needed | **Built-in (relay)** |
| Multi-target | Loop + cssh | Playbooks | Targeting | **Native (exec --targets)** |
| Output format | Text | Text | Text | **JSON** |
| Agent-friendly | No | No | No | **Yes** |
| Setup time | 5 min | 15 min | 30 min | **10 seconds (pair)** |
| Binary size | 5MB+ | 100MB+ | 200MB+ | **5MB static** |
