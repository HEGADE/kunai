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

// fetchLatestVersion returns the newest version string for the given channel:
// the latest release tag ("v0.2.0") on stable, or the nightly pre-release's
// build id on nightly. null if GitHub is unreachable / rate-limited.
export async function fetchLatestVersion(channel = ''): Promise<string | null> {
  const url = channel === 'nightly' ? NIGHTLY_URL : LATEST_URL
  try {
    // no-store so a just-published release is seen immediately, not served from
    // a cached GitHub response (which is why the banner used to need a refresh).
    const res = await fetch(url, {
      headers: { Accept: 'application/vnd.github+json' },
      cache: 'no-store',
    })
    if (!res.ok) return null
    const body = (await res.json()) as { tag_name?: string; name?: string }
    // Nightly moves the tag, so its `name` (the build id) is the comparable bit.
    return (channel === 'nightly' ? body.name || body.tag_name : body.tag_name) ?? null
  } catch {
    return null
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
