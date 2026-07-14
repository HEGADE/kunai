# Scheduler: run a prompt at a time, or right after the quota resets

## Problem

A Claude subscription's usage limit resets on a schedule (a rolling 5-hour
window, plus a 7-day weekly window). A user wants to queue a prompt the night
before and have it fire ~1 minute after the window resets, so long/overnight work
starts the instant fresh quota is available instead of waiting for the user to be
awake. More generally: schedule a prompt to run at any time, one-shot or
recurring, into a new session or an existing (resumed) one.

## Feasibility (verified)

The `claude` CLI emits a dedicated stream-json message every turn, so Kunai reads
the reset time live — no guessing, no HTTP headers, no probing:

```json
{ "type": "rate_limit_event",
  "rate_limit_info": {
    "status": "allowed",
    "resetsAt": 1784021400,          // Unix epoch seconds: when the window resets
    "rateLimitType": "five_hour" } } // also "seven_day"
```

`resetsAt` is the current window's reset instant. It advances as the account is
used and is account-global (reported on any session's turns), so the freshest
value from any active session is authoritative.

## Scope

In scope: an in-process, per-machine job scheduler with a dashboard UI. Triggers:
absolute time, or relative to the detected rate-limit reset (`window + offset`).
Targets: a new session (cwd/model/effort/mode) or a resumed existing session,
plus a prompt. Per-job re-arm (one-shot vs recurring). Catch-up on misfire.

Out of scope (future): complex cron expressions, chained/dependent jobs,
conditional triggers, editing a job's history.

## Architecture

Cross-platform for free: the scheduler is pure Go inside the always-running
server. No OS cron / launchd / Task Scheduler — one clean code path on macOS,
Linux, and Windows. The one real dependency is that the machine is awake and
Kunai is running at fire time, which is what the keep-awake toggle addresses (and
a headless Linux box is always up).

### `internal/schedule` package

- `Job` (below) is the unit of work.
- `Scheduler` owns the jobs, persists them to `schedule.json` in the data dir,
  and runs one goroutine driven by a timer set to the soonest `nextFire`. It
  exposes `List/Create/Update/Delete/SetEnabled` and a `Fire` callback the server
  wires to the session manager. A `Clock` interface (real vs. injected) makes the
  time math unit-testable.
- Reset state: the scheduler holds the latest `resetsAt` per window, updated via
  `NoteReset(window, resetsAt)` which the server calls when the driver surfaces a
  `rate_limit_event`. `"reset"`-triggered jobs compute `nextFire` from it.

Ownership: a job lives entirely on the machine it was created on — its
`schedule.json`, its goroutine, and the session it starts all run there. Other
machines never run it; the dashboard only *displays* it via fan-out.

### Job model

```go
type Job struct {
    ID        string
    Name      string
    Enabled   bool
    Trigger   Trigger
    Rearm     bool      // re-schedule after firing (one-shot vs recurring)
    Target    Target
    Prompt    string
    NextFire  time.Time // computed
    LastRun   time.Time
    LastStatus string   // "ok" | "error: ..." | "skipped"
}

type Trigger struct {
    Kind      string    // "at" | "reset"
    At        time.Time // Kind=="at"
    Window    string    // Kind=="reset": "five_hour" | "seven_day"
    OffsetSec int       // Kind=="reset": seconds after resetsAt (e.g. 60)
    // For a recurring "at" job, Rearm advances At by 24h (daily) after firing.
}

type Target struct {
    Kind      string // "new" | "resume"
    Cwd       string // Kind=="new"
    Model     string
    Effort    string
    Mode      string // permission mode for the run (default "auto")
    SessionID string // Kind=="resume"
}
```

### Firing

At `nextFire` the scheduler invokes the server's fire callback, which:
1. `new`: `Manager.Create` with the cwd/model/effort/mode, then sends the prompt.
   `resume`: create with `Resume: sessionID` (seeded from transcript), then sends.
2. Records `LastRun` + `LastStatus`.
3. Re-arm: if `Rearm`, recompute `nextFire` — for `"reset"` from the newest
   `resetsAt` (the job's own turn refreshes it), for a daily `"at"` add 24h — and
   persist. Otherwise disable the job (kept in the list as a spent one-shot).

Autonomous permission mode: an unattended 2 AM run must not block on an approval
prompt, so `Target.Mode` defaults to **auto** (or accept-edits) rather than the
interactive "ask". The user can change it per job.

### Misfire / catch-up

On startup and on the timer, any job whose `nextFire` is already in the past is
run once (catch-up) rather than skipped — "after reset" means the quota is fresh,
so late is still useful — then re-armed (or disabled). Catch-up is bounded: a job
overdue by more than one window length is marked `skipped` and re-armed to the
next occurrence, so a machine that was off for days doesn't dump a backlog.

### REST API

- `GET /api/schedule` — this machine's jobs (with computed `nextFire`).
- `POST /api/schedule` — create; returns the job.
- `PATCH /api/schedule/{id}` — update / enable-disable.
- `DELETE /api/schedule/{id}`.

Each machine serves its own jobs; the client calls a specific machine's origin
(like the rest of the multi-machine REST). Current per-window `resetsAt` is added
to `/api/stats` so the UI can show "next reset in 2h 14m" and preview fire times.

### Client / UI (dashboard, not settings)

A **Schedules** card on Home, below the stats/quick-start:
- One row per job across all machines (fan-out, tagged with `machineId` like
  sessions/history): a plain-English summary ("*in 3h 12m · 1 min after 5-hour
  reset → resume 'whisper-ui-redesign' on mac*"), a next-fire countdown, an
  enable toggle, last-run status, edit/delete.
- **+ New schedule** opens a compact form: machine → target (new: dir picker +
  model/effort/mode; or resume: pick a recent session) → prompt → trigger (at a
  time, or after-reset + offset minutes) → re-arm toggle.

`web/src/lib/api.ts` gains schedule CRUD (taking a `base` origin); the app store
fans out `loadSchedules()` and tags each job with its machine. Wire types
(`Job`, `Trigger`, `Target`) mirror the Go structs in `web/src/lib/types.ts`.

## Data flow

Create in the UI → `POST /api/schedule` on the chosen machine → persisted, timer
re-armed → at fire time the machine starts/resumes the session locally and sends
the prompt → the run's `rate_limit_event` refreshes `resetsAt` → recurring jobs
re-arm from it. The dashboard poll reflects `nextFire`/`lastStatus`.

## Error handling

- Unknown `resetsAt` (no session has run since boot): a `"reset"` job shows
  "waiting for first quota reading" and holds until the first `rate_limit_event`.
- Create/resume failure at fire time → `LastStatus = "error: ..."`, surfaced in
  the card; recurring jobs still re-arm.
- Deleting the target session of a `resume` job → fire falls back to reporting the
  error (kept simple; no auto-convert to new).

## Testing

- `internal/schedule`: pure time-math with an injected `Clock` — next-fire from
  each trigger, re-arm advancement, catch-up vs. skip thresholds, enable/disable,
  JSON round-trip persistence.
- `internal/claude`: a fixture test that a `rate_limit_event` line parses to
  `{window, resetsAt}`.
- `internal/server`: `POST/GET/DELETE /api/schedule` persist and reflect;
  `resetsAt` appears in `/api/stats`.
- Live integration: create a job with a `nextFire` a few seconds out and confirm
  the session manager starts a session and sends the prompt (Haiku, short).

## Notes

- Shared quota: scheduling the *same* job on multiple machines would make them
  compete for the one account's fresh window — the UI notes this; normally a job
  lives on one machine.
- Security: scheduled runs execute autonomously with the chosen permission mode;
  that is the user's explicit choice, consistent with the tailnet-is-the-perimeter
  model, but worth stating.
