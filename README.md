<div align="center">

<img src="docs/logo.svg" alt="Kunai" width="96" />

# Kunai

**Turns any machine you own into a Claude Code server.**

Sessions live on your hardware instead of in a terminal, so they keep working
after you close the app. One installable phone app drives every machine and every
Claude account you have, directly over your own Tailscale network.

<p>
<img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-52525b?style=flat-square" />
<img alt="Go 1.26+" src="https://img.shields.io/badge/Go-1.26%2B-52525b?style=flat-square&logo=go&logoColor=white" />
<img alt="Svelte 5" src="https://img.shields.io/badge/Svelte-5-52525b?style=flat-square&logo=svelte&logoColor=white" />
<img alt="Tailscale" src="https://img.shields.io/badge/network-Tailscale-52525b?style=flat-square&logo=tailscale&logoColor=white" />
<img alt="Platform" src="https://img.shields.io/badge/platform-Linux%20%7C%20macOS-52525b?style=flat-square" />
</p>

</div>

You are on the bus. You open the app, pick your desk machine, and ask it to fix
the test that was failing when you left. Then you lock your phone. It buzzes when
the turn is done and tells you what it cost.

That is the whole idea. You install one Go binary on each machine you work on,
and your Linux box and your Mac then sit side by side in the same app. Nothing is
proxied through a server in the middle, so your code never leaves hardware you
own. The only thing that crosses the tailnet boundary is a push notification, and
it carries none of your conversation.

---

## New in 1.5

**Several agents on one repository.** Start a session in a git worktree and it
gets its own checkout, on its own branch, sharing one object store. Three agents
can work on the same codebase at once without touching each other's files, and
none of them can derail the branch you have checked out. The agent is told it is
in a worktree and what is shared, so it does not go reinstalling dependencies that
are symlinked. Land it from the app: merge, open a pull request, or discard.
→ [Several agents, one repository](#several-agents-one-repository)

**Undo what a turn did to your files.** Every turn takes a git snapshot before it
runs. Revert is in the turn's footer, and it tells you exactly what it is about to
change first, asked of git rather than guessed from the turn: the files, how many
untracked files get deleted, and that this resets the whole repository. Undo is
there afterwards too.
→ [Undo a turn](#undo-a-turn)

**Models that are not Claude.** Point a session at a Codex or Grok subscription
and every tool, edit and permission keeps working; only the model answering
changes. Kunai runs the translation in-process, so there is no sidecar to
download. Authorize from the app.
→ [Other models](#other-models)

**Runs that start without you.** Schedule a prompt for a time, or for the moment
your usage window resets, so an agent starts overnight or the second your quota
comes back.
→ [Working while you are away](#working-while-you-are-away)

**A sidebar that reports.** Sessions group under the codebase they belong to, and
a folder says what its agents are doing (how many are working, how many need a
decision) and opens itself when one of them is waiting on you.

---

**Contents** ·
[Quick start](#quick-start) ·
[What you get](#what-you-get) ·
[Several agents, one repository](#several-agents-one-repository) ·
[Other models](#other-models) ·
[Undo a turn](#undo-a-turn) ·
[How it works](#how-it-works) ·
[Claude accounts](#claude-accounts) ·
[Channels](#channels) ·
[Working while you are away](#working-while-you-are-away) ·
[Configuration](#configuration) ·
[Security](#security) ·
[Develop](#develop)

## Quick start

### What you need

- A machine running Linux or macOS.
- [Claude Code](https://claude.com/claude-code) installed and signed in, with
  `claude` on your `PATH`.

That is the whole list. Tailscale is **optional** and buys one thing: reaching
kunai from your phone. Without it the installer sets kunai up for the machine
you are sitting at, on `localhost`, and tells you how to add the phone later; you
lose nothing by starting there. With it, you also need MagicDNS and HTTPS
certificates turned on in the Tailscale admin console, under DNS then HTTPS
Certificates.

You do not need a toolchain. The one-liner pulls a prebuilt binary, and the web
app ships already built inside it, so Node is never involved. If you install from
a source checkout instead, the script fetches a local Go toolchain under
`~/.kunai` when `go` is missing, without touching root.

### 1. Install on the machine you use most

```sh
curl -fsSL https://raw.githubusercontent.com/HEGADE/kunai/main/install.sh | bash
```

<sub>From a source checkout instead, which builds it: `git clone https://github.com/HEGADE/kunai && cd kunai && ./install.sh`</sub>

The installer downloads the binary, installs a service (a systemd user unit on
Linux, a launchd agent on macOS), health-checks it, and prints two links: one for
this machine and one for your phone.

Those are genuinely two addresses. kunai always serves `http://localhost:<port>`
alongside whatever else it is bound to, so using it on the machine it runs on
never goes through Tailscale and keeps working when Tailscale is down or you are
signed out. It answers to any `.localhost` name too, so
`http://kunai.localhost:8443` works with no setup at all: that TLD is reserved
for loopback, browsers resolve it themselves, and it still counts as a secure
context, so the app installs and notifies exactly as it does over HTTPS.

If you have Tailscale it works out your tailnet address and MagicDNS name and
mints a TLS certificate with `tailscale cert`, so the same link works from every
device you own. If you do not, it installs for this machine only and prints a
`localhost` link. That is a real install, not a degraded one: everything works
except reaching it from another device, and re-running the installer after
setting up Tailscale upgrades it in place, keeping your sessions and settings.

### 2. Put it on your phone

Open that URL. On iOS, use Safari, then Share, then Add to Home Screen, and turn
on notifications once it is installed. This machine is now your hub.

### 3. Add your other machines

Run the same installer on any other machine, pointing it at the hub so its
notifications reach your phone:

```sh
curl -fsSL https://raw.githubusercontent.com/HEGADE/kunai/main/install.sh \
  | KUNAI_HUB_URL=https://<hub>.<tailnet>.ts.net:8443 bash
```

Open the hub's app again and the new machine is already there. Discovery is
automatic; you pick the machine when you start a session.

### Keeping it updated

When a machine falls behind the latest release, the home screen shows an update
badge. Tap Update and that machine pulls the new binary from GitHub, checks its
sha256, swaps it in, and restarts, with a progress bar while it downloads. Open
sessions resume from their transcript. If a machine does not come back, the app
says so and prints the command to revive it rather than leaving you guessing.
Every machine updates itself, so there is no SSH involved.

### Nightly channel (run it alongside stable)

There is a second, bleeding-edge channel built from the `nightly` branch on
every push. It installs **beside** a stable install, with its own service
(`kunai-nightly`), its own port (`8444`), and its own data dir
(`~/.kunai-nightly`), so you can run both at once and try new features without
risking the setup you rely on. Everything in [New in 1.5](#new-in-15) landed here
first.

```sh
curl -fsSL https://raw.githubusercontent.com/HEGADE/kunai/nightly/install.sh | KUNAI_CHANNEL=nightly bash
```

<sub>Or from a checkout: `git checkout nightly && KUNAI_CHANNEL=nightly ./install.sh`</sub>

Open the stable app at `https://<host>.<tailnet>.ts.net:8443` and nightly at
`:8444`; they share nothing. A nightly install self-updates from the moving
`nightly` pre-release (checksum-verified, same one-tap flow), so it always
tracks the newest build. Nothing on the nightly channel is a stable release, so
expect rough edges and keep your day-to-day work on the stable install.

## What you get

**A fleet rather than one box.** The same binary runs everywhere and one app
aggregates all of it. The client talks to each machine directly over the tailnet,
never through a middleman. The machine you installed the app from is the hub, the
others are peers, and the home screen carries live memory, CPU, disk and uptime
for whichever one you are looking at.

**Chat that survives your phone.** Backgrounding the app kills the socket, not
the session. When you come back the client replays exactly what it missed. Old
sessions reopen with their full conversation and tool output intact, and scroll
back far enough and older history pages in from the transcript on disk.

**Replies you can actually read.** Markdown as it streams, syntax highlighting,
real red and green diffs for edits, and a card per tool call showing both the
request and what came back. Every reply has a copy button. Every reply that
touched files gets a changed-files card underneath it, one entry per file,
expandable to the diff, so you can review what a single question did from a
phone.

**A permission gate you can trust.** Anything that needs approval shows you the
actual command or diff first, with an option to allow it for the rest of the
session. Four modes: Ask, Auto, Accept edits, and Plan.

**Notifications that say something.** A finished turn tells you how long it ran
and what it cost. A failed one says it failed. A loop that ended names the limit
that ended it. None of it carries any of your conversation.

**Work that continues without you.** Loops keep an agent going with nobody at the
keyboard, bounded so they cannot run forever, and scheduled runs start one at a
time you pick or the moment a usage window resets. There is a thermal guard for a
laptop you left running. See [below](#working-while-you-are-away).

**Sessions that stay findable.** They group under the codebase they came from, so
a machine with six projects reads as six folders rather than one long list. Name a
group and that name sticks, which is what you reach for once a session spans more
than one codebase and the folder it started in stops describing it. A folder tells
you how many of its agents are working and how many are waiting on a decision, and
opens itself when one of them needs you.

## Several agents, one repository

Two agents in one checkout overwrite each other. The usual answer is to run one at
a time, which wastes the thing kunai is for.

A git worktree fixes it at the level git already understands: a second checkout of
the same repository, on its own branch, sharing one object store. Each session
gets its own files, its own branch, its own HEAD, and none of them can touch the
others or the branch you have checked out.

Start one from the sidebar's worktree button, or from the launcher. It asks what
the work is and which branch to cut from, defaulting to the branch you are standing
on rather than the repository's default, and names the branch from your prompt if
you do not name it yourself. Everything else is a choice you can leave alone.

**Dependencies come from a setup command, not a copy.** A worktree starts with
only what git tracks, so `node_modules` and `.env` are absent. A `kunai.json` at
the repo root declares one shell command to fix that:

```json
{ "setup": "pnpm install --frozen-lockfile && ln -sf \"$KUNAI_PROJECT_ROOT/.env\" .env" }
```

It runs in the new worktree with `KUNAI_PROJECT_ROOT` and `KUNAI_WORKTREE_PATH` in
the environment. Install fresh, symlink the secrets. Kunai never needs to know
what a package manager is, the setup travels with the repository, and the first
prompt waits until it finishes so no agent starts against a half-installed tree. If
there is no `kunai.json`, kunai suggests a command from the lockfile it finds and
shows it to you before it runs anything.

**The agent knows where it is.** It gets the worktree path, its branch and base,
the main checkout's path, whether setup succeeded, and which paths are symlinks
into the main checkout, read back off disk after setup rather than taken on trust,
so it does not delete or reinstall something shared. That arrives as a
system prompt rather than a first message, because a message gets compacted away
in exactly the long unattended run that most needs the facts.

**Landing it is your choice.** The session's worktree card shows how far ahead or
behind the branch is and what is uncommitted, and offers merge, a pull request via
`gh`, or discard. A merge refuses a dirty main checkout rather than stashing your
work, and a conflict is reported, never auto-resolved. Discard names the
uncommitted files and unmerged commits it is about to destroy. Nothing is ever
garbage-collected: removal is always something you asked for.

## Other models

A session can run on a Codex or Grok subscription instead of Claude. Every tool,
edit, permission prompt and file diff keeps working, because kunai keeps driving the
same `claude` CLI and only swaps the endpoint it calls out to. Only the model
answering changes.

Authorize one from Providers in the sidebar: it opens the provider's own sign-in in
your browser and the credential lands on your machine, never in kunai. The
translation between Anthropic's API and the provider's runs in-process, so there is
no sidecar to download. Pick the provider when you start a session, or switch a
running one, and the composer shows the real model it is on rather than a Claude
tier that would mean nothing there.

Because the CLI still thinks it is talking to Claude, it packs context to Claude's
window while the upstream model's may be smaller. Kunai measures each request
against the real window and drops the oldest whole turns if it would overflow,
keeping the system prompt and never orphaning a tool result from its call, so a
long session keeps working instead of failing mid-stream. Codex quota shows on the
dashboard next to Claude's.

<sub>Codex is verified end to end. Grok rides the same path with its happy path
unverified. Kimi is not built.</sub>

## Undo a turn

Every turn takes a git snapshot of the working tree before it runs, so a turn's
file changes can be undone without touching the conversation.

Revert sits in the turn's footer, next to Copy. It asks first, and what it asks
with comes from git rather than from the turn: the files it will change with git's
own status letters, how many untracked files it will delete and which, and the fact
that this resets the whole repository, so anything changed after that turn goes too,
including edits you made yourself. Afterwards, Undo revert is in the same
place: the revert captured its own safety snapshot before running.

## How it works

```
Phone or laptop (Svelte PWA)
    |  wss + REST, straight over Tailscale
    v
kunai (one Go binary, bound to the tailnet IP)
    /ws/app/:id      WebSocket bridge to the client
    session manager  per-session ring buffer, seq replay, permissions
    /api/*           sessions, history, stats, browse, upload, push, machines
    embedded PWA     served from the binary (go:embed)
    |  stdin and stdout, stream-json, one process per session
    v
claude CLI (Claude Code)
```

Kunai wraps the `claude` CLI and drives Claude Code over its stream-json control
protocol, the same protocol the official Agent SDK speaks. The web app is
compiled into the binary and served straight over the tailnet.

The tailnet is the entire auth perimeter. The server binds to the Tailscale
interface and nothing else, and your Tailscale ACLs decide who can reach it.
There is no login screen because there are no accounts.

Installed without Tailscale, the perimeter is the loopback interface instead, and
the server enforces it rather than assuming it. Binding to `localhost` sounds like
the safest possible choice, and by itself it is the opposite: nothing decides who
reaches a localhost port, so any page open in your browser can try to drive kunai.
So a local install refuses any request whose `Host` is not a loopback name, which
is what stops a hostile site pointing its own domain at 127.0.0.1, and refuses any
request carrying a cross-site `Origin`. Requests with no `Origin` are allowed:
anything that can run a command on the machine can run `claude` itself and has no
need of us.

With several machines, the one that served you the app is the hub. It owns the
machine registry, Web Push, and peer discovery. The client reads the machine list
from the hub and then connects to each machine's own tailnet origin, so no
traffic is relayed and the promise holds across the whole fleet.

## Claude accounts

One machine can drive more than one Claude account, say a personal one and a work
one, so that when a limit runs out on one you can move a session to the other.

Add one from the app, in the Accounts item in the sidebar. Give it a name, tap
the sign-in link, sign in as that account in your browser, and paste back the
code Claude gives you. No terminal, no config directories, no editing files.
Kunai never sees anything but that one link and that one code; the CLI writes its
own login into its own folder.

Once you have more than one:

- Starting a session lets you choose which account runs it.
- The composer shows which account the current session is on, and you can move a
  running session to another one. Kunai copies its transcript across so the new
  account picks up the whole conversation. Its first turn re-reads that context
  uncached, which is the cost of the move.
- The home screen shows quota per account, one line each, so you can see which
  one still has room before you switch.
- Recent lists every account's past sessions and remembers which account each one
  ran on, so reopening it puts it back where it belongs.

Accounts live in `~/.kunai/clis.json`. You can edit that by hand if you want a
different binary per account or extra environment variables, and Settings has a
manual editor for pointing at a config folder you already signed into elsewhere.

## Channels

Kunai can be reached from somewhere other than this app. Telegram works today,
Slack is next, and both live under Channels in the sidebar.

A channel needs no Tailscale on the device in your hand and no app install. The
bot long-polls Telegram outbound, so kunai still exposes nothing and still needs
no inbound hole.

**Connecting one.** Create a bot with @BotFather, open Channels, and paste its
token. Then message your bot. It answers with a six character code, the code
appears in Channels, and you approve it there. Nobody has to go hunting for a
numeric user id, and access is granted deliberately rather than by editing a
list. Codes expire after an hour, and you can revoke anyone later from the same
screen.

In the chat:

```
/new <path>   start a session in a directory
/sessions     list running sessions
/use <id>     point this chat at a session
/status       what the current session is doing
/stop         interrupt the running turn
/end          close the session
```

Anything that is not a command is sent as a prompt. Replies stream into a single
message as they are written, and a tool that needs approval arrives with Approve
and Deny buttons.

**What a channel is allowed to see.** Telegram is a third party, so by default it
carries the conversation and the controls and nothing else. A tool call is
announced by name and file path ("Edit internal/server/usage.go"), never by its
contents, and tool output is not sent at all. That keeps the accidental spills
out of a chat log you do not control: the config file an agent reads, the token a
test echoes, an env dump in a debug command. Open the app to see any of it in
full. There is a switch in Channels to send tool inputs and outputs too, which is
occasionally convenient and worth a moment's thought first.

A token can also be seeded from `-telegram-token` and `-telegram-allow` for a
scripted install, but the app is the easier route and takes effect without a
restart.

## Working while you are away

A loop re-feeds one task every time a turn ends, which is Ralph's technique, so
an agent keeps working with nobody watching. A session your phone walked away
from mid-turn also just keeps going. Loops stop at whichever of their iteration
or spend limits arrives first, or when the model reports the task done, so they
cannot run forever. A loop survives a restart, too: an auto-update or a crash
mid-run picks up from the iteration and spend it had reached, because the whole
point is that nobody is attached to notice it died.

**Runs that start without you.** Schedule a prompt for a time, or for the moment a
usage window resets, from Schedule on the home screen. The reset trigger is the
useful one: a 5-hour window that empties at 2am can have an agent waiting on it, so
the quota is spent while you are asleep instead of expiring. A scheduled run fires
at most once, since a missed occurrence beats two agents starting the same work, and
the schedule row records what happened, so "did my job run?" is answerable without
reading logs.

That is fine on a desktop and less fine on a laptop with the lid shut, where
hours of pinned CPU cooks the machine. The thermal guard is the safety net that
makes leaving it running reasonable. It is off until you enable it per machine in
Settings.

### The guard, with no special privileges

When the host runs hot for a sustained stretch, or has been held awake past a
wall-clock cap, the guard stops every session and releases the keep-awake hold.
On a closed laptop that lets it sleep, and sleeping drops the CPU to idle. Sleep
is the cooldown. A one-off spike never trips it, and whichever limit comes first
wins, the same shape a loop's caps have.

Temperature comes straight from `/sys/class/hwmon` on Linux. macOS cannot report
it without the grant below, so without that grant only the wall-clock cap can
fire.

### Lid-closed work and power-off, opt-in

Three things each need a privilege the ordinary service does not have: keeping a
laptop working with the lid shut, reading temperature on macOS, and powering the
host off as a last resort. They stay off until you install the grant once,
deliberately:

```sh
curl -fsSL https://raw.githubusercontent.com/HEGADE/kunai/main/install.sh \
  | KUNAI_THERMAL_PRIVILEGED=1 bash
```

> Put `KUNAI_THERMAL_PRIVILEGED=1` on the `bash` side of the pipe, as above. In
> front of `curl` it applies to `curl` alone, never reaches the script, and the
> grant is skipped without a word.

It asks for your password once and grants exactly this, printing what it did:

- On macOS, a sudoers NOPASSWD line for `pmset disablesleep` to hold the lid,
  `powermetrics` to read temperature, and `shutdown -h now`.
- On Linux, a polkit rule for `power-off` and the `handle-lid-switch` inhibitor.

The features still stay off until you turn them on in Settings. To check the
macOS grant landed:

```sh
ls -l /etc/sudoers.d/kunai-thermal
sudo -n /usr/bin/pmset -a disablesleep 1 && sudo -n /usr/bin/pmset -a disablesleep 0 && echo "GRANT OK"
```

Power-off is genuinely the last resort. The guard's normal answer is to stop
everything and let the machine cool. A shutdown happens only if you enabled it
and the host is still above a higher ceiling after everything of kunai's has
already been stopped, which means the heat is not its load. A refused power-off
gets logged and survived, never forced. The macOS lid setting is sticky, so kunai
clears it on a clean shutdown and again at the next start, undoing whatever a
crash left behind. By hand that is `sudo pmset -a disablesleep 0`.

## Configuration

Every option takes a flag or an environment variable.

| Flag          | Env                | Default          | What it does                                       |
| ------------- | ------------------ | ---------------- | -------------------------------------------------- |
| `-addr`       | `KUNAI_ADDR`       | `127.0.0.1:8443` | Bind address: the tailnet IP, or loopback for a local install |
| `-tls-cert`   | `KUNAI_TLS_CERT`   |                  | TLS certificate (empty means plain HTTP, which a loopback bind does not need) |
| `-tls-key`    | `KUNAI_TLS_KEY`    |                  | TLS key                                            |
| `-data`       | `KUNAI_DATA`       | `~/.kunai`       | VAPID keys, subscriptions, uploads, registry       |
| `-public-url` | `KUNAI_PUBLIC_URL` |                  | This machine's own tailnet origin                  |
| `-hub-url`    | `KUNAI_HUB_URL`    |                  | Hub origin to forward push to, set this on peers   |
| `-model`      | `KUNAI_MODEL`      |                  | Default model for new sessions                     |
| `-push-email` | `KUNAI_PUSH_EMAIL` |                  | VAPID contact address for Web Push                 |
| `-telegram-token` | `KUNAI_TELEGRAM_TOKEN` |          | Telegram bot token (empty disables the bot)        |
| `-telegram-allow` | `KUNAI_TELEGRAM_ALLOW` |          | Telegram user ids allowed to drive kunai           |
| `-telegram-detail` | `KUNAI_TELEGRAM_DETAIL` | `false` | Send tool inputs and outputs to Telegram          |

Thermal guard flags, all off or inert by default:

| Flag                 | Env                       | Default | What it does                                                |
| -------------------- | ------------------------- | ------- | ----------------------------------------------------------- |
| `-thermal-guard`     | `KUNAI_THERMAL_GUARD`     | `false` | Turn the guard on by default                                |
| `-thermal-soft-c`    | `KUNAI_THERMAL_SOFT_C`    | `90`    | Trip temperature in Celsius on Linux, `0` disables it       |
| `-thermal-max-hours` | `KUNAI_THERMAL_MAX_HOURS` | `0`     | Stop unattended work after this many hours awake, `0` is off |
| `-thermal-hard-c`    | `KUNAI_THERMAL_HARD_C`    | `0`     | Power-off ceiling, used with `-thermal-action=poweroff`     |
| `-thermal-action`    | `KUNAI_THERMAL_ACTION`    | `sleep` | What a hard trip does: `sleep` or `poweroff`                |

The installer also reads `KUNAI_CHANNEL` (`stable` or `nightly`; nightly installs
a parallel `kunai-nightly` service on port `8444` and `~/.kunai-nightly`),
`KUNAI_PORT` (default `8443` stable / `8444` nightly), `KUNAI_HUB_URL`,
`KUNAI_PUSH_EMAIL`, and `KUNAI_THERMAL_PRIVILEGED`.

## Security

Bind to the tailnet IP, never `0.0.0.0`. Tailscale ACLs are the only perimeter
there is.

Anyone who can reach the server can run Claude Code in any directory the server's
user can read. Treat access to that port as access to the machine.

Cross-origin requests are allowed, and that is only safe because the tailnet is
the perimeter and the API uses no cookies or sessions. Do not add cookie auth
without tightening it first.

Web Push is the one hop outside the tailnet, through Apple's and Google's push
services. The payload is a wake-up line kunai wrote about its own state, such as
how long a turn took, and never your conversation.

Certificates from `tailscale cert` expire in about 90 days. `certKeeper`
re-mints them before that and reloads the keypair without restarting.

## Develop

```sh
make build                       # web app plus a local binary
make release                     # cross-compiles linux and darwin, amd64 and arm64, into dist/
make deploy HOST=user@machine    # push a fresh linux build to a host and restart it

go test ./...                    # backend tests
cd web && npm run check          # svelte-check and tsc
cd web && npm run dev            # frontend dev server
```

The web app is embedded with `go:embed`, so a frontend change needs a web build
before the Go build:

```sh
cd web && npm run build && cd ..
go build -o kunai ./cmd/kunai
```

The installed service is ordinary: `systemctl --user status|restart kunai` and
`journalctl --user -u kunai -f` on Linux, `launchctl` and `~/.kunai/kunai.log` on
macOS.

### Repository layout

```
cmd/kunai/          entrypoint: flags, TLS, server wiring
internal/claude/    stream-json driver for the claude CLI, including tool results
internal/session/   session lifecycle, ring buffer, seq replay, permissions, loops
internal/server/    HTTP and WebSocket API, history, stats, uploads, machines, discovery, push, thermal guard
internal/worktree/  git worktrees: create, setup command, status, merge, the agent's brief
internal/checkpoint/ per-turn git snapshots behind undo, and the preview a revert asks with
internal/cliproxy/  in-process Anthropic to Codex/Grok translation for provider sessions
internal/schedule/  runs that fire at a time or after a usage window resets
internal/telegram/  the Telegram channel: long-poll bot, pairing, what may leave the machine
internal/push/      Web Push (VAPID) keys, subscriptions, wake-ups
internal/fsbrowse/  directory listing for the project picker
internal/webui/     embedded production build of the web app
web/                Svelte 5 and Vite PWA source
```

The `claude` stream-json protocol is undocumented. The closest thing to a
reference is the type definitions shipped with `@anthropic-ai/claude-agent-sdk`.
Kunai keeps every protocol type inside `internal/claude` so that a CLI change
stays a one-file fix.

## Contributing

Issues and pull requests are welcome. A few house rules:

- `internal/webui/dist` is committed and embedded, so rebuild the web app **before**
  the Go binary when you change the frontend. Build them the other way round and
  the binary serves the previous UI, which looks exactly like your change not
  working.
- Run `go test ./...`, `cd web && npm run check`, and `node web/tests/units.mjs`
  before opening a PR. The last one needs no test runner: node strips the types off
  a `.ts` import by itself.
- `web/tests/worktree.spec.mjs` drives the real UI against a real kunai and a real
  git repository. It needs a dev server on another port, never the one you rely on:
  `kunai -addr 127.0.0.1:8899 -data /tmp/kunai-wt`.
- No emojis and no em dashes in commit messages or docs.

## License

[MIT](LICENSE).

