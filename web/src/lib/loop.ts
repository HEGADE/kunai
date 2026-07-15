// A loop ends at whichever limit it reaches first, so the only progress worth
// drawing is progress toward the nearer one. These helpers work that out.
//
// The forecast exists because a budget you only find out about afterwards is not
// a safeguard. Once a loop has a couple of turns behind it, its burn rate says
// which limit will actually stop it, and roughly when. That turns the budget from
// fine print into the thing you can read at a glance before going to sleep.

import type { LoopStatus } from './types'

export type Binding = 'iterations' | 'spend'

export interface LoopProgress {
  // How far along the loop is toward ending, 0..1: the nearer of the two limits.
  frac: number
  // Which limit is going to end it.
  binding: Binding
  // Where spend is projected to run out, in iterations. 0 when not yet knowable.
  spendEndsAt: number
  // One line naming the limit that will end this, or '' when it is too early to
  // say honestly. Never guess from a single turn.
  note: string
}

const clamp01 = (n: number): number => (n < 0 ? 0 : n > 1 ? 1 : n)

export function loopProgress(l: LoopStatus): LoopProgress {
  const itersFrac = l.max_iters > 0 ? clamp01(l.iteration / l.max_iters) : 0
  const spendFrac = l.max_usd > 0 ? clamp01(l.spent_usd / l.max_usd) : 0

  // Two turns is the least that can imply a rate. One turn is an anecdote, and a
  // wrong forecast is worse than none when it is the thing you trusted to sleep.
  const rate = l.iteration >= 2 && l.spent_usd > 0 ? l.spent_usd / l.iteration : 0
  const spendEndsAt = rate > 0 && l.max_usd > 0 ? Math.floor(l.max_usd / rate) : 0

  const spendBinds = spendFrac > itersFrac
  const binding: Binding = spendBinds ? 'spend' : 'iterations'

  let note = ''
  if (l.state === 'running') {
    if (spendEndsAt > 0 && spendEndsAt < l.max_iters) {
      note = `At this rate the budget stops this near #${spendEndsAt}.`
    } else if (rate > 0) {
      note = `The budget holds. This runs all ${l.max_iters} iterations.`
    }
  }

  return { frac: Math.max(itersFrac, spendFrac), binding, spendEndsAt, note }
}

// usd renders money the way the rest of the chrome renders data: exact, mono, and
// never rounded to something prettier than the truth.
export function usd(n: number): string {
  if (n > 0 && n < 0.01) return '<$0.01'
  return '$' + n.toFixed(2)
}

// Why a loop ended, said plainly, in the interface's voice rather than a person's.
export function loopEnding(l: LoopStatus): string {
  switch (l.state) {
    case 'done':
      return 'Finished'
    case 'exhausted':
      return 'Reached a limit'
    case 'failed':
      return 'Stopped on an error'
    case 'stopped':
      return 'Stopped'
    default:
      return 'Running'
  }
}
