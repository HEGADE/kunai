// The guest's connection to a shared session.
//
// This is deliberately NOT ChatConnection. That class knows how to switch
// accounts, start loops, add projects, answer permission asks and reach any
// machine on the fleet; none of it is available here, and a subclass that
// disabled things one by one would be a list somebody has to keep complete. This
// holds only what a guest can do, which is read, send, and stop their own turn.

import type { AppEvent, Block, LoopStatus, SessionState, ToolResult } from './types'

// Item is one thing in the log, the same shape the owner's client renders so the
// components can be shared.
export type Item =
  | { role: 'user'; text: string; seq?: number; from?: string }
  | { role: 'assistant'; blocks: Block[]; seq?: number }
  | { role: 'compact'; pre?: number; post?: number; seq?: number }
  | { role: 'loop'; loop?: LoopStatus; seq?: number }

// ShareInfo is the gate's hello: what this link is worth, before any socket.
export interface ShareInfo {
  title: string
  tier: 'view' | 'ask' | 'work'
  expires_at: number
  paired: boolean
  taken: boolean
  pair_code?: string
  turns_left?: number
  capped?: boolean
  live: boolean
}

const RETRY_MIN = 1_000
const RETRY_MAX = 20_000

// deviceKey is this browser's proof that it is the one the owner approved. It is
// generated here and kept in localStorage rather than issued by the server,
// because the server should not be handing out anything before somebody has said
// yes. Stored per token, so opening a second share does not inherit the first's
// pairing.
function deviceKey(token: string): string {
  const k = `kunai.share.device.${token}`
  let v = localStorage.getItem(k)
  if (!v) {
    const b = new Uint8Array(16)
    crypto.getRandomValues(b)
    v = btoa(String.fromCharCode(...b)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
    localStorage.setItem(k, v)
  }
  return v
}

export class GuestConnection {
  readonly token: string
  readonly device: string

  info = $state<ShareInfo | null>(null)
  items = $state<Item[]>([])
  streaming = $state('')
  thinking = $state('')
  toolResults = $state<Record<string, ToolResult>>({})
  sessionState = $state<SessionState>('idle')
  status = $state<'connecting' | 'open' | 'closed'>('connecting')
  // gone is set when the link itself stopped working (expired or revoked), which
  // is a different thing from the socket dropping and must not silently retry.
  gone = $state('')
  errorLine = $state('')
  contextTokens = $state(0)

  private ws?: WebSocket
  private lastSeq = 0
  private epoch = ''
  private retry = RETRY_MIN
  private timer?: ReturnType<typeof setTimeout>
  private stopped = false

  constructor(token: string) {
    this.token = token
    this.device = deviceKey(token)
    void this.load()
  }

  // load fetches what the link is worth. Called on open and after pairing, so the
  // page can go from "ask to join" to "you can send" without a reload.
  async load(): Promise<void> {
    try {
      const r = await fetch(`/api/share/${this.token}`, {
        headers: { 'X-Kunai-Device': this.device },
      })
      if (r.status === 404) {
        this.gone = 'This link is no longer live.'
        return
      }
      this.info = (await r.json()) as ShareInfo
      if (!this.ws) this.connect()
    } catch {
      // A failed hello is a transport problem, not a dead link; the socket's own
      // retry will surface it if it persists.
    }
  }

  // pair asks the owner for permission to send. Returns the code to read out.
  async pair(name: string): Promise<string> {
    const r = await fetch(`/api/share/${this.token}/pair`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Kunai-Device': this.device },
      body: JSON.stringify({ device: this.device, name }),
    })
    if (!r.ok) throw new Error((await r.text()) || 'Could not ask to join')
    const body = (await r.json()) as { code: string }
    await this.load()
    return body.code
  }

  // waitForApproval polls until the owner says yes. Polling rather than pushing
  // because the answer is a single event the guest is actively waiting for, and a
  // second socket for it would be more machinery than the wait deserves.
  async pollPaired(): Promise<boolean> {
    try {
      const r = await fetch(`/api/share/${this.token}/pair`, {
        headers: { 'X-Kunai-Device': this.device },
      })
      if (!r.ok) return false
      const { paired } = (await r.json()) as { paired: boolean }
      if (paired) await this.load()
      return paired
    } catch {
      return false
    }
  }

  get canSend(): boolean {
    return !!this.info?.paired && this.info.tier !== 'view' && !this.gone
  }

  send(text: string) {
    const t = text.trim()
    if (!t || !this.canSend) return
    this.ws?.send(JSON.stringify({ t: 'prompt', text: t }))
  }

  // stop aborts the turn this guest started. The server refuses it for anything
  // the owner started, which is why the button is only offered while our own
  // turn is running.
  stop() {
    this.ws?.send(JSON.stringify({ t: 'interrupt' }))
  }

  close() {
    this.stopped = true
    clearTimeout(this.timer)
    this.ws?.close()
    this.ws = undefined
  }

  private connect() {
    if (this.stopped || this.gone) return
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
    const url = `${scheme}://${location.host}/ws/share/${this.token}?since=${this.lastSeq}&device=${encodeURIComponent(this.device)}`
    let ws: WebSocket
    try {
      ws = new WebSocket(url)
    } catch {
      this.schedule()
      return
    }
    this.ws = ws
    ws.onopen = () => {
      this.status = 'open'
      this.retry = RETRY_MIN
    }
    ws.onmessage = (e) => {
      try {
        this.apply(JSON.parse(e.data as string) as AppEvent)
      } catch {
        /* a frame we cannot read is not worth tearing the socket down for */
      }
    }
    ws.onclose = (e) => {
      if (this.ws !== ws) return
      this.ws = undefined
      this.status = 'closed'
      // The server closes normally, with a reason, when the link dies. That is
      // final: retrying would just 404 forever behind a spinner.
      if (e.code === 1000 && e.reason) {
        this.gone = e.reason
        return
      }
      this.schedule()
    }
    ws.onerror = () => {
      /* onclose follows */
    }
  }

  private schedule() {
    if (this.stopped || this.gone) return
    clearTimeout(this.timer)
    this.timer = setTimeout(() => this.connect(), this.retry)
    this.retry = Math.min(this.retry * 2, RETRY_MAX)
  }

  private apply(ev: AppEvent) {
    if (ev.seq && ev.seq <= this.lastSeq) return
    if (ev.seq) this.lastSeq = ev.seq

    switch (ev.t) {
      case 'hello':
        // The session was respawned behind this id, so everything held came from
        // a process that no longer exists. See the epoch note in types.ts.
        if (ev.epoch && this.epoch && ev.epoch !== this.epoch) {
          this.lastSeq = 0
          this.items = []
          this.streaming = ''
          this.thinking = ''
          this.toolResults = {}
        }
        if (ev.epoch) this.epoch = ev.epoch
        if (ev.state) this.sessionState = ev.state
        if (ev.context_tokens != null) this.contextTokens = ev.context_tokens
        break
      case 'user':
        this.items = [...this.items, { role: 'user', text: ev.text ?? '', seq: ev.seq, from: ev.from }]
        break
      case 'delta':
        if (!ev.parent_tool_use_id) this.streaming += ev.text ?? ''
        break
      case 'thinking':
        if (!ev.parent_tool_use_id) this.thinking += ev.text ?? ''
        break
      case 'assistant':
        if (ev.blocks?.length) {
          this.items = [...this.items, { role: 'assistant', blocks: ev.blocks, seq: ev.seq }]
        }
        this.streaming = ''
        this.thinking = ''
        if (ev.context_tokens != null) this.contextTokens = ev.context_tokens
        break
      case 'tool_result':
        if (ev.tool_use_id) {
          this.toolResults = {
            ...this.toolResults,
            [ev.tool_use_id]: {
              content: ev.content ?? '',
              isError: !!ev.is_error,
              truncated: !!ev.truncated,
            },
          }
        }
        break
      case 'compact':
        this.items = [...this.items, { role: 'compact', pre: ev.pre_tokens, post: ev.post_tokens, seq: ev.seq }]
        if (ev.context_tokens != null) this.contextTokens = ev.context_tokens
        break
      case 'state':
        if (ev.state) this.sessionState = ev.state
        break
      case 'error':
        this.errorLine = ev.message ?? ev.text ?? ''
        break
    }
  }
}
