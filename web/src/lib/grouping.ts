// Which heading a session sits under in the sidebar.
//
// Two kinds of group, and the difference is who decided the name. A **project**
// group is derived: the directory the session was started in, so it needs no
// setup and every session has one. A **workspace** group is chosen: once a
// session has more than one codebase, the directory it happened to start in
// stops describing it, so you name the group yourself and that name wins.
//
// Kept pure and free of Svelte so it can be exercised directly. The sidebar only
// renders what this returns.

import type { TaggedHistoryEntry, TaggedMeta } from './types'

// Groupable is the shape both lists share. Live sessions carry `projects`
// (how many codebases they hold); past ones do not, because a closed session's
// project list died with the process.
export interface Groupable {
  cwd: string
  workspace?: string
  projects?: number
  // repo is the main checkout when cwd is a git worktree of it. A worktree
  // session belongs to the codebase it came from, not to the directory it
  // happens to live in: grouping by that directory would give every worktree a
  // heading of its own and scatter one repository across the sidebar, which is
  // the opposite of what grouping is for.
  repo?: string
  // project is the codebase this session belongs to, worked out by the server:
  // the checkout cwd sits inside, or, for a past session launched from a folder
  // that holds no checkout, the one its own transcript says the work went into.
  //
  // Without it the heading was whatever folder somebody happened to type, so a
  // session started in ~/coding got a heading called "coding" -- a folder that
  // holds every codebase on the machine and is not one itself. Empty whenever
  // the server had no honest answer, which is why cwd is still the fallback.
  project?: string
}

export interface SessionGroup<T> {
  // key is stable and unique, for {#each}.
  key: string
  label: string
  // named is true when the user chose this group's name, which is what makes it
  // a workspace rather than a directory.
  named: boolean
  items: T[]
}

// projectName is the directory a session was started in, as a person would say
// it: the last path segment, with any trailing slashes ignored.
export function projectName(cwd: string): string {
  const trimmed = cwd.replace(/\/+$/, '')
  const base = trimmed.split('/').pop() ?? ''
  return base || trimmed || 'session'
}

// groupLabel is the heading a session belongs under. A name the user set always
// wins, because they set it precisely to override the directory.
//
// Below that the order is most-specific-claim first: repo says "this directory
// is a worktree of that codebase", project says "this directory is part of, or
// did its work in, that codebase", and cwd is what is left when neither is
// known.
export function groupLabel(s: Groupable): string {
  const named = s.workspace?.trim()
  return named || projectName(s.repo || s.project || s.cwd)
}

// isWorkspace reports whether a session holds more than one codebase, which is
// when naming its group starts to matter. Unknown for a past session, which is
// why an unnamed multi-project session falls back to its directory once closed.
export function isWorkspace(s: Groupable): boolean {
  return (s.projects ?? 0) > 1 || !!s.workspace?.trim()
}

// groupSessions buckets sessions under their heading, preserving the order they
// arrived in both between groups and within them: the caller has already sorted
// for recency or pins, and regrouping must not quietly reorder that.
//
// A group counts as named if any session in it carries a user-set workspace, so
// naming one session's workspace names the heading the others share.
export function groupSessions<T extends Groupable>(sessions: T[]): SessionGroup<T>[] {
  const byKey = new Map<string, SessionGroup<T>>()
  for (const s of sessions) {
    const label = groupLabel(s)
    const existing = byKey.get(label)
    if (existing) {
      existing.items.push(s)
      existing.named ||= !!s.workspace?.trim()
      continue
    }
    byKey.set(label, {
      key: label,
      label,
      named: !!s.workspace?.trim(),
      items: [s],
    })
  }
  return [...byKey.values()]
}

// Convenience aliases so call sites read as what they are.
export type MetaGroup = SessionGroup<TaggedMeta>
export type HistoryGroup = SessionGroup<TaggedHistoryEntry>

// A group's start target: the machine and directory a new session started from
// this heading would run in. Returns null when the group spans more than one
// directory (a hand-named workspace can hold several codebases), because there is
// then no single right answer and guessing would start work in the wrong repo.
// Kept here rather than in the sidebar so the rule is pure and testable.
export function groupStartTarget<T extends Groupable & { machineId: string }>(
  group: SessionGroup<T>,
): { machineId: string; cwd: string } | null {
  const first = group.items[0]
  // A worktree session's home is the repository, so that is what starting from
  // this heading means. Without this, one worktree in a group would make its
  // members' directories disagree and the heading would lose its start button
  // exactly when the repository had the most work going on in it.
  // The same precedence groupLabel uses, so "start here" starts where the
  // heading says. A heading rescued from a container folder must start in the
  // codebase it names, not back in the folder -- which is why the server only
  // reports a project directory that still exists.
  const home = (s: T) => s.repo || s.project || s.cwd
  if (!first || !home(first)) return null
  for (const item of group.items) {
    if (home(item) !== home(first) || item.machineId !== first.machineId) return null
  }
  return { machineId: first.machineId, cwd: home(first) }
}

// Which folder headings the sidebar actually shows.
//
// A sidebar that lists every codebase ever opened stops being a place you look
// and becomes a place you scroll: the list grew until the nav at the bottom was
// pushed off the screen. So folders holding nothing but PAST work are capped.
//
// Two things are never counted against the cap and never dropped. A folder with
// something LIVE in it stays, because the sidebar's job is to show what is
// happening, and hiding a running agent to make room for a folder somebody last
// opened on Tuesday is exactly the wrong way round. And pinned work is
// unaffected by construction, since a pin lifts a session out of the groups
// into its own flat section above them.
//
// Everything cut is one tap away under "View all sessions", which is searchable
// and paginated, and the count of what was cut is returned so that link can say
// so rather than letting a folder go missing without explanation.
export function visibleGroups<T>(
  groups: SessionGroup<T>[],
  isLive: (item: T) => boolean,
  max: number,
): { shown: SessionGroup<T>[]; hidden: number } {
  let quiet = 0
  const shown = groups.filter((g) => {
    if (g.items.some(isLive)) return true
    quiet += 1
    return quiet <= max
  })
  return { shown, hidden: groups.length - shown.length }
}
