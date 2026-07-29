import { fetchOlderTurns, listCheckpoints, listSessions, revertPreview, revertTurn, undoRevert } from './api'
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
  | { role: 'user'; text: string; attachments?: Attachment[]; seq?: number }
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

// 'gone' means the session does not exist on the server any more, as opposed to
// 'offline', which means we cannot reach it right now.
//
// They looked identical before and behaved identically, which was wrong in the
// one case that actually happens: kunai restarting (a self update does exactly
// this) ends every ordinary session, and the tab left over from before then
// reconnected forever into a 404. It read as a network problem that never
// cleared, and every control on it failed silently, because there was nothing
// there to act on.
export type ConnStatus = 'connecting' | 'online' | 'offline' | 'gone'

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
  // What each subagent did, keyed by the Agent tool call that spawned it: the
  // blocks it produced (its own tool calls and text) in arrival order, plus the
  // text it is streaming right now. The CLI reports a subagent's whole inner life
  // tagged with its parent call, so this is real activity, not a reconstruction.
  agentBlocks = $state<Record<string, Block[]>>({})
  agentStreaming = $state<Record<string, string>>({})
  status = $state<ConnStatus>('connecting')
  // Flips true once the initial backlog has fully arrived (lastSeq caught up to
  // the hello's high_seq). The view waits for this before mounting history, so a
  // long conversation appears in one paint at the bottom instead of streaming in
  // from the top. Stays true across reconnects (they only replay a small gap).
  ready = $state(false)
  // Turns (keyed by their user-message Seq) that have a restorable pre-turn git
  // snapshot, so the changed-files card can offer a revert. Refreshed on ready and
  // after every turn (a new checkpoint is taken at each turn's start).
  checkpointSeqs = $state<number[]>([])
  // Turns reverted this session -> the safety ref captured before the revert, so the
  // card can offer a one-tap Undo.
  reverted = $state<Record<number, string>>({})
  // Resolvers waiting on the initial backlog (see whenReady).
  private readyWaiters: (() => void)[] = []
  sessionState = $state<SessionState>('idle')
  // When the running turn began (unix ms), 0 when nothing is running. Set from
  // the state frames rather than from Meta, because this socket is the fastest
  // thing that knows. Not cleared on awaiting_permission: that is the same turn
  // paused, and restarting the count on approval would make a long turn that
  // stopped to ask look like it had just begun.
  turnStartedAt = $state(0)
  // Sessions start in auto (see session.DefaultPermissionMode); seed it so the
  // composer doesn't flash "Ask" before the hello frame confirms it.
  mode = $state<PermissionMode>('auto')
  // Seed model/effort to the app defaults so the composer shows a real label
  // (Opus 4.8 / High) immediately, before the hello frame lands. The server now
  // always sends a concrete model/effort, but keep the guard below so an empty
  // field can never blank the label back to the generic "Model"/"Effort".
  effort = $state(DEFAULT_EFFORT)
  cli = $state('') // which Claude account this session runs on
  // Auto-failover's progress on this session: 'deciding' while it looks for an
  // account with headroom, else ''. A failover takes seconds (it reads each
  // candidate account's quota), and without this the composer names the walled
  // account throughout, which reads as a feature that never fired.
  failover = $state('')
  // Why a failover stopped without moving the session. Cleared when a new one
  // starts, or when the session is reattached.
  failoverNote = $state('')
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
  // Which incarnation of the session we are showing. A respawn (effort change,
  // account switch, auto-failover) replaces the process behind the same id and
  // restarts event numbering at 1, so what we are holding is a different
  // conversation's worth of state. See onEpochChange.
  private epoch = ''
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

  // apiBase is the owning machine's origin, for the REST calls a view makes
  // alongside this socket. Exposed read-only rather than making base public: a
  // session's machine is fixed at construction and nothing may reassign it.
  get apiBase(): string {
    return this.base
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
      // A rejected handshake and a dropped connection look the same here: the
      // browser does not expose the HTTP status of a failed WebSocket upgrade,
      // so a 404 for a session that no longer exists arrives as a plain close.
      // The difference matters, so it is asked over REST instead, and only after
      // a couple of failures, so an ordinary blip never spends a request.
      if (this.retries >= 2) void this.checkStillThere()
      this.scheduleReconnect()
    }
    ws.onerror = () => ws.close()
  }

  // checkStillThere asks whether the session exists at all, and stops retrying
  // when it does not.
  //
  // Deliberately quiet about everything else: if the machine cannot be reached
  // the list request fails too, and that is an offline machine rather than a
  // dead session, so it says nothing and lets the reconnect carry on.
  private async checkStillThere() {
    try {
      const live = await listSessions(this.base)
      if (this.closed || this.status === 'gone') return
      if (!live.some((m) => m.id === this.id)) {
        this.status = 'gone'
        clearTimeout(this.reconnectTimer)
      }
    } catch {
      /* the machine is unreachable, which says nothing about this session */
    }
  }

  private scheduleReconnect() {
    if (this.status === 'gone') return
    clearTimeout(this.reconnectTimer)
    this.retries++
    const delay = Math.min(1000 * 2 ** this.retries, 12000) + Math.random() * 400
    this.reconnectTimer = setTimeout(() => this.connect(), delay)
  }

  // Drop everything that belonged to the previous process behind this id, so the
  // backlog that follows this hello rebuilds the conversation instead of being
  // appended to a copy of it.
  //
  // The identity fields (cwd, model, mode, effort, cli, title) are deliberately
  // NOT cleared: the same hello that triggered this is about to overwrite them,
  // and blanking them first makes the header flicker through empty values.
  private resetForNewProcess() {
    this.lastSeq = 0
    this.items = []
    this.streaming = ''
    this.thinking = ''
    this.toolResults = {}
    this.agentBlocks = {}
    this.agentStreaming = {}
    this.pending = []
    this.queued = []
    this.checkpointSeqs = []
    this.reverted = {}
    this.errorLine = ''
    // The log has to re-mount at the bottom of the rebuilt backlog rather than
    // hold the scroll position of a conversation that no longer exists.
    this.ready = false
  }

  private apply(ev: AppEvent) {
    // Dedupe: replayed backlog can overlap what we already applied.
    if (ev.seq && ev.seq <= this.lastSeq) return
    if (ev.seq) this.lastSeq = ev.seq

    switch (ev.t) {
      case 'hello':
        // A respawn puts a NEW process behind the same id, seeded with the same
        // conversation from the transcript. Everything we hold came from the dead
        // one, so keeping it would show the conversation twice, once from each.
        // The server already resends the whole backlog (it clamps our impossible
        // since), so dropping what we have is enough to rebuild cleanly.
        if (ev.epoch && this.epoch && ev.epoch !== this.epoch) this.resetForNewProcess()
        if (ev.epoch) this.epoch = ev.epoch
        this.cwd = ev.cwd ?? this.cwd
        this.model = ev.model || this.model
        this.title = ev.title ?? this.title
        if (ev.state) this.sessionState = ev.state
        if (ev.mode) this.mode = ev.mode as PermissionMode
        if (ev.effort) this.effort = ev.effort
        if (ev.cli) this.cli = ev.cli
        // Assigned rather than guarded: an attach mid-decision has to light this
        // up, and an attach after one has to clear it. A stale "moving you to
        // another account" is worse than none.
        this.failover = ev.failover_state === 'deciding' ? 'deciding' : ''
        if (this.failover) this.failoverNote = ''
        if (ev.context_tokens != null) this.contextTokens = ev.context_tokens
        for (const p of ev.pending ?? []) this.addPending(p)
        for (const q of ev.queued ?? []) this.addQueued(q)
        if (ev.projects?.length) this.projects = ev.projects
        if (ev.loop) this.loop = ev.loop
        this.histBefore = ev.hist_before ?? 0
        this.highSeq = ev.high_seq ?? 0
        if (this.highSeq === 0) this.markReady() // nothing to replay
        break
      case 'user':
        this.items = [
          ...this.items,
          { role: 'user', text: ev.text ?? '', attachments: ev.attachments, seq: ev.seq },
        ]
        break
      case 'delta':
        // A subagent's tokens belong under its Agent card, not in the main answer
        // (they used to stream into the reply as if the main agent had written them).
        if (ev.parent_tool_use_id) {
          this.appendAgentStream(ev.parent_tool_use_id, ev.text ?? '')
          break
        }
        this.streaming += ev.text ?? ''
        break
      case 'thinking':
        if (ev.parent_tool_use_id) break // the subagent's private reasoning; not the main turn's
        this.thinking += ev.text ?? ''
        break
      case 'assistant':
        // A nested frame is the subagent's own model call: file it under the Agent
        // call that spawned it. It is NOT a message in this conversation, and its
        // usage is a different context window, so it must not touch the meter.
        if (ev.parent_tool_use_id) {
          this.addAgentBlocks(ev.parent_tool_use_id, ev.blocks ?? [])
          break
        }
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
        // A turn that ran to a clean result is proof the window is not spent,
        // so the banner comes down (the client-side mirror of the server's
        // clearWall). An errored result keeps it: the error may BE the wall.
        if (!ev.is_error && this.rateLimit?.limited) {
          this.rateLimit = { ...this.rateLimit, limited: false }
        }
        // A turn just finished; its pre-turn checkpoint is now recorded, so the
        // changed-files card can offer to revert it.
        void this.refreshCheckpoints()
        break
      case 'state':
        if (ev.state) {
          // The turn clock, kept beside the state it belongs to. An open tab
          // learns a turn started the instant the socket says so, while the
          // polled list is up to a cycle behind, so the sidebar prefers this the
          // same way it already prefers this socket's state (see liveState).
          if (ev.state === 'running' && this.sessionState !== 'running') {
            this.turnStartedAt = Date.now()
          } else if (ev.state === 'idle') {
            this.turnStartedAt = 0
          }
          this.sessionState = ev.state
        }
        break
      case 'mode':
        // The server changes this too, not just the picker: a loop borrows
        // acceptEdits while it runs and gives the mode back when it ends.
        if (ev.mode) this.mode = ev.mode as PermissionMode
        break
      case 'rate_limit': {
        // Only a hard "rejected" means the window is spent. "allowed_warning"
        // is the CLI approaching the limit (e.g. 91%), not a wall, so it must
        // not raise the "rate-limited" banner.
        const limited =
          !!ev.limit_status &&
          ev.limit_status !== 'allowed' &&
          ev.limit_status !== 'allowed_warning'
        if (ev.resets_at) {
          this.rateLimit = { window: ev.window ?? 'five_hour', resetsAt: ev.resets_at, limited }
        } else if (!limited && this.rateLimit) {
          // An all-clear carries no reset time (there is no window to reset),
          // but it still has to lower the banner. This is the frame the server
          // sends when a turn completing proves the window is not spent.
          this.rateLimit = { ...this.rateLimit, limited: false }
        }
        break
      }
      case 'failover':
        if (ev.failover_state === 'deciding') {
          this.failover = 'deciding'
          this.failoverNote = ''
        } else {
          this.failover = ''
          // Only overwrite the note when this ending had something to say. A
          // stand-down (the user moved the session themselves) carries no
          // message, and inventing one would explain a thing they just did.
          if (ev.message) this.failoverNote = ev.message
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
    if (!this.ready && this.highSeq > 0 && this.lastSeq >= this.highSeq) this.markReady()
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
  // addAgentBlocks files a subagent's finished model call under the Agent tool call
  // that spawned it, and clears the live stream for that agent (the blocks are the
  // settled version of what was streaming).
  private addAgentBlocks(parentID: string, blocks: Block[]) {
    if (!blocks.length) return
    this.agentBlocks = {
      ...this.agentBlocks,
      [parentID]: [...(this.agentBlocks[parentID] ?? []), ...blocks],
    }
    if (this.agentStreaming[parentID]) {
      const next = { ...this.agentStreaming }
      delete next[parentID]
      this.agentStreaming = next
    }
  }

  // appendAgentStream accumulates a subagent's in-flight text so its progress is
  // visible while it works, instead of the turn looking stalled on one tool call.
  private appendAgentStream(parentID: string, text: string) {
    if (!text) return
    this.agentStreaming = {
      ...this.agentStreaming,
      [parentID]: (this.agentStreaming[parentID] ?? '') + text,
    }
  }

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

  private markReady() {
    this.ready = true
    this.releaseReadyWaiters()
    void this.refreshCheckpoints()
  }

  // refreshCheckpoints reloads which turns have a restorable snapshot. Best-effort:
  // a failure (non-git session, older server) just leaves the revert affordance off.
  async refreshCheckpoints() {
    try {
      const cps = await listCheckpoints(this.base, this.id)
      this.checkpointSeqs = cps.map((c) => c.seq)
    } catch {
      /* no checkpoints available */
    }
  }

  // previewRevert asks the server what reverting this turn would actually change.
  // The caller shows it before acting, because a revert resets the whole
  // repository and the turn's own file list understates that.
  previewRevert(seq: number) {
    return revertPreview(this.base, this.id, seq)
  }

  hasCheckpoint(seq: number | undefined): boolean {
    return seq != null && this.checkpointSeqs.includes(seq)
  }

  // revert undoes a turn's file changes (restores the working tree to the pre-turn
  // snapshot). Records the safety ref so the change can be undone.
  async revert(seq: number): Promise<void> {
    const res = await revertTurn(this.base, this.id, seq)
    this.reverted = { ...this.reverted, [seq]: res.safety_ref }
  }

  // undo restores the working tree to the safety snapshot a prior revert captured.
  async undo(seq: number): Promise<void> {
    const ref = this.reverted[seq]
    if (!ref) return
    await undoRevert(this.base, this.id, ref)
    const next = { ...this.reverted }
    delete next[seq]
    this.reverted = next
  }

  private releaseReadyWaiters() {
    const waiting = this.readyWaiters
    this.readyWaiters = []
    for (const w of waiting) w()
  }

  // whenReady resolves once the initial backlog has arrived, or after ms if it
  // never does. Swapping a session's connection waits on this so the outgoing
  // one can stay on screen until the incoming one can replace it without a blank
  // frame. Bounded, because a session that never comes back must not pin the
  // view to a dead connection forever.
  whenReady(ms = 8000): Promise<void> {
    if (this.ready) return Promise.resolve()
    return new Promise((resolve) => {
      let settled = false
      const finish = () => {
        if (settled) return
        settled = true
        resolve()
      }
      this.readyWaiters.push(finish)
      setTimeout(finish, ms)
    })
  }

  destroy() {
    this.closed = true
    // A destroyed connection will never become ready; free anyone waiting on it
    // rather than making them sit out the timeout.
    this.releaseReadyWaiters()
    clearTimeout(this.reconnectTimer)
    if (typeof document !== 'undefined') {
      document.removeEventListener('visibilitychange', this.revalidate)
      window.removeEventListener('online', this.revalidate)
      window.removeEventListener('focus', this.revalidate)
    }
    this.ws?.close()
  }
}
