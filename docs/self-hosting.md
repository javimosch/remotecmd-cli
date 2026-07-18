# Self-hosting the rcmd relay

> Run your own relay hub instead of using the hosted one at `rcmd.intrane.fr`.

The relay is a single Go binary with no dependencies — no database, no Redis, no Docker. It listens on a port, brokers WebSocket connections between clients and daemons, and stores state in JSON files.

## When to self-host

| Use case | Self-host? |
|----------|-----------|
| Solo dev / small team | No — use the hosted relay (€5/mo, zero ops) |
| Air-gapped network | Yes — relay runs on any Linux box |
| Compliance (data residency) | Yes — your relay, your logs |
| Large fleet (>100 targets) | Yes — dedicated relay, no shared resource |
| Just want to try it | Yes — easiest way to evaluate without an account |

## Quick start (5 minutes)

### 1. Download the binary

```bash
# Linux amd64
curl -sSL https://github.com/javimosch/remotecmd-cli/releases/latest/download/remotecmd-cli-linux-amd64 -o /usr/local/bin/remotecmd-cli
chmod +x /usr/local/bin/remotecmd-cli
```

### 2. Start the relay

```bash
remotecmd-cli relay daemon start --port 3032 -daemon
```

This starts the relay in the background on port 3032. It's now listening for WebSocket connections.

### 3. Install as a systemd service (recommended)

```bash
remotecmd-cli relay daemon systemd install
```

This creates, enables, and starts a systemd service. The relay will survive reboots and auto-restart on crash.

Manage it with:

```bash
sudo systemctl status remotecmd-relay
sudo systemctl restart remotecmd-relay
journalctl -u remotecmd-relay -f
```

To remove:

```bash
remotecmd-cli relay daemon systemd remove
```

### 4. Put it behind a reverse proxy (recommended for production)

The relay speaks plain HTTP/WebSocket. For TLS and domain routing, put it behind a reverse proxy:

**Traefik** (recommended — auto Let's Encrypt):

```yaml
# docker-compose.yml
services:
  traefik:
    image: traefik:v3
    command:
      - --providers.docker=true
      - --entrypoints.web.address=:80
      - --entrypoints.websecure.address=:443
      - --certificatesresolvers.le.acme.tlschallenge=true
      - --certificatesresolvers.le.acme.email=you@example.com
      - --certificatesresolvers.le.acme.storage=/letsencrypt/acme.json
      - --entrypoints.web.http.redirections.entrypoint.to=websecure
      - --entrypoints.web.http.redirections.entrypoint.scheme=https
    ports: ["80:80", "443:443"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - letsencrypt:/letsencrypt

  relay:
    image: alpine:latest
    command: ["/app/remotecmd-cli", "relay", "daemon", "start", "--port", "3032", "-daemon"]
    volumes:
      - ./remotecmd-cli:/app/remotecmd-cli
    labels:
      - traefik.enable=true
      - traefik.http.routers.relay.rule=Host(`relay.yourdomain.com`)
      - traefik.http.routers.relay.entrypoints=websecure
      - traefik.http.routers.relay.tls.certresolver=le
      - traefik.http.services.relay.loadbalancer.server.port=3032

volumes:
  letsencrypt:
```

**Nginx** (manual TLS):

```nginx
server {
    listen 443 ssl;
    server_name relay.yourdomain.com;

    ssl_certificate /etc/letsencrypt/live/relay.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/relay.yourdomain.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:3032;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_read_timeout 86400;
    }
}
```

The `proxy_read_timeout 86400` is important — WebSocket connections are long-lived.

### 5. Connect daemons to your relay

On each target machine:

```bash
# Install the binary
curl -sSL https://github.com/javimosch/remotecmd-cli/releases/latest/download/remotecmd-cli-linux-amd64 -o /usr/local/bin/remotecmd-cli
chmod +x /usr/local/bin/remotecmd-cli

# Point it at your relay
remotecmd-cli set-relay --url https://relay.yourdomain.com --name myserver

# Start the daemon
remotecmd-cli daemon start --token <your-token> -daemon

# Or install as systemd
remotecmd-cli daemon systemd install
```

### 6. Run commands

From any machine configured with the same relay:

```bash
remotecmd-cli --target myserver --cmd 'uptime'
```

## Architecture

```
Client ──wss──► Your relay (port 3032) ──wss──► Daemon (target 1)
                                              └── Daemon (target 2)
                                              └── Daemon (target N)
```

- **Relay** — brokers WebSocket connections, routes commands by target name
- **Daemon** — runs on each target, connects outbound to the relay, executes commands
- **Client** — you (or your AI agent), sends commands through the relay

All connections are outbound from the target's perspective. No open ports on target machines.

## Token management

The self-hosted relay uses simple token auth. Each target registers with a token. Clients must send the same token to execute commands on that target.

```bash
# Generate a token (any random string works)
openssl rand -hex 32

# Use it when starting the daemon
remotecmd-cli daemon start --token <token> -daemon

# Use it from the client
remotecmd-cli add-target --name myserver --token <token>
```

For multi-target operations, each target can have its own token, or they can share one.

## What's different from the hosted relay

| Feature | Self-hosted | Hosted (rcmd.intrane.fr) |
|---------|-------------|--------------------------|
| Relay | You run it | We run it |
| Auth | Token-based (shared secrets) | Customer accounts + Stripe billing |
| Team access | Not available | Scoped sub-tokens (admin/operator/viewer) |
| Scheduled commands (cron) | Not available | Relay-side scheduler |
| Port forwarding (tunnel) | ✅ | ✅ |
| Audit logging | Not available | Per-command audit trail |
| Email invites | Not available | Resend integration |
| Cost | Free (your infra) | €5/month |
| Setup time | 5 minutes | 0 minutes |

The self-hosted relay is the core: execute commands, transfer files, tunnel ports. The hosted relay adds the managed layer — billing, team management, scheduling, audit logs.

## Security considerations

- **TLS**: Always put the relay behind a reverse proxy with TLS. Plain HTTP is fine for testing on localhost, but never expose port 3032 directly to the internet without TLS.
- **Tokens**: Treat relay tokens like SSH keys. Anyone with the token can execute commands on the target. Use different tokens per target if you want isolation.
- **Firewall**: The relay needs one inbound port (e.g., 443 via reverse proxy). Target machines need zero inbound ports — the daemon connects outbound.
- **Logs**: The relay logs all command executions to stdout. In systemd, view with `journalctl -u remotecmd-relay`. No command content is logged by default — only target name, command ID, and success/failure.

## Troubleshooting

**"target not connected"**
The daemon isn't connected to the relay. Check:
- Is the daemon running? `remotecmd-cli daemon status`
- Can the daemon reach the relay? `curl https://relay.yourdomain.com/health`
- Is the relay name correct? `remotecmd-cli set-relay --url ... --name ...`

**"invalid token"**
The token you're sending doesn't match the token the daemon registered with. Verify with `remotecmd-cli list-targets` and compare the token.

**WebSocket upgrade fails behind reverse proxy**
Ensure your proxy forwards the `Upgrade` and `Connection` headers. See the Nginx/Traefik configs above.

**Daemon can't connect through corporate proxy**
WebSockets don't always work through HTTP proxies. If the target is behind a corporate proxy, you may need to use a relay accessible over plain HTTPS (no proxy in the path) or use Tailscale to bypass the proxy.

## Reference

- [GitHub: javimosch/remotecmd-cli](https://github.com/javimosch/remotecmd-cli)
- [Hosted relay: rcmd.intrane.fr](https://rcmd.intrane.fr)
- [Full operator guide](https://rcmd.intrane.fr/guide)
- [Agent discovery: /llms.txt](https://rcmd.intrane.fr/llms.txt)
