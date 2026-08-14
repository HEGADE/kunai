// Telling somebody something went wrong, where they will actually see it.
//
// This exists because of one specific failure. Posting a review is a button in
// the bar at the BOTTOM of the screen, and when it failed the reason was
// rendered as a line of red text at the end of the findings list -- inside the
// scroll area, above the bar, after however many findings there were. On a
// review of any length the message appeared somewhere the reader was not
// looking, and on a short one it read as part of the content rather than as an
// answer to what they had just done. "HEGADE/kunai#5 has moved on since this
// review" is the single most important sentence on that screen at that moment,
// and it was styled and placed like a footnote.
//
// The rules here are the ones that make a toast worth having rather than one
// more thing that flashes past:
//
//   - An ERROR does not auto-dismiss. It is the answer to something you asked
//     for, it usually names an action you now have to take, and a message that
//     removes itself while you are reading it is worse than no message.
//   - Anything else does, because a confirmation you have to dismiss is a chore
//     charged for the app being right.
//   - Identical messages collapse instead of stacking. Pressing a failing button
//     three times is one problem, not three.
//
// Held at module scope with ONE <Toast /> mounted per entry point, the same
// shape as lib/lightbox.svelte.ts and for the same reason: any component can
// raise one without every component owning a copy of the furniture.

export type ToastKind = 'error' | 'info' | 'done'

export interface Toast {
  id: number
  kind: ToastKind
  text: string
  // What to do about it, when there is something. A failure that names its own
  // remedy is worth far more than one that only names itself.
  action?: { label: string; run: () => void }
}

// How long a non-error stays. Long enough to read a sentence, short enough that
// it is gone before it becomes furniture.
const DWELL_MS = 4200

class Toasts {
  items = $state<Toast[]>([])
  private next = 1
  private timers = new Map<number, ReturnType<typeof setTimeout>>()

  /** Something failed. Stays until dismissed. */
  error(text: string, action?: Toast['action']) {
    return this.push('error', text, action)
  }

  /** Something worked. Goes away by itself. */
  done(text: string, action?: Toast['action']) {
    return this.push('done', text, action)
  }

  info(text: string, action?: Toast['action']) {
    return this.push('info', text, action)
  }

  dismiss(id: number) {
    const t = this.timers.get(id)
    if (t) clearTimeout(t)
    this.timers.delete(id)
    this.items = this.items.filter((x) => x.id !== id)
  }

  clear() {
    for (const t of this.timers.values()) clearTimeout(t)
    this.timers.clear()
    this.items = []
  }

  private push(kind: ToastKind, text: string, action?: Toast['action']): number {
    text = text.trim()
    if (!text) return 0
    // The same message twice is the same problem twice. Replaced rather than
    // stacked, so a button pressed three times leaves one line and not a column.
    const same = this.items.find((t) => t.text === text && t.kind === kind)
    if (same) {
      this.rearm(same)
      return same.id
    }

    const t: Toast = { id: this.next++, kind, text, action }
    this.items = [...this.items, t]
    this.rearm(t)
    return t.id
  }

  private rearm(t: Toast) {
    const existing = this.timers.get(t.id)
    if (existing) clearTimeout(existing)
    if (t.kind === 'error') return // see the note at the top: errors wait to be read
    this.timers.set(
      t.id,
      setTimeout(() => this.dismiss(t.id), DWELL_MS),
    )
  }
}

export const toasts = new Toasts()
