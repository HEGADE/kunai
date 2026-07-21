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
export function groupLabel(s: Groupable): string {
  const named = s.workspace?.trim()
  return named || projectName(s.cwd)
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
