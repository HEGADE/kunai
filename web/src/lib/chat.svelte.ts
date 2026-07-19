import { fetchOlderTurns } from './api'
import { DEFAULT_MODEL, DEFAULT_EFFORT } from './models'
import type {
  AppEvent,
  Attachment,
  Block,
  Command,
  LoopConfig,
  LoopStatus,
  PermissionMode,
  SessionState,
  ProjectInfo,
  ToolResult,
} from './types'

export type Item =
  | { role: 'user'; text: string; attachments?: Attachment[] }
  | { role: 'project'; project: ProjectInfo }
  | { role: 'compact'; preTokens: number; postTokens: number; trigger: string }
  // A moment in the loop's life: it started, it went round again, or it ended.
  // Each is a snapshot, so the log reads correctly however late you arrived.
  | { role: 'loop'; loop: LoopStatus }
  | {
      role: 'assistant'
      blocks: Block[]
      durationMs?: number
      tokens?: number
      newTokens?: number
      cachedTokens?: number
      outputTokens?: number
      costUsd?: number
    }

// A prompt waiting for the running turn to finish. The queue is the server's,
// not ours: it survives a dropped socket and runs without a client attached.
export interface QueuedPrompt {
  queue_id: string
  text: string
  attachments?: Attachment[]
}

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
  queued = $state<QueuedPrompt[]>([])
  // Every codebase this session has context for. More than one makes it a
  // workspace; the header says so.
  projects = $state<ProjectInfo[]>([])
  // The session's self-prompting run. Null until one is ever started, and it
  // keeps its final state afterwards so the log can say how it ended.
  loop = $state<LoopStatus | null>(null)
  // Tool outputs keyed by tool_use_id, looked up by each tool_use block.
  toolResults = $state<Record<string, ToolResult>>({})
  status = $state<ConnStatus>('connecting')
  // Flips true once the initial backlog has fully arrived (lastSeq caught up to
  // the hello's high_seq). The view waits for this before mounting history, so a
  // long conversation appears in one paint at the bottom instead of streaming in
  // from the top. Stays true across reconnects (they only replay a small gap).
  ready = $state(false)
  sessionState = $state<SessionState>('idle')
  // Sessions start in auto (see session.DefaultPermissionMode); seed it so the
  // composer doesn't flash "Ask" before the hello frame confirms it.
  mode = $state<PermissionMode>('auto')
  // Seed model/effort to the app defaults so the composer shows a real label
  // (Opus 4.8 / High) immediately, before the hello frame lands. The server now
  // always sends a concrete model/effort, but keep the guard below so an empty
  // field can never blank the label back to the generic "Model"/"Effort".
  effort = $state(DEFAULT_EFFORT)
  cli = $state('') // which Claude account this session runs on
  cwd = $state('')
  model = $state(DEFAULT_MODEL)
  title = $state('')
  // Tokens occupying the context window, from the newest model call's usage. 0
  // until the first turn produces an assistant message (a fresh or resumed
  // session may have none reported yet). Drives the composer's context meter and
  // updates live on every assistant frame, on compaction, and from hello on
  // (re)attach, so a foregrounded client shows the current fill, not a stale one.
  contextTokens = $state(0)
  errorLine = $state('')
  // Latest usage-window status from the CLI; drives the in-chat "schedule after
  // reset". limited is true when the last turn was rejected for quota.
  rateLimit = $state<{ window: string; resetsAt: number; limited: boolean } | null>(null)

  // Reverse scroll: the transcript byte offset older-than-seed history begins
  // before (from hello). >0 means there are older turns on disk to page in when
  // the log is scrolled to the top. loadingOlder guards against overlapping pages.
  histBefore = $state(0)
  loadingOlder = $state(false)
  get hasMoreHistory(): boolean {
    return this.histBefore > 0
  }

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
    // A backgrounded phone's socket dies silently; the meter and chat then sit on
    // whatever value they last saw (a just-compacted session shows its ~12k
    // post-compaction size) until the reconnect backoff happens to fire. Snap back
    // the moment we return to the foreground or the network comes back, so hello
    // re-seeds the real state (context, queue, state) at once instead of after a
    // wait. Idempotent: since=lastSeq means a live socket replays an empty gap.
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', this.revalidate)
      window.addEventListener('online', this.revalidate)
      window.addEventListener('focus', this.revalidate)
    }
  }

  // Force an immediate reconnect if the socket is not healthy, bypassing the
  // backoff. A CONNECTING socket is left alone; an OPEN one is assumed live (a
  // dead one fires onclose on resume, which routes here via the CLOSED branch).
  private revalidate = () => {
    if (this.closed) return
    if (document.visibilityState === 'hidden') return
    const rs = this.ws?.readyState
    if (rs === WebSocket.OPEN || rs === WebSocket.CONNECTING) return
    clearTimeout(this.reconnectTimer)
    this.retries = 0
    this.connect()
  }

  // origin is the machine this session lives on, for scoping uploads etc.
  get origin(): string {
    return this.base || location.origin
  }

  // sessionId is this session's id on its machine, for review/diff calls.
  get sessionId(): string {
    return this.id
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
        if (ev.cli) this.cli = ev.cli
        if (ev.context_tokens != null) this.contextTokens = ev.context_tokens
        for (const p of ev.pending ?? []) this.addPending(p)
        for (const q of ev.queued ?? []) this.addQueued(q)
        if (ev.projects?.length) this.projects = ev.projects
        if (ev.loop) this.loop = ev.loop
        this.histBefore = ev.hist_before ?? 0
        this.highSeq = ev.high_seq ?? 0
        if (this.highSeq === 0) this.ready = true // nothing to replay
        break
      case 'user':
        this.items = [...this.items, { role: 'user', text: ev.text ?? '', attachments: ev.attachments }]
        break
      case 'delta':
        this.streaming += ev.text ?? ''
        break
      case 'thinking':
        this.thinking += ev.text ?? ''
        break
      case 'assistant':
        this.items = [...this.items, { role: 'assistant', blocks: ev.blocks ?? [] }]
        // Each assistant message reports the context sent for that model call, so
        // the meter tracks the newest one (and stays live through a long turn).
        // Only while live, never during replay: seeded history carries no usage on
        // assistant turns, so letting a replayed compaction below drive the meter
        // would strand it at that compaction's post_tokens. hello is the truth on
        // attach; the backlog is for the log, not the meter.
        if (this.ready && ev.context_tokens != null) this.contextTokens = ev.context_tokens
        this.streaming = ''
        this.thinking = ''
        break
      case 'permission':
        this.addPending(ev)
        break
      case 'permission_resolved':
        this.pending = this.pending.filter((p) => p.request_id !== ev.request_id)
        break
      case 'queued':
        this.addQueued(ev)
        break
      case 'project':
        if (ev.project) {
          this.items = [...this.items, { role: 'project', project: ev.project }]
          if (!this.projects.some((p) => p.path === ev.project!.path)) {
            this.projects = [...this.projects, ev.project]
          }
        }
        break
      case 'unqueued':
        // It either started running (a 'user' event follows) or was cancelled.
        this.queued = this.queued.filter((q) => q.queue_id !== ev.queue_id)
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
      case 'loop':
        // Every change to the loop arrives whole, so the bar reads from one
        // object and the log keeps the snapshot as its own moment in the story.
        if (ev.loop) {
          this.items = [...this.items, { role: 'loop', loop: ev.loop }]
          // A seam is a lap recovered from a transcript on resume: the loop that
          // ran it is long gone, so it marks the log but never drives the bar.
          if (ev.loop.state !== 'seam') this.loop = ev.loop
        }
        break
      case 'compact':
        // The conversation was summarised, so the context window shrank. A live
        // compaction is the only frame that reports the new size (no assistant
        // message follows it), so it must drive the meter. A *replayed* one is
        // history: it is followed in the transcript by turns that regrew the
        // window, but those seeded turns carry no usage, so honouring a replayed
        // compaction would pin the meter at its post_tokens (the resumed-session
        // bug). Guard on ready: hello already carries the true current size.
        if (this.ready && ev.context_tokens != null) this.contextTokens = ev.context_tokens
        this.items = [
          ...this.items,
          {
            role: 'compact',
            preTokens: ev.pre_tokens ?? 0,
            // The divider shows the raw conversation-only post size (matching the
            // CLI banner); context_tokens is the meter value with overhead added.
            postTokens: ev.post_tokens ?? ev.context_tokens ?? 0,
            trigger: ev.trigger ?? 'manual',
          },
        ]
        break
      case 'result':
        this.streaming = ''
        this.thinking = ''
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
                {
                  ...it,
                  durationMs: ev.duration_ms,
                  tokens: ev.tokens,
                  newTokens: ev.new_tokens,
                  cachedTokens: ev.cached_tokens,
                  outputTokens: ev.output_tokens,
                  costUsd: ev.cost_usd,
                },
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
      case 'mode':
        // The server changes this too, not just the picker: a loop borrows
        // acceptEdits while it runs and gives the mode back when it ends.
        if (ev.mode) this.mode = ev.mode as PermissionMode
        break
      case 'rate_limit':
        if (ev.resets_at) {
          this.rateLimit = {
            window: ev.window ?? 'five_hour',
            resetsAt: ev.resets_at,
            // Only a hard "rejected" means the window is spent. "allowed_warning"
            // is the CLI approaching the limit (e.g. 91%), not a wall, so it must
            // not raise the "rate-limited" banner.
            limited:
              !!ev.limit_status &&
              ev.limit_status !== 'allowed' &&
              ev.limit_status !== 'allowed_warning',
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

  // itemFromEvent maps a seeded/paged history event to a log item, mirroring the
  // live cases in apply() so paged-in older turns render identically. A
  // tool_result is applied to the results map by the caller, not returned here.
  private itemFromEvent(ev: AppEvent): Item | null {
    switch (ev.t) {
      case 'user':
        return { role: 'user', text: ev.text ?? '', attachments: ev.attachments }
      case 'assistant':
        return { role: 'assistant', blocks: ev.blocks ?? [] }
      case 'project':
        return ev.project ? { role: 'project', project: ev.project } : null
      case 'loop':
        return ev.loop ? { role: 'loop', loop: ev.loop } : null
      case 'compact':
        return {
          role: 'compact',
          preTokens: ev.pre_tokens ?? 0,
          postTokens: ev.post_tokens ?? ev.context_tokens ?? 0,
          trigger: ev.trigger ?? 'manual',
        }
      default:
        return null
    }
  }

  // loadOlder pages in the turns just older than what is loaded (reverse scroll):
  // it fetches the previous transcript slice, prepends it older-first, and advances
  // the cursor. Returns how many items were prepended so the view can hold its
  // scroll position across the growth.
  async loadOlder(): Promise<number> {
    if (this.histBefore <= 0 || this.loadingOlder) return 0
    this.loadingOlder = true
    try {
      const res = await fetchOlderTurns(this.base, this.id, this.histBefore)
      const older: Item[] = []
      for (const ev of res.events) {
        if (ev.t === 'tool_result') {
          if (ev.tool_use_id)
            this.toolResults = {
              ...this.toolResults,
              [ev.tool_use_id]: {
                content: ev.content ?? '',
                isError: ev.is_error ?? false,
                truncated: ev.truncated ?? false,
              },
            }
          continue
        }
        const it = this.itemFromEvent(ev)
        if (it) older.push(it)
      }
      this.items = [...older, ...this.items]
      this.histBefore = res.older ?? 0
      return older.length
    } catch {
      return 0
    } finally {
      this.loadingOlder = false
    }
  }

  // Deduped by id: hello carries the queue and the replayed backlog repeats it.
  private addQueued(ev: AppEvent) {
    if (!ev.queue_id) return
    if (this.queued.some((q) => q.queue_id === ev.queue_id)) return
    this.queued = [
      ...this.queued,
      { queue_id: ev.queue_id, text: ev.text ?? '', attachments: ev.attachments },
    ]
  }

  // addProject hands another codebase to this session. The server scans it and
  // gives the model a description; the reply is a project card in the chat.
  addProject(path: string) {
    this.send({ t: 'add_project', path })
  }

  // startLoop hands the task to the session, which re-feeds it every time a turn
  // ends. The loop lives on the server, so closing this tab does not stop it and
  // the limits keep applying whether anyone is watching or not.
  startLoop(loop: LoopConfig) {
    this.send({ t: 'start_loop', loop })
  }

  stopLoop() {
    this.send({ t: 'stop_loop' })
  }

  cancelQueued(queue_id: string) {
    this.queued = this.queued.filter((q) => q.queue_id !== queue_id) // optimistic
    this.send({ t: 'cancel_queued', queue_id })
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
    if (typeof document !== 'undefined') {
      document.removeEventListener('visibilitychange', this.revalidate)
      window.removeEventListener('online', this.revalidate)
      window.removeEventListener('focus', this.revalidate)
    }
    this.ws?.close()
  }
}
