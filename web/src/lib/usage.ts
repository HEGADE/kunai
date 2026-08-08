// What the work cost, sliced for the page.
//
// The server sends the WHOLE history once -- a few thousand (day, model) rows,
// which is small -- and this file slices it. That is deliberate: switching
// between 7, 30 and 90 days is then instant and costs no round trip, where a
// server-side window would re-fetch on every click for a saving of a few
// kilobytes.
//
// Everything here is pure, for the same reason `grouping.ts` and `loop.ts` are:
// the arithmetic is the part that can be quietly wrong, and a pure function can
// be tested without mounting anything.

export interface ModelStat {
  model: string
  agent: string
  /** False when kunai has no rate for this model. `cost` is then 0 and must be
   *  shown as "not priced", never as free. */
  priced: boolean
  cost: number
  savings: number
  in: number
  w5: number
  w1: number
  r: number
  out: number
  n: number
}

export interface DayStat {
  day: string
  models: ModelStat[]
}

export interface UsageReport {
  days: DayStat[]
  models: ModelStat[]
  files: number
  scanned_at: number
  scanning: boolean
}

export const PERIODS = [7, 30, 90] as const
export type Period = (typeof PERIODS)[number]

/** Tokens the model actually processed, cached or not. */
export function totalTokens(m: ModelStat): number {
  return m.in + m.w5 + m.w1 + m.r + m.out
}

/** All cache writes, whatever their TTL. The split matters for pricing, not for
 *  reading. */
export function cacheWrite(m: ModelStat): number {
  return m.w5 + m.w1
}

const EMPTY: Omit<ModelStat, 'model' | 'agent'> = {
  priced: true,
  cost: 0,
  savings: 0,
  in: 0,
  w5: 0,
  w1: 0,
  r: 0,
  out: 0,
  n: 0,
}

function add(into: ModelStat, from: ModelStat) {
  into.cost += from.cost
  into.savings += from.savings
  into.in += from.in
  into.w5 += from.w5
  into.w1 += from.w1
  into.r += from.r
  into.out += from.out
  into.n += from.n
  // Unpriced is contagious on purpose: a bucket holding even one unpriced model
  // has a cost that is a floor, not a total, and rolling it up as fully priced
  // would launder that.
  if (!from.priced) into.priced = false
}

/** The last `days` calendar days of the report, oldest first, with a row for
 *  every day in the window including the ones nothing happened on. A chart with
 *  gaps silently compresses time and makes an idle week look like a busy one. */
export function window_(report: UsageReport | null, days: number, today = new Date()): DayStat[] {
  const byDay = new Map((report?.days ?? []).map((d) => [d.day, d]))
  const out: DayStat[] = []
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(today.getFullYear(), today.getMonth(), today.getDate() - i)
    const key = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
    out.push(byDay.get(key) ?? { day: key, models: [] })
  }
  return out
}

/** Roll a set of days up into one row per model, biggest spender first. */
export function byModel(days: DayStat[]): ModelStat[] {
  const acc = new Map<string, ModelStat>()
  for (const d of days) {
    for (const m of d.models) {
      let cur = acc.get(m.model)
      if (!cur) {
        cur = { ...EMPTY, model: m.model, agent: m.agent }
        acc.set(m.model, cur)
      }
      add(cur, m)
    }
  }
  return [...acc.values()].sort((a, b) => b.cost - a.cost || totalTokens(b) - totalTokens(a))
}

/** Roll up by agent family (Claude / Codex / Grok), which is the split worth
 *  leading with: the question is which brain the month went on, not which point
 *  release. */
export function byAgent(days: DayStat[]): ModelStat[] {
  const acc = new Map<string, ModelStat>()
  for (const d of days) {
    for (const m of d.models) {
      let cur = acc.get(m.agent)
      if (!cur) {
        cur = { ...EMPTY, model: m.agent, agent: m.agent }
        acc.set(m.agent, cur)
      }
      add(cur, m)
    }
  }
  return [...acc.values()].sort((a, b) => b.cost - a.cost || totalTokens(b) - totalTokens(a))
}

/** One total for the window. */
export function totals(days: DayStat[]): ModelStat {
  const t: ModelStat = { ...EMPTY, model: '', agent: '' }
  for (const d of days) for (const m of d.models) add(t, m)
  return t
}

/** What fraction of the window's tokens kunai could put a price on. The page
 *  shows this rather than hiding it: a headline cost covering 98% of the tokens
 *  is a different claim from one covering all of them. */
export function pricedShare(days: DayStat[]): number {
  let priced = 0
  let all = 0
  for (const d of days) {
    for (const m of d.models) {
      const t = totalTokens(m)
      all += t
      if (m.priced) priced += t
    }
  }
  return all === 0 ? 1 : priced / all
}

/** Per-day cost, for the chart. */
export function dailyCost(days: DayStat[]): number[] {
  return days.map((d) => d.models.reduce((s, m) => s + m.cost, 0))
}

/** Per-day tokens, for the chart's other mode. */
export function dailyTokens(days: DayStat[]): number[] {
  return days.map((d) => d.models.reduce((s, m) => s + totalTokens(m), 0))
}

// Formatting. Money and counts both need to stay legible at wildly different
// magnitudes -- $0.04 and $12,500 on the same page, 130M and 10.6B in the same
// row -- so neither uses a fixed precision.

export function money(n: number): string {
  if (!isFinite(n)) return '$0'
  const abs = Math.abs(n)
  if (abs >= 1000) return '$' + n.toLocaleString('en-US', { maximumFractionDigits: 0 })
  if (abs >= 1) return '$' + n.toFixed(2)
  if (abs === 0) return '$0'
  return '$' + n.toFixed(abs >= 0.01 ? 2 : 4)
}

export function compact(n: number): string {
  const abs = Math.abs(n)
  if (abs >= 1e9) return (n / 1e9).toFixed(abs >= 1e10 ? 1 : 2) + 'B'
  if (abs >= 1e6) return (n / 1e6).toFixed(abs >= 1e7 ? 0 : 1) + 'M'
  if (abs >= 1e3) return (n / 1e3).toFixed(0) + 'K'
  return String(Math.round(n))
}

// Both ends are clamped, and the two clamps are the same rule seen from either
// side. Printing 99.93% as "100%" next to its own complement as "<0.1%" reads as
// a contradiction and quietly claims complete coverage the page does not have,
// which on the "cost quality" panel is exactly the claim being audited.
export function percent(f: number): string {
  const p = f * 100
  if (p > 0 && p < 0.1) return '<0.1%'
  if (p < 100 && p > 99.9) return '>99.9%'
  return p.toFixed(p >= 10 ? 0 : 1) + '%'
}

/** "Jul 14" — the chart's axis and the breakdown's day column. */
export function dayLabel(day: string): string {
  const [y, m, d] = day.split('-').map(Number)
  if (!y || !m || !d) return day
  return new Date(y, m - 1, d).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
}
