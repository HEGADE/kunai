#!/usr/bin/env bash
# Kunai installer. Run this on the machine that will host Claude Code:
#
#   ./install.sh
#
# It finds or builds the kunai binary, detects your Tailscale address, mints a
# TLS certificate, installs a service (systemd user unit on Linux, launchd
# LaunchAgent on macOS), starts it, and prints the URL to open on your devices.
#
# Optional environment:
#   KUNAI_PORT        listen port (default 8443)
#   KUNAI_PUSH_EMAIL  contact email for Web Push (VAPID)
#   KUNAI_HUB_URL     on a peer machine, the hub origin to forward push wake-ups
#                     to (e.g. https://hub.tailnet.ts.net:8443)
set -euo pipefail

say()  { printf '%s\n' "$*"; }
fail() { printf 'error: %s\n' "$*" >&2; exit 1; }

PORT="${KUNAI_PORT:-8443}"
DATA_DIR="$HOME/.kunai"
BIN_DIR="$HOME/.local/bin"
HERE="$(cd "$(dirname "$0")" && pwd)"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  arm64)   ARCH=arm64 ;;
esac
PLAT="$OS-$ARCH"

# dl_stdout/dl_file fetch a URL (via curl or wget) to stdout / to a file.
dl_stdout() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL -m 15 "$1"
  elif command -v wget >/dev/null 2>&1; then wget -qO- --timeout=15 "$1"
  else return 1; fi
}
dl_file() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL -m 600 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then wget -q --timeout=600 -O "$2" "$1"
  else return 1; fi
}

# ensure_go sets GO to a usable `go`. If none is on PATH it bootstraps the
# official Go toolchain into $DATA_DIR/toolchain/go — a self-contained tarball,
# so no package manager, no root, and no PATH pollution (used only to build).
GO=""
GO_FALLBACK_VERSION="go1.23.5"
ensure_go() {
  if command -v go >/dev/null 2>&1; then GO="$(command -v go)"; return; fi
  local root="$DATA_DIR/toolchain/go" ver url tmp
  if [ -x "$root/bin/go" ]; then GO="$root/bin/go"; return; fi
  ver="$(dl_stdout 'https://go.dev/VERSION?m=text' 2>/dev/null | head -1 | tr -d '[:space:]')"
  [ -n "$ver" ] || ver="$GO_FALLBACK_VERSION"
  url="https://go.dev/dl/${ver}.${OS}-${ARCH}.tar.gz"
  say "go not found — fetching ${ver} (${OS}-${ARCH}) into $root ..."
  mkdir -p "$DATA_DIR/toolchain"
  tmp="$(mktemp)"
  if dl_file "$url" "$tmp" && tar -C "$DATA_DIR/toolchain" -xzf "$tmp" 2>/dev/null && [ -x "$root/bin/go" ]; then
    GO="$root/bin/go"
    say "installed Go toolchain ($ver)"
  else
    say "note: could not bootstrap Go (will look for a prebuilt binary instead)"
  fi
  rm -f "$tmp"
}

# --- prerequisites ----------------------------------------------------------

# Find claude even when this shell's PATH is minimal (e.g. over ssh).
CLAUDE_BIN="$(command -v claude 2>/dev/null || true)"
for c in "$HOME/.local/bin/claude" /usr/local/bin/claude /opt/homebrew/bin/claude; do
  [ -n "$CLAUDE_BIN" ] && break
  [ -x "$c" ] && CLAUDE_BIN="$c"
done
[ -n "$CLAUDE_BIN" ] \
  || fail "the 'claude' CLI is required on this machine. Install Claude Code first: https://claude.com/claude-code"

command -v tailscale >/dev/null 2>&1 \
  || fail "tailscale is required. Install it and run 'tailscale up' first."

TS_IP="$(tailscale ip -4 2>/dev/null | head -1 || true)"
[ -n "$TS_IP" ] || fail "could not get a Tailscale IP. Is tailscale up?"

FQDN="$(tailscale status --json 2>/dev/null \
  | /usr/bin/env sed -n 's/.*"DNSName": *"\([^"]*\)".*/\1/p' | head -1 | sed 's/\.$//')"
[ -n "$FQDN" ] || fail "could not determine this machine's MagicDNS name. Enable MagicDNS for your tailnet."

# --- find or build the binary -----------------------------------------------

BIN=""
# In a source checkout, always build fresh — prebuilt dirs (dist/, ./kunai) may
# be stale artifacts from an earlier `make release`, so they must NOT win here.
# Go is bootstrapped automatically if it's not already installed.
if [ -f "$HERE/go.mod" ]; then
  ensure_go
fi
if [ -n "$GO" ]; then
  # The web app (internal/webui/dist) is committed and embedded, so npm is only
  # needed in the rare case that dist is missing.
  if [ ! -f "$HERE/internal/webui/dist/index.html" ]; then
    command -v npm >/dev/null 2>&1 || fail "building from source needs npm for the web app (or use a prebuilt binary)"
    say "building web app..."
    (cd "$HERE/web" && npm install --no-fund --no-audit >/dev/null && npm run build >/dev/null)
  fi
  say "building kunai..."
  VERSION="$(cd "$HERE" && git describe --tags --always 2>/dev/null || echo dev)"
  (cd "$HERE" && CGO_ENABLED=0 "$GO" build \
    -ldflags="-s -w -X 'github.com/hegade/kunai/internal/server.buildVersion=$VERSION'" \
    -o "$HERE/kunai" ./cmd/kunai)
  BIN="$HERE/kunai"
fi

# Release tarball (no toolchain): use a bundled prebuilt binary.
if [ -z "$BIN" ]; then
  for c in "$HERE/dist/kunai-$PLAT" "$HERE/kunai-$PLAT" "$HERE/kunai"; do
    if [ -f "$c" ]; then BIN="$c"; break; fi
  done
fi

if [ -z "$BIN" ] && command -v gh >/dev/null 2>&1; then
  say "downloading prebuilt binary from the latest release..."
  gh release download -R HEGADE/kunai --pattern "kunai-$PLAT" -O "$HERE/kunai-$PLAT" --clobber \
    && chmod +x "$HERE/kunai-$PLAT" && BIN="$HERE/kunai-$PLAT"
fi

[ -n "$BIN" ] || fail "no kunai binary and could not build one (Go bootstrap needs curl or wget and network access). Put a kunai-$PLAT binary next to this script, or install Go, or install gh."

# --- TLS certificate over Tailscale -----------------------------------------

TLS_DIR="$DATA_DIR/tls"
mkdir -p "$TLS_DIR"
CRT="$TLS_DIR/$FQDN.crt"
KEY="$TLS_DIR/$FQDN.key"
if [ ! -s "$CRT" ] || [ ! -s "$KEY" ]; then
  say "minting TLS certificate for $FQDN..."
  if ! tailscale cert --cert-file "$CRT" --key-file "$KEY" "$FQDN" 2>/dev/null; then
    sudo tailscale cert --cert-file "$CRT" --key-file "$KEY" "$FQDN" \
      && sudo chown "$USER" "$CRT" "$KEY" \
      || fail "could not mint a certificate. Enable HTTPS certificates in the Tailscale admin console (DNS > HTTPS Certificates)."
  fi
fi

# --- install binary ----------------------------------------------------------

mkdir -p "$BIN_DIR" "$DATA_DIR"
install -m 0755 "$BIN" "$BIN_DIR/kunai.new"
mv "$BIN_DIR/kunai.new" "$BIN_DIR/kunai"

PUSH_ARG=""
[ -n "${KUNAI_PUSH_EMAIL:-}" ] && PUSH_ARG="-push-email ${KUNAI_PUSH_EMAIL}"

# This machine's own tailnet origin, so it can identify itself in the machine
# registry the multi-machine client reads.
PUBLIC_URL="https://$FQDN:$PORT"
IDENT_ARGS="-public-url $PUBLIC_URL"
# On a peer machine, point -hub-url at the machine you installed the PWA from so
# its Web Push wake-ups reach your phone via that hub.
[ -n "${KUNAI_HUB_URL:-}" ] && IDENT_ARGS="$IDENT_ARGS -hub-url ${KUNAI_HUB_URL}"

# --- service ------------------------------------------------------------------

if [ "$OS" = "linux" ] && command -v systemctl >/dev/null 2>&1; then
  UNIT_DIR="$HOME/.config/systemd/user"
  mkdir -p "$UNIT_DIR"
  CLAUDE_DIR="$(dirname "$CLAUDE_BIN")"
  cat > "$UNIT_DIR/kunai.service" <<EOF
[Unit]
Description=Kunai - self-hosted client for Claude Code
After=network-online.target tailscaled.service

[Service]
Environment=PATH=$CLAUDE_DIR:/usr/local/bin:/usr/bin:/bin
ExecStart=$BIN_DIR/kunai -addr $TS_IP:$PORT -tls-cert $CRT -tls-key $KEY -data $DATA_DIR $IDENT_ARGS $PUSH_ARG
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
EOF
  export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
  loginctl enable-linger "$USER" 2>/dev/null || sudo loginctl enable-linger "$USER" 2>/dev/null \
    || say "note: could not enable lingering; the service stops when you log out (sudo loginctl enable-linger $USER)"
  systemctl --user daemon-reload
  systemctl --user enable --now kunai >/dev/null 2>&1 || systemctl --user restart kunai
  sleep 2
  systemctl --user is-active --quiet kunai || {
    journalctl --user -u kunai -n 10 --no-pager >&2 || true
    fail "service failed to start (see log above)"
  }
elif [ "$OS" = "darwin" ]; then
  # macOS: a launchd LaunchAgent is the systemd-user equivalent (auto-start,
  # keep-alive, survives reboot).
  PLIST="$HOME/Library/LaunchAgents/com.kunai.agent.plist"
  mkdir -p "$HOME/Library/LaunchAgents"
  CLAUDE_DIR="$(dirname "$CLAUDE_BIN")"
  ARGS=(-addr "$TS_IP:$PORT" -tls-cert "$CRT" -tls-key "$KEY" -data "$DATA_DIR" -public-url "$PUBLIC_URL")
  [ -n "${KUNAI_HUB_URL:-}" ]    && ARGS+=(-hub-url "$KUNAI_HUB_URL")
  [ -n "${KUNAI_PUSH_EMAIL:-}" ] && ARGS+=(-push-email "$KUNAI_PUSH_EMAIL")
  {
    printf '<?xml version="1.0" encoding="UTF-8"?>\n'
    printf '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n'
    printf '<plist version="1.0"><dict>\n'
    printf '  <key>Label</key><string>com.kunai.agent</string>\n'
    printf '  <key>ProgramArguments</key><array>\n'
    printf '    <string>%s</string>\n' "$BIN_DIR/kunai"
    for a in "${ARGS[@]}"; do printf '    <string>%s</string>\n' "$a"; done
    printf '  </array>\n'
    printf '  <key>EnvironmentVariables</key><dict><key>PATH</key><string>%s</string></dict>\n' "$CLAUDE_DIR:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin"
    printf '  <key>RunAtLoad</key><true/>\n'
    printf '  <key>KeepAlive</key><true/>\n'
    printf '  <key>StandardOutPath</key><string>%s</string>\n' "$DATA_DIR/kunai.log"
    printf '  <key>StandardErrorPath</key><string>%s</string>\n' "$DATA_DIR/kunai.log"
    printf '</dict></plist>\n'
  } > "$PLIST"
  launchctl unload "$PLIST" 2>/dev/null || true
  sleep 1 # let any previous instance release the port before rebinding
  launchctl load "$PLIST" || fail "launchctl load failed (see $DATA_DIR/kunai.log)"
  # Readiness is confirmed by the shared health check below.
else
  say "no service manager for this platform. Run manually:"
  say "  $BIN_DIR/kunai -addr $TS_IP:$PORT -tls-cert $CRT -tls-key $KEY -data $DATA_DIR $IDENT_ARGS $PUSH_ARG"
fi

# --- health check -------------------------------------------------------------

URL="https://$FQDN:$PORT"
if command -v curl >/dev/null 2>&1; then
  for i in 1 2 3 4 5 6 7 8 9 10 11 12; do
    if curl -s -m 4 -o /dev/null "$URL/api/stats"; then
      say ""
      say "kunai is running."
      say ""
      say "  Open on any device in your tailnet:  $URL"
      say ""
      say "  iPhone: open it in Safari, then Share > Add to Home Screen."
      if [ "$OS" = "darwin" ]; then
        say "  Manage: launchctl list | grep kunai; logs: $DATA_DIR/kunai.log"
        say "  Stop:   launchctl unload ~/Library/LaunchAgents/com.kunai.agent.plist"
      else
        say "  Manage: systemctl --user status|restart kunai; logs: journalctl --user -u kunai -f"
      fi
      exit 0
    fi
    sleep 1
  done
  if [ "$OS" = "darwin" ]; then
    fail "server did not answer at $URL/api/stats. Check: tail $DATA_DIR/kunai.log"
  fi
  fail "server did not answer at $URL/api/stats. Check: journalctl --user -u kunai -n 20"
fi
say "installed. Open $URL"
