import { DEFAULT_MODEL, DEFAULT_EFFORT } from './models'
import type {
  AppEvent,
  Attachment,
  Block,
  Command,
  PermissionMode,
  SessionState,
  ToolResult,
} from './types'

export type Item =
  | { role: 'user'; text: string }
  | { role: 'assistant'; blocks: Block[]; durationMs?: number; tokens?: number; costUsd?: number }

export interface PendingPermission {
  request_id: string
  tool_name: string
  input: unknown
  perm_title?: string
  description?: string
}

export type ConnStatus = 'connecting' | 'online' | 'offline'

// ChatConnection owns one session's live view. It survives socket drops: on
// reconnect it asks the server for everything after the last seq it saw, so a
// backgrounded phone rejoins without losing or duplicating messages.
export class ChatConnection {
  items = $state<Item[]>([])
  streaming = $state('')
  thinking = $state('')
  pending = $state<PendingPermission[]>([])
  // Tool outputs keyed by tool_use_id, looked up by each tool_use block.
  toolResults = $state<Record<string, ToolResult>>({})
  status = $state<ConnStatus>('connecting')
  // Flips true once the initial backlog has fully arrived (lastSeq caught up to
  // the hello's high_seq). The view waits for this before mounting history, so a
  // long conversation appears in one paint at the bottom instead of streaming in
  // from the top. Stays true across reconnects (they only replay a small gap).
  ready = $state(false)
  sessionState = $state<SessionState>('idle')
  mode = $state<PermissionMode>('default')
  // Seed model/effort to the app defaults so the composer shows a real label
  // (Opus 4.8 / High) immediately, before the hello frame lands. The server now
  // always sends a concrete model/effort, but keep the guard below so an empty
  // field can never blank the label back to the generic "Model"/"Effort".
  effort = $state(DEFAULT_EFFORT)
  cwd = $state('')
  model = $state(DEFAULT_MODEL)
  title = $state('')
  // Tokens occupying the context window, from the latest turn's result. 0 until
  // the first turn completes (a fresh or resumed session has none reported yet).
  // Drives the composer's context meter and updates live on every result.
  contextTokens = $state(0)
  errorLine = $state('')
  // Latest usage-window status from the CLI; drives the in-chat "schedule after
  // reset". limited is true when the last turn was rejected for quota.
  rateLimit = $state<{ window: string; resetsAt: number; limited: boolean } | null>(null)

  private ws?: WebSocket
  private lastSeq = 0
  private highSeq = 0 // last seq the server had buffered when we attached
  private retries = 0
  private closed = false
  private reconnectTimer?: ReturnType<typeof setTimeout>

  // base is the owning machine's origin ('' = this origin / hub).
  constructor(
    private base: string,
    private id: string,
  ) {
    this.connect()
  }

  // origin is the machine this session lives on, for scoping uploads etc.
  get origin(): string {
    return this.base || location.origin
  }

  private connect() {
    this.status = this.retries === 0 ? 'connecting' : 'offline'
    const u = new URL(this.base || location.origin)
    const scheme = u.protocol === 'https:' ? 'wss' : 'ws'
    const url = `${scheme}://${u.host}/ws/app/${this.id}?since=${this.lastSeq}`
    const ws = new WebSocket(url)
    this.ws = ws

    ws.onopen = () => {
      this.retries = 0
      this.status = 'online'
    }
    ws.onmessage = (e) => {
      try {
        this.apply(JSON.parse(e.data) as AppEvent)
      } catch {
        /* ignore malformed frame */
      }
    }
    ws.onclose = () => {
      if (this.closed) return
      this.status = 'offline'
      this.scheduleReconnect()
    }
    ws.onerror = () => ws.close()
  }

  private scheduleReconnect() {
    clearTimeout(this.reconnectTimer)
    this.retries++
    const delay = Math.min(1000 * 2 ** this.retries, 12000) + Math.random() * 400
    this.reconnectTimer = setTimeout(() => this.connect(), delay)
  }

  private apply(ev: AppEvent) {
    // Dedupe: replayed backlog can overlap what we already applied.
    if (ev.seq && ev.seq <= this.lastSeq) return
    if (ev.seq) this.lastSeq = ev.seq

    switch (ev.t) {
      case 'hello':
        this.cwd = ev.cwd ?? this.cwd
        this.model = ev.model || this.model
        this.title = ev.title ?? this.title
        if (ev.state) this.sessionState = ev.state
        if (ev.mode) this.mode = ev.mode as PermissionMode
        if (ev.effort) this.effort = ev.effort
        for (const p of ev.pending ?? []) this.addPending(p)
        this.highSeq = ev.high_seq ?? 0
        if (this.highSeq === 0) this.ready = true // nothing to replay
        break
      case 'user':
        this.items = [...this.items, { role: 'user', text: ev.text ?? '' }]
        break
      case 'delta':
        this.streaming += ev.text ?? ''
        break
      case 'thinking':
        this.thinking += ev.text ?? ''
        break
      case 'assistant':
        this.items = [...this.items, { role: 'assistant', blocks: ev.blocks ?? [] }]
        this.streaming = ''
        this.thinking = ''
        break
      case 'permission':
        this.addPending(ev)
        break
      case 'permission_resolved':
        this.pending = this.pending.filter((p) => p.request_id !== ev.request_id)
        break
      case 'tool_result':
        if (ev.tool_use_id) {
          this.toolResults = {
            ...this.toolResults,
            [ev.tool_use_id]: {
              content: ev.content ?? '',
              isError: ev.is_error ?? false,
              truncated: ev.truncated ?? false,
            },
          }
        }
        break
      case 'result':
        this.streaming = ''
        this.thinking = ''
        if (ev.context_tokens != null) this.contextTokens = ev.context_tokens
        // Stamp the turn's last assistant message with its duration, tokens, and
        // cost so the per-turn footer can show them. Stop at the user message
        // that opened the turn (a turn with no assistant reply has nothing to
        // stamp).
        if (ev.duration_ms != null || ev.tokens != null || ev.cost_usd != null) {
          for (let k = this.items.length - 1; k >= 0; k--) {
            const it = this.items[k]
            if (it.role === 'user') break
            if (it.role === 'assistant') {
              this.items = [
                ...this.items.slice(0, k),
                { ...it, durationMs: ev.duration_ms, tokens: ev.tokens, costUsd: ev.cost_usd },
                ...this.items.slice(k + 1),
              ]
              break
            }
          }
        }
        break
      case 'state':
        if (ev.state) this.sessionState = ev.state
        break
      case 'rate_limit':
        if (ev.resets_at) {
          this.rateLimit = {
            window: ev.window ?? 'five_hour',
            resetsAt: ev.resets_at,
            // Anything other than "allowed" means the window is exhausted/limited.
            limited: !!ev.limit_status && ev.limit_status !== 'allowed',
          }
        }
        break
      case 'error':
        this.errorLine = ev.message ?? 'error'
        // A rate-limit-flavored error also flips the banner on, in case the CLI
        // signals the wall via an error rather than a rejected status.
        if (/rate.?limit|usage limit|quota/i.test(ev.message ?? '') && this.rateLimit) {
          this.rateLimit = { ...this.rateLimit, limited: true }
        }
        break
    }

    // Backlog fully drained: the initial history is now all present, so the view
    // can mount it in one pass and pin to the bottom.
    if (!this.ready && this.highSeq > 0 && this.lastSeq >= this.highSeq) this.ready = true
  }

  private addPending(ev: AppEvent) {
    if (!ev.request_id) return
    if (this.pending.some((p) => p.request_id === ev.request_id)) return
    this.pending = [
      ...this.pending,
      {
        request_id: ev.request_id,
        tool_name: ev.tool_name ?? 'tool',
        input: ev.input,
        perm_title: ev.perm_title,
        description: ev.description,
      },
    ]
  }

  private send(cmd: Command) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(cmd))
  }

  sendPrompt(text: string, attachments: Attachment[] = []) {
    const t = text.trim()
    if (!t && attachments.length === 0) return
    this.send({ t: 'prompt', text: t, attachments: attachments.length ? attachments : undefined })
  }

  resolve(
    request_id: string,
    behavior: 'allow' | 'deny',
    always = false,
    answers?: Record<string, string>,
  ) {
    // Optimistically clear; the server also emits permission_resolved.
    this.pending = this.pending.filter((p) => p.request_id !== request_id)
    this.send({ t: 'permission', request_id, behavior, always, answers })
  }

  interrupt() {
    this.send({ t: 'interrupt' })
  }

  setMode(mode: PermissionMode) {
    this.mode = mode
    this.send({ t: 'set_mode', mode })
  }

  // switch the model for subsequent turns (optimistic; the CLI applies it live).
  setModel(model: string) {
    this.model = model
    this.send({ t: 'set_model', model })
  }

  destroy() {
    this.closed = true
    clearTimeout(this.reconnectTimer)
    this.ws?.close()
  }
}
