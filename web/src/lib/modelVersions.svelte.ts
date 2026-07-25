// Self-updating model versions. The picker sends a family ALIAS (opus/sonnet/
// haiku/fable), which the CLI resolves to the latest model of that family -- so a
// new release (Opus 5) is already reachable with no change. Only the picker's
// version LABEL used to go stale, because it was hardcoded. This store learns the
// newest version per family from the real model ids the CLI reports (session and
// history metadata, e.g. "claude-opus-4-8"), persists them, and hands them to the
// labels -- so the number self-heals from real usage instead of a release.

// FAMILIES are the Claude tiers the picker offers (the one genuinely manual list:
// a brand-new family, like Fable was, is rare and a deliberate product choice).
export const FAMILIES = ['opus', 'sonnet', 'haiku', 'fable']

// FALLBACK is the seed label shown before the client has ever seen a resolved id
// for a family. Kept roughly current; it only ever shows until the first real
// session of that family teaches the true version.
const FALLBACK: Record<string, string> = { opus: '4.8', sonnet: '5', haiku: '4.5', fable: '5' }

const KEY = 'kunai-model-versions'

function load(): Record<string, string> {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) || '{}')
    return v && typeof v === 'object' ? v : {}
  } catch {
    return {}
  }
}

const learned = $state<Record<string, string>>(load())

// discovered holds the authoritative versions a machine read straight from its
// claude binary (Stats.model_versions). It takes priority over lazy learning and
// the seed, so a new model (Opus 5) labels correctly the instant stats load, with
// no session needed. Merged newest-wins across a possibly mixed-version fleet.
const discovered = $state<Record<string, string>>({})

// parseModelId pulls the family and version out of a resolved id like
// "claude-opus-4-8" or "claude-haiku-4-5-20251001" -> {opus, 4.8} / {haiku, 4.5}.
export function parseModelId(id: string): { family: string; version: string } | null {
  const m = id.toLowerCase()
  const family = FAMILIES.find((f) => m.includes(f))
  if (!family) return null
  const match = m.match(new RegExp(`${family}[-_]?(\\d+)(?:[-_](\\d+))?`))
  if (!match) return { family, version: '' }
  return { family, version: match[2] ? `${match[1]}.${match[2]}` : match[1] }
}

const asNumber = (v: string) => parseFloat(v) || 0

// learnModel records the newest version seen for a family from a resolved id. A
// bare alias ("opus", no version) teaches nothing and is ignored.
export function learnModel(id: string | undefined | null) {
  if (!id) return
  const p = parseModelId(id)
  if (!p || !p.version) return
  if (!learned[p.family] || asNumber(p.version) > asNumber(learned[p.family])) {
    learned[p.family] = p.version
    try {
      localStorage.setItem(KEY, JSON.stringify(learned))
    } catch {
      // Private mode / storage full: labels just fall back to the seed. Not fatal.
    }
  }
}

// setDiscovered merges a machine's CLI-read versions (Stats.model_versions),
// keeping the newest across the fleet.
export function setDiscovered(map: Record<string, string> | undefined | null) {
  if (!map) return
  for (const [fam, ver] of Object.entries(map)) {
    if (ver && (!discovered[fam] || asNumber(ver) > asNumber(discovered[fam]))) {
      discovered[fam] = ver
    }
  }
}

// familyVersion is the version to show for a family: the CLI-discovered version
// first (authoritative, no session needed), else learned from real usage, else the
// built-in seed. Reactive -- reading it in a component tracks the stores, so a
// label updates the moment a newer version is known.
export function familyVersion(family: string): string {
  return discovered[family] ?? learned[family] ?? FALLBACK[family] ?? ''
}
