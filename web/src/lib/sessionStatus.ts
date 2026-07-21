// The one place a session's state becomes something a person reads.
//
// The vocabulary was written twice before this, once in the tab strip and once
// as the sidebar's presence dot, and the two had already drifted: the strip knew
// about "offline" and the sidebar did not. Both now ask this module, so a new
// state is added in one file and appears everywhere it should.

import type { SessionState } from './types'

// StatusKind is the vocabulary. It is deliberately not SessionState: the wire
// states say what the CLI is doing, while these say what it means for you, and
// two of them (offline, error) have no wire state at all.
export type StatusKind = 'needs' | 'running' | 'error' | 'offline' | 'done'

export interface SessionStatus {
  kind: StatusKind
  label: string
}

// StatusInput is what a row knows. `online` and `errored` come from a live
// connection and are undefined when nothing is attached, which is the ordinary
// case for a sidebar row that is not also an open tab: then the session's own
// state is all there is, and it is enough.
export interface StatusInput {
  state: SessionState
  online?: boolean
  errored?: boolean
}

// Labels are short because they share a 288px row with a session name, and the
// name is what you are actually scanning for. "Asking" rather than "Needs you"
// for the same reason, and because it is the honest word: the session is asking
// you something, which you may well answer with a no.
const LABELS: Record<StatusKind, string> = {
  needs: 'Asking',
  running: 'Running',
  error: 'Error',
  offline: 'Offline',
  done: 'Done',
}

export function sessionStatus(input: StatusInput): SessionStatus {
  const kind = kindOf(input)
  return { kind, label: LABELS[kind] }
}

// turnStatus is what belongs at the end of a turn, or null for nothing at all.
//
// A footer is an artifact of one turn, while a session's state is about right
// now, and conflating them put "Running" under a reply that had already been
// written. Two rules follow. "Running" is never shown here: the streaming
// indicator below the log already says it, and on a finished-looking reply it
// reads as a lie. And "Done" is claimed only once the turn really ended, which
// is exactly when its duration and cost arrive, so the badge can never promise
// more than the numbers beside it.
//
// The rest are worth saying whether or not the turn ended, because they are the
// reason it has not: a session parked on a question, a failed turn, a dropped
// socket.
export function turnStatus(session: SessionStatus, ended: boolean): SessionStatus | null {
  if (session.kind === 'running') return null
  if (session.kind === 'done') return ended ? session : null
  return session
}

// kindOf resolves the one thing worth saying about a session, in the order you
// would want to hear it.
//
// Offline comes first because it is the only answer that admits we cannot see:
// reporting a stale "Running" from a dropped socket is worse than saying so.
// After that the order is urgency, and "needs you" outranks an error because a
// session parked on a permission ask is still recoverable by one tap.
function kindOf({ state, online, errored }: StatusInput): StatusKind {
  if (online === false) return 'offline'
  if (state === 'awaiting_permission') return 'needs'
  if (errored) return 'error'
  if (state === 'running' || state === 'starting') return 'running'
  return 'done'
}
