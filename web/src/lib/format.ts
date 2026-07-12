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
