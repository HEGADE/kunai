// Working through a review: what is active, what has been decided, what will be
// sent.
//
// Pure, and out of the component on purpose. The numbers here decide what lands
// publicly on somebody's pull request under a shared bot identity, and that is
// not a thing to leave spread across a template where the only way to check it
// is to click.
//
// The vocabulary is ACCEPT and DISMISS rather than keep and drop, and the change
// is not cosmetic. "Keep" describes what the app does with a row; "accept"
// describes what the reader thinks of the claim, which is the actual decision
// being made. It also gives the third state a name: a finding nobody has looked
// at yet is UNDECIDED, and a screen that cannot tell that from "kept" cannot
// tell you how far through you are.

import type { ReviewFinding, Severity } from './api'
// Extension spelled out so the unit suite can run this under plain node; see
// lib/review.ts for the same note.
import { severityRank } from './severity.ts'

export type Verdict = 'accept' | 'dismiss'

/** Decisions so far, by finding index. */
export type Verdicts = Record<number, Verdict | undefined>

export interface FindingEdit {
  title: string
  body: string
  severity: Severity
}
export type Edits = Record<number, FindingEdit>

/** The severity a finding will be posted at, after any overrule. */
export function effectiveSeverity(f: ReviewFinding, edits: Edits): Severity {
  return edits[f.index]?.severity ?? f.severity
}

/**
 * Findings worst first.
 *
 * Sorted on the client as well as the server, which is not redundant: an
 * overruled severity has to move the row immediately, and the server does not
 * hear about that until the review is posted.
 */
export function ordered(findings: ReviewFinding[], edits: Edits): ReviewFinding[] {
  return [...findings].sort(
    (a, b) => severityRank(effectiveSeverity(a, edits)) - severityRank(effectiveSeverity(b, edits)),
  )
}

/** What will be sent, and how far through the reader is. */
export interface Tally {
  total: number
  accepted: number
  dismissed: number
  /** Decided either way. What "n of 3 resolved" counts. */
  resolved: number
  /** Never looked at. These are POSTED: silence is not a dismissal. */
  undecided: number
  /** Accepted plus undecided: everything that goes to GitHub. */
  sending: number
  blockers: number
  counts: Record<Severity, number>
}

/**
 * Count the deck.
 *
 * The rule worth stating: an UNDECIDED finding is sent. Silence is not a
 * dismissal, and a reviewer that quietly dropped everything you had not got to
 * would be worse than one that posted too much: you would never know what it
 * had found. Dismissing is the deliberate act, and it is the only thing that
 * removes a finding.
 */
export function tally(findings: ReviewFinding[], verdicts: Verdicts, edits: Edits): Tally {
  const counts: Record<Severity, number> = { blocker: 0, major: 0, minor: 0 }
  let accepted = 0
  let dismissed = 0
  for (const f of findings) {
    const v = verdicts[f.index]
    if (v === 'accept') accepted++
    else if (v === 'dismiss') dismissed++
    if (v === 'dismiss') continue
    const s = effectiveSeverity(f, edits)
    if (s in counts) counts[s]++
    else counts.minor++
  }
  const resolved = accepted + dismissed
  return {
    total: findings.length,
    accepted,
    dismissed,
    resolved,
    undecided: findings.length - resolved,
    sending: findings.length - dismissed,
    blockers: counts.blocker,
    counts,
  }
}

/** What the send control promises. */
export function sendLabel(t: Tally, posting: boolean): string {
  if (posting) return 'Posting'
  if (!t.sending) return 'Post the summary'
  return `Post ${t.sending} finding${t.sending === 1 ? '' : 's'}`
}

/** The review's headline: a sentence, because three numbers is a puzzle. */
export function headline(t: Tally): string {
  if (!t.total) return 'Nothing worth reporting'
  const c = t.counts
  const word = (n: number) =>
    ['', 'One', 'Two', 'Three', 'Four', 'Five', 'Six', 'Seven', 'Eight', 'Nine'][n] ?? String(n)
  if (c.blocker) {
    return c.blocker === 1 ? 'One thing should block this merge' : `${word(c.blocker)} things should block this merge`
  }
  if (c.major) return c.major === 1 ? 'One thing worth fixing' : `${word(c.major)} things worth fixing`
  return c.minor === 1 ? 'One small thing' : `${word(c.minor)} small things`
}

/** Two-digit row numbers, so the queue's gutter never reflows at ten. */
export function pad(n: number): string {
  return String(n).padStart(2, '0')
}

/** Move the cursor without falling off either end. */
export function step(active: number, by: number, length: number): number {
  if (length <= 0) return 0
  return Math.min(Math.max(active + by, 0), length - 1)
}
