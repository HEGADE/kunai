// What a folder in the sidebar says about the work going on inside it.
//
// The sidebar groups sessions by the codebase they belong to, and a folder is
// where its work lives whatever state that work is in. But with several repos
// open you then have to read every row to find the agent that stopped to ask you
// something, which is the one thing in the whole list that is actually waiting on
// you. So the folder reports it: the heading carries a summary, and a folder
// holding a question opens itself.
//
// The alternative was to lift the busy sessions into a section of their own at
// the top. That is the shape this deliberately does NOT have: it empties the
// folder you were looking at and puts the same session in two places.
//
// One rule decides everything here, and it comes from a badge that was built on
// this data once and reverted for lying: only `running` and `awaiting_permission`
// may be counted. A resumed session reports `starting` until its first prompt
// ever arrives, so a summary that trusted `starting` would announce work on a
// session that is sitting there doing nothing, forever. Silence is the honest
// answer for every other state.

// Stateful is the shape a live session presents here. Past sessions have no
// state at all, which is why it is optional: they simply never count.
export interface Stateful {
  state?: string
}

export function isWorking(s: Stateful): boolean {
  return s.state === 'running'
}

// workedFor is how long the running turn has been going, for the row that says
// so. The point is the difference between busy and stuck: "working" alone is the
// same word at twenty seconds and twenty minutes, and only one of those is worth
// getting up for.
//
// Coarse on purpose. A ticking seconds count past the first minute is motion in
// the corner of your eye for a number nobody reads that precisely, so it settles
// to whole minutes and then to hours. Returns '' when there is no turn to
// measure, which is what a resumed session honestly has: the timestamp comes
// from the turn, never from when the tab was opened.
export function workedFor(startedAtMs: number, nowMs: number): string {
  if (!startedAtMs || nowMs < startedAtMs) return ''
  const secs = Math.floor((nowMs - startedAtMs) / 1000)
  if (secs < 60) return `${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m`
  const hours = Math.floor(mins / 60)
  const rem = mins % 60
  return rem ? `${hours}h ${rem}m` : `${hours}h`
}

// isAwaiting is the permission gate: the agent has stopped and cannot continue
// until you answer. This is the only state worth interrupting a glance for.
export function isAwaiting(s: Stateful): boolean {
  return s.state === 'awaiting_permission'
}

// isUnreadDone reports "the agent finished while you were away": the session is
// idle, its last turn ended after this device last looked at it, and this
// device HAS looked at it (a session never visited here counts as read, so a
// fresh install does not light the whole list up as just-finished).
//
// This is the row the attention model makes loud. The inversion comes from
// t3code's sidebar and it is the part worth copying exactly: a WORKING session
// is not your problem yet -- there is nothing to do until it stops -- so it
// recedes, and the prominence is saved for the one that stopped with results
// you have not seen. Most agent UIs make the running row the loudest, which
// optimises for watching agents instead of for acting on them.
export function isUnreadDone(
  s: Stateful & { turn_ended_at?: number },
  visitedAt: number,
): boolean {
  if (isWorking(s) || isAwaiting(s)) return false
  return !!s.turn_ended_at && visitedAt > 0 && s.turn_ended_at > visitedAt
}

// recedes reports a row with nothing for you in it right now: working (nothing
// to do until it stops) with nothing unread. The active row and one waiting on
// you are never receded, and idle rows keep their ordinary weight -- receding
// is for motion, not for rest.
export function recedes(
  s: Stateful & { turn_ended_at?: number },
  visitedAt: number,
  isActive: boolean,
): boolean {
  return isWorking(s) && !isActive && !isUnreadDone(s, visitedAt)
}

// summarise is the line beside a folder's name, or '' when there is nothing
// truthful to say. "Needs you" comes last because it is what the eye should stop
// on, and it is never merged into the working count: an agent waiting on you is
// not working, it is stopped.
export function summarise(items: Stateful[]): string {
  let working = 0
  let waiting = 0
  for (const s of items) {
    if (isAwaiting(s)) waiting++
    else if (isWorking(s)) working++
  }
  const parts: string[] = []
  if (working) parts.push(`${working} working`)
  if (waiting) parts.push(`${waiting} needs you`)
  return parts.join(' · ')
}

// needsAttention reports whether a folder holds a question. A folder that does
// is force-opened however it was left, because a collapsed folder hiding an
// agent waiting on a click you never saw is worse than one you have to close
// again.
export function needsAttention(items: Stateful[]): boolean {
  return items.some(isAwaiting)
}

// hasWork reports any live turn in the folder, for the presence mark on a
// collapsed heading.
export function hasWork(items: Stateful[]): boolean {
  return items.some(isWorking)
}
