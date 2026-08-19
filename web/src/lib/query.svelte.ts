// A small stale-while-revalidate cache for the REST calls this app repeats.
//
// Why this exists, measured rather than assumed. On one idle page load the client
// fired 31 API requests in 30 seconds, 8 of them near-simultaneous exact
// duplicates. Every /api/usage call went out TWICE, once per account, because
// Home is mounted twice -- the dashboard and the sidebar's compact copy -- and
// each fetched independently. That endpoint shells the `claude` CLI, takes about
// two seconds, and writes a transcript the server then deletes; the server holds a
// mutex across the shell, so the duplicate did not double the cost, it just
// blocked a handler waiting for it. Dialogs were as bad in a different way: the
// worktree branch list, the setup proposal and a provider's model list were
// refetched from scratch every time a dialog opened, so opening one always meant
// looking at "reading branches…" for something that had not changed.
//
// Three behaviours fix all of that, and they are the three any SWR library gives
// you:
//
//  1. Dedup. Two callers asking for the same key at the same moment share one
//     request. This is what the double-mounted Home needed.
//  2. A shared cache. A second caller, or the same caller after remounting, gets
//     the last value immediately.
//  3. Revalidate in the background. Past its TTL the cached value is still served
//     at once and refreshed behind it, so a dialog opens with real content and
//     corrects itself rather than opening empty.
//
// Hand-written rather than TanStack Query for the same reason the rest of this
// client is: the dependency list is three packages, the app already has a
// runes-based store layer this has to interoperate with, and the whole mechanism
// is a hundred lines. The cache policy is the part worth owning.

import { SvelteMap } from 'svelte/reactivity'

export interface Entry<T> {
  data?: T
  error?: string
  // at is when data (or error) was recorded, for the TTL. 0 means never fetched.
  at: number
}

// The cache is a reactive map so a component reading a key re-renders when any
// other component's fetch fills it in.
const store = new SvelteMap<string, Entry<unknown>>()

// In-flight requests, keyed the same way. Deliberately NOT reactive: a promise is
// not state anyone renders, and making it reactive would re-run effects on every
// request start and finish.
const inflight = new Map<string, Promise<unknown>>()

// DEFAULT_TTL is deliberately short. This is a cache for avoiding duplicate and
// repeated work within a few seconds of itself, not a store of record; the poll
// loop in app.svelte.ts remains what keeps long-lived data current.
export const DEFAULT_TTL = 15_000

// USAGE_TTL sits just under Home's own 60s refresh interval, so the periodic
// refresh still goes out while everything inside one tick is shared. Deliberately
// not longer: the server caches for 60s too, and a client TTL past that would
// show a number staler than the server was willing to serve.
export const USAGE_TTL = 55_000

// Longer, for things that only change when someone does something: a repository's
// branch list, its setup command, whether a folder is a git repo at all.
export const SLOW_TTL = 60_000

export function peek<T>(key: string): Entry<T> | undefined {
  return store.get(key) as Entry<T> | undefined
}

export function isFresh(key: string, ttl = DEFAULT_TTL): boolean {
  const e = store.get(key)
  return !!e && e.at > 0 && Date.now() - e.at < ttl
}

// fetchQuery is the imperative half: give me this value, using the cache when it
// is fresh, sharing a request when one is already out.
//
// force skips the freshness check but still joins an in-flight request, which is
// what you want after a mutation: refresh now, but do not stampede.
export async function fetchQuery<T>(
  key: string,
  fn: () => Promise<T>,
  opts: { ttl?: number; force?: boolean } = {},
): Promise<T> {
  const ttl = opts.ttl ?? DEFAULT_TTL
  const existing = store.get(key) as Entry<T> | undefined
  if (!opts.force && existing && existing.at > 0 && Date.now() - existing.at < ttl) {
    if (existing.error) throw new Error(existing.error)
    return existing.data as T
  }
  const running = inflight.get(key) as Promise<T> | undefined
  if (running) return running

  const p = fn()
    .then((data) => {
      store.set(key, { data, at: Date.now() })
      return data
    })
    .catch((e: unknown) => {
      const message = (e as Error)?.message || 'request failed'
      // Keep any previous data alongside the error: a blip should not blank a
      // dialog that was showing something correct a second ago.
      const prev = store.get(key) as Entry<T> | undefined
      store.set(key, { data: prev?.data, error: message, at: Date.now() })
      throw e
    })
    .finally(() => {
      inflight.delete(key)
    })

  inflight.set(key, p)
  return p
}

// invalidate drops cached entries so the next read refetches. A prefix, because
// keys are namespaced ("wt:branches:/path") and a mutation usually invalidates a
// family rather than one exact key.
export function invalidate(prefix: string) {
  for (const key of [...store.keys()]) {
    if (key.startsWith(prefix)) store.delete(key)
  }
}

// Resource is what a component holds: the last known value, whether a request is
// out, and the last error. It reads the shared cache, so two components holding
// the same key show the same thing without either knowing about the other.
//
// Construct it at component top level and call load() from an $effect, rather
// than having it own an effect internally: the key usually depends on props, and
// making the component say when to load keeps that dependency where it is
// visible instead of hidden in here.
export class Resource<T> {
  key = $state('')
  loading = $state(false)
  private ttl: number

  constructor(ttl = DEFAULT_TTL) {
    this.ttl = ttl
  }

  // entry tracks the reactive map, so filling this key anywhere updates every
  // Resource pointing at it.
  private entry = $derived(this.key ? (store.get(this.key) as Entry<T> | undefined) : undefined)

  get data(): T | undefined {
    return this.entry?.data
  }
  get error(): string {
    return this.entry?.error ?? ''
  }
  // stale means "showing something, and a refresh is out behind it". A caller can
  // use it for a quiet hint; it must not be used to hide the data.
  get stale(): boolean {
    return this.loading && this.data !== undefined
  }
  // pending is the only state that deserves a skeleton: nothing to show yet.
  get pending(): boolean {
    return this.loading && this.data === undefined && !this.error
  }

  async load(key: string, fn: () => Promise<T>, opts: { force?: boolean } = {}) {
    this.key = key
    if (!key) return
    if (!opts.force && isFresh(key, this.ttl)) return
    this.loading = true
    try {
      await fetchQuery(key, fn, { ttl: this.ttl, force: opts.force })
    } catch {
      // The error is on the cache entry, which `error` above reads. Throwing here
      // would make every call site write the same try/catch.
    } finally {
      this.loading = false
    }
  }
}

// Key builders, so two call sites cannot cache the same request under two names.
// That is the failure this whole module would otherwise invite: a shared cache
// with unshared keys is just a slower cache.
export const keys = {
  usage: (base: string, cli: string) => `usage:${base}:${cli}`,
  branches: (base: string, repo: string) => `wt:branches:${base}:${repo}`,
  setup: (base: string, repo: string) => `wt:setup:${base}:${repo}`,
  isRepo: (base: string, path: string) => `wt:isrepo:${base}:${path}`,
  providerModels: (base: string, cli: string) => `provider:models:${base}:${cli}`,
  githubApp: (base: string) => `gh:app:${base}`,
  pulls: (base: string, repo: string) => `gh:pulls:${base}:${repo}`,
}
