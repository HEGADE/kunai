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

const LABELS: Record<StatusKind, string> = {
  needs: 'Needs you',
  running: 'Running',
  error: 'Error',
  offline: 'Offline',
  done: 'Done',
}

export function sessionStatus(input: StatusInput): SessionStatus {
  const kind = kindOf(input)
  return { kind, label: LABELS[kind] }
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
