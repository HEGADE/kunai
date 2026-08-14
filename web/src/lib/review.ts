// Triage arithmetic for a drafted review.
//
// Everything here is pure and lives outside the component for one reason: the
// numbers a reviewer decides on must be the numbers that get posted, and the way
// that goes wrong is silent. The headline once counted the severity the model
// wrote rather than the one the reader overruled it to, so demoting the only
// blocker to a minor still announced "1 blocker" -- the summary lying about the
// exact thing it exists to summarise. That is a two-line function and a
// three-line test, and it had no business being spread across a 700-line view.

import type { ReviewFinding, Severity } from './api'
// Extension spelled out because the unit suite runs this under plain node, which
// strips the types but does not resolve an extensionless path. Type-only imports
// are erased entirely, which is why the line above does not need one.
import { severityRank } from './severity.ts'

// FindingEdit is a reader's rewrite of one finding. The anchor is deliberately
// absent: file, line and side decide which line of somebody's pull request a
// comment lands on and stay server-side.
export interface FindingEdit {
  title: string
  body: string
  severity: Severity
}

// Edits are held by index rather than by position, because the list reorders as
// severities are overruled and a position would then name a different finding.
export type Edits = Record<number, FindingEdit>

/** The severity a finding will actually be posted at. */
export function effectiveSeverity(f: ReviewFinding, edits: Edits): Severity {
  return edits[f.index]?.severity ?? f.severity
}

/** The finding as it will be posted, with any rewrite folded in. */
export function asPosted(f: ReviewFinding, edits: Edits): ReviewFinding {
  const e = edits[f.index]
  return e ? { ...f, title: e.title, body: e.body, severity: e.severity } : f
}

// Ordered worst first, then filtered.
//
// Sorted on the client as well as the server, which is not redundant: an
// overruled severity has to move the card immediately, and the server does not
// hear about it until Post.
export function ordered(findings: ReviewFinding[], edits: Edits, filter: Severity | 'all'): ReviewFinding[] {
  return [...findings]
    .sort((a, b) => severityRank(effectiveSeverity(a, edits)) - severityRank(effectiveSeverity(b, edits)))
    .filter((f) => filter === 'all' || effectiveSeverity(f, edits) === filter)
}

// Decision is the state of the triage: what is going, what is not, and how much
// is still unread. Every count is taken at the EDITED severity.
export interface Decision {
  keep: number
  drop: number
  total: number
  blockers: number
  inline: number
  summary: number
  counts: Record<Severity, number>
}

export function decide(findings: ReviewFinding[], dropped: Set<number>, edits: Edits): Decision {
  const counts: Record<Severity, number> = { blocker: 0, major: 0, minor: 0 }
  let keep = 0
  let inline = 0
  for (const f of findings) {
    if (dropped.has(f.index)) continue
    keep++
    if (f.inline) inline++
    const s = effectiveSeverity(f, edits)
    if (s in counts) counts[s]++
    else counts.minor++
  }
  return {
    keep,
    drop: findings.length - keep,
    total: findings.length,
    blockers: counts.blocker,
    inline,
    summary: keep - inline,
    counts,
  }
}

// What the button promises.
//
// Dropping every finding is a decision rather than a dead end: "I looked, there
// is nothing worth flagging" is a review worth sending, so the label changes
// what it says instead of the control going grey.
export function postLabel(d: Decision, posting: boolean): string {
  if (posting) return 'Posting'
  if (!d.keep) return 'Post summary'
  return `Post ${d.keep} finding${d.keep === 1 ? '' : 's'}`
}

// Whether a finding has been looked at, which is what the progress count is
// about. Dropping it, keeping it explicitly, or rewriting it all count; a
// finding nobody has touched does not.
export function decidedCount(findings: ReviewFinding[], seen: Set<number>): number {
  return findings.filter((f) => seen.has(f.index)).length
}
