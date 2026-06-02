#!/bin/sh
# remotecmd-cli install + pair script
# Usage: curl -sSL https://raw.githubusercontent.com/javimosch/remotecmd-cli/master/install.sh | sh -s -- --relay <url> --code <code>
set -e

REPO="javimosch/remotecmd-cli"
INSTALL_DIR="${HOME}/.local/bin"
BIN="remotecmd-cli"
RELAY_URL=""
PAIR_CODE=""

# --- Parse args ---
while [ $# -gt 0 ]; do
  case "$1" in
    --relay) RELAY_URL="$2"; shift 2 ;;
    --code)  PAIR_CODE="$2"; shift 2 ;;
    *) shift ;;
  esac
done

if [ -z "$RELAY_URL" ] || [ -z "$PAIR_CODE" ]; then
  echo "Usage: install.sh --relay <relay-url> --code <pair-code>"
  exit 1
fi

# --- Detect arch ---
ARCH=$(uname -m)
OS=$(uname -s | tr '[:upper:]' '[:lower:]')

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

ASSET="${BIN}-${OS}-${ARCH}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"

# --- Stop any running daemon before overwriting binary ---
RCMD_EXISTING="${INSTALL_DIR}/${BIN}"
if [ -x "$RCMD_EXISTING" ]; then
  "$RCMD_EXISTING" daemon stop 2>/dev/null || true
fi
# Also stop systemd service if present
if command -v systemctl >/dev/null 2>&1; then
  systemctl --user stop remotecmd.service 2>/dev/null || true
fi

# --- Install binary ---
mkdir -p "$INSTALL_DIR"
TMP_BIN="/tmp/${BIN}.tmp.$$"

echo "[remotecmd] Downloading $BIN ($OS/$ARCH)..."
if command -v curl >/dev/null 2>&1; then
  curl -sSL "$DOWNLOAD_URL" -o "$TMP_BIN"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$TMP_BIN" "$DOWNLOAD_URL"
else
  echo "Error: curl or wget is required"
  exit 1
fi

chmod +x "$TMP_BIN"
# Replace atomically; works even if destination is on a different fs
cp -f "$TMP_BIN" "${INSTALL_DIR}/${BIN}" && rm -f "$TMP_BIN"

# Add to PATH if not already there
case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    export PATH="${INSTALL_DIR}:$PATH"
    # Persist in shell rc
    for RC in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
      if [ -f "$RC" ]; then
        if ! grep -q "$INSTALL_DIR" "$RC" 2>/dev/null; then
          echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "$RC"
        fi
        break
      fi
    done
    ;;
esac

RCMD="${INSTALL_DIR}/${BIN}"
HOSTNAME_VAL=$(hostname 2>/dev/null || echo "peer")

# --- Configure relay ---
echo "[remotecmd] Configuring relay: $RELAY_URL (name: $HOSTNAME_VAL)..."
"$RCMD" set-relay --url "$RELAY_URL" --name "$HOSTNAME_VAL"

# --- Save pair code ---
echo "[remotecmd] Saving pair code..."
mkdir -p "${HOME}/.remotecmd"
echo "$PAIR_CODE" > "${HOME}/.remotecmd/pair_code"
chmod 600 "${HOME}/.remotecmd/pair_code"

# --- Start daemon (background, persistent) ---
echo "[remotecmd] Starting daemon..."

# Try systemd user service first
if command -v systemctl >/dev/null 2>&1 && systemctl --user status >/dev/null 2>&1; then
  SERVICE_DIR="${HOME}/.config/systemd/user"
  mkdir -p "$SERVICE_DIR"
  cat > "${SERVICE_DIR}/remotecmd.service" << UNIT
[Unit]
Description=remotecmd-cli daemon
After=network.target

[Service]
ExecStart=${RCMD} daemon start
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
UNIT
  systemctl --user daemon-reload
  systemctl --user enable remotecmd.service
  systemctl --user restart remotecmd.service
  echo "[remotecmd] Daemon started via systemd user service (auto-starts on login)"
else
  # Fallback: nohup background
  "$RCMD" daemon start --daemon
  echo "[remotecmd] Daemon started in background (nohup)"
fi

echo "[remotecmd] Done! Connecting to relay and sending pair code..."
echo "[remotecmd] Your machine will appear as target: $HOSTNAME_VAL"
