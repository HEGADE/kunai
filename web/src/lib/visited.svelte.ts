// When you last looked at each session, on THIS device.
//
// This is the reference point for "the agent finished while you were away": a
// session whose turn ended after your last visit is Done and unread, and the
// sidebar sets it bright. It is deliberately per-device (localStorage, not the
// server): what your phone has seen says nothing about what your laptop has,
// and syncing it would make one glance anywhere mark everything read
// everywhere.
//
// A session never visited on this device counts as READ, not unread. The
// comparison in lib/sidebar.ts only fires when a visit stamp exists, so a
// fresh install (or a cleared browser) does not light up every session in the
// list as if it all just finished.

const KEY = 'kunai-visited'
// Bounded so the record cannot grow for ever: sessions come and go, and one
// stamp per session you have ever opened would outlive most of them. The
// newest N cover every session the sidebar can show.
const MAX_ENTRIES = 300

function read(): Record<string, number> {
  try {
    const raw = localStorage.getItem(KEY)
    return raw ? (JSON.parse(raw) as Record<string, number>) : {}
  } catch {
    return {}
  }
}

class VisitedStore {
  private stamps = $state<Record<string, number>>(read())

  private key(machineId: string, id: string): string {
    return `${machineId}:${id}`
  }

  // at returns when this session was last visited here, or 0 for never.
  at(machineId: string, id: string): number {
    return this.stamps[this.key(machineId, id)] ?? 0
  }

  // touch records "the user is looking at this session right now".
  touch(machineId: string, id: string) {
    const key = this.key(machineId, id)
    let next: Record<string, number> = { ...this.stamps, [key]: Date.now() }
    const keys = Object.keys(next)
    if (keys.length > MAX_ENTRIES) {
      const newest = keys.sort((a, b) => next[b] - next[a]).slice(0, MAX_ENTRIES)
      next = Object.fromEntries(newest.map((k) => [k, next[k]]))
    }
    this.stamps = next
    try {
      localStorage.setItem(KEY, JSON.stringify(next))
    } catch {
      // A browser refusing storage costs the memory across reloads, not the
      // in-session behaviour.
    }
  }
}

export const visited = new VisitedStore()
