// What a running review is doing, read off the session it is running in.
//
// A review takes minutes, and the screen for those minutes was a phase name, a
// clock and an empty page. Everything needed to make it legible was already on
// the client and unread: the review is an ORDINARY SESSION, its socket is
// already open, and every file it opens and every pattern it greps for arrives
// as a tool call. Nothing had to be sent for this; it only had to be looked at.
//
// Kept pure and out of the component so it can be exercised directly. What it
// answers is deliberately narrow, because these are the questions somebody
// waiting actually has: what is it reading NOW, what has it read, and how much
// of the change has it been through.

import type { Item } from './chat.svelte'

/** One thing the reviewer did: opened a file, searched for something. */
export interface Act {
  kind: 'read' | 'search' | 'other'
  /** The file for a read, the pattern for a search, the tool name otherwise. */
  what: string
  /** The tool as the CLI names it, for the cases the three kinds do not cover. */
  tool: string
}

const READERS = new Set(['Read', 'NotebookRead'])
const SEARCHERS = new Set(['Grep', 'Glob'])

/**
 * Every tool call the reviewer has made, oldest first.
 *
 * Task is deliberately included and named: during verification the whole phase
 * IS subagents, so dropping them would make the busiest part of a review look
 * like nothing happening at all.
 */
export function acts(items: Item[]): Act[] {
  const out: Act[] = []
  for (const it of items) {
    if (it.role !== 'assistant') continue
    for (const b of it.blocks) {
      if (b.type !== 'tool_use' || !b.name) continue
      const input = (b.input ?? {}) as Record<string, unknown>
      if (READERS.has(b.name)) {
        out.push({ kind: 'read', what: str(input.file_path ?? input.notebook_path), tool: b.name })
      } else if (SEARCHERS.has(b.name)) {
        out.push({ kind: 'search', what: str(input.pattern ?? input.path ?? ''), tool: b.name })
      } else {
        out.push({ kind: 'other', what: str(input.description ?? input.prompt ?? ''), tool: b.name })
      }
    }
  }
  return out
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

/** The files it has opened, most recent LAST, each listed once. */
export function opened(list: Act[]): string[] {
  const seen: string[] = []
  for (const a of list) {
    if (a.kind !== 'read' || !a.what) continue
    // A file read twice is one file that has been looked at, not two. Moved to
    // the end rather than left where it was, because re-opening something is
    // itself recent activity and that is what this list is ordered by.
    const at = seen.indexOf(a.what)
    if (at >= 0) seen.splice(at, 1)
    seen.push(a.what)
  }
  return seen
}

/** The last thing it did, which is the answer to "what is it doing now". */
export function latest(list: Act[]): Act | null {
  return list.length ? list[list.length - 1] : null
}

/**
 * How much of the change it has opened.
 *
 * Matched by SUFFIX, because a tool call carries an absolute path inside the
 * worktree and the pull request lists repository-relative ones. Comparing them
 * directly reports zero every time, which is the sort of quietly-wrong number
 * that is worse than no number.
 */
export function coverage(files: string[], readPaths: string[]): { seen: number; total: number } {
  if (!files.length) return { seen: 0, total: 0 }
  const seen = files.filter((f) => readPaths.some((p) => p === f || p.endsWith('/' + f))).length
  return { seen, total: files.length }
}

/** The diff's size, which is what says whether a wait is reasonable. */
export function changeSize(files: { additions?: number; deletions?: number }[]): {
  files: number
  additions: number
  deletions: number
} {
  let additions = 0
  let deletions = 0
  for (const f of files) {
    additions += f.additions ?? 0
    deletions += f.deletions ?? 0
  }
  return { files: files.length, additions, deletions }
}

/** Just the file's name, for a list where the directory is noise. */
export function baseName(path: string): string {
  return path.split('/').pop() || path
}

/** How long each phase took, in order, from the recorded timeline. */
export function phaseSpans(
  timeline: { phase: string; at: string }[],
  nowMs: number,
): { phase: string; ms: number; running: boolean }[] {
  return timeline.map((t, i) => {
    const from = Date.parse(t.at)
    const next = timeline[i + 1] ? Date.parse(timeline[i + 1].at) : nowMs
    return {
      phase: t.phase,
      ms: Number.isFinite(from) && next > from ? next - from : 0,
      running: i === timeline.length - 1,
    }
  })
}
