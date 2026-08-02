import { setUnauthorizedHandler } from './api'

// Whether this device has to sign in, and the call that does it.
//
// The state is discovered rather than configured: nothing asks the server "is
// there a lock?" on startup. The first request that comes back 401 turns the
// screen on. That is what keeps the PIN out of the way on loopback and on the
// tailnet, where there is no gate and a sign-in prompt would be a lie.
class LanPin {
  /** True once the server has refused a request for want of a session. */
  required = $state(false)
  /** How long the throttle says to wait, in ms. Zero when not locked out. */
  retryAfterMs = $state(0)
  busy = $state(false)
  error = $state('')

  constructor() {
    setUnauthorizedHandler(() => {
      this.required = true
    })
  }

  /** A hint about which device this is, for the owner's sign-out list. */
  private deviceLabel(): string {
    const ua = navigator.userAgent
    for (const [needle, name] of [
      ['iPhone', 'iPhone'],
      ['iPad', 'iPad'],
      ['Android', 'Android'],
      ['Macintosh', 'Mac'],
      ['Windows', 'Windows'],
      ['Linux', 'Linux'],
    ] as const) {
      if (ua.includes(needle)) return name
    }
    return 'device'
  }

  async signIn(pin: string): Promise<boolean> {
    if (this.busy) return false
    this.busy = true
    this.error = ''
    try {
      const res = await fetch('/api/lan/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ pin, label: this.deviceLabel() }),
      })
      const body = await res.json().catch(() => ({}))
      if (res.status === 429) {
        this.retryAfterMs = body.retry_after_ms ?? 60_000
        this.error = ''
        return false
      }
      if (!res.ok) {
        // Deliberately vague, matching the server: it does not say whether the
        // PIN was wrong or whether one is even set, and neither should this.
        this.error = 'That PIN was not accepted.'
        return false
      }
      this.retryAfterMs = 0
      this.required = false
      // A full reload rather than patching state: everything fetched before the
      // gate opened failed, so the cheapest correct thing is to start again.
      location.reload()
      return true
    } catch {
      this.error = 'Could not reach kunai.'
      return false
    } finally {
      this.busy = false
    }
  }

  /** Counts the lockout down so the screen can say when to try again. */
  tick(ms: number) {
    if (this.retryAfterMs > 0) this.retryAfterMs = Math.max(0, this.retryAfterMs - ms)
  }
}

export const lanPin = new LanPin()
