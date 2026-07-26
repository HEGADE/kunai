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

// isAwaiting is the permission gate: the agent has stopped and cannot continue
// until you answer. This is the only state worth interrupting a glance for.
export function isAwaiting(s: Stateful): boolean {
  return s.state === 'awaiting_permission'
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
