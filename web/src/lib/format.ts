// formatDuration renders a millisecond span the way a turn footer reads best:
// sub-minute times keep one decimal of seconds ("1.4s"), tens of seconds drop it
// ("12s"), and anything past a minute becomes "6m 43s".
export function formatDuration(ms: number): string {
  if (!ms || ms < 0) return ''
  const totalSec = ms / 1000
  if (totalSec < 10) return `${totalSec.toFixed(1)}s`
  if (totalSec < 60) return `${Math.round(totalSec)}s`
  const m = Math.floor(totalSec / 60)
  const s = Math.round(totalSec % 60)
  return s ? `${m}m ${s}s` : `${m}m`
}

// formatTokens compacts a token count: 940, 3.2k, 1.4M.
export function formatTokens(n: number): string {
  if (!n || n < 0) return ''
  if (n < 1000) return `${n}`
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`
  return `${(n / 1_000_000).toFixed(1)}M`
}

// formatCost renders a USD cost with enough precision to be meaningful at small
// values: <$0.01 shows 3 decimals, otherwise 2.
export function formatCost(usd: number): string {
  if (!usd || usd < 0) return ''
  return usd < 0.01 ? `$${usd.toFixed(3)}` : `$${usd.toFixed(2)}`
}
