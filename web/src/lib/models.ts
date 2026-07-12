// Curated options for the model and reasoning-effort pickers. The claude CLI
// accepts a model alias (opus/sonnet/haiku/fable) or a full id for `--model` /
// set_model, and an `--effort` level. Effort is a spawn-time flag only, so it is
// chosen when a session is created and cannot change mid-session.

export interface Option {
  id: string
  label: string
  hint?: string
}

// App defaults for new sessions (both the New Session dialog and one-tap starts).
export const DEFAULT_MODEL = 'opus'
export const DEFAULT_EFFORT = 'high'

// Current version per family, shown in the picker labels. The composer button
// prefers the real version the CLI reports (see modelLabel); this map is only a
// best-effort label before a session resolves, so keep it roughly current.
const VERSIONS: Record<string, string> = { opus: '4.8', sonnet: '5', haiku: '4.5', fable: '5' }
const ver = (fam: string) => (VERSIONS[fam] ? ` ${VERSIONS[fam]}` : '')

// Runtime-switchable models (composer). Aliases resolve to the latest of each.
export const MODELS: Option[] = [
  { id: 'opus', label: `Opus${ver('opus')}`, hint: 'Most capable' },
  { id: 'sonnet', label: `Sonnet${ver('sonnet')}`, hint: 'Balanced' },
  { id: 'haiku', label: `Haiku${ver('haiku')}`, hint: 'Fastest, cheapest' },
  { id: 'fable', label: `Fable${ver('fable')}`, hint: 'Latest flagship' },
]

// Reasoning effort levels (new session only). '' means the CLI default.
export const EFFORTS: Option[] = [
  { id: 'low', label: 'Low' },
  { id: 'medium', label: 'Medium' },
  { id: 'high', label: 'High' },
  { id: 'xhigh', label: 'X-High' },
  { id: 'max', label: 'Max', hint: 'Deepest reasoning' },
]

// modelLabel maps a model string to a short "Family Version" label for the
// composer button. It parses the real version out of the id the CLI reports
// (e.g. "claude-opus-4-8" -> "Opus 4.8", "claude-haiku-4-5-20251001" -> "Haiku
// 4.5"), and falls back to the family's current version for a bare alias.
export function modelLabel(model: string): string {
  const m = model.toLowerCase()
  const fam = ['opus', 'sonnet', 'haiku', 'fable'].find((f) => m.includes(f))
  if (!fam) return 'Model'
  const cap = fam[0].toUpperCase() + fam.slice(1)
  const match = m.match(new RegExp(`${fam}[-_]?(\\d+)(?:[-_](\\d+))?`))
  const version = match ? (match[2] ? `${match[1]}.${match[2]}` : match[1]) : VERSIONS[fam] ?? ''
  return version ? `${cap} ${version}` : cap
}
