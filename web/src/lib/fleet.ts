// One socket per machine, carrying what the client used to fetch on a timer.
//
// The poll loop asked every machine for its session list every few seconds. That
// was wrong twice over: too slow, because the sidebar reports what each agent is
// doing and a status board that lags by up to eight seconds will tell you an
// agent is working after it stopped to ask you something; and too much, because
// the answer usually had not changed and the cost was a request per machine per
// tick, growing with the size of the fleet rather than with how much was going on.
//
// This does not replace the REST endpoints. They remain the way the app loads and
// the way it recovers: the socket is an accelerator, and everything still works
// with it disconnected, just at the old cadence. That is deliberate, because a
// machine can be reachable for HTTP and not for WebSocket (a proxy, an old
// server without the route) and the fleet must not go dark when it is.

import type { Meta, Stats } from './types'

// Mirrors fleetMsg in internal/server/fleet.go; keep the two in step by hand, as
// with the rest of the wire contract.
export interface FleetMsg {
  t: 'sessions' | 'stats'
  sessions?: Meta[]
  stats?: Stats
}

export interface FleetHandlers {
  onSessions: (sessions: Meta[]) => void
  onStats: (stats: Stats) => void
  // onOpen and onClose let the caller keep polling only while the push is down,
  // which is what makes this an accelerator rather than a second source of truth.
  onOpen?: () => void
  onClose?: () => void
}

// Reconnect backoff. Starts fast because the common disconnect is a laptop lid or
// a phone unlocking, where the machine is already there; grows so an unreachable
// peer is not hammered.
const RETRY_MIN = 1_000
const RETRY_MAX = 30_000

export class FleetSocket {
  private ws: WebSocket | null = null
  private retry = RETRY_MIN
  private timer: ReturnType<typeof setTimeout> | undefined
  private stopped = false
  private readonly url: string

  constructor(
    base: string,
    private handlers: FleetHandlers,
  ) {
    // base is '' for the hub (this origin) and a full origin for a peer, the same
    // convention every call in api.ts uses.
    const origin = base || location.origin
    this.url = origin.replace(/^http/, 'ws') + '/ws/fleet'
  }

  start() {
    this.stopped = false
    this.open()
  }

  stop() {
    this.stopped = true
    clearTimeout(this.timer)
    // Close the socket without waiting for a reply: this runs when a machine is
    // removed or the app tears down, and there is nothing to say.
    this.ws?.close()
    this.ws = null
  }

  get connected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN
  }

  private open() {
    if (this.stopped) return
    let ws: WebSocket
    try {
      ws = new WebSocket(this.url)
    } catch {
      // A malformed origin, which is a permanent failure rather than a blip: back
      // off to the maximum instead of retrying in a second forever.
      this.retry = RETRY_MAX
      this.schedule()
      return
    }
    this.ws = ws

    ws.onopen = () => {
      this.retry = RETRY_MIN
      this.handlers.onOpen?.()
    }
    ws.onmessage = (e) => {
      let msg: FleetMsg
      try {
        msg = JSON.parse(e.data as string)
      } catch {
        return
      }
      if (msg.t === 'sessions' && msg.sessions) this.handlers.onSessions(msg.sessions)
      else if (msg.t === 'stats' && msg.stats) this.handlers.onStats(msg.stats)
    }
    // One handler for both: onerror is always followed by onclose, and reconnecting
    // from each would open two sockets.
    ws.onclose = () => {
      if (this.ws !== ws) return // superseded by a newer socket
      this.ws = null
      this.handlers.onClose?.()
      this.schedule()
    }
    ws.onerror = () => {
      /* onclose follows; nothing to do here */
    }
  }

  private schedule() {
    if (this.stopped) return
    clearTimeout(this.timer)
    this.timer = setTimeout(() => this.open(), this.retry)
    this.retry = Math.min(this.retry * 2, RETRY_MAX)
  }
}
