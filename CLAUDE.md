# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Kunai: a single Go binary that wraps the `claude` CLI and serves an embedded
Svelte PWA directly over Tailscale (no relay). One `claude` process per
session, driven over stdio; phone/laptop clients attach over WebSocket.

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

Deploy to the production box (`your-hub`, systemd user service, Tailscale SSH):

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o /tmp/kunai-linux-amd64 ./cmd/kunai
scp /tmp/kunai-linux-amd64 user@your-hub:/home/ninja/kunai.new
ssh user@your-hub 'export XDG_RUNTIME_DIR=/run/user/1000; chmod +x ~/kunai.new && mv ~/kunai.new ~/kunai && systemctl --user restart kunai'
```

Live URL: `https://your-hub.tailnet.ts.net:8443`. Logs:
`journalctl --user -u kunai -f` on your-hub.

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

Contracts that must stay in sync manually:

- `internal/session/protocol.go` (AppEvent/Command) mirrors
  `web/src/lib/types.ts`.
- Session state strings (`starting|idle|running|awaiting_permission`) appear
  in both, plus status maps in `Chat.svelte`/`Sidebar.svelte`.

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
