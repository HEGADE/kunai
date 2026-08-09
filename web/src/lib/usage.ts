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

/** Fold several machines' reports into one.
 *
 *  A fleet is the unit the question is actually asked in: "what did my work
 *  cost" does not mean "on this laptop". Each machine scans only its own
 *  transcripts, so the buckets are disjoint by construction and summing them is
 *  correct -- with one stated exception, which is why the page keeps a per-machine
 *  breakdown rather than only the total. If you sync `~/.claude` between machines
 *  (Syncthing, Dropbox, a shared home directory), the same session exists on
 *  both, each machine counts it, and the fleet total double counts it. Nothing in
 *  a report identifies a session, so this cannot be detected here; what the
 *  breakdown CAN do is make it visible, because two machines claiming the same
 *  suspiciously identical figure is the tell. This is the same failure that once
 *  made the whole page read $44k instead of $11k, one level up.
 *
 *  A report that could not be fetched is simply absent from `reports`. The caller
 *  must say so: a fleet total missing a machine is a floor, not a total, and
 *  silently smaller is the one way this number can lie without being wrong. */
export function mergeReports(reports: UsageReport[]): UsageReport {
  const byDay = new Map<string, Map<string, ModelStat>>()
  for (const r of reports) {
    for (const d of r.days ?? []) {
      let bucket = byDay.get(d.day)
      if (!bucket) {
        bucket = new Map()
        byDay.set(d.day, bucket)
      }
      for (const m of d.models) {
        let cur = bucket.get(m.model)
        if (!cur) {
          cur = { ...EMPTY, model: m.model, agent: m.agent }
          bucket.set(m.model, cur)
        }
        add(cur, m)
      }
    }
  }
  const days = [...byDay.entries()]
    .sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0))
    .map(([day, models]) => ({ day, models: [...models.values()] }))
  return {
    days,
    models: byModel(days),
    files: reports.reduce((s, r) => s + (r.files ?? 0), 0),
    scanned_at: reports.reduce((s, r) => Math.max(s, r.scanned_at ?? 0), 0),
    // Any machine still scanning means the fleet total is still growing, so the
    // page keeps polling and keeps saying so.
    scanning: reports.some((r) => r.scanning),
  }
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

export type Metric = 'cost' | 'tokens'

/** The one number a metric asks of a bucket, so the chart, the tooltip and the
 *  table all read the same field and cannot drift apart. */
export function valueOf(m: ModelStat, metric: Metric): number {
  return metric === 'cost' ? m.cost : totalTokens(m)
}

export interface StackPart {
  agent: string
  value: number
}

export interface DayStack {
  day: string
  total: number
  /** Only the agents that did work that day, in the fixed agent order (the
   *  caller's `order`), so a segment never moves between columns. */
  parts: StackPart[]
}

/** The daily chart's data: one column per day, split by agent.
 *
 *  Splitting the column rather than totalling it is the whole point of the
 *  chart. A plain daily total says a Tuesday was busy; a split one says the
 *  Tuesday was busy because Codex ran all afternoon, which is the thing you
 *  came to find out and cannot get from any other view on the page. */
export function stack(days: DayStat[], metric: Metric, order: string[]): DayStack[] {
  const rank = (a: string) => {
    const i = order.indexOf(a)
    return i < 0 ? order.length : i
  }
  return days.map((d) => {
    const by = new Map<string, number>()
    for (const m of d.models) by.set(m.agent, (by.get(m.agent) ?? 0) + valueOf(m, metric))
    const parts = [...by.entries()]
      .filter(([, v]) => v > 0)
      .map(([agent, value]) => ({ agent, value }))
      .sort((a, b) => rank(a.agent) - rank(b.agent))
    return { day: d.day, total: parts.reduce((s, p) => s + p.value, 0), parts }
  })
}

/** The window immediately before the one on screen, same length, so a headline
 *  can say whether this is a heavy period or an ordinary one. A number with
 *  nothing to compare it against is the reason nobody knows what $400 means. */
export function priorWindow(
  report: UsageReport | null,
  days: number,
  today = new Date(),
): DayStat[] {
  const end = new Date(today.getFullYear(), today.getMonth(), today.getDate() - days)
  return window_(report, days, end)
}

/** The top of the chart's scale: the peak rounded up to a number a person would
 *  have chosen. An axis labelled "$47.13" is the peak wearing a hat; the reader
 *  wants a ruler, and a ruler has round marks on it. */
export function niceMax(peak: number): number {
  if (!(peak > 0)) return 1
  const mag = Math.pow(10, Math.floor(Math.log10(peak)))
  const n = peak / mag
  // Fine-grained on purpose. A coarse 1/2/5 ladder rounds a 1.13B peak up to 2B
  // and spends nearly half the chart's height on air above the tallest bar,
  // which flattens everything below it.
  const steps = [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]
  return (steps.find((s) => n <= s) ?? 10) * mag
}

/** Relative change, or null when there is nothing to compare against.
 *
 *  Null rather than +100% or Infinity on a zero baseline, and that distinction
 *  is the honest one: "first activity in this window" is a different statement
 *  from "up 100%", and printing the second for the first is how a chart lies
 *  without any of its numbers being wrong. */
export function deltaPct(now: number, before: number): number | null {
  if (before <= 0) return null
  return (now - before) / before
}

/** "+24%" / "-8%" / "155x" / "no change". Signed, because the sign is the
 *  message.
 *
 *  Past a ten-fold rise a percentage stops being read: "+15418%" is a number
 *  nobody converts in their head, and it reads as a bug rather than as a fact
 *  about a machine that was idle last month. A multiplier is the same figure in
 *  a form a person can hold. */
export function signedPct(f: number): string {
  const p = f * 100
  if (Math.abs(p) < 0.5) return 'no change'
  if (f >= 10) {
    const x = 1 + f
    return (x >= 100 ? x.toFixed(0) : x.toFixed(1)) + 'x'
  }
  const s = Math.abs(p) >= 10 ? p.toFixed(0) : p.toFixed(1)
  return (p > 0 ? '+' : '') + s + '%'
}

/** The earliest day the report has anything for, or '' when it has nothing. */
export function firstDay(report: UsageReport | null): string {
  let min = ''
  for (const d of report?.days ?? []) if (!min || d.day < min) min = d.day
  return min
}

/** Whether a comparison against the preceding period is honest.
 *
 *  It is not, when the records do not reach back that far. kunai started
 *  recording on some day, and a window straddling that day is compared against
 *  a period that is empty because nothing was WRITTEN then, not because nothing
 *  happened -- so the page reported a 155-fold rise in spend on a machine whose
 *  habits had not changed at all. The delta is withheld rather than qualified:
 *  a wrong number with a footnote is still the number people quote. */
export function comparable(
  report: UsageReport | null,
  days: number,
  today = new Date(),
): boolean {
  const first = firstDay(report)
  if (!first) return false
  const prior = priorWindow(report, days, today)
  return prior.length > 0 && first <= prior[0].day
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
  if (p === 0) return '0%' // exact, not a clamp: "0.0%" implies a rounding that did not happen
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
