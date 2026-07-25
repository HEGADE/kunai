// Curated options for the model and reasoning-effort pickers. The claude CLI
// accepts a model alias (opus/sonnet/haiku/fable) or a full id for `--model` /
// set_model, and an `--effort` level. Effort is a spawn-time flag only, so it is
// chosen when a session is created and cannot change mid-session.

import { familyVersion, parseModelId } from './modelVersions.svelte'

export interface Option {
  id: string
  label: string
  hint?: string
}

// App defaults for new sessions (both the New Session dialog and one-tap starts).
export const DEFAULT_MODEL = 'opus'
export const DEFAULT_EFFORT = 'high'

// Runtime-switchable models (composer). The id is a family ALIAS, which the CLI
// resolves to the latest model of that family -- so a new release is reachable
// with no change here. Version labels are NOT baked in; they come from
// modelOptionLabel, which reads the version learned from real usage.
export interface ModelFamily {
  id: string
  hint?: string
}
export const MODELS: ModelFamily[] = [
  { id: 'opus', hint: 'Most capable' },
  { id: 'sonnet', hint: 'Balanced' },
  { id: 'haiku', hint: 'Fastest, cheapest' },
  { id: 'fable', hint: 'Latest flagship' },
]

// modelOptionLabel is the picker label for a family alias, e.g. "Opus 5". The
// version is the latest learned from a resolved id (else the seed), so it stays
// current without a release. Call it in a template so it tracks the learned store.
export function modelOptionLabel(id: string): string {
  const cap = id.charAt(0).toUpperCase() + id.slice(1)
  const v = familyVersion(id)
  return v ? `${cap} ${v}` : cap
}

// modelFamily returns the family alias a resolved model id belongs to (for the
// picker's active state), or '' if it matches none.
export function modelFamily(model: string): string {
  return parseModelId(model)?.family ?? ''
}

// Reasoning effort levels (new session only). '' means the CLI default.
export const EFFORTS: Option[] = [
  { id: 'low', label: 'Low' },
  { id: 'medium', label: 'Medium' },
  { id: 'high', label: 'High' },
  { id: 'xhigh', label: 'X-High' },
  { id: 'max', label: 'Max', hint: 'Deepest reasoning' },
]

// effortLabel maps an effort id to its display label ('' -> "Effort").
export function effortLabel(id: string): string {
  return EFFORTS.find((e) => e.id === id)?.label ?? (id || 'Effort')
}

// modelLabel maps a model string to a short "Family Version" label for the
// composer button. It parses the real version out of the id the CLI reports
// (e.g. "claude-opus-4-8" -> "Opus 4.8", "claude-haiku-4-5-20251001" -> "Haiku
// 4.5"), and falls back to the family's learned version for a bare alias.
export function modelLabel(model: string): string {
  const p = parseModelId(model)
  if (!p) return 'Model'
  const cap = p.family.charAt(0).toUpperCase() + p.family.slice(1)
  const version = p.version || familyVersion(p.family)
  return version ? `${cap} ${version}` : cap
}
