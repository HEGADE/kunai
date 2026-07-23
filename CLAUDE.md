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

**Nightly channel.** A second, bleeding-edge channel built from the `nightly`
branch coexists with a stable install, so you can run new work beside the setup
you rely on. `KUNAI_CHANNEL=nightly ./install.sh` installs a separate
`kunai-nightly` service on port 8444 with its own `~/.kunai-nightly` data dir and
binary, so nothing is shared. A build-time `buildChannel` ldflag (set by
`make ... CHANNEL=nightly`) decides which release the self-updater pulls from:
the moving `nightly` pre-release for nightly, `/releases/latest` for stable, so
the two never cross over. `.github/workflows/nightly.yml` rebuilds every platform
on each push to the branch and refreshes that pre-release; the client version
check is channel-aware (nightly compares a moving build id, stable keeps semver).
The providers feature and its fixes live on `nightly` and are deliberately NOT
merged to `main` until Grok/Kimi are verified and the Codex-quota display exists.

Hub URL: `https://<hub>.<tailnet>.ts.net:8443`. Logs:
`journalctl --user -u kunai -f` (Linux) or `~/.kunai/kunai.log` (macOS). TLS certs
are minted with `tailscale cert` (roughly 90-day expiry); `certKeeper`
(`internal/server/tls.go`) auto-renews them, re-minting via `tailscale cert` once
within 20 days of expiry and hot-reloading the new keypair from disk without a
restart.

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
- `internal/server/guardian.go`: the thermal safety net (see the invariants
  below). A background loop reads `cpuTemp()` and, when the host runs too hot or
  has been held awake too long, calls `Manager.StopForThermal` to end every
  session and drops the keep-awake hold so a closed-lid machine sleeps and cools.
  Temperature is read in the stats platform files (`cpuTemp()`, real on Linux via
  `/sys/class/hwmon`, 0 on macOS until a privileged Phase 2). Policy persists in
  `thermal.json`, mirroring `awake.json`.
- `internal/server/clis.go`: named Claude CLIs, so one machine can drive more than
  one Claude account. A `CLIProfile` is a name plus the binary to run plus optional
  env (a `CLAUDE_CONFIG_DIR` pointing at another account's auth). The list loads
  from `clis.json` (a starter file is written on first boot), the default is a
  single `Claude`/`claude`, and the first profile is always the default. The chosen
  profile flows `handleCreateSession` -> `CreateOptions{CLIName,Bin,Env}` ->
  `claude.Options{Bin,Env}`, where the driver execs that binary with the env
  appended. `/api/stats` sends the profile names (only when there is a real choice)
  for the New Session picker; `Meta.CLI` records which account a session runs on.
  A resumed loop carries the account: `LoopPersist` saves `CLIName/Bin/Env` and
  `resumeOneLoop` passes them back through `CreateOptions`, so an overnight loop on
  a work account stays on it across a restart instead of reverting to the default.
  Recent is per-account: an account's config dir (`CLIProfile.Dir` or its
  `CLAUDE_CONFIG_DIR`, folded into the driver env by `effectiveEnv`) is where its
  transcripts live, so `scanHistory` walks each account's `<configDir>/projects`
  and tags every `HistoryEntry.CLI`; the client sends that `cli` back on reopen and
  `handleCreateSession` seeds from that account's dir. `transcriptPath` and the
  loaders take the config dir; `RestartWithEffort` preserves the account across the
  respawn so an effort change never drops a work session to the default. A session
  shows and can switch its account live: hello carries `CLI`, the composer has an
  account pill (shown when the machine has >1 account), and
  `POST /api/sessions/{id}/account` copies the transcript into the target account's
  projects folder and calls `RestartWithAccount` (the shared `restart` core with an
  account override) to resume under it. Claude ties a conversation's memory to the
  account's config dir, so the copy is what lets the other account continue with
  full context; its first turn re-reads everything uncached (the accepted cost).
- `internal/server/accountlogin.go`: adding an account **from the app**, no
  terminal. `claude auth login --claudeai` is a full-screen TUI (nothing prints on
  a plain pipe; the OAuth URL only appears under a real terminal), and its
  subscription flow is a paste-code exchange (`redirect_uri=platform.claude.com/oauth/code/callback`,
  then "Paste code here"), NOT a localhost callback: so the driver runs it under a
  PTY (`creack/pty`) in a fresh config dir (`<dataDir>/accounts/<slug>`), scrapes
  the one URL out (`oauthURL`, matched only once terminated so a mid-read buffer
  can't truncate it), streams the one pasted code in, and verifies with
  `auth status --json` before saving the profile to `clis.json`. `login/start`
  returns the URL, `login/finish` the code; abandoned flows are swept on a TTL.
  When a login **hangs** (the CLI never exits after the code, the `loginDoneTimeout`
  case), the failure carries what the CLI was doing instead of a generic timeout:
  `ptyTail` keeps a bounded, redacted capture of the CLI's terminal output (the
  pasted code and anything token-shaped are stripped) and folds it into the error
  and the log. A **silent** tail is itself the diagnosis and says so: a login that
  hangs having printed nothing is blocked on an out-of-band prompt, on macOS a
  Keychain unlock a headless launchd service cannot answer. Discarding this output
  (the old `drain`) was the real gap in diagnosing a stuck login.
  Newer `claude` CLIs (2.1.217+) changed `--claudeai` from paste-code to a
  **localhost loopback** flow (`redirect_uri=http://localhost:<port>/callback`),
  which broke this login: a code redirected to a local port can't be carried to
  another machine. But kunai runs ON that machine, so it **bridges** the callback
  itself. `loopbackTarget` detects a localhost `redirect_uri` in the scraped URL;
  `finish` then does an HTTP GET to that local port (`forwardLoopback`, both
  loopback families tried) instead of typing the code into the PTY, handing the
  code to the CLI's own callback server. `codeFromPaste` accepts a bare code, a
  `code=&state=` fragment, or the whole failed callback URL, and reuses the state
  the authorize URL carried. This preserves the promise: the account owner
  authenticates in **their own browser** (credentials never leave it), only the
  code crosses to the machine running the CLI, and the localhost hop is local to
  that machine, so the two people can be on different networks. Confirmed against
  a real 2.1.217 login end to end (a shared account added on another person's Mac
  whose CLI produced the loopback flow), on top of the unit tests for each piece.
  Why one CLI emits loopback and another paste-code for the same version and
  command is still unexplained: there is no login flag to force paste-code
  (`--claudeai`/`--console`/`--email`/`--sso` are the only ones), so the flow is
  the CLI's own environment-dependent choice, and kunai handles both rather than
  trying to steer it. A loopback login
  can also finish with **no paste at all**: if the browser is on this machine it
  hits the CLI's localhost callback directly and the CLI exits. So a single
  `watch` goroutine per flow owns the PTY, waits for the CLI to exit, and
  `finalize`s the outcome once, registering the account via a callback whether
  the exit came from a pasted code or the browser completing it. `finish` waits
  on that; a `login/status` poll reads it, so the client closes the dialog
  hands-free in the local-browser case instead of waiting on a paste that never
  comes.
  The client surface is `Accounts.svelte` (a dedicated view off the sidebar, NOT in
  Settings): lists accounts with signed-in status and a two-step add flow (name ->
  open link + paste code). Nothing but the URL out and the code in ever crosses
  kunai: the user authenticates directly with Anthropic in their browser and the
  CLI writes its own login into the account's dir. The E2E test that spawns a real
  login is gated on `KUNAI_E2E`.
- `internal/server/usage.go`: the account's subscription quota, the same two
  numbers `claude`'s `/usage` prints, on the dashboard. A `rate_limit_info` frame
  only carries a window's reset time and whether a turn was rejected, so the "how
  full is it" half has to come from the account. There is no daily window; the
  limits are 5-hour and 7-day. We get them by **shelling the CLI**
  (`claude -p --session-id <uuid> /usage`, free: no model call, no tokens) rather
  than by calling the account's HTTP endpoint, and the reason is credentials: the
  CLI already knows how to read its own login, which on macOS lives in the
  Keychain rather than a file. Shelling means kunai never touches that login, so
  it can never rotate a token out from under a running session or drop a field
  and log the account out. The costs are real and accepted: ~2s per poll (hence
  the 60s cache) and prose to parse instead of JSON. Two costs are load-bearing
  and must not regress. **Every `-p` run records a transcript**, so the poll
  passes its *own* uuid and deletes exactly that file (`dropTranscript`); without
  it a 60s cadence buries the Recent list in ~1400 `/usage` sessions a day, and a
  fixed uuid cannot be reused (the CLI rejects it as "already in use"). And the
  CLI prints **no year** on a reset (`Jul 17, 10:29pm (Asia/Kolkata)`), so the
  parse infers the year that puts the reset ahead of now, which is what makes a
  window spanning New Year come out right. `usageRun` is injectable for the same
  reason `guardian.go` has `execRun`: a test asserts the command instead of
  spawning a real claude.
- `internal/server/providers.go`, `cliproxy.go`, `cliproxy_login.go`:
  **proxy-backed providers**, so one machine can run non-Claude models (Codex,
  Grok, Kimi) without leaving the `claude` agent. The whole idea rests on one
  fact: kunai keeps driving `claude`; only the model endpoint it calls out to
  changes. `claude` honours `ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN` and the
  per-slot `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL`, so pointing it at a
  local CLIProxyAPI (github.com/router-for-me/CLIProxyAPI) that fronts those
  subscriptions keeps every tool, permission, edit, and bash call intact and
  swaps only the brain. A `Provider` (name + base_url + token + slot->model map)
  compiles to a `CLIProfile` whose `Env` carries exactly those vars, so it flows
  through the *entire* existing session/switch/loop machinery unchanged; the only
  special-casing is `isProxyProfile` (true when the env has a base URL), which
  skips the OAuth sign-in preflight and the `/usage` poll (neither means anything
  for a proxied account). A provider left with a blank base_url points at the
  **managed sidecar** kunai runs itself (`cliproxyManager`): it downloads a
  pinned CLIProxyAPI release, verifies it against a hardcoded sha256 (all four
  platforms pinned; a mismatch is refused), on macOS ad-hoc codesigns it (Apple
  Silicon kills an unsigned binary on exec, so the same signing is applied to
  `install.sh`'s prebuilt download and to `update.go`'s self-update), writes a
  localhost-only config on a free ephemeral port, and supervises the process for
  the server's lifetime (restart on crash, stop on shutdown). Because the port is
  assigned asynchronously, `ensureCLIProxyReady` blocks a provider session create
  (and account-switch-to-a-provider) until the sidecar has a real address, or the
  baked `ANTHROPIC_BASE_URL` would be empty and the session would hang. Providers
  default to `session.ProviderPermissionMode` (accept-edits), and `restart()`
  re-applies it whenever the account is proxy-backed, because auto mode judges a
  Bash command's safety with a *second* model call that a proxied model can
  rate-limit and stall on. `cliproxy_login.go` authorizes a provider from the
  app: it runs the sidecar's own `-codex-login`/`-xai-login`/`-kimi-login` under
  `--no-browser`, scrapes the OAuth URL from stdout, and bridges the localhost
  callback with the same `loopbackTarget`/`codeFromPaste`/`forwardLoopback`
  helpers the Claude account login uses; the sidecar's file watcher loads the new
  credential with no restart. The composer shows a provider session's real model
  (from `/api/stats` `provider_models`) and lets you switch it
  (`/api/sessions/{id}/provider-model` updates the mapping and respawns), since
  the Claude-tier picker is meaningless there. `codexusage.go` puts a Codex
  provider's ChatGPT quota on the dashboard, the same two numbers Claude shows:
  the proxy exposes no rate-limit info and there is no `codex /usage` to shell, so
  kunai reads the account's OAuth token (the managed sidecar's own, kept fresh by
  the sidecar, else `~/.codex/auth.json`) and calls ChatGPT's `wham/usage` backend
  endpoint, the one CodexBar reads. This is the single place kunai reads a login it
  otherwise only shells, and it is read-only, only to show a number. The windows
  are placed by length, not a fixed 5h/7d, because a plan varies (a ChatGPT Go plan
  has one ~30-day window); a short one is the session row, a long one the weekly
  row, so the reset time is always honest. Confirmed end to end on Codex (login,
  session, model switch, account switch, and a real 17% quota reading) by an
  automated Playwright pass, which also caught the `send on closed channel`
  respawn crash fixed in `driver.go`. **Tested for Codex only; Grok and Kimi ride
  the same path but are unverified.**
- `internal/cliproxy/codex`, `internal/cliproxy/grok`: the **native provider
  proxies**, kunai's own in-process replacement for the CLIProxyAPI sidecar, so a
  Codex or Grok provider needs no 40MB download at all. The whole idea rests on one
  measured fact: the 40MB sidecar IS the Anthropic<->provider translator matrix, so
  embedding its SDK does not shrink anything (kunai 9.3->37.7MB), but porting only
  the ~1500-LOC claude<->responses translator does (+0.41MB). `codex` ports that
  translator verbatim from CLIProxyAPI (MIT; proven against its own golden tests),
  wraps it in an executor (OAuth load+refresh in `auth.go`, the upstream call and
  SSE stream-translate in `proxy.go`), and a native OAuth login in `login.go` (PKCE
  S256 against auth.openai.com, the localhost:1455 callback), so Codex is fully
  sidecar-free including sign-in. `grok` **reuses the codex translator unchanged**,
  because xAI's `/responses` is the same OpenAI-Responses format; it only adds the
  xAI endpoint (`cli-chat-proxy.grok.com`), the grok CLI token (`~/.grok/auth.json`,
  refreshed via its OIDC issuer), and the `xai-grok-cli` headers. `internal/server/
  nativecodex.go`/`nativegrok.go` serve each on a localhost port and `providerProfile`
  bakes it as the provider's `ANTHROPIC_BASE_URL`; `anyProviderNeedsSidecar` skips the
  download on boot and create when every provider is native or external. Both are
  opt-in and off by default (`-native-codex`/`KUNAI_NATIVE_CODEX=1`,
  `-native-grok`/`KUNAI_NATIVE_GROK=1`). Live-proven against real Codex and real Grok:
  single-turn, multi-turn tool use, reasoning-signature replay (Codex accepts the
  replayed signature, so the reference's replay cache is unnecessary here because the
  claude CLI replays reasoning itself), the real `claude` CLI end to end, and a full
  kunai WebSocket + UI session, all with the sidecar never downloaded. Kimi K3 is the
  remaining provider (Moonshot's Anthropic-native `api.kimi.com/coding/v1/messages`,
  the easiest of the three), not built yet.
- `internal/project`: reads a directory into the description a session hands a model
  (`Scan` -> `Info`, `Info.Brief()`): layout, language mix, git head from `.git`,
  the files that name it. It never opens the code, and the walk skips `.git`,
  `node_modules` and friends and is capped, because it runs while someone waits.
- `internal/server`: REST, WS, and the embedded PWA. `history.go` scans
  `~/.claude/projects/*/<sessionId>.jsonl` transcripts for the Recent list and
  parses them into seed turns on resume (that is why resumed sessions show their old
  conversation and tool outputs: `--resume` alone loads model context but never
  re-emits messages). Resume seeding is **tail-capped** (`seedTailBytes`,
  `transcriptTail`): only the last few MB of the transcript are read, aligned to a
  line start, so resume time stays constant as a session grows. Parsing a 69MB
  transcript in full took ~1.8s of synchronous handler time (two scans) and was
  the whole "resume is slow" delay; the client only mounts the trailing window
  anyway, so the tail is all a reopen shows. Scrollback past the tail is **paged
  in from disk on reverse scroll**, not lost: hello carries `hist_before` (the
  byte offset older history begins before, from `loadTranscriptSeed`), and
  `GET /api/sessions/{id}/history?before=<n>` (`handleOlderTurns`) returns the
  previous `histChunkBytes` slice parsed into the same app events a live seed
  emits (`session.SeedEvent`, shared so paged and seeded turns render identically),
  plus the next older cursor (0 = start reached). `ChatConnection.loadOlder`
  prepends them and `Chat.svelte`'s `maybeReveal` triggers it at the top of the
  window; byte-offset pages tile `[0, hist_before)` with no gap or overlap against
  the seed (`TestReverseScrollPagesEveryOlderTurn`). The one remaining trade: the
  overhead measurement only sees compactions inside the tail (an older one
  re-measures live at the next compaction).
- The changed-files review is **client-side and per-query**, not a server endpoint:
  `web/src/components/TurnChanges.svelte` renders what each query changed straight
  from that turn's Edit/Write/MultiEdit tool inputs (`fileEditsOf` in `toolMeta.ts`).
  See the "Rich chat rendering" section. An earlier git-shelling model
  (`internal/server/review.go`, a `/changes` + `/diff` endpoint pair diffing the
  working tree against a base commit) was removed: it read as one session-wide blob
  and went "Clean" the moment the work was committed, when what was wanted was
  always "what did *this query* change". The locally-built `/kunai` binary is still
  gitignored so it never shows as a phantom untracked change.
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
- `ToolCard.svelte` is the wrapper: a tool call is a **light activity line**, not
  a bordered box: the row only highlights on hover / while open, and expanding
  threads the detail beneath it with a hairline rule. A `Bash` call reads as a
  terminal prompt (`❯` + command), with the agent's `cd <dir> &&` boilerplate
  dropped from the collapsed line (the full command stays in the body).
- `web/src/lib/{highlight,diff,toolMeta}.ts` hold the shared, pure helpers.
  `highlight.js` is the only new runtime dependency.

`TurnChanges.svelte` renders a **per-query** changed-files card, right under the
reply that made the changes: the files that query's Edit/Write/MultiEdit calls
touched, each expandable to its diff. It is fed entirely from the turn's own tool
inputs (`fileEditsOf` in `toolMeta.ts`, the sibling of `fileChangesOf`) and the
same `DiffView`/`CodeView` the tool cards use, so it is **client-side only** (no
git, no server round-trip), scoped to one query, and stays correct after the work
is committed, because the diffs live in the conversation, not the working tree.
`Chat.svelte` renders one after every turn (the card self-hides when the turn
edited no files), so each query owns its own review. This deliberately **replaced**
an earlier git-shelling model (a single session-wide panel fed by
`internal/server/review.go`) that kept confusing: it showed the whole working tree
against a base commit, so it read as one big blob and went "Clean" the moment the
work was committed. The wanted behaviour was always "what did *this query* change",
which the tool inputs already answer. The `review.go`/`review_diff.go` endpoints
and their `Changes.svelte` client are gone; per-turn edits are the source of truth.

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
  The cache is **sticky with a last-seen window** (`peerTTL`), and that is
  load-bearing: a scan returns nil both when `tailscale status` itself fails
  (timeout, missing CLI) and when a peer's probe blips for one round, and the old
  cache *overwrote its whole result set with that nil*, so a single transient
  hiccup dropped every live peer from `/api/machines` until the next good scan.
  The client mirrors the hub's list verbatim, so the machine flickered out of the
  sidebar and only came back on a hard refresh. Now `scanPeers`/`tailscalePeers`
  return an `ok` bool that is false ONLY when tailscale could not be queried at
  all (distinct from a real empty tailnet), `merge` upserts each found peer's
  last-seen and prunes only peers unseen for the whole `peerTTL`, and a failed
  scan (`ok=false`) leaves the known peers untouched and does not advance the
  freshness clock. So a live machine survives a blipped round, and the fleet is
  warmed at startup (`go s.discover(true)`) so the first client load already sees
  it. `merge` is a pure method on `discoveryCache` so the stickiness is
  unit-testable (`discover_test.go`) without shelling tailscale.
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
  `project`, `compact`, `loop`, and `mode` all live there.
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
- The scheduler **reserves an occurrence and saves it before firing**. Marking a
  job fired afterwards meant a restart mid-fire re-ran it, which duplicated a
  session. At-most-once is the deliberate choice: a missed run beats two agents.
  The job list itself is always persisted (`schedule.json`), so a restart before
  the fire time never loses a job; only a restart landing in the seconds-wide
  `mgr.Create` window at the exact fire moment drops that one occurrence, which is
  the accepted cost of at-most-once. `runOne` logs every outcome and records
  `LastStatus` (`fired` / `skipped (overdue)` / `error: …`), surfaced in the
  schedule row, so "did my job run?" is answerable from the UI or the logs (a fire
  that failed silently used to leave no trace at all).
- A **reset** trigger **pins** the observed reset onto the job (`Job.ArmedReset`)
  and fires at that reset plus the offset, never recomputing from the live
  `resets` map. A `rate_limit_info`'s `resetsAt` is always the *current* (future)
  window's end, so recomputing every tick left the fire time perpetually ahead of
  now and the job never became due on an always-on machine. The pin is persisted
  on the job, so it also survives a restart (the `resets` map is in-memory only);
  firing clears the pin so a rearm job latches onto the next observed reset.
  `allowed_warning` (the CLI approaching the wall, e.g. 91%) is **not** a limit:
  only `rejected` marks the window spent, so a warning never raises the banner or
  stops a loop, though its `resetsAt` is still recorded for pinning.
- Only fingerprinted `assets/*` may be cached immutably. `sw.js`, its registration
  shim, the manifest and the shell must revalidate: an immutably cached service
  worker strands clients on an old build no matter how often they reload.
- A long-open PWA updates itself (`web/src/lib/updater.ts`): the browser only
  re-checks the service worker on a navigation, so `startUpdatePolling` calls
  `registration.update()` on an interval and on refocus, and the existing
  `controllerchange` reload swaps in the new build. The reload is **held while the
  composer has an unsent prompt or a staged attachment** (the only thing a reload
  would lose) and applies the moment it clears, so an auto-refresh never eats a
  draft. `Chat.svelte` registers that guard via `setReloadGuard`.
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
  `fireJob`. An ask that still gets through parks the loop rather than killing it,
  because your answer is worth more than the iterations it would throw away and
  nothing is spent while it waits, but the bar has to say so: a loop you believe is
  running while it sits on a click you never saw is worse than one that stopped.
  A loop must also not fire the per-turn "done" notification on every iteration;
  it announces its own ending instead.
- A running loop survives a restart (auto-update, crash, OOM, the service manager
  bouncing us), because the whole point is that nobody is attached to notice it
  died. `internal/server/looppersist.go` writes `loops/<sessionId>.json` while a
  loop runs and deletes it the moment the loop ends; on boot `resumeLoops`
  recreates each surviving session with `--resume` and calls `Session.ResumeLoop`
  to continue from the saved iteration and spend. The safety rests on one rule:
  the file exists ONLY while running, so a loop the thermal guard stopped (or that
  finished, or that you stopped) has no file and is never restarted, and the
  delete on a terminal state runs before the guardian's poweroff so it wins that
  race. A resumed CLI process starts its cost count at zero (verified against a
  real CLI), so `ResumeLoop` sets `startCost` to the negative of the prior spend
  to make the running total continue correctly; the iteration cap carries over as
  a plain integer, so it binds exactly even if the money math ever drifted.
  `maxLoopResumes` bounds a crash loop: a loop that keeps dying without ever
  ending cleanly is given up on rather than restarted forever.
- Every iteration a loop sends is wrapped in `<loop-iteration n=".." of="..">`
  (`session.LoopPrompt`, read back by `session.ParseLoopIteration`). The CLI writes
  every turn we send into the transcript, and resuming reads that file back, so
  without the wrapper reopening a fifty-iteration loop replayed fifty copies of the
  same instructions as user messages: the compaction summary's bug wearing a
  different hat. `history.go` turns those frames into `loop` seed turns, and they
  seed as `LoopSeam`, never `LoopRunning`: the loop died with the process that
  ran it, so a resumed session must show the seams without lighting up a live meter
  for a loop that is over.
- A permission mode change must be broadcast, not just sent to the CLI. It does
  not always come from a click: a loop borrows `acceptEdits` and hands it back, so
  a mode set server-side has to reach attached clients or the composer keeps
  showing the mode you last picked while the session runs in another one.
- The thermal guardian (`internal/server/guardian.go`) is a whole-machine safety
  net for unattended work, not a loop feature: a loop or a session a phone walked
  away from can pin the CPU for hours, and with the lid shut that cooks a laptop.
  When the host runs too hot (sustained, with hysteresis so a one-off spike never
  nukes a session) or has been held awake past a wall-clock cap, it stops every
  session via `Manager.StopForThermal` and releases the keep-awake hold. It does
  NOT power the machine off in this phase: the heat is the running turns, so
  stopping them is the fix, and on a closed lid dropping the hold lets the machine
  sleep, which drops the CPU to idle. Sleep is the cooldown. The two arming
  conditions are the same "whichever comes first" shape as a loop's caps, and the
  wall-clock cap is the macOS-safe fallback because macOS CPU temperature cannot be
  read without root or CGO (so `cpuTemp()` returns 0 there and only the time cap
  can fire). The guard depends on a `stopper` interface, not the concrete
  `*session.Manager`, so its safety logic is unit-testable without spawning claude.
- The guard's privileged escalations (Phase 2, default off) are the hard-ceiling
  poweroff, the lid-closed hold, and reading Mac temperature. Each needs a grant
  the plain service lacks, added by `install.sh` only under
  `KUNAI_THERMAL_PRIVILEGED=1`: a macOS sudoers NOPASSWD line for
  `pmset`/`powermetrics`/`shutdown`, or a Linux polkit rule for
  `org.freedesktop.login1.power-off` and
  `org.freedesktop.login1.inhibit-handle-lid-switch`. Every privileged action goes
  through the injectable `execRun` var so a test asserts the exact command without
  running it. The poweroff is the LAST resort: it fires only when the host is still
  over the hard ceiling after the soft trip already stopped everything of ours (so
  the heat is not our load), and a denied poweroff is logged and survived, never
  fatal. The lid hold is privileged on BOTH platforms, not just macOS: a Linux
  block inhibitor on `handle-lid-switch` is denied to an unprivileged user
  ("Failed to inhibit: Access denied"), so `lidhold_linux.go` watches for the
  child dying at once and reports the refusal instead of recording a phantom hold.
  macOS `pmset disablesleep` is sticky global state, so `lidhold_darwin.go` clears
  it at boot (undoing a crash that left it on) and the server clears it on graceful
  shutdown. Apple Silicon has no unprivileged die temperature: the `smc`
  powermetrics sampler does not even exist there (confirmed on a real Mac16,12,
  "unrecognized sampler: smc"), so the Mac guard runs on thermal PRESSURE instead
  (`sudo powermetrics --samplers thermal`, levels nominal/fair/serious/critical).
  `cpuTemp()` is 0 on macOS; `thermalPressure()` carries the level, and the guard
  trips on Serious (soft) or Critical (hard/poweroff). The `Stats` split is
  deliberate: `cpu_temp_c` for degree hosts (Linux), `thermal_pressure` for Apple
  Silicon, and the UI shows whichever the host reports. The parse lives in the
  platform-neutral `thermal_parse.go` so it is testable on Linux against captured
  output even though the reader is not. The privileged reader/hold/poweroff cannot
  run from a Linux dev box; only the pressure parse and the guard logic are proven
  there, so the Mac path must still be exercised on real hardware.
- A compaction (`/compact`, or automatic near the limit) is context, not
  conversation. The CLI feeds the summary back as a plain-string `user` frame and
  writes it to the transcript flagged `isCompactSummary`; both must be dropped.
  Seeding it replayed tens of thousands of characters as a user message and buried
  the conversation on every resumed session. Only the boundary is shown
  (`CompactDivider.svelte`). The boundary is also the *only* report of the new
  context size, because a compaction emits no assistant message: drop the frame
  and the context meter sits on the pre-compaction number until the next turn
  happens to correct it. The wire spells the metadata snake_case
  (`compact_metadata`/`post_tokens`); the transcript file on disk spells the same
  data camelCase (`compactMetadata`/`postTokens`), so each side decodes its own.
- But `post_tokens` counts only the compacted *conversation*, not the fixed
  overhead that stays resident in the window (system prompt, tool schemas, memory,
  skills, tens of thousands of tokens). Setting the meter to the bare `post_tokens`
  reads far too LOW right after a `/compact` (13k when Claude's own `/context`
  shows ~50k). The overhead is NOT recoverable from the frame: `pre_tokens` is the
  full pre-compaction context, the *same basis* as the assistant usage the meter
  comes from, so `pre_tokens - post_tokens` over-subtracts and collapses the meter
  right back to `post_tokens` (this was a real, twice-shipped bug). The only honest
  source is measurement: the gap between a compaction's `post_tokens` and the first
  assistant usage after it is the overhead (plus that turn's new prompt), so the
  smallest such gap is the estimate. The meter is then `post_tokens + overhead`.
  The overhead is measured live (`Session.overhead`, refined on the first usage
  after each compaction via `pendingPost`) and seeded from the transcript on resume
  (`loadTranscriptContextTokens` returns it too, carried through `CreateOptions.Overhead`
  and preserved across `RestartWithEffort`), so a resumed session is right the
  moment it next compacts instead of only after a full turn. The compaction event
  carries both: `context_tokens` is `post_tokens + overhead` (drives the meter) and
  `post_tokens` is the raw conversation-only size (the divider shows it, matching
  the CLI's own `/compact` banner).
- Sessions spawn in `session.DefaultPermissionMode` (auto), applied as the CLI flag
  at spawn so it holds from the first tool call. Sending it afterwards is too late.
  Scheduled jobs deliberately keep `acceptEdits`: auto can still stop for a risky
  action, which for an unattended run means stalling forever.

## Channels

A **channel** is a way to reach kunai that is not the PWA. Telegram is the first;
the UI and the server both assume there will be more (Slack is already listed as
a placeholder), so the shape matters more than the one implementation.

- `internal/telegram`: the bot. It **long-polls outbound**, so kunai still exposes
  nothing to the internet and needs no inbound hole, which is the point: the phone
  does not need Tailscale running to drive a session. `client.go` is the API
  (`ok:false` is an error, text clamped to 4096 runes), `commands.go` the command
  and callback vocabulary, `store.go` the persisted token/allow-list/bindings,
  `bot.go` the poll loop and one event pump per chat.
- **Pairing, not a numeric allow list.** A stranger who messages the bot gets a
  short code (`pairCode`, ambiguous glyphs excluded) which the owner approves in
  Channels. Codes expire in an hour. An empty allow list means nobody: a chat with
  this bot is a shell on the machine, so the safe direction is closed.
- **`render.go` owns what may leave the machine.** Telegram is a third party and
  everything sent through it lands in a log nobody here controls, so the default
  (`StrictPolicy`) sends a tool's name and shape, never file contents or command
  output. The risk being guarded is not really your source, it is the incidental
  spill: a config file the agent read, a token a test echoed. `Detail` turns it on
  deliberately and is off by default.
- **The channel never creates a session itself.** `internal/server/channelsessions.go`
  is the adapter and the only place a chat-born session is made, so it goes through
  `armSession` (notifications, rate-limit handling), the configured model/effort,
  the right Claude account, and the same transcript seeding a reopen in the app
  uses. The `telegram.Sessions` interface is deliberately narrow (Start, Resume,
  Recent, Get, List, Close) rather than passing `session.CreateOptions` through:
  a chat does not choose a model, and the next channel implements one thing
  instead of rediscovering how a session is born. Before this, a session started
  from Telegram silently skipped `armSession` and could not be resumed at all.
- **Closing a session is not losing it, and the chat must say so.** The transcript
  is on disk, so every exit (`/end`, or the session being closed in the app, which
  is the common case) answers with `resumeOffer`: a `/resume <id>` line that
  survives scrollback and a one-tap button carrying the id. The chat's binding is
  deliberately **kept** when its session dies, because it is the only record of
  which conversation that chat was having; `current()` reports "not live", never
  "not known". Telling someone to `/new` there would throw the conversation away.
  Callback data is capped at 64 bytes by Telegram, so an id that will not fit
  drops the button and keeps the command (`resumeKeyboard`).
- **The reply is a rich message, so Markdown renders.** The model writes
  Markdown and plain text is why a heading arrived as literal `**` and a fence
  as three backticks. Rich messages (Bot API 10.1) take **GitHub Flavored
  Markdown directly** (`InputRichMessage.markdown`, exactly one of markdown or
  html), which is the dialect already in hand, so there is no converter to keep
  honest against half-streamed text. The rejected alternatives: MarkdownV2 fails
  the whole message on one unescaped character, of which model output is full,
  and HTML would mean writing that converter. Rich applies **only to the model's
  reply**; everything the bot says itself stays plain, because those lines carry
  paths and tool names that a Markdown parser would mangle (`foo_bar_baz`).
  Rich also raises the cap from 4096 to 32768 runes (`clampRich`).
- **A draft must be retired, not just outlived** (`clearDraft`). A draft occupies
  the chat until something replaces it, so posting the finished reply on top of a
  live one leaves a block of empty space under the last message that **stays**.
  Leaving the chat and coming back hides it, because that rebuilds the view from
  the message list and a draft is not in it: that asymmetry is the tell, and it
  is what distinguished this from a rendering glitch. Empty text is the only
  retirement the Bot API offers, since MTProto's `clear_draft` flag is not
  exposed on `sendMessage`/`sendRichMessage`. Only sent when this reply actually
  drafted, or the empty push would plant a draft instead of clearing one.
- **A reply streams as a draft, and falls back to edits.** `sendMessageDraft`
  (Bot API 9.3, opened to all bots in 9.5) is the endpoint Telegram built for
  this and animates text the way its own assistant does; `editMessageText` works
  everywhere but is rate-limited hard enough that rewriting faster than about
  once a second gets the bot throttled mid-answer (hence `draftEvery` 400ms vs
  `editEvery` 1500ms). `stream.go` drafts by default and decides by **trying**:
  a draft is a private-chat method, so rather than sniff the chat type, the first
  refusal turns drafting off for that chat and the reply carries on as edits.
  That flag is per chat, not per turn, so a group costs one failed call ever, and
  `Reset` deliberately does not re-arm it. Two consequences of the API shape are
  load-bearing: a draft is an **ephemeral ~30s preview**, so `Flush` must still
  post the finished reply as a real message (a short reply whose flush text
  matches the draft is the case that would otherwise vanish), and equal
  `draft_id`s **animate into each other**, so it is one non-zero id per reply,
  incremented on `Reset`. The accepted cost: prose written before a long tool
  call scrolls off when its preview expires and returns when the turn ends.
  Rich and drafting are **two independent capability flags**, degraded one rung
  at a time (rich draft -> plain draft -> edits), each remembered per chat.
  A refused *draft* only loses a preview so it degrades and returns, but a
  refused *final send* would lose the whole reply, so `post` retries plain
  within the same call. `Reset` re-arms neither.
- **A downgrade needs a refusal, not a hiccup** (`unsupported`, `giveUp`). A
  capability is off for the life of the chat, so only a flat 4xx from Telegram
  may cost one: a transport timeout and a 429 both say nothing about what the
  chat supports. Degrading on any error is what made streaming "weird and slow"
  on a flaky route, since one timeout dropped rich and the next dropped
  drafting, leaving the chat on 1500ms edits for good. Every downgrade is
  logged, because otherwise the only symptom is a reply that quietly got worse.
- **`retry_after` is obeyed, not just noticed** (`backOff`, `coolUntil`). A 429
  carries a wait, and Telegram's edge caches the penalty window, so retrying
  early **resets** it and the wait gets longer: ignoring it turns one throttled
  push into a throttled turn. This was a real bug in other bots (agno #7360)
  before it was one here. Streaming pushes and the keep-alive both hold until
  the window lapses. The **finished reply is the exception**: it is the one
  thing that must not be dropped, so `post` waits the throttle out (bounded by
  `maxFinalWait`) and sends anyway. Note the 30 req/s ceiling is per bot token
  and shared across every method, drafts and `sendChatAction` included, so the
  budget is per machine, not per chat.
- **The draft is kept alive while a turn runs** (`stream.Refresh`, driven by the
  typing heartbeat at `draftRefresh`). A draft dies after ~30s and a model can
  think for longer than that without emitting a token, so without this a long
  answer showed nothing at all until it landed. With no text yet it sends an
  **empty** draft, which is Telegram's native "Thinking..." placeholder, so the
  wait before the first word reads as a wait rather than as silence. It stops
  once the real message is posted, and a placeholder never counts as `shown`.
- **A broken route is survived, and the token never reaches the log.**
  `transport.go` exists because of a real fifteen-minute outage: IPv6 to
  api.telegram.org completed 3 TCP handshakes in 10, while IPv4 to the same host
  and IPv6 to other hosts were both 10 for 10 (the v6 route left the country and
  came back at 270ms; ICMP crossed it happily, so `ping6` said all was well).
  What made an intermittent fault permanent was **connection reuse**: Go races
  the families, keeps the winner, and pins every later request to it, so winning
  once on v6 meant every poll after rode the bad path and burned the full 65s
  timeout. The fix is therefore NOT at the dial. On a transport failure (never
  on an `ok:false` refusal, which is a real round trip) the client drops its
  pooled connections and pins new ones to IPv4 for `familyPin`; a failed v4 dial
  releases the pin at once, so an IPv6-only network still works. The bot has its
  own `http.Transport` so closing idle connections cannot reach the rest of
  kunai's HTTP. Separately, the token is **in the request URL**, so a raw
  transport error puts full control of the bot into journalctl: `redact` strips
  it while keeping the wrapped error, so `errors.Is` still sees the deadline.
- **The typing indicator is a heartbeat, not a call.** Telegram's chat action
  expires after five seconds and is cleared the moment the bot sends anything,
  and a turn here runs for minutes while posting tool lines. `typing.go` re-asserts
  it every 4s, driven by the **session's state** rather than by the prompt path,
  so a turn started in the app shows in the chat too and the bubble drops the
  instant the session stops to ask permission (where it would be a lie).

## UI conventions

Dark near-monochrome theme; tokens in `web/src/app.css`. No gradients, glows, or
emojis in the UI. White is the only accent (primary buttons); amber and green are
reserved for status dots and the permission gate. Fonts: Geist (UI), Geist Mono
(paths and code), Source Serif 4 (Claude's rendered markdown only). Paths use the
rtl-ellipsis trick and need `unicode-bidi: plaintext` to keep the leading slash from
jumping to the end.

- The composer floats on the canvas with no full-width divider or band; the
  field's own edge defines it. The chat header is the exception: it is short and
  ghost-buttoned (no chrome at rest, a panel fill on hover) and sits on a hairline
  that **fades at both ends** (a masked gradient, not a hard rule), so the compact
  top chrome reads as a seam over the canvas. A hairline `.asep` sets the terminal
  Close action apart from the safe ones.
- Sessions are grouped in the sidebar by the codebase they belong to
  (`web/src/lib/grouping.ts`, pure and testable). Two kinds of heading, and the
  difference is who chose the name: a **project** group is derived from the
  directory the session started in, so every session has one for free; a
  **workspace** group is named by hand, which is what you reach for once a
  session holds more than one codebase and the directory it happened to start in
  stops describing it. A named heading wins over the derived one, sessions
  sharing a name group together, and clearing the name drops them back under
  their folder. Pinned stays flat (a pin is a priority list; grouping it would
  bury the thing you pinned), and a single group renders no heading at all, so a
  one-project machine looks exactly as it did before.
- The workspace name lives in `sessionMetaStore` beside the rename and the pin,
  keyed by session id, because the grouping has to **outlive the process**: a
  session named while running must still be in that workspace tomorrow when it is
  a transcript in Recent. That is also its one limit: a closed session's project
  list died with it, so `Meta.Projects` (the count that marks a session as worth
  naming) is live-only, and an *unnamed* multi-project session falls back to its
  directory once closed. Naming it is what makes the grouping permanent.
- Sessions in the sidebar are single-line rows: a chat-bubble icon and the title,
  with a right-edge fade instead of a hard ellipsis (no path, time, or machine
  chip). Active sessions get a small presence dot on the icon. (A text status
  badge per row was tried and reverted: the wire `state` is unreliable for a
  resumed session (it reads `starting` until the first prompt and never carries
  a turn's numbers on reopen), so any label built on it kept lying. Left out
  until the server exposes a state a badge can trust.)
- Open sessions live in a tab strip (`Tabs.svelte`), terminal-style, rendered as
  the **left of the header's top row** so the session actions ride the same line
  to its right (Chat.svelte's `.toprow`); the path sits on a quieter second row
  (`.pathrow`). Tabs is nested in the header rather than a sibling above it, which
  is why Chat imports and renders it, not App. Each tab keeps its own
  `ChatConnection` alive, not just the active one, so switching is instant and
  every tab's dot reports that session's real state: a tab is an agent that keeps
  working while you look at another one, so the strip doubles as a status board
  (amber pulses when a session needs you). Closing a tab only detaches the view;
  ending a session is a separate, explicit action.
- The header is one row and holds only what you **act on**: back/home, the tabs,
  and the action buttons. A session's *reference* context (cwd, git branch, the
  account it runs on, the codebases it spans) is not an action, so it lives
  behind the info button in `SessionInfo.svelte` (a small popover, folder
  copyable) rather than taking chrome. This retired three scattered bits at once:
  the cwd row, the `+N projects` pill, and repeating the account. The tab still
  names the session and shows its status; a fresh session's empty state still
  shows the cwd on open, so nothing is lost by moving it off the bar.
- The header's top row is the topmost chrome, so **it** owns `--safe-top` (a
  phone's status bar); the tab strip inside it no longer insets, and nothing
  below re-insets. Whatever is topmost carries the safe area.
- Mono is the data voice, and it is what makes the chrome legible at a glance: the
  context meter (`Context.svelte`), the token split, the project card, and the
  composer's paths all read as data, not prose. Prose explains; mono states.
- A turn's footer carries the turn's stats (duration, token split, cost) and a
  Copy button. The numbers come only from the live `result` stream and are never
  written to the transcript, so a turn seeded on reopen shows Copy but no stats:
  that is a known limitation, not a bug to keep chasing.
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
