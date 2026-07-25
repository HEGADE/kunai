// One-click update support. Detection is client-side: the app fetches GitHub's
// latest published release tag once (and periodically) and compares it to each
// machine's reported version (already known from /api/stats). Servers only
// contact GitHub on an explicit Update tap, so this preserves the relay-free
// ethos — no phone-home.

const LATEST_URL = 'https://api.github.com/repos/HEGADE/kunai/releases/latest'
// The nightly channel is one moving pre-release; its `name` is the build id
// (e.g. "nightly-ab12cd3"), which changes every push, so a string compare tells
// a nightly install it is behind.
const NIGHTLY_URL = 'https://api.github.com/repos/HEGADE/kunai/releases/tags/nightly'

// VersionCheck is what a check produced. The failure is reported rather than
// folded into a null, because the two are not the same thing to a user: no
// answer looks exactly like "you are up to date", and that is how a machine sat
// three releases behind with nothing on screen to say so.
export interface VersionCheck {
  tag: string | null
  // rateLimited is GitHub refusing because too many checks came from this
  // address. Unauthenticated callers get 60 an hour, shared by every tab, every
  // phone and every laptop on the same connection, so this is not exotic.
  rateLimited: boolean
  failed: boolean
}

// fetchLatestVersion returns the newest version string for the given channel:
// the latest release tag ("v0.2.0") on stable, or the nightly pre-release's
// build id on nightly.
export async function fetchLatestVersion(channel = ''): Promise<VersionCheck> {
  const url = channel === 'nightly' ? NIGHTLY_URL : LATEST_URL
  try {
    // no-store so a just-published release is seen immediately, not served from
    // a cached GitHub response (which is why the banner used to need a refresh).
    const res = await fetch(url, {
      headers: { Accept: 'application/vnd.github+json' },
      cache: 'no-store',
    })
    if (!res.ok) {
      // 403 is what the rate limiter returns; 429 is the documented one. Both
      // mean "ask again later", not "there is nothing new".
      return { tag: null, rateLimited: res.status === 403 || res.status === 429, failed: true }
    }
    const body = (await res.json()) as { tag_name?: string; name?: string }
    // Nightly moves the tag, so its `name` (the build id) is the comparable bit.
    const tag = (channel === 'nightly' ? body.name || body.tag_name : body.tag_name) ?? null
    return { tag, rateLimited: false, failed: tag === null }
  } catch {
    return { tag: null, rateLimited: false, failed: true }
  }
}

// parseSemver takes "v0.2.0", "0.2.0", or a git-describe string like
// "v0.1.0-5-gabc123" and returns its core [major, minor, patch]. Returns null
// for anything that is not release-versioned (a bare sha or "dev" build we
// cannot meaningfully compare). Requiring at least major.minor keeps an
// all-digit short sha from being mistaken for a version.
function parseSemver(v: string): [number, number, number] | null {
  const core = v.replace(/^v/, '').split('-')[0]
  const parts = core.split('.')
  if (parts.length < 2) return null
  const nums = parts.map((p) => Number(p))
  if (nums.some((n) => !Number.isInteger(n))) return null
  return [nums[0] ?? 0, nums[1] ?? 0, nums[2] ?? 0]
}

// updateAvailable is true only when we can confidently say `current` is behind
// `latest`. On nightly the version is a moving build id, so any difference means
// a newer build is out (a plain string compare). On stable both must parse as
// X.Y.Z and current < latest; a dev/sha build (which we can't compare) returns
// false, so we never nag on an uncertain comparison.
export function updateAvailable(
  current: string | undefined,
  latest: string | null,
  channel = '',
): boolean {
  if (!current || !latest) return false
  if (channel === 'nightly') return current !== latest
  const c = parseSemver(current)
  const l = parseSemver(latest)
  if (!c || !l) return false
  for (let i = 0; i < 3; i++) {
    if (c[i] < l[i]) return true
    if (c[i] > l[i]) return false
  }
  return false
}

// --- caching -------------------------------------------------------------------

// A release is not a per-minute event, so the answer is good for a long while.
// The floor bounds even a deliberate re-check, so a reload loop cannot spend the
// allowance.
export const VERSION_TTL = 30 * 60_000
export const VERSION_FLOOR = 60_000

const cacheKey = (channel: string) => `kunai-latest:${channel || 'stable'}`

// Cached in localStorage rather than in memory so it is shared by every tab of
// this origin: two tabs polling independently is two tabs' worth of GitHub's
// allowance for one answer.
export function readCachedVersion(channel: string): { tag: string; at: number } | null {
  try {
    const raw = localStorage.getItem(cacheKey(channel))
    if (!raw) return null
    const v = JSON.parse(raw) as { tag?: string; at?: number }
    return v.tag && v.at ? { tag: v.tag, at: v.at } : null
  } catch {
    return null
  }
}

export function writeCachedVersion(channel: string, tag: string, at: number): void {
  try {
    localStorage.setItem(cacheKey(channel), JSON.stringify({ tag, at }))
  } catch {
    // A browser refusing storage costs a re-check, nothing more.
  }
}
