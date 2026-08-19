// Who is asking for a review, on this device.
//
// Every install posts as the same `kunai[bot]`, which is the point: one
// consistent reviewer identity on the org's pull requests. The cost is that a
// review carries no trace of which colleague ran it, so two reviews on one pull
// request are indistinguishable and nobody knows who to ask about a finding.
//
// One line in the review body fixes that, and this is where the name comes from.
// Per device rather than per server, because it identifies the PERSON at the
// keyboard: the same machine used by two people should not attribute one's
// reviews to the other, and a name synced across a fleet would do exactly that.

const KEY = 'kunai-github-handle'

// handle returns the GitHub username to credit, or '' when none is set. An
// unset handle is not an error: the review still posts, it just says "requested
// via kunai" instead of naming somebody.
export function handle(): string {
  try {
    return localStorage.getItem(KEY)?.trim() ?? ''
  } catch {
    return ''
  }
}

// setHandle records it. Stored with the leading @ stripped and re-added on use,
// so it reads the same whether or not somebody typed one.
export function setHandle(value: string) {
  const clean = value.trim().replace(/^@+/, '')
  try {
    if (clean) localStorage.setItem(KEY, clean)
    else localStorage.removeItem(KEY)
  } catch {
    // A browser refusing storage costs the attribution, not the review.
  }
}

// mention is the handle as it should appear in a review body: "@shorya", or ''.
export function mention(): string {
  const h = handle()
  return h ? '@' + h : ''
}
