// Context-window usage derived from a session's latest result.usage. The claude
// CLI does not report the model's window size over the wire, so it is a
// best-effort per-family constant here; the used-token count itself is exact.

const WINDOWS: { family: string; window: number }[] = [
  { family: 'opus', window: 1_000_000 },
  { family: 'sonnet', window: 1_000_000 },
  { family: 'haiku', window: 200_000 },
]
const DEFAULT_WINDOW = 200_000

// contextWindow returns the model's context-window size in tokens. Accepts an
// alias ("opus") or a full CLI id ("claude-opus-4-8").
export function contextWindow(model: string): number {
  const m = model.toLowerCase()
  return WINDOWS.find((w) => m.includes(w.family))?.window ?? DEFAULT_WINDOW
}

// formatTokens renders a token count compactly: 512, 27.9k, 432k, 1.0M.
export function formatTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 100_000) return Math.round(n / 1_000) + 'k'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'k'
  return String(Math.round(n))
}

export interface ContextUsage {
  used: number
  window: number
  usedFrac: number // 0..1, clamped
  usedPct: number // 0..100
  freePct: number // 0..100
  label: string // "27.9k / 1.0M"
}

// contextUsage computes the meter's derived numbers for a token count + model.
export function contextUsage(tokens: number, model: string): ContextUsage {
  const window = contextWindow(model)
  const used = Math.max(0, tokens)
  const usedFrac = window > 0 ? Math.min(1, used / window) : 0
  const usedPct = usedFrac * 100
  return {
    used,
    window,
    usedFrac,
    usedPct,
    freePct: Math.max(0, 100 - usedPct),
    label: `${formatTokens(used)} / ${formatTokens(window)}`,
  }
}
