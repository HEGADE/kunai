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

**Contents** ·
[Quick start](#quick-start) ·
[What you get](#what-you-get) ·
[How it works](#how-it-works) ·
[Claude accounts](#claude-accounts) ·
[Telegram bot](#telegram-bot-optional) ·
[Working while you are away](#working-while-you-are-away) ·
[Configuration](#configuration) ·
[Security](#security) ·
[Develop](#develop)

## Quick start

### What you need

- A machine on your Tailscale tailnet, running Linux or macOS.
- [Claude Code](https://claude.com/claude-code) installed and signed in, with
  `claude` on your `PATH`.
- MagicDNS and HTTPS certificates turned on in the Tailscale admin console, under
  DNS then HTTPS Certificates.

You do not need a toolchain. The one-liner pulls a prebuilt binary, and the web
app ships already built inside it, so Node is never involved. If you install from
a source checkout instead, the script fetches a local Go toolchain under
`~/.kunai` when `go` is missing, without touching root.

### 1. Install on the machine you use most

```sh
curl -fsSL https://raw.githubusercontent.com/HEGADE/kunai/main/install.sh | bash
```

<sub>From a source checkout instead, which builds it: `git clone https://github.com/HEGADE/kunai && cd kunai && ./install.sh`</sub>

The installer downloads the binary, works out your tailnet address and MagicDNS
name, mints a TLS certificate with `tailscale cert`, installs a service (a
systemd user unit on Linux, a launchd agent on macOS), health-checks it, and
prints the URL to open.

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
keyboard, bounded so they cannot run forever, with a thermal guard for a laptop
you left running. See [below](#working-while-you-are-away).

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

## Telegram bot (optional)

Kunai can also be driven from a Telegram chat, which needs no Tailscale on the
device you are holding and no app install. The bot long-polls Telegram outbound,
so kunai still exposes nothing and still needs no inbound hole.

```sh
kunai -telegram-token 123456:ABC... -telegram-allow 11111111
```

Both are also environment variables (`KUNAI_TELEGRAM_TOKEN`,
`KUNAI_TELEGRAM_ALLOW`). The allow list is Telegram user ids, comma separated,
and it has no default: with none set the bot refuses everyone and says so at
startup. Treat it as seriously as SSH, because a chat with this bot can run
commands on this machine.

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

**What Telegram is allowed to see.** Telegram is a third party, so by default it
carries the conversation and the controls and nothing else. A tool call is
announced by name and file path ("Edit internal/server/usage.go"), never by its
contents, and tool output is not sent at all. That keeps the accidental spills
out of a chat log you do not control: the config file an agent reads, the token a
test echoes, an env dump in a debug command. Open the app to see any of it in
full. `-telegram-detail` turns that off and sends tool inputs and outputs too,
which is occasionally convenient and worth a moment's thought first.

## Working while you are away

A loop re-feeds one task every time a turn ends, which is Ralph's technique, so
an agent keeps working with nobody watching. A session your phone walked away
from mid-turn also just keeps going. Loops stop at whichever of their iteration
or spend limits arrives first, or when the model reports the task done, so they
cannot run forever.

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
| `-addr`       | `KUNAI_ADDR`       | `127.0.0.1:8443` | Bind address, which should be the tailnet IP       |
| `-tls-cert`   | `KUNAI_TLS_CERT`   |                  | TLS certificate (empty means plain HTTP, dev only) |
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

The installer also reads `KUNAI_PORT` (default `8443`), `KUNAI_HUB_URL`,
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

- `internal/webui/dist` is committed and embedded, so rebuild the web app before
  the Go binary when you change the frontend.
- Run `go test ./...` and `cd web && npm run check` before opening a PR.
- No emojis and no em dashes in commit messages or docs.

## License

[MIT](LICENSE).
