---
name: remotecmd-fleet-update
description: Roll a new remotecmd release out across a fleet of targets and relays safely — version audit, per-platform update commands, in-place re-exec semantics, relay-first ordering, Windows dual-daemon pairs, musl/static container builds, and relay-secret admission gotchas. Use when asked to "update all nodes", "check node versions", or perform a fleet upgrade.
---

# Fleet update runbook

Goal: every reachable daemon (and the relay) on the latest GitHub release, verified, without losing the access path mid-rollout.

## 1. Find the latest version

```sh
gh release view --repo <owner>/remotecmd-cli --json tagName   # authoritative
git -C <repo> tag --sort=-creatordate | head -3               # fallback
```

Do NOT assume the local client's version is latest — a node can run newer than your CLI. The `list-targets` VERSION column marks `*` = older than *your client*, so upgrade your own CLI first (`remotecmd-cli update`) or the `*` markers lie.

## 2. Audit

```sh
remotecmd-cli list-targets --refresh     # VERSION + STATUS columns
```

Bucket the result:

- **up + older** → update now
- **up + latest** → skip
- **down** → skip, report; never block the rollout on them
- **`?` version** → Windows/macOS or unprobed; verify per-platform before claiming updated

## 3. Update patterns (pick per node)

All v2.6.x+ daemons self-update **in place**: same PID, SIGUSR2 re-exec after in-flight commands finish — the `rcx` result still returns, the node stays registered.

### Linux, daemon binary writable by the daemon's user

```sh
rcx <node> '/proc/$PPID/exe daemon update'
```

`/proc/$PPID/exe` is the running daemon's own binary — NOT the PATH copy. They can differ; updating the wrong file leaves the daemon on the old version.

### Linux, binary owned by root / daemon runs as non-root

The updater can't swap a file it doesn't own — run it with sudo:

```sh
rcx <node> 'sudo -n /proc/$PPID/exe daemon update'   # needs passwordless sudo
```

### Pre-2.6 daemons (no self-updater)

Bootstrap once with the release binary — it installs and restarts the old daemon (transient systemd timer under a unit; `"method":"manual"` + exit 3 for unsupervised processes, which then need one manual restart):

```sh
rcx <node> 'curl -fsSL -o /tmp/rc https://github.com/<owner>/remotecmd-cli/releases/latest/download/remotecmd-cli-linux-amd64 && chmod +x /tmp/rc && /tmp/rc daemon update'
```

For arm64 use `remotecmd-cli-linux-arm64(-static)`; for Alpine/musl containers use the **`-static`** asset — the dynamic build fails with `not found` (missing interpreter).

### Relay host — order matters

The relay and the node daemon often share ONE binary file. Update the **relay first or last, never rely on either surviving**:

```sh
rcx <relay-host> '/proc/$PPID/exe daemon update'                        # node daemon
rcx <relay-host> '~/.local/bin/remotecmd-cli relay daemon update'       # relay re-execs onto the new binary
```

The relay re-exec is in-place too (same listening socket), but every client sees a brief reconnect wave — do it once, not per-node.

### Windows — dual-daemon pairs

A Windows box may run TWO independent daemons (e.g. a SYSTEM service and a second named instance). **Never update both at once** — keep one as the access path:

1. Download the release `.exe` to `C:\Windows\Temp\rc.exe` via one target.
2. Through the OTHER target: `rc.exe daemon update --name <other-name>` (named daemons) or `rc.exe daemon update --pid <pid>` (unnamed — get the pid from `tasklist | findstr remotecmd`).
3. Wait ~10s, verify that target re-registered on the new version, then update the second one the same way through the first.

The updater renames the running exe aside, installs, and a detached helper restarts with rollback to `.bak`; the helper log is `C:\WINDOWS\SystemTemp\rcmd-restart-*.log`. After a helper restart the daemon runs outside its scheduled task until next boot.

### Containers (docker exec'd daemons, not the main process)

The in-place updater only sees processes it can signal. For a daemon started via `docker exec -d`, either:

- if reachable: `rcx <node> '/proc/$PPID/exe daemon update'` (works — PID namespaces are handled in v2.6.3+), or
- if down: `docker cp` the correct-arch **static** binary into the container path, `docker exec -d ... daemon start`, and update the host-side restore copy the watchdog uses.

## 4. Relay-secret admission gotcha

On a relay with `RELAY_SECRET`, a daemon registers only if its config secret matches, its name is in `RELAY_SECRET_EXEMPT`, it was previously admitted (name+token remembered), or it presents a valid activation key. Symptom in the daemon log:

```
Relay error: authentication required: relay secret not provided
```

Fix the secret in the daemon's `config.json` (`relay.secret`) — on BOTH the live config and any watchdog/template copy that restores it — then restart the daemon. Check the relay's `/etc/*relay*.env` (or unit `Environment=`) for the expected value and the exempt list.

## 5. Verify

```sh
remotecmd-cli list-targets --refresh   # every updated node shows the new version, no `*`
```

Spot-check `daemon_version` in an `rcx` result (absent on old relays — verify with `<exe> version` instead). Then update your own CLI if it wasn't already latest.

## 6. Report

Table: node | old → new | method | status. Explicitly list skipped-down nodes so nobody assumes they were updated.
