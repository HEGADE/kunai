# Kunai

A self-hosted, relay-free mobile and desktop client for Claude Code.

Kunai is a single Go binary that runs on your own machine, wraps the `claude`
CLI, and serves an installable web app straight over your Tailscale network.
Your phone or laptop talks directly to your machine over the tailnet -- no
cloud relay sits between you and Claude, so every token takes the shortest
possible path.

## Why

Anthropic's Remote Control and third-party tools route every message through a
relay server before it reaches your machine, adding a round-trip on top of
Claude's own generation time. A Tailscale connection between your devices is
direct peer-to-peer almost all of the time, so a client that talks straight
over that tunnel is noticeably snappier. Kunai's hard rule: nothing but a
generic push wake-up ever leaves the tailnet.

## Features

- Token-by-token streaming of Claude's responses, rendered as markdown with a
  serif reading face for prose and monospace for code.
- Live tool-call visibility with an approve/deny gate for anything requiring
  permission, including "always allow this session".
- Permission modes switchable from the composer: Ask, Auto, Accept edits, Plan.
- Sessions survive the phone: backgrounding kills the socket, not the session.
  Reconnects replay exactly what was missed via per-session sequence numbers.
- Session history: past Claude Code sessions are listed and can be resumed
  with their full conversation restored, even after a server restart.
- Instant session opens: the API returns immediately while the CLI boots in
  the background; prompts typed meanwhile are queued and flushed.
- Multiple concurrent sessions across arbitrary project directories, with a
  directory browser and one-tap quick-start chips for recent projects.
- File and image attachments; images go to Claude as vision input.
- Web Push notifications (VAPID) when a session needs approval or finishes
  while the app is backgrounded. The push payload is a generic wake-up only;
  content is pulled fresh over the tailnet on reconnect.
- Home dashboard with live host stats: memory, disk, load, uptime, active
  sessions, and the installed Claude Code version.
- Installable PWA (standalone, no address bar) on iOS and Android; responsive
  two-pane layout on desktop.

## Architecture

```
Phone / laptop (Svelte PWA)
        |  wss (direct over Tailscale)
        v
kunai (single Go binary, bound to the tailnet IP)
  - /ws/app/:id     WebSocket bridge to the client
  - session manager per-session ring buffer, seq replay, permissions
  - /api/*          sessions, history, stats, browse, upload, push
  - embedded PWA    served from the binary (go:embed)
        |  stdin/stdout stream-json (one process per session)
        v
claude CLI (Claude Code)
```

The server drives Claude Code over its stream-json control protocol on
stdin/stdout -- the same protocol the official Agent SDK uses -- including the
`can_use_tool` permission handshake, interrupts, model switching, and
permission modes. All protocol types live in `internal/claude`.

The tailnet is the entire auth perimeter: the server binds to the Tailscale
interface only, and Tailscale ACLs decide who can reach it. There is no
separate login system.

## Repository layout

```
cmd/kunai/          entrypoint: flags, TLS, server wiring
internal/claude/    stream-json driver for the claude CLI
internal/session/   session lifecycle, ring buffer, seq replay, permissions
internal/server/    HTTP + WebSocket API, history, stats, uploads
internal/push/      Web Push (VAPID) keys, subscriptions, wake-ups
internal/fsbrowse/  directory listing for the project picker
internal/webui/     embedded production build of the web app
web/                Svelte + Vite PWA source
```

## Requirements

- A machine on your tailnet (Linux is the primary target; macOS works).
- [Claude Code](https://claude.com/claude-code) installed and authenticated
  (`claude` on PATH).
- Tailscale, with MagicDNS and HTTPS certificates enabled for the tailnet
  (admin console: DNS > HTTPS Certificates).

## Quick start

On the machine that will host Claude Code, either download a release:

```sh
gh release download -R HEGADE/kunai -p install.sh -p "kunai-linux-amd64" && chmod +x install.sh kunai-*
./install.sh
```

or clone and install (builds from source; needs Go 1.22+ and Node 20+):

```sh
gh repo clone HEGADE/kunai && cd kunai
./install.sh
```

The installer finds or builds the binary, detects your tailnet address, mints
a TLS certificate with `tailscale cert`, installs a systemd user service that
survives reboots, health-checks it, and prints the URL to open. Re-running it
updates the binary in place. On iOS, open the URL in Safari and use
Share > Add to Home Screen, then enable notifications from the installed app.

Environment overrides: `KUNAI_PORT` (default 8443), `KUNAI_PUSH_EMAIL`
(contact for Web Push).

## Manual build and run

```sh
make build       # web app + local binary (or: cd web && npm run build, then go build ./cmd/kunai)
make release     # cross-compiles linux/darwin amd64+arm64 into dist/
make deploy HOST=user@machine   # push a linux build to a host running the service

./kunai -addr <tailnet-ip>:8443 \
  -tls-cert <machine>.<tailnet>.ts.net.crt \
  -tls-key <machine>.<tailnet>.ts.net.key \
  -data ~/.kunai
```

Manage the installed service with `systemctl --user status|restart kunai` and
`journalctl --user -u kunai -f`.

## Flags

| Flag          | Env               | Default          | Description                              |
| ------------- | ----------------- | ---------------- | ---------------------------------------- |
| `-addr`       | `KUNAI_ADDR`      | `127.0.0.1:8443` | Bind address (use the tailnet IP)        |
| `-tls-cert`   | `KUNAI_TLS_CERT`  |                  | TLS certificate (empty = HTTP, dev only) |
| `-tls-key`    | `KUNAI_TLS_KEY`   |                  | TLS key                                  |
| `-data`       | `KUNAI_DATA`      | `~/.kunai`       | VAPID keys, subscriptions, uploads       |
| `-model`      | `KUNAI_MODEL`     |                  | Default model for new sessions           |
| `-push-email` | `KUNAI_PUSH_EMAIL`|                  | VAPID contact for Web Push               |

## Security notes

- Bind to the tailnet IP, never `0.0.0.0`. Tailscale ACLs are the perimeter.
- Anyone who can reach the server can run Claude Code in any directory the
  server's user can read. Treat access to the port as access to the machine.
- Web Push is the single hop outside the tailnet (Apple/Google push services).
  The payload is a generic "needs your attention" string, never content.

## Development

```sh
# Backend tests
go test ./...

# Frontend dev server (proxying to a locally running kunai is up to you)
cd web && npm run dev
```

The `claude` stream-json protocol is undocumented; the closest reference is
the type definitions shipped with `@anthropic-ai/claude-agent-sdk`. Kunai's
driver keeps every protocol type in `internal/claude` so CLI changes are a
one-file fix.
