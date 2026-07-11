# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Kunai: a single Go binary that wraps the `claude` CLI and serves an embedded
Svelte PWA directly over Tailscale (no relay). One `claude` process per
session, driven over stdio; phone/laptop clients attach over WebSocket.

**Multi-machine:** every machine runs the same binary. The machine you install
the PWA from is the **hub** (serves the app, owns Web Push + the machine
registry + peer discovery); the others are **peers**. The client fetches the
machine list from the hub, then talks **directly** to each machine's tailnet
origin for REST + WS — no proxy hop, so the relay-free promise holds across the
fleet. See "Multi-machine" below.

## Build and test

The frontend build outputs into `internal/webui/dist`, which is committed and
embedded via `go:embed`. **Any frontend change requires rebuilding the web app
before rebuilding the Go binary**, or the binary serves stale assets:

```sh
cd web && npm run build && cd ..          # -> internal/webui/dist
go build -o kunai ./cmd/kunai
```

```sh
go test ./...                                          # unit tests
go test ./internal/session/ -run TestSequencing -v     # single test
KUNAI_E2E=1 go test ./internal/server/ -run TestEndToEnd -v  # opt-in: spawns a real claude
cd web && npm run check                                # svelte-check + tsc
```

Run locally (needs `claude` on PATH): `go run ./cmd/kunai -addr 127.0.0.1:8899 -data /tmp/kunai-data`.
Without `-tls-cert/-tls-key` it serves plain HTTP (fine for dev; PWA install
and push need HTTPS).

Deploy the hub (`your-hub`, systemd user service, Tailscale SSH) — `make deploy`
cross-builds linux/amd64 with the version stamp, scps, and restarts:

```sh
make deploy HOST=user@your-hub
```

Install/upgrade a machine from a source checkout (one command; systemd on Linux,
launchd on macOS):

```sh
./install.sh                                          # standalone or hub
KUNAI_HUB_URL=https://your-hub.tailnet.ts.net:8443 ./install.sh   # a peer
```

`install.sh` **always builds fresh in a source checkout** — it must never reuse a
stale `dist/` or `./kunai` artifact (that was a real bug). `internal/webui/dist`
(including the fingerprinted `assets/*.js|css`) is committed and embedded, so
`.gitignore` only ignores the repo-root `/dist/` release dir — never
`internal/webui/dist`.

Hub URL: `https://your-hub.tailnet.ts.net:8443`. Logs:
`journalctl --user -u kunai -f` (Linux) or `~/.kunai/kunai.log` (macOS).
TLS certs are minted with `tailscale cert` and are NOT auto-renewed yet (~90-day
expiry).

## Architecture

Data flow, end to end:

```
PWA (web/) <--wss /ws/app/:id--> internal/server <--> internal/session <--stdio stream-json--> claude CLI
```

- `internal/claude` — the driver. Spawns
  `claude -p --input-format stream-json --output-format stream-json
  --include-partial-messages --verbose --permission-prompt-tool stdio` and
  speaks the control protocol (initialize handshake, `can_use_tool`,
  interrupt, set_model, set_permission_mode) over stdin/stdout NDJSON.
  **All protocol types live in `protocol.go`** so a CLI change is a one-file
  fix. The protocol is undocumented; the reference is the `.d.ts` files in the
  `@anthropic-ai/claude-agent-sdk` npm package. The hidden `--sdk-url`
  websocket flag is NOT usable — current CLIs reject non-Anthropic hosts; do
  not attempt it.
- `internal/session` — app-facing layer. Each `Session` stamps every event
  with a monotonic `Seq`, keeps a ring buffer (cap 4000), and fans out to any
  number of subscribers. Client reconnects send `?since=<seq>` and get the gap
  replayed — this is how mobile backgrounding works; the claude process is
  never tied to a client socket. The `hello` frame carries state, permission
  mode, and still-pending permission asks.
- `internal/server` — REST + WS + embedded PWA. `history.go` scans
  `~/.claude/projects/*/<sessionId>.jsonl` transcripts for the Recent list and
  parses them into seed turns on resume (that is why resumed sessions show
  their old conversation: `--resume` alone loads model context but never
  re-emits messages).
- `web/` — Svelte 5 (runes: `$state`/`$derived` in `.svelte.ts` stores),
  Vite + vite-plugin-pwa with `injectManifest` and a hand-written `src/sw.ts`.
- `internal/server/stats.go` is cross-platform (disk via `syscall.Statfs`,
  versions); memory/uptime/load are platform-split into `stats_linux.go`
  (`/proc`) and `stats_darwin.go` (`sysctl` + `vm_stat`, called by **absolute
  path** because launchd's minimal PATH lacks `/usr/sbin`).

## Multi-machine

The **hub** is whichever machine served the PWA (`window.location.origin`). It
owns the registry, Web Push, and discovery. **Peers** are identical binaries the
client reaches directly. Server pieces (all additive):

- `internal/server/cors.go` — wildcard `Access-Control-Allow-Origin` on `/api/*`
  + `OPTIONS` preflight, so the hub's PWA can call peer origins cross-origin.
  Cross-origin **WS already works** (`ws.go` sets `OriginPatterns:["*"]`).
- `internal/server/machines.go` — self identity from `-public-url`
  (`id` = first FQDN label) + a `machines.json` registry. `GET /api/machines`
  returns `self ∪ manual ∪ discovered − ignored`; `POST`/`DELETE /api/machines`.
- `internal/server/discover.go` — `GET /api/machines/discover` shells
  `tailscale status --json`, probes each online peer's `/api/stats` on the Kunai
  port, keeps the ones that answer as Kunai (cached, folded into `/api/machines`
  so peers "appear on their own"). Finds the CLI on PATH or the macOS app bundle.
- `internal/server/pushfwd.go` — a peer started with `-hub-url` forwards a
  generic wake-up to the hub's `POST /api/push/relay` (the hub holds the phone's
  subscription). No `-hub-url` ⇒ the machine pushes locally (unchanged).

Client (`web/src/lib/`): `api.ts` functions and `ChatConnection` take a `base`
origin (`''` = hub); **`push.ts` stays hub-relative** (push is hub-only). The
app store seeds "self" from `location`, loads the registry from the hub, and
`refresh()` **fans out** over all machines with `Promise.allSettled`, tagging
each `Meta`/`HistoryEntry` with its `machineId` (wire types stay pure —
`TaggedMeta`/`TaggedHistoryEntry` intersect the tag on at fetch time). Routing is
`/m/<machineSlug>/<sessionId>` (legacy bare `/<id>` resolves to self). The
sidebar has a machine **dropdown** filter; the dashboard a per-machine stats
picker that also scopes "Start on <machine>".

Contracts that must stay in sync manually:

- `internal/session/protocol.go` (AppEvent/Command) mirrors
  `web/src/lib/types.ts`.
- Session state strings (`starting|idle|running|awaiting_permission`) appear
  in both, plus status maps in `Chat.svelte`/`Sidebar.svelte`.
- `MachineInfo` (`machines.go`) mirrors `web/src/lib/types.ts`, and `/api/stats`
  `Stats` fields mirror the `Stats` interface there.

Behavioral invariants that were bugs before — do not regress:

- Approving `can_use_tool` MUST echo the original tool input as
  `updatedInput`; an allow without it makes the CLI execute the tool with
  empty input.
- Session create/resume is async: `Manager.Create` returns immediately
  (`starting` state), the CLI boots in a background goroutine, and prompts
  queue in the driver's out channel. The driver writes `initialize` directly
  to stdin before starting its write loop so a queued prompt can never
  overtake the handshake.
- The claude process lifetime must never be bound to an HTTP request context.
- Push payloads carry a generic wake-up string only, never session content —
  the relay-free promise of the project.
- The CORS wildcard is safe **only** because the tailnet is the entire auth
  perimeter and the API uses no cookies/credentials. Do not add cookie/session
  auth without tightening CORS first.
- Only the hub sends Web Push (one VAPID subscription per origin); peers forward.
- Session ids are unique only per machine, so client-side `{#each}` keys must be
  composite (`machineId:id`) and the client always routes REST/WS to a session's
  owning machine (never assumes the current origin).

## UI conventions

Dark near-monochrome theme; tokens in `web/src/app.css`. No gradients, glows,
or emojis in the UI. White is the only accent (primary buttons); amber/green
are reserved for status dots and the permission gate. Fonts: Geist (UI),
Geist Mono (paths/code), Source Serif 4 (Claude's rendered markdown only).
Paths use the rtl-ellipsis trick and need `unicode-bidi: plaintext` to keep
the leading slash from jumping to the end.

## Commit conventions

No `Co-Authored-By` trailers and no emojis in commit messages (owner
requirement; history was rewritten once to remove them).
