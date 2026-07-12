// Curated options for the model and reasoning-effort pickers. The claude CLI
// accepts a model alias (opus/sonnet/haiku/fable) or a full id for `--model` /
// set_model, and an `--effort` level. Effort is a spawn-time flag only, so it is
// chosen when a session is created and cannot change mid-session.

export interface Option {
  id: string
  label: string
  hint?: string
}

// Runtime-switchable models (composer). Aliases resolve to the latest of each.
export const MODELS: Option[] = [
  { id: 'opus', label: 'Opus', hint: 'Most capable' },
  { id: 'sonnet', label: 'Sonnet', hint: 'Balanced' },
  { id: 'haiku', label: 'Haiku', hint: 'Fastest, cheapest' },
  { id: 'fable', label: 'Fable', hint: 'Latest flagship' },
]

// Reasoning effort levels (new session only). '' means the CLI default.
export const EFFORTS: Option[] = [
  { id: '', label: 'Default', hint: 'Model default' },
  { id: 'low', label: 'Low' },
  { id: 'medium', label: 'Medium' },
  { id: 'high', label: 'High' },
  { id: 'xhigh', label: 'X-High' },
  { id: 'max', label: 'Max', hint: 'Deepest reasoning' },
]

// modelLabel maps a model string (an alias or the full id the CLI reports) to a
// short label for the composer button; falls back to a generic 'Model'.
export function modelLabel(model: string): string {
  const m = model.toLowerCase()
  if (m.includes('opus')) return 'Opus'
  if (m.includes('sonnet')) return 'Sonnet'
  if (m.includes('haiku')) return 'Haiku'
  if (m.includes('fable')) return 'Fable'
  return 'Model'
}
