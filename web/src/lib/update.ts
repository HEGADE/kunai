// One-click update support. Detection is client-side: the app fetches GitHub's
// latest published release tag once (and periodically) and compares it to each
// machine's reported version (already known from /api/stats). Servers only
// contact GitHub on an explicit Update tap, so this preserves the relay-free
// ethos — no phone-home.

const LATEST_URL = 'https://api.github.com/repos/HEGADE/kunai/releases/latest'

// fetchLatestVersion returns the latest release tag (e.g. "v0.2.0"), or null if
// GitHub is unreachable / rate-limited (unauthenticated is 60 req/hr, plenty).
export async function fetchLatestVersion(): Promise<string | null> {
  try {
    const res = await fetch(LATEST_URL, { headers: { Accept: 'application/vnd.github+json' } })
    if (!res.ok) return null
    const body = (await res.json()) as { tag_name?: string }
    return body.tag_name ?? null
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
// `latest` — both parse as X.Y.Z and current < latest. A dev/sha build (which
// we can't compare) returns false: we never nag on an uncertain comparison.
export function updateAvailable(current: string | undefined, latest: string | null): boolean {
  if (!current || !latest) return false
  const c = parseSemver(current)
  const l = parseSemver(latest)
  if (!c || !l) return false
  for (let i = 0; i < 3; i++) {
    if (c[i] < l[i]) return true
    if (c[i] > l[i]) return false
  }
  return false
}
