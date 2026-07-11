#!/usr/bin/env bash
# Kunai installer. Run this on the machine that will host Claude Code:
#
#   ./install.sh
#
# It finds or builds the kunai binary, detects your Tailscale address, mints a
# TLS certificate, installs a systemd user service (Linux), starts it, and
# prints the URL to open on your phone or laptop.
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
for c in "$HERE/dist/kunai-$PLAT" "$HERE/kunai-$PLAT" "$HERE/kunai"; do
  if [ -f "$c" ]; then BIN="$c"; break; fi
done

if [ -z "$BIN" ] && [ -f "$HERE/go.mod" ] && command -v go >/dev/null 2>&1; then
  if [ ! -f "$HERE/internal/webui/dist/index.html" ]; then
    command -v npm >/dev/null 2>&1 || fail "building from source needs npm for the web app (or use a prebuilt binary)"
    say "building web app..."
    (cd "$HERE/web" && npm install --no-fund --no-audit >/dev/null && npm run build >/dev/null)
  fi
  say "building kunai..."
  (cd "$HERE" && CGO_ENABLED=0 go build -ldflags="-s -w" -o "$HERE/kunai" ./cmd/kunai)
  BIN="$HERE/kunai"
fi

if [ -z "$BIN" ] && command -v gh >/dev/null 2>&1; then
  say "downloading prebuilt binary from the latest release..."
  gh release download -R HEGADE/kunai --pattern "kunai-$PLAT" -O "$HERE/kunai-$PLAT" --clobber \
    && chmod +x "$HERE/kunai-$PLAT" && BIN="$HERE/kunai-$PLAT"
fi

[ -n "$BIN" ] || fail "no kunai binary found and cannot build one. Put kunai-$PLAT next to this script, or install go+npm, or install gh."

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
else
  say "no systemd found: starting is manual on this platform. Run:"
  say "  $BIN_DIR/kunai -addr $TS_IP:$PORT -tls-cert $CRT -tls-key $KEY -data $DATA_DIR $IDENT_ARGS $PUSH_ARG"
fi

# --- health check -------------------------------------------------------------

URL="https://$FQDN:$PORT"
if command -v curl >/dev/null 2>&1; then
  for i in 1 2 3 4 5; do
    if curl -s -m 4 -o /dev/null "$URL/api/stats"; then
      say ""
      say "kunai is running."
      say ""
      say "  Open on any device in your tailnet:  $URL"
      say ""
      say "  iPhone: open it in Safari, then Share > Add to Home Screen."
      say "  Manage: systemctl --user status|restart kunai; logs: journalctl --user -u kunai -f"
      exit 0
    fi
    sleep 1
  done
  fail "server did not answer at $URL/api/stats. Check: journalctl --user -u kunai -n 20"
fi
say "installed. Open $URL"
