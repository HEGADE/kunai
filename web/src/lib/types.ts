// Wire types mirroring the Go server's internal/session protocol.

export type SessionState = 'starting' | 'idle' | 'running' | 'awaiting_permission'

export interface Stats {
  hostname: string
  os: string
  arch: string
  sessions: number
  uptime_sec: number
  load1: number
  mem_total: number
  mem_available: number
  disk_total: number
  disk_free: number
  cores: number
  claude_version: string
  kunai_version: string
  kunai_uptime_sec: number
  keep_awake: boolean
  keep_awake_supported: boolean
  cpu_temp_c: number // hottest CPU sensor in Celsius; 0 on macOS (no degrees there)
  thermal_pressure: string // macOS thermal pressure level; '' on Linux (uses degrees)
  thermal_trip: boolean // guardian is holding everything stopped after a trip
  thermal_guard: boolean // guard enabled on this machine
  thermal_soft_c: number // trip temperature
  thermal_max_hours: number // wall-clock cap on unattended work
  thermal_hard_c: number // poweroff ceiling (0 = never)
  thermal_action: string // 'sleep' | 'poweroff'
  thermal_privileged: boolean // admin grant (poweroff/lid) is actually in place
  keep_lid: boolean // lid-closed hold currently held (privileged)
  keep_lid_supported: boolean // platform can hold the lid (Phase 2)
  rate_resets?: Record<string, number> // window -> unix seconds it resets
  clis?: string[] // Claude accounts a new session can pick (first is the default)
}

// One subscription quota window's fill, read from the CLI's own /usage. A
// stream `rate_limit` event only says when a window resets and whether the last
// turn was rejected; the "how full" half only exists here.
export interface UsageWindow {
  percent: number // 0-100
  resets_at?: number // unix seconds; absent = unknown
}

// The default account's quota. A missing window means the CLI did not report
// that limit, which reads as absent rather than as an empty meter.
// `unavailable` is the normal not-an-error state: logged out, on an API key, or
// no CLI to ask.
export interface Usage {
  session?: UsageWindow // rolling 5-hour
  weekly?: UsageWindow // rolling 7-day
  fetched_at?: number
  unavailable?: string
}

// A named Claude account (CLI) a machine can run sessions on.
export interface CLIProfile {
  name: string
  bin: string
  dir?: string // the account's Claude config folder (what separates two logins)
}

// The thermal safety guard's policy, mirroring the Go guardConfig.
export interface ThermalConfig {
  enabled: boolean
  soft_c: number // trip temperature in Celsius (0 = no temperature check)
  max_hours: number // stop unattended work after this long awake (0 = no cap)
  hard_c: number // poweroff ceiling (0 = never)
  action: 'sleep' | 'poweroff' // what a hard trip does
}

// --- scheduler ---

export interface Trigger {
  kind: 'at' | 'reset'
  at?: string // RFC3339 (kind === 'at')
  window?: 'five_hour' | 'seven_day' // kind === 'reset'
  offset_sec?: number
}
export interface Target {
  kind: 'new' | 'resume'
  cwd?: string
  model?: string
  effort?: string
  mode?: string
  session_id?: string
}
export interface Job {
  id: string
  name?: string
  enabled: boolean
  trigger: Trigger
  rearm: boolean
  target: Target
  prompt: string
  last_run?: string
  last_status?: string
  next_fire?: string
}
export type TaggedJob = Job & { machineId: string }

export interface Meta {
  id: string
  cwd: string
  model: string
  effort?: string
  cli?: string // the Claude account this session runs on
  title: string
  state: SessionState
  created_at: string
  pinned?: boolean // user override, merged from the server's session-metadata store
}

export interface Block {
  type: 'text' | 'tool_use' | 'thinking'
  text?: string
  id?: string
  name?: string
  input?: unknown
}

// A self-prompting run. Mirrors session.LoopStatus: the server owns every number
// here, so a client that was away renders the whole thing without its own tally.
// 'seam' is a lap replayed from a transcript, not a loop that is running now.
export type LoopState = 'running' | 'done' | 'stopped' | 'exhausted' | 'failed' | 'seam'

export interface LoopStatus {
  state: LoopState
  prompt: string
  promise?: string
  iteration: number
  max_iters: number
  spent_usd: number
  max_usd: number
  reason?: string
}

// What starts a loop. The server clamps every limit, so these are requests.
export interface LoopConfig {
  prompt: string
  promise?: string
  max_iters: number
  max_usd: number
}

// OlderTurns is one reverse-scroll page: turns older than the requested cursor as
// app events, plus the next older cursor (0 = start of transcript reached).
export interface OlderTurns {
  events: AppEvent[]
  older: number
}

export interface AppEvent {
  seq: number
  t:
    | 'hello'
    | 'user'
    | 'delta'
    | 'thinking'
    | 'assistant'
    | 'permission'
    | 'permission_resolved'
    | 'tool_result'
    | 'compact'
    | 'loop'
    | 'mode'
    | 'queued'
    | 'unqueued'
    | 'project'
    | 'result'
    | 'state'
    | 'error'
    | 'rate_limit'
  // hello
  id?: string
  cwd?: string
  model?: string
  title?: string
  state?: SessionState
  mode?: string
  effort?: string
  high_seq?: number
  // hello: transcript byte offset older-than-seed history begins before (reverse
  // scroll cursor); 0/absent means nothing older to page in.
  hist_before?: number
  pending?: AppEvent[]
  queued?: AppEvent[]
  // hello: every codebase this session has context for
  projects?: ProjectInfo[]
  // project: a codebase just added
  project?: ProjectInfo
  // hello / loop: the session's self-prompting run, whole, on every change
  loop?: LoopStatus
  // queued / unqueued: a prompt parked until the running turn ends
  queue_id?: string
  // delta / thinking / user / error
  text?: string
  message?: string
  // user: what was attached to the prompt (metadata only)
  attachments?: Attachment[]
  // assistant
  blocks?: Block[]
  // permission
  request_id?: string
  tool_name?: string
  tool_use_id?: string
  input?: unknown
  perm_title?: string
  description?: string
  suggestions?: unknown
  behavior?: 'allow' | 'deny'
  // hello / assistant: context-window occupancy from the newest model call.
  // compact: the occupancy left after the summary replaced the conversation.
  context_tokens?: number
  // compact: what the window held before, and what triggered the summary. The
  // summary text is never sent — it is the model's context, not a message.
  pre_tokens?: number
  // compact: the raw conversation-only size the CLI reported (context_tokens adds
  // the resident overhead back for the meter). The divider shows this so it matches
  // Claude's own /compact banner.
  post_tokens?: number
  trigger?: string
  // result
  is_error?: boolean
  duration_ms?: number
  tokens?: number
  // result: the turn's token split (new = read fresh, cached = context re-read)
  new_tokens?: number
  cached_tokens?: number
  output_tokens?: number
  cost_usd?: number
  // tool_result (tool_use_id + is_error reused)
  content?: string
  truncated?: boolean
  // rate_limit
  window?: string
  resets_at?: number
  limit_status?: string
}

// A tool's output, correlated to its tool_use block by id.
export interface ToolResult {
  content: string
  isError: boolean
  truncated: boolean
}

// A codebase the session has context for. Metadata only: nothing in it has been
// read, and the model reaches the files by path when it needs them.
export interface Lang {
  name: string
  files: number
}
export interface ProjectInfo {
  name: string
  path: string
  branch?: string
  remote?: string
  langs?: Lang[]
  dirs?: string[]
  docs?: string[]
  build?: string[]
  files?: number
}

export interface Attachment {
  id: string
  name: string
  media_type: string
}

export type PermissionMode = 'default' | 'acceptEdits' | 'auto' | 'plan'

export type Command =
  | { t: 'prompt'; text: string; attachments?: Attachment[] }
  | {
      t: 'permission'
      request_id: string
      behavior: 'allow' | 'deny'
      always?: boolean
      answers?: Record<string, string> // AskUserQuestion: question text -> answer
    }
  | { t: 'interrupt' }
  | { t: 'set_model'; model: string }
  | { t: 'set_mode'; mode: PermissionMode }
  | { t: 'cancel_queued'; queue_id: string }
  | { t: 'add_project'; path: string }
  | { t: 'start_loop'; loop: LoopConfig }
  | { t: 'stop_loop' }

export interface HistoryEntry {
  id: string
  cwd: string
  title: string
  cli?: string // the Claude account this session belongs to (reopen on it)
  mtime: string
  pinned?: boolean // user override, merged from the server's session-metadata store
}

// --- multi-machine ---

// MachineInfo is the wire shape the hub serves at GET /api/machines. `url` is
// the machine's tailnet origin; the client talks to it directly (REST + WS).
export interface MachineInfo {
  id: string // short stable slug (first FQDN label)
  label: string
  url: string // origin, no trailing slash; '' means "this machine" (hub)
  self: boolean
}

// Machine is the client-side runtime view: registry info + liveness/probe state.
export interface Machine extends MachineInfo {
  online: boolean
  stats: Stats | null
}

// Sessions and history are tagged client-side with the machine they came from.
// The wire types stay pure; the tag is added at fetch time.
export type TaggedMeta = Meta & { machineId: string }
export type TaggedHistoryEntry = HistoryEntry & { machineId: string }

export interface DirEntry {
  name: string
  dir: boolean
  path: string
}

export interface Listing {
  path: string
  parent: string
  entries: DirEntry[]
}

// One Claude account on a machine, for the Accounts screen.
export interface AccountInfo {
  name: string
  default: boolean
  ready: boolean
}
