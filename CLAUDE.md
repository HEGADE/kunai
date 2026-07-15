# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Kunai: a single Go binary that wraps the `claude` CLI and serves an embedded
Svelte PWA directly over Tailscale (no relay). One `claude` process per session,
driven over stdio; phone and laptop clients attach over WebSocket.

**Multi-machine:** every machine runs the same binary. The machine you install the
PWA from is the **hub** (serves the app, owns Web Push, the machine registry, and
peer discovery); the others are **peers**. The client fetches the machine list from
the hub, then talks **directly** to each machine's tailnet origin for REST and WS.
No proxy hop, so the relay-free promise holds across the fleet. See "Multi-machine"
below.

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
Without `-tls-cert/-tls-key` it serves plain HTTP (fine for dev; PWA install and
push need HTTPS).

Deploy the hub (`your-hub`, systemd user service, Tailscale SSH). `make deploy`
cross-builds linux/amd64 with the version stamp, scps, and restarts:

```sh
make deploy HOST=user@your-hub
```

Install or upgrade a machine from a source checkout (one command; systemd on Linux,
launchd on macOS):

```sh
./install.sh                                          # standalone or hub
KUNAI_HUB_URL=https://<hub>.<tailnet>.ts.net:8443 ./install.sh   # a peer
```

`install.sh` **always builds fresh in a source checkout**. It must never reuse a
stale `dist/` or `./kunai` artifact (that was a real bug). `internal/webui/dist`
(including the fingerprinted `assets/*.js|css`) is committed and embedded, so
`.gitignore` only ignores the repo-root `/dist/` release dir, never
`internal/webui/dist`.

Hub URL: `https://<hub>.<tailnet>.ts.net:8443`. Logs:
`journalctl --user -u kunai -f` (Linux) or `~/.kunai/kunai.log` (macOS). TLS certs
are minted with `tailscale cert` and are NOT auto-renewed yet (roughly 90-day
expiry).

## Architecture

Data flow, end to end:

```
PWA (web/) <--wss /ws/app/:id--> internal/server <--> internal/session <--stdio stream-json--> claude CLI
```

- `internal/claude`: the driver. Spawns
  `claude -p --input-format stream-json --output-format stream-json
  --include-partial-messages --verbose --permission-prompt-tool stdio` and speaks
  the control protocol (initialize handshake, `can_use_tool`, interrupt, set_model,
  set_permission_mode) over stdin/stdout NDJSON. **All protocol types live in
  `protocol.go`** so a CLI change is a one-file fix. Tool results (which the CLI
  feeds back as `user` frames) are decoded in `toolresult.go` and surfaced as
  `EventToolResult`, correlated to their tool call by `tool_use_id`. The protocol
  is undocumented; the reference is the `.d.ts` files in the
  `@anthropic-ai/claude-agent-sdk` npm package. The hidden `--sdk-url` websocket
  flag is NOT usable: current CLIs reject non-Anthropic hosts, so do not attempt it.
- `internal/session`: app-facing layer. Each `Session` stamps every event with a
  monotonic `Seq`, keeps a ring buffer (`ringCapacity`, 8000), and fans out to any
  number of subscribers. Client reconnects send `?since=<seq>` and get the gap
  replayed. This is how mobile backgrounding works; the claude process is never tied
  to a client socket. The `hello` frame is the whole attachable state: cwd, model,
  effort, permission mode, `high_seq`, context tokens, pending permission asks,
  queued prompts, and the session's projects. Anything a late or reconnecting client
  needs belongs there, not only in the replayed events.
- `internal/session/loop.go`: the self-prompting run (see the invariants below).
  `Session.StartLoop` re-feeds one task each time a turn ends, until a limit it
  cannot argue with stops it.
- `internal/project`: reads a directory into the description a session hands a model
  (`Scan` -> `Info`, `Info.Brief()`): layout, language mix, git head from `.git`,
  the files that name it. It never opens the code, and the walk skips `.git`,
  `node_modules` and friends and is capped, because it runs while someone waits.
- `internal/server`: REST, WS, and the embedded PWA. `history.go` scans
  `~/.claude/projects/*/<sessionId>.jsonl` transcripts for the Recent list and
  parses them into seed turns on resume (that is why resumed sessions show their old
  conversation and tool outputs: `--resume` alone loads model context but never
  re-emits messages).
- `web/`: Svelte 5 (runes: `$state`/`$derived` in `.svelte.ts` stores), Vite plus
  vite-plugin-pwa with `injectManifest` and a hand-written `src/sw.ts`.
- `internal/server/stats.go` is cross-platform (disk via `syscall.Statfs`,
  versions); memory, uptime, and load are platform-split into `stats_linux.go`
  (`/proc`) and `stats_darwin.go` (`sysctl` plus `vm_stat`, called by **absolute
  path** because launchd's minimal PATH lacks `/usr/sbin`).

## Rich chat rendering

The web client renders the conversation richly from data already on the client
(tool inputs) plus tool results streamed from the driver:

- `web/src/components/Markdown.svelte` highlights fenced code with `highlight.js`
  (a curated language set; a deliberately desaturated near-monochrome theme in
  `web/src/hljs-theme.css`) and adds a copy button. The in-flight streaming block
  renders unhighlighted for speed (`live` prop); committed blocks highlight once via
  a pure `$derived`.
- `web/src/components/tools/ToolBody.svelte` dispatches per tool: `Edit` and
  `MultiEdit` render a red/green line diff (`web/src/lib/diff.ts`), `Write` shows
  highlighted content, `Bash` shows the command, `Read`/`Grep`/`Glob` show fields,
  `TodoWrite` a checklist, with a JSON fallback for unknown tools.
  `ResultView.svelte` renders the tool's output beneath the request.
- `web/src/lib/{highlight,diff,toolMeta}.ts` hold the shared, pure helpers.
  `highlight.js` is the only new runtime dependency.

The log is windowed, and that is load-bearing rather than an optimisation. The
whole backlog arrives at once on open, so `Chat.svelte` waits for `chat.ready` (the
client's `lastSeq` reaching the hello's `high_seq`) and then mounts only a trailing
window of turns, pinned to the bottom, in one paint. Scrolling up reveals more and
re-anchors by distance from the bottom; the window only grows, so what you are
reading never shifts. Mounting turns as they stream is what made opening a long
session crawl from the top.

Tool outputs flow end to end: `internal/claude/toolresult.go`
(`ParseToolResultBlocks`) is shared by the live driver (`route()` handles the
`user` frame) and transcript seeding (`internal/server/history.go`), so resumed
sessions show outputs too. The wire event tag is `tool_result` with `tool_use_id`,
`content`, `is_error`, and `truncated`; output is capped at 24 KB. The client keys
results by `tool_use_id` in `chat.toolResults` and each `ToolCard` looks up its own.

## Multi-machine

The **hub** is whichever machine served the PWA (`window.location.origin`). It owns
the registry, Web Push, and discovery. **Peers** are identical binaries the client
reaches directly. Server pieces (all additive):

- `internal/server/cors.go`: wildcard `Access-Control-Allow-Origin` on `/api/*`
  plus `OPTIONS` preflight, so the hub's PWA can call peer origins cross-origin.
  Cross-origin **WS already works** (`ws.go` sets `OriginPatterns:["*"]`).
- `internal/server/machines.go`: self identity from `-public-url` (`id` is the first
  FQDN label) plus a `machines.json` registry. `GET /api/machines` returns self plus
  manual plus discovered, minus ignored; `POST` and `DELETE /api/machines`.
- `internal/server/discover.go`: `GET /api/machines/discover` shells
  `tailscale status --json`, probes each online peer's `/api/stats` on the Kunai
  port, and keeps the ones that answer as Kunai (cached, folded into `/api/machines`
  so peers "appear on their own"). Finds the CLI on PATH or the macOS app bundle.
- `internal/server/pushfwd.go`: a peer started with `-hub-url` forwards a generic
  wake-up to the hub's `POST /api/push/relay` (the hub holds the phone's
  subscription). With no `-hub-url`, the machine pushes locally (unchanged).

Client (`web/src/lib/`): `api.ts` functions and `ChatConnection` take a `base`
origin (`''` means the hub); **`push.ts` stays hub-relative** (push is hub-only).
The app store seeds "self" from `location`, loads the registry from the hub, and
`refresh()` **fans out** over all machines with `Promise.allSettled`, tagging each
`Meta`/`HistoryEntry` with its `machineId` (wire types stay pure;
`TaggedMeta`/`TaggedHistoryEntry` intersect the tag at fetch time). Routing is
`/m/<machineSlug>/<sessionId>` (legacy bare `/<id>` resolves to self). The sidebar
has a machine **dropdown** filter; the dashboard has a per-machine stats picker that
also scopes "Start on <machine>".

Contracts that must stay in sync manually:

- `internal/session/protocol.go` (AppEvent/Command) mirrors `web/src/lib/types.ts`.
  `AppEvent` is one flat struct shared by every event tag, so a new field means
  editing both files and saying which tag it belongs to: `tool_result`, the token
  split on `result`, `context_tokens`, `attachments`, `queued`/`unqueued`,
  `project`, `compact`, and `loop` all live there.
- Session state strings (`starting|idle|running|awaiting_permission`) appear in
  both, plus status maps in `Chat.svelte`/`Sidebar.svelte`.
- `MachineInfo` (`machines.go`) mirrors `web/src/lib/types.ts`, and `/api/stats`
  `Stats` fields mirror the `Stats` interface there.

Behavioral invariants that were bugs before (do not regress):

- Approving `can_use_tool` MUST echo the original tool input as `updatedInput`; an
  allow without it makes the CLI execute the tool with empty input.
- Session create and resume are async: `Manager.Create` returns immediately
  (`starting` state), the CLI boots in a background goroutine, and prompts queue in
  the driver's out channel. The driver writes `initialize` directly to stdin before
  starting its write loop so a queued prompt can never overtake the handshake.
- The claude process lifetime must never be bound to an HTTP request context.
- Push payloads carry a generic wake-up string only, never session content. This is
  the relay-free promise of the project.
- The CORS wildcard is safe **only** because the tailnet is the entire auth
  perimeter and the API uses no cookies or credentials. Do not add cookie or session
  auth without tightening CORS first.
- Only the hub sends Web Push (one VAPID subscription per origin); peers forward.
- Session ids are unique only per machine, so client-side `{#each}` keys must be
  composite (`machineId:id`) and the client always routes REST/WS to a session's
  owning machine (it never assumes the current origin).
- A `result` frame's `usage` is **cumulative over every model call in the turn**, and
  its `total_cost_usd` is a **running session total**. So context comes from the
  newest assistant message's per-call usage (never the result), and the per-turn cost
  is the difference against the last total (`turnResult`). Reading either verbatim
  produced a meter past 100% and a footer claiming the whole session's spend on every
  turn.
- A prompt sent while a turn runs is **queued in the session**, not the client: the
  phone may be gone. `Prompt` claims the turn under the same lock that tested for it,
  or a second prompt races into the CLI mid-stream. Stop clears the queue.
- The scheduler **reserves an occurrence and saves it before firing**. Marking a job
  fired afterwards meant a restart mid-fire re-ran it, which duplicated a session.
  At-most-once is the deliberate choice: a missed run beats two agents.
- Only fingerprinted `assets/*` may be cached immutably. `sw.js`, its registration
  shim, the manifest and the shell must revalidate: an immutably cached service
  worker strands clients on an old build no matter how often they reload.
- A loop (`internal/session/loop.go`) is a self-prompting run: the same task fed
  back every time a turn ends, which is Ralph's technique (ghuntley.com/ralph).
  It lives in the session for the same reason the queue does, because the point is
  that nobody is attached. The hard part is stopping, so every exit is a limit the
  loop cannot argue with: iterations, spend, the completion promise, a spent usage
  window, a failed turn, or Stop. Both user limits are hard and whichever comes
  first wins; `max_iters` is the one that still works when the CLI reports no cost,
  so it is never optional. Spend is measured as a delta against the session total
  at start, or a loop begun in a long conversation would inherit its whole bill and
  stop instantly. `Interrupt` must end the loop or Stop just looks broken. A loop
  takes `acceptEdits` for its duration and hands the mode back afterwards: auto
  still stops to ask about a risky action, which for an unattended run is a hang,
  not caution (proven the hard way: a real loop sat at `awaiting_permission` on its
  first file write and did nothing). This is the same trade the scheduler makes in
  `fireJob`. A loop must also not fire the per-turn "done" notification on every
  iteration; it announces its own ending instead.
- A compaction (`/compact`, or automatic near the limit) is context, not
  conversation. The CLI feeds the summary back as a plain-string `user` frame and
  writes it to the transcript flagged `isCompactSummary`; both must be dropped.
  Seeding it replayed tens of thousands of characters as a user message and buried
  the conversation on every resumed session. Only the boundary is shown
  (`CompactDivider.svelte`). Its `post_tokens` is the *only* report of the new
  context size, because a compaction emits no assistant message: drop the frame
  and the context meter sits on the pre-compaction number until the next turn
  happens to correct it. The wire spells the metadata snake_case
  (`compact_metadata`/`post_tokens`); the transcript file on disk spells the same
  data camelCase (`compactMetadata`/`postTokens`), so each side decodes its own.
- Sessions spawn in `session.DefaultPermissionMode` (auto), applied as the CLI flag
  at spawn so it holds from the first tool call. Sending it afterwards is too late.
  Scheduled jobs deliberately keep `acceptEdits`: auto can still stop for a risky
  action, which for an unattended run means stalling forever.

## UI conventions

Dark near-monochrome theme; tokens in `web/src/app.css`. No gradients, glows, or
emojis in the UI. White is the only accent (primary buttons); amber and green are
reserved for status dots and the permission gate. Fonts: Geist (UI), Geist Mono
(paths and code), Source Serif 4 (Claude's rendered markdown only). Paths use the
rtl-ellipsis trick and need `unicode-bidi: plaintext` to keep the leading slash from
jumping to the end.

- The chat header and composer float on the canvas with no full-width divider or
  band; the field's own edge defines it.
- Sessions in the sidebar are single-line rows: a chat-bubble icon and the title,
  with a right-edge fade instead of a hard ellipsis (no path, time, or machine
  chip). Active sessions get a small presence dot on the icon.
- Open sessions live in a tab strip above the chat (`Tabs.svelte`), terminal-style.
  Each tab keeps its own `ChatConnection` alive, not just the active one, so
  switching is instant and every tab's dot reports that session's real state: a
  tab is an agent that keeps working while you look at another one, so the strip
  doubles as a status board (amber pulses when a session needs you). Closing a tab
  only detaches the view; ending a session is a separate, explicit action.
- The tab owns a session's name and status, so the chat header carries what the
  tab cannot: the cwd, as a muted mono path (rtl-ellipsis). Do not repeat the
  title or the status dot there.
- The tab strip is the top of the session view, so **it** owns `--safe-top`; the
  header must not inset again. Whatever is topmost carries the safe area.
- Mono is the data voice, and it is what makes the chrome legible at a glance: the
  context meter (`Context.svelte`), the token split, the project card, and the
  composer's paths all read as data, not prose. Prose explains; mono states.
- A turn's tokens are shown split (new vs cached) with an info button, never as one
  total: a long turn re-reads its context on every tool call, so the total runs to
  millions and reads as nonsense next to the price.
- Anything that is context rather than conversation gets a card, not a bubble: a
  project joining the session (`ProjectCard.svelte`) and the files a message carried
  (`FileChips.svelte`) are metadata, and neither ships bytes back to the client.
- Queued prompts sit above the composer, numbered, because the order is what they
  run in. While a turn runs, Send stays next to Stop and queues.
- A loop shows one meter, not two. It ends at whichever limit arrives first, so
  the only honest reading of how close it is to over is the nearer of the two, and
  the line under it names which one and roughly when (`web/src/lib/loop.ts`). A
  budget you only learn about afterwards is not a safeguard, so the limits are the
  middle of the start form under the sentence that says what they do, not settings
  at the bottom. Iterations are hairline seams like a compaction boundary, never a
  card each: at fifty of them they would drown the work they exist to mark.
- Code syntax highlighting is deliberately desaturated (a neutral brightness ramp);
  diffs use the muted green and red at low opacity.

## Commit conventions

No `Co-Authored-By` trailers, no emojis, and no em dashes in commit messages or
docs (owner requirement; the project is intended to be open source, and history was
rewritten once to remove co-author and emoji trailers).
