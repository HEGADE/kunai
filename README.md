# Kunai

A self-hosted, relay-free client for Claude Code. Run it on your own machines and
drive Claude Code from your phone or laptop, straight over your Tailscale network,
with no cloud relay in between.

Kunai is a single Go binary. It wraps the `claude` CLI, serves an installable web
app (a PWA), and talks to your devices directly over the tailnet. Your phone talks
to your machine, and nothing but a content-free push notification ever leaves the
network.

## Why

Anthropic's Remote Control and similar third-party tools route every message
through a relay server before it reaches your machine, which adds a round trip on
top of Claude's own generation time. A Tailscale link between your devices is
direct peer to peer almost all of the time, so a client that talks straight over
that tunnel feels noticeably faster. Kunai's hard rule: the only thing that ever
leaves the tailnet is a generic push wake-up.

## Features

**Rich, readable chat**

- Token-by-token streaming, rendered as markdown with a serif reading face for
  prose and monospace for code.
- Syntax-highlighted code blocks with a copy button, in a restrained
  near-monochrome theme.
- Real red and green diffs for `Edit` and `MultiEdit`, drawn from the change the
  model requested.
- A card per tool (Bash, Read, Write, Edit, Grep, Glob, TodoWrite, and more) that
  shows both the request and the output: command stdout, file contents, results,
  and errors, correlated to their tool call.

**Control and safety**

- A live approve and deny gate for any tool that needs permission, including
  "always allow this session". The gate shows the actual diff or command you are
  approving.
- Permission modes switchable from the composer: Ask, Auto, Accept edits, Plan.

**Sessions that survive the phone**

- Backgrounding the app kills the socket, not the session. On reconnect the client
  replays exactly what it missed using per-session sequence numbers.
- Full history: past sessions are listed and can be resumed with their
  conversation and tool outputs restored, even after a server restart.
- Instant opens: the API returns immediately while the CLI boots in the
  background, and prompts typed in the meantime are queued.
- Many concurrent sessions across any project directory, with a directory browser
  and quick-start chips for recent projects.

**A fleet, not one box**

- Run the same binary on several machines. The machine you install the app from is
  the hub; the others are peers. One client aggregates sessions from all of them
  and talks to each machine directly over the tailnet.
- Auto-discovery: the hub finds tailnet peers that run Kunai and lists them for
  you.
- A home dashboard with live per-machine stats (memory, CPU, disk, load, uptime,
  sessions) that you switch between.

**Everything else**

- File and image attachments; images go to Claude as vision input.
- Web Push notifications (VAPID) when a session needs you or finishes while the app
  is backgrounded. The payload is a generic wake-up; content is fetched fresh over
  the tailnet on reconnect.
- Installable PWA (standalone, no address bar) on iOS and Android, with a
  responsive two-pane layout on desktop.

## Architecture

```
Phone / laptop (Svelte PWA)
    |  wss + REST, direct over Tailscale
    v
kunai (single Go binary, bound to the tailnet IP)
    /ws/app/:id      WebSocket bridge to the client
    session manager  per-session ring buffer, seq replay, permissions
    /api/*           sessions, history, stats, browse, upload, push, machines
    embedded PWA     served from the binary (go:embed)
    |  stdin/stdout stream-json, one process per session
    v
claude CLI (Claude Code)
```

Kunai drives Claude Code over its stream-json control protocol on stdin and
stdout, the same protocol the official Agent SDK uses. That covers the
`can_use_tool` permission handshake, tool results, interrupts, model switching,
and permission modes. All protocol types live in `internal/claude`.

The tailnet is the entire auth perimeter. The server binds to the Tailscale
interface only, and Tailscale ACLs decide who can reach it. There is no separate
login system.

### Multi-machine

The hub is whichever machine served the PWA. It owns the machine registry, Web
Push, and peer discovery. Peers are identical binaries. The client fetches the
machine list from the hub, then connects directly to each machine's tailnet origin
for REST and WebSocket traffic. No request is proxied through the hub, so the
relay-free promise holds across the whole fleet. Cross-origin access is enabled
with a CORS allowance that is safe here because the tailnet is the perimeter and
the API uses no cookies.

Web Push is the one shared piece: only the hub holds the phone's subscription, so a
peer forwards a generic wake-up to the hub, which sends the notification.

## Requirements

- A machine on your Tailscale tailnet. Linux and macOS are both supported.
- [Claude Code](https://claude.com/claude-code) installed and authenticated
  (`claude` on your PATH).
- Tailscale with MagicDNS and HTTPS certificates enabled (admin console: DNS, then
  HTTPS Certificates).
- To build from source: Go 1.22 or newer and Node 20 or newer.

## Quick start

On the machine that will host Claude Code, clone the repo and run the installer:

```sh
git clone https://github.com/HEGADE/kunai && cd kunai
./install.sh
```

The installer builds the binary, detects your tailnet address, mints a TLS
certificate with `tailscale cert`, installs a service (a systemd user unit on
Linux or a launchd agent on macOS), health-checks it, and prints the URL to open.
Re-running it updates in place.

On your phone, open that URL and install the app. On iOS use Safari, then Share,
then Add to Home Screen, and enable notifications from the installed app.

To add another machine to the fleet, run the same command on it and point it at
your hub so its notifications reach you:

```sh
KUNAI_HUB_URL=https://<hub>.<tailnet>.ts.net:8443 ./install.sh
```

Environment overrides: `KUNAI_PORT` (default 8443), `KUNAI_PUSH_EMAIL` (contact for
Web Push), `KUNAI_HUB_URL` (hub origin for a peer).

## Build and run manually

```sh
make build       # web app plus a local binary
make release     # cross-compiles linux and darwin (amd64 and arm64) into dist/
make deploy HOST=user@machine   # push a fresh linux build to a host and restart it

./kunai -addr <tailnet-ip>:8443 \
  -tls-cert <machine>.<tailnet>.ts.net.crt \
  -tls-key <machine>.<tailnet>.ts.net.key \
  -public-url https://<machine>.<tailnet>.ts.net:8443 \
  -data ~/.kunai
```

The web app is embedded into the binary with `go:embed`, so any frontend change
needs a web rebuild before the Go build:

```sh
cd web && npm run build && cd ..
go build -o kunai ./cmd/kunai
```

Manage the installed service with `systemctl --user status|restart kunai` and
`journalctl --user -u kunai -f` on Linux, or `launchctl` and `~/.kunai/kunai.log`
on macOS.

## Flags

| Flag          | Env                | Default          | Description                                        |
| ------------- | ------------------ | ---------------- | -------------------------------------------------- |
| `-addr`       | `KUNAI_ADDR`       | `127.0.0.1:8443` | Bind address (use the tailnet IP in production)    |
| `-tls-cert`   | `KUNAI_TLS_CERT`   |                  | TLS certificate (empty means plain HTTP, dev only) |
| `-tls-key`    | `KUNAI_TLS_KEY`    |                  | TLS key                                            |
| `-data`       | `KUNAI_DATA`       | `~/.kunai`       | VAPID keys, subscriptions, uploads, registry       |
| `-public-url` | `KUNAI_PUBLIC_URL` |                  | This machine's own tailnet origin                  |
| `-hub-url`    | `KUNAI_HUB_URL`    |                  | Hub origin to forward push to (set on peers)       |
| `-model`      | `KUNAI_MODEL`      |                  | Default model for new sessions                     |
| `-push-email` | `KUNAI_PUSH_EMAIL` |                  | VAPID contact for Web Push                         |

## Security notes

- Bind to the tailnet IP, never `0.0.0.0`. Tailscale ACLs are the perimeter.
- Anyone who can reach the server can run Claude Code in any directory the
  server's user can read. Treat access to the port as access to the machine.
- The CORS allowance is safe only because the tailnet is the perimeter and the API
  uses no cookies or sessions. Do not add cookie auth without tightening it.
- Web Push is the single hop outside the tailnet (Apple and Google push services).
  The payload is a generic wake-up string, never content.
- TLS certificates from `tailscale cert` are not auto-renewed yet, so plan to
  re-mint them (roughly every 90 days).

## Repository layout

```
cmd/kunai/          entrypoint: flags, TLS, server wiring
internal/claude/    stream-json driver for the claude CLI, including tool results
internal/session/   session lifecycle, ring buffer, seq replay, permissions
internal/server/    HTTP and WebSocket API, history, stats, uploads, machines, discovery, push relay
internal/push/      Web Push (VAPID) keys, subscriptions, wake-ups
internal/fsbrowse/  directory listing for the project picker
internal/webui/     embedded production build of the web app
web/                Svelte 5 and Vite PWA source
```

## Development

```sh
go test ./...            # backend tests
cd web && npm run check  # svelte-check and tsc
cd web && npm run dev    # frontend dev server
```

The `claude` stream-json protocol is undocumented. The closest reference is the
type definitions shipped with `@anthropic-ai/claude-agent-sdk`. Kunai keeps every
protocol type in `internal/claude` so a CLI change stays a one-file fix.

## Contributing

Issues and pull requests are welcome. A few house rules:

- The frontend build in `internal/webui/dist` is committed and embedded, so
  rebuild the web app before the Go binary when you change the frontend.
- Run `go test ./...` and `cd web && npm run check` before opening a PR.
- No emojis or em dashes in commit messages or docs.

## License

MIT. See [LICENSE](LICENSE).
