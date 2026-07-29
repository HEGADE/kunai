// Snoozing a session: park it on a shelf until a time, unless it needs you
// sooner.
//
// The design is t3code's (whose sidebar this borrows from), and the part worth
// keeping exactly is the wake rule: a snooze is "not now", never "not even if
// you're stuck". A snoozed session that stops to ask permission, or finishes a
// turn, has raised its hand, and the shelf lets it back out early -- otherwise
// snoozing an overnight agent would be how you miss the one click it needed at
// 2am. The row comes back at its ordinary position (the sidebar's sort is
// static), so a Woke pill carries the signal the position change would have.
//
// Pure and free of Svelte so the rules can be read in one place; the sidebar
// only renders what these return.

// Snoozeable is what the rules need to know, shared by live sessions and
// history entries. All times unix ms.
export interface Snoozeable {
  snoozed_until?: number
  snoozed_at?: number
  // Live sessions only; absent on history entries, which can never raise a hand.
  state?: string
  turn_ended_at?: number
}

// raisedHand reports the session asking to come off the shelf early: it is
// blocked on you, or it finished work after you parked it.
export function raisedHand(s: Snoozeable): boolean {
  if (s.state === 'awaiting_permission') return true
  return !!s.turn_ended_at && !!s.snoozed_at && s.turn_ended_at > s.snoozed_at
}

// isSnoozed reports whether the row belongs on the snoozed shelf right now:
// snoozed, not yet due, and not raising its hand.
export function isSnoozed(s: Snoozeable, nowMs: number): boolean {
  return !!s.snoozed_until && s.snoozed_until > nowMs && !raisedHand(s)
}

// isWoke reports a row that came back from a snooze -- the timer ran out or the
// hand went up -- and has not been visited since. The snooze record itself is
// what carries the pill; opening the session clears the record and with it the
// pill.
export function isWoke(s: Snoozeable, nowMs: number): boolean {
  return !!s.snoozed_until && !isSnoozed(s, nowMs)
}

// snoozeIn is the countdown a shelved row shows: the return ticket is the row's
// whole story. Coarse like workedFor, because nobody reads a shelf to the
// minute.
export function snoozeIn(untilMs: number, nowMs: number): string {
  const mins = Math.max(1, Math.round((untilMs - nowMs) / 60_000))
  if (mins < 60) return `${mins}m`
  const hours = Math.round(mins / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.round(hours / 24)}d`
}

export interface SnoozePreset {
  label: string
  until: number // unix ms
}

// snoozePresets are the four times worth offering, resolved against now (so
// they are computed when the menu opens, not when the row mounted). "This
// evening" is suppressed when it is less than an hour away or already past;
// day arithmetic goes through setDate rather than adding 86400000ms so a DST
// boundary still lands on 9am, not 8 or 10.
export function snoozePresets(now: Date): SnoozePreset[] {
  const out: SnoozePreset[] = []
  out.push({ label: 'In 1 hour', until: now.getTime() + 3600_000 })

  const evening = new Date(now)
  evening.setHours(18, 0, 0, 0)
  if (evening.getTime() - now.getTime() > 3600_000) {
    out.push({ label: 'This evening', until: evening.getTime() })
  }

  const tomorrow = new Date(now)
  tomorrow.setDate(tomorrow.getDate() + 1)
  tomorrow.setHours(9, 0, 0, 0)
  out.push({ label: 'Tomorrow 9am', until: tomorrow.getTime() })

  const nextWeek = new Date(now)
  // Days until next Monday; a Monday goes to the following one.
  const days = ((8 - nextWeek.getDay()) % 7) || 7
  nextWeek.setDate(nextWeek.getDate() + days)
  nextWeek.setHours(9, 0, 0, 0)
  out.push({ label: 'Next week', until: nextWeek.getTime() })

  return out
}
