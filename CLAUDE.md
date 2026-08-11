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
Without `-tls-cert/-tls-key` it serves plain HTTP, and on a **loopback** address
that is not a limitation: `localhost` and `127.0.0.1` are secure contexts by
specification, so the PWA, its service worker and Web Push all work with no
certificate. Only a non-loopback address needs TLS before a browser will install
the app. (This used to say "dev only; PWA install and push need HTTPS", which told
people local mode was broken while it worked in front of them.)

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

`install.sh` picks its own mode and **never blocks on Tailscale**. With a tailnet
and MagicDNS it mints a cert and binds the tailnet IP, as before; with anything
missing (no CLI, not connected, no MagicDNS, or a cert that will not mint) it
falls back to local mode, binds `127.0.0.1`, and says so. Claude Code is the only
hard prerequisite left. The finish screen always prints **both** ways in, "On this
machine" and "From your phone", so the second one is either a link or the steps to
get one, rather than silently absent.

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
The native-provider work (Codex and Grok in-process proxies, native Codex login,
Codex/Grok quota) shipped to `main` in **v1.0.0**, was **reverted** as too buggy
(`main` went Claude-only for v1.0.2), then soaked and hardened on `nightly` and
**shipped back to `main` in v1.1.0** -- this time on the path a provider session
actually takes, because the native proxies are now **on by default**
(`-native-codex`/`-native-grok` default true, falling back to the CLIProxyAPI
sidecar only if the login is missing). The v1.0.0 bug was that native was off by
default, so the default path used the unfixed sidecar; every hardening below is on
by default now. Kimi is not built (no subscription). Providers are default-on but
inert for a Claude-only user: the sidecar download is skipped when no provider is
configured (`anyProviderNeedsSidecar`), so a stable install that never adds a
provider is unaffected by the re-ship.

The re-ship hardens the seam the CLI cannot see, because pointing `claude` at a
non-Claude backend means the CLI packs context to Claude's window while the upstream
model's is smaller (`internal/cliproxy/codex/resilience.go`, shared by the Grok
proxy):
- **Context-window sliding.** Before a request is sent, `FitContextToWindow`
  estimates its tokens (`EstimateTokens`, a deliberately conservative bytes/4.0 -- it
  must over-count, since the failure mode is letting an over-window request through)
  against the real upstream window (`ModelWindow`: ~260k Codex, ~240k Grok,
  env-overridable via `KUNAI_CODEX_WINDOW`/`KUNAI_GROK_WINDOW`). If the request
  overflows, it **drops the oldest whole turns until it fits** -- a sliding window --
  keeping the system prompt and tools and never orphaning a tool_result from its
  tool_use, so the session keeps working on its recent context instead of the
  upstream dropping the request mid-stream (the "stream disconnected" report). This
  is what the CLI's own compaction would do if it knew the real, smaller window; it
  does not, because it thinks it is Claude, so the proxy slides the window itself.
  Only when even the single latest turn plus the fixed overhead cannot fit does it
  return Anthropic's own `prompt is too long` 400 (`GuardContextWindow` /
  `PromptTooLongMessage`), the one case nothing can save. Playwright against a real
  Codex session proved a 9-turn conversation that grew past a lowered window kept
  answering every turn while the log showed `trimmed N oldest message(s)`, and that a
  single file read cannot overflow (the CLI caps a tool result) -- only conversation
  accumulated over many turns does, which is why the bug needed a long session to
  show. In practice the trim rarely fires: the CLI is NOT in 1M-context mode for a
  provider session (its request carries `context-management-2025-06-27` but no
  `context-1m` beta, confirmed live), so for a normal-window model like gpt-5.5
  (~272k) the CLI's own compaction fires around ~180k -- with a real summary -- well
  before the proxy would trim; the trim is the safety net for a smaller-window model
  or an edge spike. The one honest residue: when the trim does fire, the model forgets
  the oldest turns (a drop, not a summary). The context meter now reads the real
  provider window (`web/src/lib/context.ts` knows gpt/codex ~272k, grok ~256k) so a
  near-full provider session no longer pins falsely at 100%.
- **Guaranteed stream terminal.** `StreamTranslate` tracks whether it emitted
  `message_stop`; a socket drop, an early EOF, or an inline `response.failed` becomes
  a typed Anthropic `error` event (overflow -> `invalid_request_error` so the CLI
  compacts; a plain drop -> retryable `api_error`) rather than a truncated stream the
  CLI reports as disconnected. A client-cancelled request (the CLI got what it
  needed) is distinguished by `ctx.Err()` and never fabricates an error.
- **Overflow error mapping.** `ClassifyUpstreamError` reshapes an upstream
  context-length rejection into the same `prompt is too long`, and keeps the earlier
  permanent-vs-transient split (quota exhausted -> non-retryable 400).
- **Dual token format.** `codex/auth.go` now reads both the flat sidecar-login shape
  and the codex CLI's nested `tokens{...}` `~/.codex/auth.json`, deriving expiry from
  the access-token JWT. Pointing a Codex provider at a real Codex login used to fail
  with "no access or refresh token".
- **Grok rotating tokens + dead-login clarity.** xAI rotates refresh tokens (each
  refresh revokes the old one), so `grok/auth.go` now writes the rotated token back
  to `~/.grok/auth.json` (`persistLocked`, read-modify-write preserving other
  fields); without this, kunai refreshed once, kept the new token only in memory, and
  every restart re-read the now-revoked token and 401'd. A login that genuinely
  cannot refresh returns an actionable "run `grok` to sign in again" error, and an
  auth failure is now a non-retryable 400 (not a 401): the CLI retries a 401, so a
  dead login used to hang the turn on "Working..." for minutes before failing --
  which, on the sidecar path, was the "cooling down credentials" 3-5 minute hang.
  Both proxies return auth failures as invalid_request_error so they surface at once.

Validated live end to end: real `claude` CLI -> native proxy -> real Codex, single
and multi-turn with Read/Write tools, through the full server over WebSocket. Grok's
free tier was quota-exhausted at test time, so only its error path was exercised
live; its happy path rides the same shared translator/stream code as Codex.

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
- `internal/usagestats` (+ `internal/server/usagepage.go`, the Usage view): what
  the work **cost**, which is the other half of the question `usage.go` answers.
  The quota meters say whether you can keep going; this says what you have been
  spending it on. It needed nothing to have been recorded in advance, because
  every assistant message in every transcript already carries its own `usage`
  block and model, so the whole page is computed **retroactively** over history
  that predates the feature. The corpus is the constraint (~1.5GB over ~145 files
  across seven accounts here), so a scan is **incremental by byte offset**: a
  transcript is append-only, the index in `<dataDir>/usage-index.json` remembers
  how far into each file the last pass read, and what it keeps per file is
  (day, model) buckets rather than anything proportional to size. It scans one
  file per **session**, not per file on disk, and that is a correctness rule
  rather than an optimisation: an account switch copies the whole transcript into
  the target account's folder, so a conversation that has moved around exists
  under every account it ever ran on, and counting files counts it once per copy.
  One session here sat in SEVEN folders and made up ~1.1GB of a 1.5GB corpus, so
  the page reported $44k where the truth was $11k. `scanHistory` already had this
  problem and already solved it the same way (newest mtime wins: the copies are
  successive prefixes of one another, so the newest is both the account it last
  ran on and the most complete record). Measured on the
  real corpus: 3.2s cold, **1.2ms warm**, a 20KB index. A file that SHRANK was
  replaced rather than appended to (the account-switch copy, and the `/usage`
  poll deleting its own transcript every minute) so its offset is meaningless and
  it is rescanned from zero; a file that has gone drops out entirely. The first
  scan never happens inside a request: it is warmed on boot beside
  `go s.discover(true)`, the endpoint answers immediately with the last report,
  and the client polls while `scanning` is true. Two honesty rules are
  load-bearing and must not regress. The headline is **not a bill** -- everything
  runs on subscriptions, so it is the counterfactual API cost, and the caption
  saying so is part of the number rather than a footnote. And a model with no
  published rate is reported **unpriced**, never folded in at a neighbour's rate
  and never shown as free: its tokens still count, the page states what share of
  the corpus it could not price, and `percent` clamps BOTH ends (`>99.9%` as well
  as `<0.1%`) because 99.93% printed as "100%" beside its own complement as
  "<0.1%" claims exactly the completeness being audited. The agent bar is
  **token** share, not cost share, for the same reason: an unpriced agent
  contributes zero cost, so a cost bar would show Claude at 100% on a machine
  that also ran millions of Codex tokens. Cache tiers are priced apart (5m write
  1.25x, 1h write 2x), so the transcript's own summed
  `cache_creation_input_tokens` is deliberately not what is used, and the cache
  READ discount is per-rate rather than a constant: Anthropic and OpenAI both bill
  a cached token at 0.1x input, xAI at 0.15x, and one hardcoded multiplier
  under-priced every Grok read. Every rate is a published list price read from the
  provider's own page (Anthropic, OpenAI, xAI), not recalled, and the non-Claude
  rows are the sub-200k/272k tier a coding session almost always sits in, so a very
  long context is under-priced rather than over-. A built-in table is still wrong
  twice over -- it goes stale on the next price change and cannot know a model
  released after the binary -- so `<dataDir>/pricing.json` overrides it
  (`{"gpt-6": {"in": 7, "out": 21}}`, prefix-matched the same way, an override
  beating a built-in of the same key). A missing file is normal and a malformed
  one is ignored rather than fatal: a typo in an optional override must not take
  the page down. Anything still unlisted stays unpriced rather than guessed, so
  the honesty rule is unchanged and only the source of truth is extensible.
  The view is a **route** (`/usage`), not a dialog, and that is a fix rather than
  a preference: a modal is for a decision you are making on top of what you were
  doing, and it takes the screen hostage to say so. Usage is a place you read,
  compare and come back to, so being a route buys the back button, a reload that
  lands where you were, a link you can send, and the full width the charts want
  instead of a 720px sheet with the app greyed out behind it. `syncUrl` puts
  Usage AHEAD of the active session (else opening it from inside a conversation
  leaves the address bar on the session and back does nothing), `applyPath`
  checks `/usage` BEFORE `currentPath` (which would otherwise read `usage` as a
  bare legacy session id), and `open()` clears it. It renders in the main pane,
  so `App.svelte`'s phone rule is now `data-full` ("something covers the
  sidebar") rather than `data-has-chat`; `themeColorFor` took the same rename for
  the same reason, since a session was simply the only thing that used to qualify.
  The page's charts are the second sanctioned break in the near-monochrome rule,
  and it is the same break as the first: colour is spent only where it stands for
  somebody else's product, because there it IS the information. Here that is
  load-bearing rather than decorative, since a stacked column split three ways is
  unreadable in three steps of the same gray. `web/src/lib/agentColors.ts` holds
  one hue per agent family in a **fixed order** (colour follows the agent, never
  its rank, so a quiet week cannot reshuffle the stack and repaint what a reader
  already learned), and the values are validated rather than chosen: every
  adjacent AND all-pairs gate passes for the three agents that occur -- CVD dE 9.4
  worst pair, normal-vision 20.9, all >= 3:1 on `--bg`. Two near misses are the
  reason to keep validating: Anthropic's own `--claude` clay (`#d97757`) FAILS
  against Codex's green at dE 4.6 under protanopia, so the chart uses a deeper
  step of that hue, and the obvious blue/violet pairing collapses to dE 1.9.
  Colour is never the only channel regardless -- every segment carries a 2px gap
  in the surface colour, every legend row names itself, and the breakdown table
  repeats every number as text. Two honesty rules joined the existing ones. A
  period-over-period delta is **withheld unless the records reach back through the
  whole prior window** (`comparable`): kunai started recording on some day, and a
  window straddling it is compared against a period that is empty because nothing
  was WRITTEN then, which reported a 155-fold rise in spend on a machine whose
  habits had not changed at all. And a rise past ten-fold prints as a multiplier,
  because "+15418%" is not a number anyone converts; it reads as a bug.
  The page reads the **whole fleet**, not one machine, and that is the default
  once there is one: three machines on a single Claude account are one bill being
  spent three ways, and reading them one at a time is the reader doing the
  addition kunai should have done. It fans out over `app.machines` with
  `Promise.allSettled` (the same shape as `AppStore.refresh`) and `mergeReports`
  folds the `(day, model)` buckets. Two rules make that honest. A machine that
  did not answer keeps its row and is **named on screen**: a fleet total missing
  a machine is a floor, not a total, and silently smaller is the only way these
  numbers can mislead while every one of them is correct. And a **Machine**
  breakdown sits beside Model and Day, because summing is only correct while each
  machine scans its own transcripts: sync `~/.claude` between them (Syncthing,
  Dropbox, a shared home) and the same session is counted once per machine.
  Nothing in a report identifies a session, so that cannot be detected in the
  client -- but two machines claiming a suspiciously identical figure is the
  tell, which is exactly what the breakdown shows. It is the same failure that
  once made the page read $44k instead of $11k, one level up. Note the fan-out
  needs the CORS wildcard, so it works on a tailnet install and NOT in local
  mode, where CORS is deliberately off -- which costs nothing, since local mode
  is by definition one machine.
  The page is a reading surface and **does not follow anything live**, which took
  fixing twice over. `load()` reads `app.machines`, and calling it straight from
  an `$effect` made the machine list a DEPENDENCY of that effect: the app store
  replaces the array on its own poll beat, so every few seconds the effect
  re-ran, blanked `sources` and refetched the whole fleet, tearing the DOM down
  under the reader. The effect now keys on `scope` plus a `fleetKey` STRING of
  machine ids (an array derived changes identity on every repoll even when the
  fleet has not moved) and calls `load()` inside `untrack`. On top of that the
  reports are cached at MODULE scope for `CACHE_MS` (60s, matching
  `usagepage.go`'s own `usageMaxAge`, since the server cannot produce anything
  newer inside that window anyway), so leaving the page and coming back repaints
  from what is in hand instead of blanking to a spinner. The only thing still
  followed is `scanning`, and only while it is true, because a cold first scan
  genuinely does grow. Re-reading is otherwise a button, and its label is an
  absolute time rather than "3 minutes ago": a relative one has to tick to stay
  true, which is the live thing being removed. Measured after the fix: 3 requests
  at first paint and **zero** over the next 40 idle seconds, a hover held for six
  seconds without the chart being rebuilt under it, and no refetch at all on
  leave-and-return inside the window.
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
  default to `session.ProviderPermissionMode` (auto), and `restart()` re-applies
  it whenever the account is proxy-backed, so a Codex/Grok session starts with the
  same hands-off safe-command behavior as Claude. The native proxies handle the
  CLI's non-streaming Bash safety-classifier call correctly; the cost is one
  extra provider model call for each non-obvious Bash command. `cliproxy_login.go`
  authorizes a provider from the app: it runs the sidecar's own
  `-codex-login`/`-xai-login`/`-kimi-login` under
  `--no-browser`, scrapes the OAuth URL from stdout, and bridges the localhost
  callback with the same `loopbackTarget`/`codeFromPaste`/`forwardLoopback`
  helpers the Claude account login uses; the sidecar's file watcher loads the new
  credential with no restart. The composer shows a provider session's real model
  (from `/api/stats` `provider_models`) and lets you switch it
  (`/api/sessions/{id}/provider-model` updates the mapping and respawns), since
  the Claude-tier picker is meaningless there. `codexusage.go` puts a Codex
  provider's ChatGPT quota on the dashboard, the same two numbers Claude shows:
  the proxy exposes no rate-limit info and there is no `codex /usage` to shell, so
  kunai reads the account's OAuth token (the managed sidecar's own, else
  `~/.codex/auth.json`) and calls ChatGPT's `wham/usage` backend endpoint, the one
  CodexBar reads. This is the single place kunai reads a login it otherwise only
  shells, and it is read-only, only to show a number.
  Both quota readers **refresh an expired token** rather than sending it, and
  that is not an optimisation: they used to read the file and post whatever was
  in it, so once the access token lapsed they posted a dead one every minute
  forever while the refresh token needed to fix it sat in the same file they had
  just read. On this machine Codex's token expired on Aug 1 and the dashboard
  said "no quota" for a week with everything required to recover sitting on disk.
  The proxies already knew how to refresh, so the readers now share that code
  (`codex.Credentials`, `grok.Token`) rather than keeping a second, dumber view
  of the same file; the manager is shared per path, which for Grok is a
  correctness rule rather than tidiness, since xAI revokes the old refresh token
  on each rotation and two independent managers would invalidate each other.
  The residue: `~/.codex/auth.json` is the codex CLI's file, so `owns` is false
  and kunai's refresh is held in memory only — a restart starts again from
  whatever the CLI last wrote, which is correct but means the refresh repeats.
  A quota that cannot be read **says why, to the person rather than the log**.
  Both caches kept a precise sentence, reported it to the journal, and returned a
  bare nil, so the handler answered "usage not available for this provider" and
  the dashboard printed "no quota" — indistinguishable from "this provider has no
  quota to show", which is the wrong conclusion and the one a reader reaches. The
  reason now rides `unavailable` and the dashboard prints it under the pills.
  The Grok free tier is one of those reasons and is NOT a failure: xAI publishes
  no proactive endpoint for it, so it only appears once a request is refused. The windows
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
- **Making pictures** (`internal/cliproxy/codex/imagetool.go`,
  `internal/server/generatedimages.go`): pick Codex in kunai, ask for an image,
  get one. Editing an image you upload works the same way. The premise had to be
  measured, because the obvious conclusion is wrong and was very nearly shipped
  as the answer: Claude cannot draw at all, and the Codex proxy posts to
  `chatgpt.com/backend-api/codex/responses`, which reads like a coding endpoint,
  so the expected verdict was "this needs an OpenAI platform key billed per
  picture, and no subscription covers it". It does not. That endpoint accepts the
  Responses API's built-in `{"type":"image_generation"}` tool on the same OAuth
  token the Codex provider already uses, and it both generates from a prompt and
  **edits** an image handed to it as an `input_image` (the backend reports
  `action:"edit"`). Probed live before a line was written: a 1254x1254 PNG each
  way, billed as ordinary tokens (2326 in / 69 out for a generate), no API key
  anywhere.
  Three facts fix the shape. The **`claude` CLI never asks for the tool**, since
  it does not know it exists, so the proxy adds it on the way out rather than
  passing one through -- there is no amount of prompting that makes Claude Code
  declare another vendor's built-in. It is injected **after**
  `ConvertClaudeRequestToCodex`, not inside it, because that translator rewrites
  any non-`function` tool into a function (the tool loop in
  `translate_request.go`), so a built-in added before translation arrives
  nameless and is refused; doing it in the Codex proxy's own request builder also
  leaves Grok, which shares the translator, untouched. And the picture comes back
  inside a stream the CLI reads as an **Anthropic** response, which has no way to
  carry an image in an assistant message, so it is written to disk and announced
  as markdown -- which cost nothing to build, because `withLocalImages` in
  `Markdown.svelte` already turns `![alt](path)` in a reply into a request to the
  owner-only file route. Inlining it as a base64 data URL would also have
  rendered and was rejected: a megabyte of base64 would enter the transcript and
  be replayed into every later turn's context, a quarter of a million tokens to
  show one picture once.
  Images land in `<dataDir>/generated-images/` rather than the session's working
  directory, and that is forced rather than preferred: the proxy is one
  process-wide server for every session on the machine and the request carries a
  model and messages, not a session id, so it cannot know whose cwd to use. It is
  also the better answer, since a picture is not source and writing one into
  somebody's repository makes their next `git status` a mess they did not ask
  for. `handleSessionFile` gains that directory as a root beside the session's
  own folders and changes in no other way: still owner-only, still images-only,
  still size-capped and symlink-resolved, and still absent from the share gate.
  A **guest** gets a separate route rather than that one
  (`internal/server/sharedimage.go`, `GET /api/share/{token}/image?path=`),
  because before images existed nothing in a shared conversation needed a file
  and every picture rendered as the explained broken frame. It serves ONLY the
  generated-images directory, which holds nothing but what the model drew in the
  conversation the guest is already watching, so it leaks nothing they cannot
  read. The path they send is reduced to its base name and joined to that one
  directory, which makes traversal inexpressible rather than merely refused, and
  the owner's session-folder route stays owner-only and off the gate with its
  test intact. A guest also gets the **same "Working…" line the owner
  sees**: sending left nothing at all between the message and the first token, so
  a turn that thinks for half a minute was indistinguishable from one that was
  never delivered, and a guest has no other way to check. The header dot and the
  Stop button did change, but neither is where somebody looks after pressing
  Send. And a guest with a work link can **send a picture**
  (`internal/server/shareupload.go`), because a screenshot is most of what
  somebody sends when describing a problem. Three rules replace the blanket
  refusal that stood there, and each closes a hole the others do not. **Images
  only**: a non-image upload is copied into the session's working directory so
  the agent can read it, which for a guest is writing a file into somebody else's
  repository, while an image is inlined as base64 and never touches the project
  -- so the safe subset is exactly the one offered. **Only the paired guest**,
  since uploading is sending. And **only ids this link was issued**
  (`guestFiles`), which is the load-bearing one: the uploads directory holds the
  OWNER's files too and an id is all that names one, so an unchecked id is a way
  to have somebody else's screenshot inlined and read back. `redactEvent` keeps a
  guest's own attachments and still strips the owner's, or the message they just
  sent comes back without the picture that was the point of it.
  The directory is swept to `imageKeep` oldest-first, because pictures are ~800KB
  and nothing else would ever delete one. `SetImageSaver` is the whole switch:
  with no saver the tool is never offered, so the capability is gated on being
  able to deliver the result rather than on a flag that could disagree with
  reality. Proven end to end through the real app on a real Codex session --
  generate, and edit of an uploaded PNG -- each rendering inline in the chat at
  1254x1254.
- `internal/project`: reads a directory into the description a session hands a model
  (`Scan` -> `Info`, `Info.Brief()`): layout, language mix, git head from `.git`,
  the files that name it. It never opens the code, and the walk skips `.git`,
  `node_modules` and friends and is capped, because it runs while someone waits.
- `internal/preview` (+ `internal/server/preview.go`, `previewforward.go`,
  `previewcwd.go`): **seeing what the agent built**. kunai could always say what
  an agent WROTE and never what it MADE -- the agent ends a task by running the
  thing (`npm run dev`, a docs server) and that is a port on a machine you are not
  sitting at. The package answers which ports are listening and whose they are;
  `previewforward.go` binds the SAME port on the tailnet address and splices TCP,
  so a phone can open it.
  Two attribution facts are load-bearing. Ancestry ALONE is wrong for the main
  case: the agent backgrounds the dev server, the shell exits, and the kernel
  reparents it to init, severing its chain to `claude` -- so `OwnedBy` matches by
  ancestry **or** by working directory (`withinDir`, compared segment-wise so
  `/home/me/app2` is not inside `/home/me/app`).
  But that rule is only as good as the directory, and it must be **refused for a
  container** (`attributableDir`): a session started in the HOME directory
  matched every process under home, which on a personal machine is nearly all of
  them. Observed live -- a session opened in `/home/ninja` offered `:8443 kunai`
  and the two ephemeral ports of the other instance's native Codex and Grok
  proxies as previews to share. Home, anything above it (`/home`, `/Users`) and
  the filesystem root attribute nothing and leave ancestry to work alone, which
  is the same distinction the sidebar's grouping had to learn: `~/coding` is
  where codebases live, not a codebase.
  And **another kunai is never a preview** (`isOurs`). `selfPID` covers this
  process only, which was enough until a machine ran two of them -- and the
  nightly channel is *designed* to sit beside a stable install, so two is the
  ordinary state of a developer's machine. Matched on process name, the one
  identifier both listener backends produce, against this binary's own base name
  plus the `kunai` prefix (the channels are `kunai` and `kunai-nightly` by
  design). It is a superset of the `selfPID` rule and keeps its load-bearing
  property: the **socket** is skipped, never the port, or a forwarded preview
  (two listeners on one port) would lose its row the instant you shared it.
  And **do not go back to lsof for the socket list on Linux.** That was the
  original source and it silently went blind: a real `next-server` holding
  `*:3000`, owned by kunai's own user, with a readable `/proc/<pid>/fd/22 ->
  socket:[N]` and an entry in `/proc/net/tcp6`, produced **zero** rows from
  `lsof -i -P -n` and from `lsof -p <pid>` while lsof listed kunai's own sockets
  in the same run -- so the card was empty for exactly the case the feature
  exists for, with nothing wrong in the attribution logic. Reproduced twice on a
  real Next.js dev server. `listen_linux.go` therefore reads `/proc/net/tcp`
  and `/proc/net/tcp6` directly (state `0A` = LISTEN), decodes the hex address
  into a real `net.IP` so `::1` and IPv4-mapped loopback need no special case,
  and maps socket inode -> pid by walking `/proc/<pid>/fd`, which is what `ss`
  does. Proven side by side against lsof on the failing machine: 8 servers vs 7,
  the difference being the one that mattered. lsof stays as the fallback and is
  still the only path on macOS (no `/proc`), which is why `Listeners()` returns
  an `ok` bool -- "I cannot look" and "nothing is listening" must never collapse
  into the same answer, the same rule `scanPeers` learned in `discover.go`.
  Three more that were shipped wrong once each. The link is **always `http://`**:
  taking the scheme from `-public-url` produced `https://host:3000`, because that
  origin is https only since kunai terminates TLS on its OWN port with a
  tailscale cert -- one port over sits a plain dev server, and the forwarder is a
  raw TCP splice that adds no TLS, so the phone got `ERR_SSL_PROTOCOL_ERROR`
  (OpenSSL: "wrong version number"). Only the hostname is worth taking from the
  origin. kunai's own listeners are excluded **by pid** (`preview.selfPID`),
  never by port: forwarding a preview makes kunai a second listener on that same
  port, and since entries collapse by port, a port-based exclusion deleted the
  row the instant you shared it -- still forwarded, with the Stop button gone
  with it. And the process name comes from `/proc/<pid>/cmdline`, because `comm`
  is truncated at 15 bytes (`TASK_COMM_LEN`) and rendered Next.js as the
  baffling `next-server (v1`. The card is width-matched to the composer
  (`max-width: 720px`), which `.actionbar` in `Chat.svelte` had already learned:
  full-bleed, it hangs off both sides of the field it sits above.
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
  (a curated language set; the theme lives in `web/src/hljs-theme.css`) and adds
  a copy button. The in-flight streaming block
  renders unhighlighted for speed (`live` prop); committed blocks highlight once via
  a pure `$derived`.
- **A tool that returned a picture shows the picture.** Reading an image rendered
  as the literal text `[image]`, which is the marker the driver leaves where the
  bytes were -- right on the wire and useless on screen, and worst in exactly the
  case kunai exists for, looking at a machine you are not sitting at. Nothing has
  to be sent to fix it: the file is on that machine and the file route already
  serves it, the tool's own **input** says which file (the result carries no
  name), and the marker says there was one. `claude.ImageResultMarker` and
  `IMAGE_RESULT_MARKER`/`imageResultPath` (`web/src/lib/toolMeta.ts`) are the two
  halves of that contract, pinned by a test on the Go side. Both halves are
  required: the marker alone cannot say which file, and a path alone would draw a
  frame around any Read of a `.png`. The extension list mirrors `imageTypes` in
  `sessionfile.go` so the frame is never drawn around a request the route will
  refuse. The **honest limit**: that route is confined to the session's own
  folders, so an image the agent read from outside them (a screenshot in `/tmp`)
  is a 403 and renders as the explained broken frame rather than the picture.
- **An image in a reply is a thing you can use**, not just something that paints.
  `withImageFrames` (in `Markdown.svelte`) resolves the src as before and then
  wraps every image in a `<figure>` with a caption and a hover toolbar: expand
  and download. Rendering it was only half the job -- a picture arrives at
  whatever size the model drew it, inside a message column narrower than that,
  and the one thing you want to do with a picture somebody made for you is keep
  it. Inline height is capped (`460px`, px not vh so a phone's address bar hiding
  cannot resize it) so one image cannot push the rest of the reply off screen;
  full size is one click away.
  Four things are load-bearing. The frame is built from **real DOM nodes** and
  the caption is set with `textContent`, because alt text is model-written prose
  and string-concatenating it into markup is how a caption becomes an injection.
  The actions are wired by **delegation** in the component, for the same reason
  the code-copy button is: figures live inside `{@html}`, so there is no
  component per picture to hang a handler on. `lib/lightbox.svelte.ts` is a
  module-level store with ONE `<Lightbox />` mounted per entry point (App and
  Share), because Markdown renders once per assistant block and dozens are on
  screen at a time -- per-message overlay state would mount dozens of key
  handlers each with its own idea of whether it is open. And `saveImage`
  (`lib/imageActions.ts`) **fetches the bytes and saves a blob** rather than
  pointing an `<a download>` at the URL: the download attribute is ignored
  cross-origin, and cross-origin is the ordinary case here, since a session on a
  peer machine is served from that machine's origin while the app came from the
  hub. Left as a plain link, Download on a peer's image navigates away from the
  conversation instead of saving. It falls back to a plain link when fetch is
  refused, which is better than a button that appears to do nothing.
  The saved file is named from the `path` query parameter rather than the URL
  (`fileNameFor`), because every image in a session shares one route and a name
  taken from the path segments would call all of them "file". A picture that
  will not load is **marked and explains itself**: the file route refuses
  anything outside the session's folders (403) or that is not a raster image
  (415), and the browser's broken-image glyph says which of those happened not at
  all. The error listener is on the **capture** phase, since an `<img>` error
  does not bubble.
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

While a turn RUNS, its activity is **one line naming the call happening now**
(`LiveActivity.svelte`), not the list of everything so far. That distinction is
the whole design and it was briefly lost: opening the disclosure by default
showed every call at once, so the answer to "what is it doing NOW" sat at the
bottom of a growing column of what it had already done, which is clutter dressed
as information. Collapsed, the head IS the answer -- the current tool, the file
or the **command** it is running (a Bash call says which command, since for a
shell call the command is the entire answer), and a count of what came before.
It names the most recent call whether or not that call has come back, which
matters more than it sounds: it used to be shown only while UNANSWERED, and a
Read answers in a blink, so the head said "Thinking" for the whole turn and the
file it read could only be seen by expanding. A settled call recedes a step and
takes a tick, so the line reads as "it did this and is thinking about it" rather
than as a call still in flight.
Everything else is the record of how the answer was reached, and it belongs
behind a click: the disclosure here mid-turn, or the `ToolGroup` summary once the
turn ends. `liveOpen` therefore defaults to false and is reset per turn, keyed on
the NUMBER OF TURNS rather than on `running`, because a turn's blocks change
constantly while it works and only a new query should overrule the reader.
Opened, it shows the tool calls ONLY, in one bounded self-scrolling block BELOW
the reply. The order is load-bearing: what the agent has said so far is the thing
being read, so it stays where reading starts and grows downward as prose does,
while the activity sits at the bottom edge next to `Working…`, which is where the
eye already is. Above the reply it is chrome in front of the content, and
interleaved through it a paragraph and a command take turns shoving the page
down. It used to
render every block, which was wrong twice: `Chat.svelte` renders the prose below
it anyway, so opening printed the answer a second time, and interleaving the two
made a paragraph and a command take turns shoving the conversation down the page.
Calls above, prose below, and the box capped (260px, px not vh) so a turn that
makes forty of them does not grow the page by forty rows -- which is the exact
failure `LiveActivity` was built to prevent and that opening it by default
reintroduced.

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
- `POST /api/handoff` (`handoff.go`) turns a terminal Claude Code session into a
  kunai link, for the `/kunai` slash command the server itself writes to
  `~/.claude/commands/kunai.md` on every boot (`handoffcmd.go`, so a self-update
  alone is enough; it briefly lived in `install.sh`, which a self-update never
  runs). Nothing has to be transferred: the CLI already
  wrote the conversation to the transcript kunai's Recent list reopens from, and
  a running session exports `CLAUDE_CODE_SESSION_ID`, which is exactly that
  file's name (verified). The endpoint deliberately **does not start the
  session**: the terminal's own `claude` is still alive when the command fires,
  and two processes appending to one transcript corrupts it. The link resumes on
  **open** (`/resume/<id>` -> `AppStore.resumeById`), by which time the script has
  exited the terminal (`kill $CLAUDE_PID`, after a delay so the turn renders).
  The URL is also written straight to `/dev/tty`, because the CLI captures the
  command's stdout and the link has to survive that exit. `--fork-session` was
  rejected as the default: a fork diverges silently, and the ask is to continue.
- `GET /api/sessions/{id}/file` (`sessionfile.go`) serves an image the agent made,
  so a screenshot appears in the conversation instead of as a path only the
  machine can open. It is **owner-only at every tier and must never be registered
  on the share gate**: a share link is a public URL, and a route that reads files
  inside the session's folders would hand whoever holds it every image in the
  project. Pinned by the gate's 404 list in `sharegate_test.go`. Confinement is
  `pathguard` (symlinks resolved before the containment check), and the served
  set is raster images only — SVG is refused because it is a scriptable document
  and this serves from kunai's own origin.
- A **Funnel mapping is re-aimed when the gate moves**
  (`reopenPublicPortIfStale`, the counterpart to `closePublicPortIfIdle`). The
  mapping is written into tailscaled and points at a NUMBER, so a restart that
  lands the share gate on a different loopback port leaves it resolving to
  nothing: the public link dies while the tailnet path keeps working, which
  reads as "sharing is broken outside Tailscale" rather than as a stale mapping,
  since from the owner's own machine nothing looks wrong. kunai self-updates and
  the service manager restarts it unattended, which is precisely when nobody is
  watching a link they handed out. `funnelStatus` already RECOGNISED the stale
  mapping (`staleLoopback`) and offered the port back; only a human clicking
  "make public" ever acted on it. It is narrow on purpose: it repoints only a
  funnel port already served and pointing at a loopback address with nothing
  behind it, so a Funnel the owner made for their own app is untouched, the same
  rule the close path follows. Every caller now reads the config through
  `askFunnel`, or a new one silently shells out in a test that thought it had
  stubbed the answer.
- A **Funnel mapping outlives the process that made it**, and on kunai's own port
  that is fatal rather than untidy. `tailscale funnel --https=<port>` is written
  into tailscaled, so it survives every restart and every reinstall; if it lands
  on the port kunai serves, kunai can never bind again, exits, is restarted by
  launchd/systemd, and is never up long enough to clear the mapping it made.
  Observed on a real Mac: `8443` funnelled to `127.0.0.1:59100`, a share gate
  port from days earlier, looping every ten seconds with nothing in the log but
  `bind: address already in use`. `funnelStatus` already refuses to OFFER a port
  anything on this machine is listening on (that is what the `SO_REUSEADDR`-off
  probe in `listenerOn` is for -- macOS otherwise permits the bind and reports
  the port free), but prevention cannot help a mapping that already exists.
  `diagnoseBindConflict` closes the other half: a bind failure asks tailscale who
  holds the port and turns the error into the command that frees it. It reports
  rather than repairs, deliberately -- turning off somebody's Funnel is a change
  to their tailnet, made by a program that has just failed to start, on a guess
  about what a loopback target used to be.
- The CORS wildcard is safe **only** because the tailnet is the entire auth
  perimeter and the API uses no cookies or credentials. Do not add cookie or session
  auth without tightening CORS first. It is **off in local mode**, where that
  premise does not hold: see below.
- **The loopback listener always runs** (`localAddr`/`serveLocal`). A tailnet
  install serves `127.0.0.1` on the SAME port as well as its tailnet address, so
  using kunai on the machine it runs on never goes through Tailscale. The tailnet
  URL does resolve here (MagicDNS points it at this machine's own interface), but
  taking it means your own laptop needs tailscaled up, MagicDNS resolving and a
  valid certificate to reach a program running locally; sign out and the app dies
  with it. The same port is free to take because the main listener binds a
  specific address, not every interface. The local listener is plain HTTP, which
  is not a downgrade (loopback is a secure context) and is in fact required: the
  tailnet certificate names a ts.net host, so `https://localhost` would be
  refused. A failed bind is logged and survived, never fatal.
  The app also answers to any `*.localhost` name, so the local link can read
  `http://kunai.localhost:8443` instead of a bare port. That is free rather than
  clever: RFC 6761 reserves the whole `.localhost` TLD for loopback, browsers
  resolve it themselves without touching DNS, and it keeps secure-context status,
  so there is no certificate, no resolver config and no `/etc/hosts` entry. It
  does not weaken the rebinding guard, which turns on an attacker owning a name
  that resolves here, and `.localhost` cannot be registered; the suffix match
  requires the dot, so `localhost.evil.example` is still refused. The OS resolver
  is not obliged to know the name (systemd-resolved does, macOS may not), so
  `install.sh` **proves the name against the running server before printing it**
  and falls back to plain `localhost` rather than hand out a link that may not
  open. A local CA (the OrbStack/mkcert route to `https://` on a local name) was
  considered and rejected: the certificate is the easy part, but trusting it means
  a root-owned system store, a separate NSS database for each of Firefox and
  Chrome, an admin GUI prompt on macOS that a headless launchd service cannot
  answer, and a manual per-device install on phones -- to buy a padlock on an
  origin browsers already treat as secure. `tailscale cert` remains the answer for
  a real name with real HTTPS.
  The client half is load-bearing and was missed first time round: a machine
  reports itself with its `-public-url`, its tailnet origin, so taking that
  literally sent every request and socket for **this** machine back out to the
  tailnet name. The page loaded over localhost and then said the machine was
  offline. `app.svelte.ts` now uses `location.origin` for the `self` entry, on the
  grounds that the origin which just served the app is the one address proven
  reachable. Peers are untouched; their published URL is the only way to them.
- **The network listener is locked** (`internal/lanauth` for the rules,
  `internal/server/lanauth.go` for the wire, `lanauthadmin.go` for managing it).
  kunai is open source, so the whole scheme is public to an attacker; nothing here
  is protected by being hard to find, and each layer states which other one covers
  its weakness. A PIN is 6-12 digits, refused at *set* time if it is one of the
  handful everyone picks (repeats, runs, keypad shapes), stored as argon2id with a
  random salt and never in the clear. The PIN buys a **session**: 32 random bytes,
  stored only as a SHA-256, in an `HttpOnly`/`SameSite=Strict` cookie -- a cookie
  specifically because a browser cannot set headers on a **websocket** handshake,
  and a token in the query string is the one place credentials reliably reach
  logs. What makes six digits defensible is the **throttle**, and its design turns
  on one fact: on a local network an attacker picks their own source address, so
  per-source limits are an inconvenience, not a bound. There is therefore a
  **global** counter that actually holds the line, the state is **persisted** so a
  restart does not hand back a fresh budget, and the table is capped so a map
  keyed by an attacker-chosen address cannot be grown instead of guessed. The
  throttle is consulted *before* the PIN is checked, every failure looks
  identical from outside (a wrong PIN and an unset one are the same reply, and an
  unset one still burns the same argon2 time), and the throttle key comes from the
  connection, never from `X-Forwarded-For`. The accepted cost is that an attacker
  can lock the owner out; it is bounded, and **loopback never authenticates**, so
  the machine itself is always the way back in. TLS is not optional on this
  listener: a self-signed cert is minted and *kept* (a cert that changed each boot
  would train you to click through warnings), because without encryption the PIN
  and every request after it cross a shared network in the clear. The gate's
  allowlist is written as "everything under `/api/` and `/ws/` is private unless
  named", so a route added later is closed by default -- pinned by a test, since
  getting it the other way round fails silently.
- **LAN access** (`internal/server/lan.go`, `-lan`/`KUNAI_LAN=1`, **off by
  default**, and it refuses to start without a PIN) serves every private address on the host so another device on the
  same wifi can open the app with no Tailscale. It is the web app and nothing
  else: a LAN address is not a secure context, so the browser withholds service
  workers (no PWA install, no offline shell, no auto-update) and Web Push.
  Measured, not assumed -- the app renders and the websocket streams. Each
  private IPv4 gets its own listener (`lanAddrs`), skipping loopback and the
  100.64/10 tailnet range because those are already served; a wildcard bind would
  simply fail against the main listener. `lanGuard` is its perimeter, and it is
  stricter than the loopback one in one way: the `Host` must be a private
  **address literal**, never a name, which makes DNS rebinding inexpressible
  rather than merely detected. The honest limit, which the flag's help text says:
  kunai has no login, so any device that can reach the port can drive the agent.
  The guard stops hostile web pages, not a machine on your wifi making the request
  itself. Turning it on means trusting the network.
- `web/src/lib/clipboard.ts` (`copyText`) exists because `navigator.clipboard` is
  not merely unreliable off a secure context, it is **undefined**. Every Copy
  button therefore did nothing on a LAN address, and `Markdown.svelte`'s
  `navigator.clipboard?.writeText(x).then(...)` read as guarded while calling
  `.then` on `undefined`, an uncaught TypeError inside a click handler. `copyText`
  falls back to a throwaway textarea plus `document.execCommand('copy')`, verified
  working in a real insecure-context browser.
- **Local mode** (`internal/server/localmode.go`) is a loopback-bound install,
  what you get with no Tailscale. It always worked -- the binary defaults to
  127.0.0.1 and a loopback origin is a secure context, so the PWA and its service
  worker install with no certificate -- but `install.sh` treated Tailscale as a
  prerequisite and refused to proceed, so the people it suited least, the ones who
  only wanted kunai on the machine in front of them, could not install at all.
  Tailscale is now optional and buys exactly one thing: the phone. What is
  load-bearing is that binding loopback **removes a perimeter rather than
  tightening one**. Nothing decides who reaches a localhost port, so every page in
  the browser can try, and `POST /api/sessions` takes any cwd and spawns a CLI in
  it. So local mode brings its own guard, wrapped outermost around everything
  including the websocket routes (a handshake is an ordinary request until it
  upgrades, which is why `ws.go` can go on accepting any origin): the `Host` must
  be a loopback name, and a cross-site `Origin` is refused. Both are needed and
  neither covers the other. DNS rebinding resolves an attacker's domain to
  127.0.0.1, so the browser sends no cross-site `Origin` at all and only the
  `Host` betrays it; and merely withholding the CORS header is no defence when the
  damage is done by the request arriving, since a POST that starts a session has
  already started it. A request with no `Origin` is allowed, because anything that
  can run curl here can run `claude` directly. Mode is derived from the bind
  address, never a flag, so a tailnet install is bit-for-bit unchanged and there
  is nothing to set wrongly; an empty host (`:8443`) is every interface and
  deliberately NOT local.
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
- The turn-end hook must never be called inline, and the respawn's wait for the
  old driver must never be unbounded. `Session.afterTurn` runs ON the driver event
  pump, and that goroutine's exit is what closes `Done()`; the hook's job is to
  respawn the session, and `Manager.restart` waits on `Done()`. So calling it
  inline deadlocked the pump against itself: auto-failover reacting to a spent
  window killed the CLI and then waited 37 minutes for the goroutine it was
  running on to return. The session was left with a dead process while still
  listed idle on the walled account, and because that `Done()` could now never
  close, every later restart piled up behind it: four manual account switches from
  the app blocked on the same channel, so the POST never answered and the app went
  on showing the account it was already using. The wall is exactly when somebody
  needs to move accounts, so the two failures arrived together. Both halves are
  load-bearing: the hook is dispatched with `go` (as the driver-ended path in
  `pump` already did, and for this reason), and the wait has a `closeGrace` bound
  that logs and proceeds, because `Close()` has already cancelled and killed the
  process so respawning is safe, and a UI action must answer even when a pump is
  wedged for some other reason. Pinned by `TestTurnEndHookDoesNotDeadlockRespawn`.
- An auto-failover must say it is happening, and must lose to a human.
  Choosing where to move a walled session means reading every candidate account's
  quota, and for a Claude account that is a `claude /usage` shell at a couple of
  seconds each. So there is a multi-second gap in which the composer correctly
  names the account that has just been walled and nothing else is on screen. A
  failover that worked perfectly was therefore reported as never firing: the log
  said `rolled from "claude-work" to "Codex"`, the user saw the limit message plus
  the old account, concluded it was broken, and switched by hand. Silence is
  indistinguishable from a broken feature, so `Session.BeginFailover` announces
  before the decision starts and `EndFailover` retracts with a reason if the
  session stays put; the state rides on `hello` too, because a phone that attaches
  mid-decision needs it. A successful roll announces nothing, since it replaces
  the session and the new `hello` names the new account.
  Two consequences follow. That manual switch **wins**: the decision re-reads the
  live session from the manager and stands down if the account changed under it
  (`movedByHand`), which also avoids rolling a `Session` the user's own respawn
  has already closed. And a provider is a **fallback, not a peer**: ranking on raw
  headroom sent a Claude session to Codex on 85%-vs-40%, silently changing which
  model answered, and the old "prefer Claude on a tie" rule could never fire
  because two quota percentages are never exactly equal. `sameBrain` now takes any
  Claude account above `sameBrainFloor` ahead of any provider, so moving accounts
  at a wall changes the bill and not the agent.
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
- **Yolo mode** (`session.BypassPermissionMode`, `bypassPermissions`) is not the
  end of the mode scale, it is a different kind of thing, and
  every rule below follows from one measured fact rather than from caution: a CLI
  spawned in it sends **no `can_use_tool` at all** (verified against 2.1.222 --
  a Bash call ran with zero control requests). So kunai's own tool boundary, which
  is implemented AS a `can_use_tool` handler, is not loosened by this mode, it is
  never consulted. That boundary is the **share guard**, so a shared session and
  bypass can never coexist, and the two orders are refused in two places because
  neither covers the other: `Session.SetPermissionMode` refuses bypass while a
  `ToolGuard` is installed (shared first, then YOLO), and the share create handler
  refuses a session already in bypass with a 409 (YOLO first, then shared). A
  share's own standing mode goes through `session.ValidGuestMode`, not
  `ValidPermissionMode`, since `applyShareTier` respawns the session into it.
  Refused rather than silently downgraded: the owner turned it on deliberately.
  A **loop keeps bypass** rather than borrowing `acceptEdits` (`loopModeFor`) --
  the borrow exists to make an unattended run more autonomous, and applied here it
  would do the opposite, hanging overnight on the Bash calls the owner had
  arranged not to be asked about, which is the exact failure `LoopPermissionMode`
  exists to prevent. Two client rules. `chat.setMode` is **not optimistic**,
  unlike `setModel`, because this one can be refused: setting it locally first
  left the composer reporting a permission state the session was not in, which is
  worse than either outcome it was hiding. And `dispatch` now calls
  `Session.ReportError` instead of only logging, because a refusal nobody is told
  about reads as a broken button.
  There is **no confirmation step**, and the one that was there is worth
  recording as a mistake: it hijacked the composer, which is a thing you do to
  somebody rather than for them, and it bought nothing that the row and the
  colour do not already buy. What replaced it is what the other four modes have
  always had -- a one-line description in the menu -- plus an info affordance for
  the part that does not fit on a line. The description had to be written
  carefully, because the obvious phrasing is wrong: "runs commands without
  asking" describes **Auto** too, which already runs safe ones on its own. The
  difference is not whether it runs commands, it is that nothing is left that
  makes it stop, so the hint is "Never asks, not even about risky commands" and
  the info bubble names what that covers (deletes, `git push`, installs, network
  calls) and that it is not confined to the session's folder. One layout note
  that cost a round trip: `.mode-pop button` sets `width: 100%`, which outranks a
  bare `.minfo`, so the info button took the whole row and squeezed the label
  into one word per line -- the icon's rules are scoped through `.mrow` for that
  reason.
  It is **named rather than described**, which is the opposite of the rule the
  other four modes follow, and that is the point: "Never ask" (the first label)
  read as one more setting on the same dial, and this is not on the dial. A name
  you have to learn is a name you cannot pick by accident. The state is then
  carried by the **composer itself** rather than by the pill: `.field.yolo`
  colours the border, the caret and the text you are typing with `--yolo-ink`
  (`#e0b978`, --busy's hue at a brighter step, 10.7:1 on `--panel` where 4.5 is
  the floor for prose). Both channels are needed -- a border is chrome the eye
  stops seeing, and text colour alone shows nothing on an empty composer. This is
  the one place amber is worn by prose instead of by a dot, and it earns the
  exception the same way the brand marks earn theirs: it is the only channel
  reporting a state whose mistakes cannot be taken back after the fact.
  **Yolo is kunai's mode, not the CLI's**, and that is what makes it a mode you
  can turn on without losing the process. `Session.onPermission` answers the ask
  itself when `s.mode` is bypass, and the CLI runs in `acceptEdits` underneath
  (`CLIModeFor`). Two measurements forced this. The CLI's own
  `bypassPermissions` cannot be set on a running session -- it answers `Cannot
  set permission mode to bypassPermissions because the session was not launched
  with --dangerously-skip-permissions` -- so using it made entering Yolo a
  respawn, and a respawn blanks the conversation for several seconds while it
  reloads, on a mode people flip on mid-task. And passing
  `--dangerously-skip-permissions` on every spawn to make the runtime switch
  legal is worse than it sounds: measured in kunai against a real CLI, that flag
  **overrides `--permission-mode` entirely**, so a session in Ask stopped asking
  about a `curl`. kunai is the `--permission-prompt-tool`, so it does not need
  the CLI to stop asking; it can stop asking the person. The auto-allow sits
  **after** the share guard deliberately, so a shared session's folder boundary
  still runs first and can still deny -- the guard is now consulted under Yolo
  rather than bypassed by it, which is why this version is safer than the
  original as well as quieter.

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
- **Files go both ways, and each direction has its own rule.** Inbound: a message
  carrying a photo or document has no `Text` at all (what you typed arrives as
  `Caption`), so reading only `Text` made a screenshot sent to the bot vanish
  silently. The largest photo rendition is taken, a document is taken as sent (which
  covers an image sent "as a file", uncompressed), and the bytes are handed to
  `Sessions.SendFiles` -- the channel never learns where uploads live or what shape
  the model wants, so the adapter stores them in the SAME uploads dir and builds
  content with the SAME `buildContent` as an app upload. A caption-less file supplies
  its own words, because an empty prompt is rejected and strands the turn on
  "Working...". Outbound (`/get <path>`) is deliberately NOT gated on `Detail`: that
  policy guards *incidental* spill, and a path you typed is the opposite of
  incidental. It is gated on **location** instead -- `resolveInside` resolves the path
  within the session's own folder, following symlinks BEFORE the check, so no
  spelling of the argument reaches `~/.claude` credentials or `/etc`. Always
  `sendDocument`, never `sendPhoto`: the reason to look at a file the agent made is
  usually to read it, and `sendPhoto` recompresses.
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

Dark near-monochrome theme; tokens in `web/src/app.css`. No glows or emojis in the
UI, and no gradients except **one**: the home screen's ambient wash
(`.ambient` in `Home.svelte`), two radial pools at a few percent white that drift on
a slow 42s cycle. It is deliberately near-invisible and sits under the content at
`z-index: 0` with `pointer-events: none`, so the page still reads as flat monochrome
and nothing competes with the data; `prefers-reduced-motion` drops the motion and
keeps the wash. Do not spend this exception anywhere else.
White is the only accent (primary buttons); amber and green are
reserved for status dots and the permission gate. Hue carries meaning in exactly
three places, each of which had to earn it: code syntax highlighting (below), the
brand marks that say which account a session runs on, and the Usage page's agent
palette (`web/src/lib/agentColors.ts`, validated for colour-vision separation
against `--bg`). The last two are the same rule, which is the rule to apply to any
fourth candidate: a colour is spent only where it stands for somebody else's
product, because there the colour is the identity and carries real information.
kunai's own furniture stays on the gray ramp. Fonts: Geist (UI), Geist Mono
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
  difference is who chose the name: a **project** group is derived, so every
  session has one for free; a **workspace** group is named by hand, which is what
  you reach for once a session holds more than one codebase and the directory it
  happened to start in stops describing it. A named heading wins over the derived
  one, sessions sharing a name group together, and clearing the name drops them
  back under their folder. Pinned stays flat (a pin is a priority list; grouping
  it would bury the thing you pinned), and a single group renders no heading at
  all, so a one-project machine looks exactly as it did before.
  What the derived heading is derived FROM took a second pass. It was the
  directory the session started in, which is not the same thing as its codebase:
  a session launched from `~/coding` got a heading called **coding**, a folder
  that holds every codebase on the machine and is not one itself. Twelve of
  twenty-five rows on this machine sat under `~`, `~/coding` or `/tmp` on that
  rule. The obvious fix -- hide a session whose folder has no `.git` -- is wrong,
  and measurably so: the two LARGEST transcripts here (51MB and 10MB) were both
  launched from `~/coding`, so it would have deleted the biggest work on the
  machine from Recent. The folder is uninformative; the session is not. So
  `Meta.Project`/`HistoryEntry.Project` carry a derived answer and `groupLabel`
  prefers `repo || project || cwd` (most-specific claim first: `repo` is "cwd is
  a worktree OF that codebase", `project` is "cwd is part of, or did its work in,
  that codebase"). It is derived in two steps. `project.Root` walks up for a
  `.git`, which alone merges `kunai/web` back under `kunai` and costs a few
  stats; that is all a **live** session gets, because this runs on every poll and
  reading a transcript per session per poll to improve a heading is not a trade
  worth making. A **past** session gets the second step (`projectDir` in
  `history.go`): cwd is a container, so ask the transcript where the work went.
  Every transcript line records the directory the agent was in, so the immediate
  child of cwd that most of them name is the codebase -- bucketed by immediate
  child, or a session that spent its time in `hiring-god/web` files under a
  heading called **web**. This is free, and staying free is the constraint:
  `claudeTitle` already read a 128KB tail of every transcript on every poll, so
  the histogram rides those same bytes (`tailBytes` read once, `claudeTitle` and
  `tailDirs` both fed from it). Taking it from the head instead would have meant
  giving up `probeTranscript`'s early break and unmarshalling sixty whole lines
  per transcript per poll, for a worse answer -- the tail is the better sample on
  its own terms, since it reports where the session was working by the end rather
  than which folder somebody typed at the start. Three things a candidate must
  be, each of which a real session tripped over: not a dotfolder (`~/.claude` is
  not a project), a directory that still **exists** (a heading you cannot start a
  session in is a bad heading, and one deleted folder was outpolling the right
  answer 25 to 26), and a **clear winner** (twice the runner-up, at least three
  lines). A near tie is not noise to be broken: it means the session genuinely
  spanned several codebases, there is no single honest heading, and the right
  answer is to leave it under its folder where naming it a workspace is offered.
  Measured on the real corpus: container headings 12 rows -> 8, four sessions
  moved to the codebase they actually worked in, `/api/history` unchanged at 37ms.
  The residue: a **live** session launched from a container folder keeps that
  folder as its heading until it closes, since only the transcript can rescue it.
- The workspace name lives in `sessionMetaStore` beside the rename and the pin,
  keyed by session id, because the grouping has to **outlive the process**: a
  session named while running must still be in that workspace tomorrow when it is
  a transcript in Recent. That is also its one limit: a closed session's project
  list died with it, so `Meta.Projects` (the count that marks a session as worth
  naming) is live-only, and an *unnamed* multi-project session falls back to its
  directory once closed. Naming it is what makes the grouping permanent.
- A live session in the sidebar is a **three-line row**, read top to bottom:
  the codebase and what the agent is doing (`Working 17s`, or `Needs you`), then
  the session's title bright and bold, then the branch the work lands on and the
  brand mark of the account paying for it (`web/src/lib/providerMarks.ts`).
  **Each line appears only when it has something to say that is not already on
  screen**: the project is dropped when the group heading directly above already
  says it, and the branch line is dropped when there is no branch, in which case
  the mark rides on the title. Rendering all three unconditionally printed the
  same folder name on the heading, the row's top line and inside its bottom line
  at once. The branch is read from `.git/HEAD` for ANY session
  (`project.Branch`, applied in `worktreeStore.tagRepos`), not just a kunai-made
  worktree, or that line would be blank for most sessions. The active row takes a filled card, which
  earlier single-line rows deliberately avoided; with three lines the list needs
  a shape to say where one row ends. A past session stays a quiet single line.
  The status badge here was **tried once before and reverted for lying**, and
  what makes it honest now is data rather than presentation: only `running` and
  `awaiting_permission` are ever named (a resumed session reports `starting`
  until its first prompt), and the duration comes from `Meta.TurnStartedAt`,
  stamped in `startTurnLocked` and zeroed when the session goes idle -- never on
  `awaiting_permission`, which is the same turn paused, so approving does not
  restart the clock. A resumed session has no running turn and therefore shows
  no duration, which is the honest answer rather than a clock started at reopen.
  The client prefers an open tab's socket over the polled list for both the
  state and the start time (`liveState`, `liveTurnStart`), because the poll is a
  cycle behind by design.
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
- Code syntax highlighting is the **one place hue is used to carry meaning**, and
  it is assigned by what a reader is actually looking for. Code here arrives
  inside a reply, in a log of what an agent already did, usually skimmed: the
  information is in the VALUES (the string it wrote, the path it touched, the
  number it chose) and the NAMES it called, while `const` and `function` are
  scaffolding you already know is there. So saturation goes on values and names
  and the structure recedes — the inverse of an editor theme. Four hues, in
  kunai's own muted register rather than an IDE's (`--code-string` sand,
  `--code-number` orchid, `--code-name` cyan, `--code-keyword` slate), none of
  them colliding with the green and red that mean status elsewhere. Every one
  clears 4.5:1 against the code block's `--bg`, comments included: kunai's source
  is comment-dense and those comments are the reasoning, so they recede without
  being the faintest thing on screen (they sat at `--text-4` and 3.1:1 before).
  A ```diff block keeps `--live`/`--alert`, since those already mean added and
  removed everywhere else. This deliberately **replaced** an all-grey theme whose
  rule was "differentiation comes from brightness, not hue"; that read as
  uniform at a glance, which is the one thing code cannot afford.

## Commit conventions

No `Co-Authored-By` trailers, no emojis, and no em dashes in commit messages or
docs (owner requirement; the project is intended to be open source, and history was
rewritten once to remove co-author and emoji trailers).
