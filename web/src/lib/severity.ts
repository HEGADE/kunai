// Severity, as the review shows it.
//
// Mirrors internal/review/severity.go. The two must change together, and the
// server is authoritative: it normalises whatever the model wrote onto these
// three values, so nothing here has to cope with a fourth.
//
// Colour is spent here, which is a deliberate exception to this app's rule that
// hue is reserved for status dots, the permission gate, code highlighting, and
// the marks that stand for somebody else's product. Severity IS a status, so it
// reuses the tokens that already mean it: --alert for the thing that stops a
// merge, --busy for the thing that needs attention, and the gray ramp for the
// thing that does not. No new hue is introduced.
//
// And colour is never the only channel. Every severity carries its name as text
// beside the mark, for the same reason the usage chart's segments do: a reader
// who cannot separate red from amber must still be able to read the review.

import type { Severity } from './api'

export const SEVERITIES: Severity[] = ['blocker', 'major', 'minor']

// severityRank orders findings most serious first. Unknown values sort last: a
// severity nobody recognises must not be able to push itself to the top of a
// review. Mirrors Severity.Rank in internal/review/severity.go.
export function severityRank(s: Severity | string): number {
  switch (s) {
    case 'blocker':
      return 0
    case 'major':
      return 1
    case 'minor':
      return 2
    default:
      return 3
  }
}

export function severityLabel(s: Severity | string): string {
  switch (s) {
    case 'blocker':
      return 'Blocker'
    case 'major':
      return 'Major'
    case 'minor':
      return 'Minor'
    default:
      return String(s)
  }
}

// What each severity means, in the words a person would use. Shown in the edit
// control, because someone overruling the reviewer's judgement needs to know
// what they are choosing between.
export function severityHint(s: Severity | string): string {
  switch (s) {
    case 'blocker':
      return 'Do not merge: it breaks, loses data, or opens a hole'
    case 'major':
      return 'A real bug or risk that should be fixed'
    default:
      return 'Worth knowing, not worth blocking on'
  }
}

// A one-line summary of what was found, which is the review's headline. Written
// out rather than shown as three numbers, because "2 blockers, 5 minor" is read
// at a glance and "2 / 0 / 5" is a puzzle.
export function verdictLine(counts: Record<Severity, number>): string {
  const parts: string[] = []
  if (counts.blocker) parts.push(`${counts.blocker} blocker${counts.blocker > 1 ? 's' : ''}`)
  if (counts.major) parts.push(`${counts.major} major`)
  if (counts.minor) parts.push(`${counts.minor} minor`)
  if (!parts.length) return 'Nothing worth reporting'
  return parts.join(', ')
}

// tally counts findings by severity.
export function tally<T extends { severity: Severity }>(items: T[]): Record<Severity, number> {
  const out: Record<Severity, number> = { blocker: 0, major: 0, minor: 0 }
  for (const f of items) {
    if (f.severity in out) out[f.severity]++
    else out.minor++
  }
  return out
}
