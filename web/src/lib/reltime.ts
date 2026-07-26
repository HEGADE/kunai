// How long ago something happened, in as few characters as possible.
//
// Two callers with two budgets. The all-sessions view has a whole row to spend,
// so it says "3d ago"; a sidebar row has the space left over after the title, so
// it says "3d". Same thresholds either way, because the same session appearing as
// "3d" in one list and "2d" in the other is the kind of small lie that makes
// people stop trusting both.
//
// Pure and free of Svelte so the boundaries can be exercised directly, which
// matters: every one of them is an off-by-one waiting to happen.

const MINUTE = 60
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR
// Calendar months and years vary; these are the approximations a glance wants,
// not a date library. Past a month nobody is counting.
const MONTH = 30 * DAY
const YEAR = 12 * MONTH

// secondsSince is split out so tests can supply "now" instead of racing the
// clock, and returns 0 rather than a negative for a timestamp in the future
// (clock skew between machines on the tailnet is real).
export function secondsSince(iso: string, now = Date.now()): number {
  const t = new Date(iso).getTime()
  if (!t || Number.isNaN(t)) return -1
  return Math.max(0, Math.floor((now - t) / 1000))
}

// shortAgo is the sidebar's voice: "now", "9m", "3h", "5d", "2mo", "1y". No
// "ago", because in a column of times the column itself says it.
export function shortAgo(iso: string, now = Date.now()): string {
  const s = secondsSince(iso, now)
  if (s < 0) return ''
  if (s < MINUTE) return 'now'
  if (s < HOUR) return `${Math.floor(s / MINUTE)}m`
  if (s < DAY) return `${Math.floor(s / HOUR)}h`
  if (s < MONTH) return `${Math.floor(s / DAY)}d`
  if (s < YEAR) return `${Math.floor(s / MONTH)}mo`
  return `${Math.floor(s / YEAR)}y`
}

// longAgo is the list voice: "just now", "9m ago", "3d ago".
export function longAgo(iso: string, now = Date.now()): string {
  const s = secondsSince(iso, now)
  if (s < 0) return ''
  if (s < MINUTE) return 'just now'
  return `${shortAgo(iso, now)} ago`
}
