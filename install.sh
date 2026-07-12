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
fail() { printf '%serror:%s %s\n' "${C_R:-}${C_B:-}" "${C_RST:-}" "$*" >&2; exit 1; }

# Colors — only when writing to a terminal, and never if NO_COLOR is set.
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  C_G=$'\033[32m'; C_R=$'\033[31m'; C_Y=$'\033[33m'; C_DIM=$'\033[2m'; C_B=$'\033[1m'; C_RST=$'\033[0m'
else
  C_G=; C_R=; C_Y=; C_DIM=; C_B=; C_RST=
fi

# Preflight report rows. chk_bad records a hint and flags MISSING so we can show
# the whole picture at once, then fail with everything the user needs to fix.
MISSING=0; HINTS=""; NEED_CLAUDE=0; NEED_TS=0
chk_ok()  { printf '  %s✓%s %s%-12s%s %s%s%s\n' "$C_G" "$C_RST" "$C_B" "$1" "$C_RST" "$C_DIM" "$2" "$C_RST"; }
chk_opt() { printf '  %s•%s %s%-12s%s %s%s%s\n' "$C_Y" "$C_RST" "$C_B" "$1" "$C_RST" "$C_DIM" "$2" "$C_RST"; }
chk_bad() {
  printf '  %s✗%s %s%-12s%s %s%s%s\n' "$C_R" "$C_RST" "$C_B" "$1" "$C_RST" "$C_R" "$2" "$C_RST"
  MISSING=1
  HINTS="${HINTS}
    ${C_B}${1}${C_RST} — ${3}"
}

# find_bin locates a CLI on PATH, else at known fallback locations (e.g. the
# Tailscale CLI lives inside the macOS app bundle and isn't on PATH by default).
find_bin() {
  local n="$1"; shift
  local p; p="$(command -v "$n" 2>/dev/null || true)"
  if [ -n "$p" ]; then printf '%s' "$p"; return 0; fi
  local c; for c in "$@"; do [ -x "$c" ] && { printf '%s' "$c"; return 0; }; done
  return 1
}

# ask prompts on the controlling terminal (works even under `curl | bash`, where
# stdin is the piped script). Returns 0 only on an explicit yes; if there's no
# terminal it declines, so non-interactive runs never auto-install.
ask() {
  # Open the controlling terminal on fd 3; if there isn't one, decline silently.
  { exec 3<>/dev/tty; } 2>/dev/null || return 1
  printf '%s%s%s [y/N] ' "$C_B" "$1" "$C_RST" >&3
  local a; IFS= read -r a <&3 || { exec 3<&- 3>&-; return 1; }
  exec 3<&- 3>&-
  case "$a" in y | Y | yes | YES | Yes) return 0 ;; *) return 1 ;; esac
}

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
# sha256_of prints the hex sha256 of a file (empty if no hasher is available).
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  else printf ''; fi
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

# --- prerequisites (preflight report) ---------------------------------------

# Locate the CLIs even when PATH is minimal (ssh) or the CLI lives off PATH
# (Tailscale on macOS ships its CLI inside the app bundle).
CLAUDE_BIN="$(find_bin claude "$HOME/.local/bin/claude" /usr/local/bin/claude /opt/homebrew/bin/claude || true)"
TS_BIN="$(find_bin tailscale /Applications/Tailscale.app/Contents/MacOS/Tailscale /usr/local/bin/tailscale /opt/homebrew/bin/tailscale /usr/bin/tailscale || true)"

say ""
say "${C_B}Kunai installer${C_RST} ${C_DIM}· $PLAT${C_RST}"
say ""

if [ -n "$CLAUDE_BIN" ]; then chk_ok "Claude Code" "$CLAUDE_BIN"
else chk_bad "Claude Code" "not found" "install Claude Code and sign in: https://claude.com/claude-code"; NEED_CLAUDE=1; fi

if [ -n "$TS_BIN" ]; then chk_ok "Tailscale" "$TS_BIN"
else chk_bad "Tailscale" "CLI not found" "install Tailscale; on macOS the CLI is inside the app (/Applications/Tailscale.app/Contents/MacOS/Tailscale)"; NEED_TS=1; fi

TS_IP=""; FQDN=""
if [ -n "$TS_BIN" ]; then
  TS_IP="$("$TS_BIN" ip -4 2>/dev/null | head -1 || true)"
  if [ -n "$TS_IP" ]; then chk_ok "Tailnet" "$TS_IP"
  else chk_bad "Tailnet" "not connected" "run: tailscale up"; fi

  FQDN="$("$TS_BIN" status --json 2>/dev/null \
    | /usr/bin/env sed -n 's/.*"DNSName": *"\([^"]*\)".*/\1/p' | head -1 | sed 's/\.$//')"
  if [ -n "$FQDN" ]; then chk_ok "MagicDNS" "$FQDN"
  else chk_bad "MagicDNS" "no name" "enable MagicDNS in the Tailscale admin console (DNS tab)"; fi
fi

# Build toolchain is informational — the installer handles it automatically.
if command -v go >/dev/null 2>&1; then chk_ok "Go" "$(command -v go)"
elif [ -f "$HERE/go.mod" ]; then chk_opt "Go" "not installed — will fetch a local toolchain"
else chk_opt "Go" "not needed — using a prebuilt binary"; fi

# A downloader is required only when we'll need to fetch something.
if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1 \
   && { [ ! -f "$HERE/go.mod" ] || ! command -v go >/dev/null 2>&1; }; then
  chk_bad "curl / wget" "not found" "install curl or wget (needed to download Go or a binary)"
fi

say ""
if [ "$MISSING" -ne 0 ]; then
  printf '%s✗ some prerequisites are missing:%s\n' "$C_R$C_B" "$C_RST"
  printf '%s\n\n' "$HINTS"

  # On Linux we can offer to install the missing CLIs (official installers,
  # behind an explicit y/N). macOS Tailscale is a GUI app and Claude sign-in is a
  # browser flow, so there — and in any non-interactive run — we just guide and
  # ask the user to re-run.
  if [ "$OS" = "linux" ] && { [ "$NEED_TS" = 1 ] || [ "$NEED_CLAUDE" = 1 ]; }; then
    did=0
    if [ "$NEED_TS" = 1 ] && ask "Install Tailscale now?  (curl -fsSL https://tailscale.com/install.sh | sh)"; then
      say "${C_DIM}installing Tailscale…${C_RST}"
      dl_stdout https://tailscale.com/install.sh | sh && did=1 || say "${C_Y}Tailscale install did not complete${C_RST}"
    fi
    if [ "$NEED_CLAUDE" = 1 ] && ask "Install Claude Code now?  (curl -fsSL https://claude.ai/install.sh | bash)"; then
      say "${C_DIM}installing Claude Code…${C_RST}"
      dl_stdout https://claude.ai/install.sh | bash && did=1 || say "${C_Y}Claude Code install did not complete${C_RST}"
    fi
    say ""
    if [ "$did" = 1 ]; then
      say "${C_G}Installed.${C_RST} Now sign in — run ${C_B}tailscale up${C_RST} and ${C_B}claude${C_RST} once — then re-run ${C_B}./install.sh${C_RST}"
      exit 0
    fi
  fi

  fail "install the above, then re-run ./install.sh"
fi

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

# Release tarball (no toolchain): use a bundled prebuilt binary if one is here.
if [ -z "$BIN" ]; then
  for c in "$HERE/dist/kunai-$PLAT" "$HERE/kunai-$PLAT" "$HERE/kunai"; do
    if [ -f "$c" ]; then BIN="$c"; break; fi
  done
fi

# Otherwise fetch the matching prebuilt from the latest GitHub release (curl or
# wget only — no gh, git, or Go) and verify its sha256. This is what makes
# `curl -fsSL …/install.sh | bash` work; re-running it later updates in place.
if [ -z "$BIN" ]; then
  REL="https://github.com/HEGADE/kunai/releases/latest/download"
  out="$DATA_DIR/kunai-$PLAT"
  mkdir -p "$DATA_DIR"
  say "${C_DIM}downloading prebuilt kunai ($PLAT) from the latest release…${C_RST}"
  if dl_file "$REL/kunai-$PLAT" "$out"; then
    want="$(dl_stdout "$REL/checksums.txt" 2>/dev/null | awk -v f="kunai-$PLAT" '{n=$2; sub(/^\*/,"",n); if (n==f) print $1}' | head -1)"
    got="$(sha256_of "$out")"
    if [ -n "$want" ] && [ -n "$got" ] && [ "$want" != "$got" ]; then
      rm -f "$out"; fail "checksum mismatch for kunai-$PLAT (expected $want, got $got)"
    fi
    chmod +x "$out"; BIN="$out"
  elif command -v gh >/dev/null 2>&1; then
    say "${C_DIM}trying gh…${C_RST}"
    gh release download -R HEGADE/kunai --pattern "kunai-$PLAT" -O "$out" --clobber \
      && chmod +x "$out" && BIN="$out"
  fi
fi

[ -n "$BIN" ] || fail "no kunai binary and could not build one (Go bootstrap needs curl or wget and network access). Put a kunai-$PLAT binary next to this script, or install Go, or install gh."

# --- TLS certificate over Tailscale -----------------------------------------

TLS_DIR="$DATA_DIR/tls"
mkdir -p "$TLS_DIR"
CRT="$TLS_DIR/$FQDN.crt"
KEY="$TLS_DIR/$FQDN.key"
if [ ! -s "$CRT" ] || [ ! -s "$KEY" ]; then
  say "${C_DIM}minting TLS certificate for $FQDN…${C_RST}"
  if ! "$TS_BIN" cert --cert-file "$CRT" --key-file "$KEY" "$FQDN" 2>/dev/null; then
    sudo "$TS_BIN" cert --cert-file "$CRT" --key-file "$KEY" "$FQDN" \
      && sudo chown "$USER" "$CRT" "$KEY" \
      || fail "could not mint a TLS certificate. Enable HTTPS Certificates in the Tailscale admin console (DNS tab > HTTPS Certificates), then re-run."
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
      say "${C_G}${C_B}kunai is running.${C_RST}"
      say ""
      say "  Open on any device in your tailnet:  ${C_B}$URL${C_RST}"
      say ""
      say "  iPhone: open it in Safari, then Share > Add to Home Screen."
      say "  ${C_DIM}Update later: re-run this installer (it swaps the binary and restarts the service).${C_RST}"
      if [ "$OS" = "darwin" ]; then
        say "  ${C_DIM}Manage: launchctl list | grep kunai; logs: $DATA_DIR/kunai.log${C_RST}"
        say "  ${C_DIM}Stop:   launchctl unload ~/Library/LaunchAgents/com.kunai.agent.plist${C_RST}"
      else
        say "  ${C_DIM}Manage: systemctl --user status|restart kunai; logs: journalctl --user -u kunai -f${C_RST}"
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
