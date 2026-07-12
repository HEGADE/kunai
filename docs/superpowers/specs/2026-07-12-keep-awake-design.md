# Keep-awake while locked / idle (opt-in, cross-platform)

## Problem

When a machine running Kunai goes to sleep, the `claude` sessions pause and its
Tailscale link drops, so the phone can no longer reach it. A user who locks a
laptop (or leaves it idle with the lid open) wants its sessions to stay alive and
reachable.

## Scope

In scope: prevent **idle system sleep** while the machine is locked or idle, as an
opt-in, per-machine setting, on macOS and Windows (and best-effort Linux). This
needs **no administrator rights**.

Explicitly out of scope (deferred): keeping a MacBook awake with the **lid
closed**. macOS force-sleeps on lid close and only `root` can disable that
(`pmset disablesleep`), which requires a privileged helper and carries thermal
risk in a bag. Not built here.

## Guiding constraint and the safety property

The chosen mechanism is an **in-process power assertion**: Kunai holds the
assertion only while the toggle is on, and the assertion is released the moment it
is toggled off **or the Kunai process stops**. Nothing global or sticky is written
to the OS, so a crash can never leave a machine stuck awake. This is the primary
reason to prefer in-process assertions over flipping global power config
(`pmset` / `powercfg`), which is sticky, can require admin, and leaks on crash.

Kunai is a single Go binary built with `CGO_ENABLED=0`, so every mechanism below
is reachable from pure Go (a child process, or a `syscall` DLL call) with no cgo.

## Architecture

### `internal/awake` package

A small interface with platform-split implementations, each independently
testable:

```go
// Keeper prevents the host from idle-sleeping while enabled. Set(true) acquires
// the hold; Set(false) or process exit releases it. Safe to call repeatedly.
type Keeper interface {
    Set(on bool) error
    Enabled() bool
    Supported() bool // false where the platform can't (client hides the toggle)
}
```

- `awake_darwin.go` — holds a child `caffeinate -i -w <pid>` process (asserts
  `PreventUserIdleSystemSleep`, i.e. blocks idle system sleep on both AC and
  battery; lid close is intentionally still allowed to sleep). `Set(true)` starts
  it if not running; `Set(false)` kills it. `-w <pid>` makes caffeinate exit when
  kunai exits, so the hold self-releases even if kunai is killed without running
  cleanup (a Unix child does NOT die with its parent by default). `caffeinate` is
  a built-in; `Supported()` is true.
- `awake_windows.go` — a dedicated goroutine, pinned with `runtime.LockOSThread`,
  calls `SetThreadExecutionState(ES_CONTINUOUS | ES_SYSTEM_REQUIRED)` via
  `kernel32.dll` (through `syscall.NewLazyDLL`) and stays alive while enabled,
  re-asserting every 30s as belt-and-suspenders (the state is thread-scoped, so
  pinning the thread is what keeps it valid). `Set(false)` stops the goroutine and
  calls `SetThreadExecutionState(ES_CONTINUOUS)` to clear it. `Supported()` true.
- `awake_linux.go` — best-effort: `Set(true)` starts a held `systemd-inhibit
  --what=sleep --why="kunai keep-awake" sleep infinity` child (killed on
  `Set(false)`, and `Pdeathsig=SIGKILL` so it dies with kunai on an uncaught
  kill). `Supported()` is true only if `systemd-inhibit` is on PATH; otherwise it
  is a no-op returning `Supported() == false`.

Windows note: the server did not previously compile for Windows (`stats.go` used
unix-only `syscall.Statfs`, and `hostUptimeLoad`/`memInfo` were darwin/linux
only). To make keep-awake real on Windows, host stats were platform-split:
`stats_unix.go` (darwin/linux `diskInfo`) and `stats_windows.go` (kernel32
`GetDiskFreeSpaceExW` / `GlobalMemoryStatusEx` / `GetTickCount64`, pure syscall,
no cgo). The binary now cross-compiles for windows/amd64 and windows/arm64. A
Windows installer/service is out of scope here (run the binary manually for now).
- `awake_other.go` (build tag fallback) — no-op, `Supported() == false`.

The Keeper is created once in `cmd/kunai` wiring and injected into the server.
Concurrency: implementations guard their state with a mutex; `Set` is idempotent.

### Persistence

The on/off choice is per-machine, stored in the data dir as `awake.json`
(`{"enabled": true}`), written on every toggle. On boot, `cmd/kunai` reads it and
calls `Keeper.Set(enabled)` so the preference survives restarts and the one-click
update. If the file is absent, the default is off.

### REST API

- `POST /api/awake` with body `{"enabled": bool}` → applies via `Keeper.Set`,
  persists, returns `{"enabled", "supported"}`. On an unsupported platform it
  returns `supported:false` and does not error.
- `/api/stats` gains two fields: `keep_awake bool` and `keep_awake_supported
  bool`, so the fan-out already carries current state to the dashboard and the
  client can hide the toggle where it is a no-op.

Types stay in sync manually per the project's existing contract note
(`Stats` mirrors `web/src/lib/types.ts`).

### Client UI

A per-machine **"Keep awake while locked"** toggle in **Settings → Machines**
(each machine controls its own; the toggle calls that machine's origin, like the
rest of the multi-machine REST). Shown only when `keep_awake_supported` is true
for that machine. Sub-label:

> Prevents idle sleep so sessions stay reachable. The lid must stay open; this
> drains battery, so keep the machine on power.

`web/src/lib/api.ts` gains `setKeepAwake(base, enabled)`; the app store's stats
already surface `keep_awake` per machine.

## Data flow

Toggle in Settings → `POST /api/awake` to that machine → `Keeper.Set` acquires or
releases the OS assertion + writes `awake.json` → next `/api/stats` poll reflects
`keep_awake`, and the toggle stays in sync across devices via the existing fan-out.

## Error handling

- A failed `Set` (e.g. `caffeinate` missing) returns an error surfaced to the
  client as a toast; the persisted value is not changed on failure.
- Unsupported platform: `POST /api/awake` returns `supported:false`, toggle hidden.
- Release is best-effort and also runs on server shutdown (defer in `Run`) as a
  belt-and-suspenders alongside the process-exit guarantee.

## Testing

- `internal/awake`: a cross-platform test that `Set(true)` then `Set(false)`
  flips `Enabled()` and is idempotent (double `Set(true)` holds one child, not
  two). On darwin/linux, assert the child process is running while enabled and
  gone after release. Windows `SetThreadExecutionState` is verified by a smoke
  test that the proc call returns non-zero (guarded by `runtime.GOOS`).
- `internal/server`: `POST /api/awake` persists to `awake.json` and reflects in a
  subsequent `/api/stats`; unsupported path returns `supported:false`.
- Manual: on the Mac, enable the toggle, confirm `pmset -g assertions` lists a
  `PreventUserIdleSystemSleep` from `caffeinate`; lock the screen and confirm a
  session stays reachable from the phone; toggle off and confirm the assertion is
  gone.

## Out of scope / future

- Lid-closed (root `pmset disablesleep` via a privileged helper) — deferred.
- AC-power gating / auto-release on idle timeout — not needed for the lid-open
  tier; can be added later if battery drain proves annoying.
