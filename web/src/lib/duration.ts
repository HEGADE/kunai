// Composing and describing how long a share link lives.
//
// Pure, and separate from the dialog, because two of these are rules rather than
// formatting: the bounds have to match what the server enforces, and the wording
// of an expiry is the only part of the picker a person actually reasons about.

/** Matches share.MinTTL / share.MaxTTL in Go. The server clamps too; this is so
 *  the dialog never shows a number the server would quietly change. */
export const MIN_TTL = 5 * 60
export const MAX_TTL = 5 * 24 * 60 * 60

/** clampTTL brings a composed duration inside the allowed range. */
export function clampTTL(secs: number): number {
  if (!Number.isFinite(secs)) return MIN_TTL
  return Math.max(MIN_TTL, Math.min(MAX_TTL, Math.round(secs)))
}

/** splitDuration turns seconds into the days/hours/minutes the picker composes,
 *  so an existing share can be edited in the same units it was created in. */
export function splitDuration(secs: number): { days: number; hours: number; mins: number } {
  const s = Math.max(0, Math.round(secs))
  return {
    days: Math.floor(s / 86400),
    hours: Math.floor((s % 86400) / 3600),
    mins: Math.floor((s % 3600) / 60),
  }
}

/**
 * expiryWords says when a link dies, as an instant rather than an interval.
 *
 * "5 days" is abstract and has to be added to the current time in your head
 * before it means anything. "Saturday at 22:40" is the thing you can check
 * against your week, which is what people are actually deciding: does this
 * outlive the meeting, does it die before I go away.
 *
 * Today and tomorrow are named rather than dated, because within that range the
 * date is the part you have to translate.
 */
export function expiryWords(atMs: number, nowMs: number = Date.now()): string {
  const at = new Date(atMs)
  const now = new Date(nowMs)
  if (atMs <= nowMs) return 'expired'

  const time = at.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  const midnight = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
  const dayGap = Math.round((midnight(at) - midnight(now)) / 86400000)

  if (dayGap === 0) return `expires today at ${time}`
  if (dayGap === 1) return `expires tomorrow at ${time}`
  // Within the week ahead, the weekday is more useful than the date.
  if (dayGap < 7) {
    return `expires ${at.toLocaleDateString(undefined, { weekday: 'long' })} at ${time}`
  }
  return `expires ${at.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })} at ${time}`
}
