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
  rate_resets?: Record<string, number> // window -> unix seconds it resets
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
  title: string
  state: SessionState
  created_at: string
}

export interface Block {
  type: 'text' | 'tool_use' | 'thinking'
  text?: string
  id?: string
  name?: string
  input?: unknown
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
  pending?: AppEvent[]
  queued?: AppEvent[]
  // hello: every codebase this session has context for
  projects?: ProjectInfo[]
  // project: a codebase just added
  project?: ProjectInfo
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
  // hello / assistant: context-window occupancy from the newest model call
  context_tokens?: number
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

export interface HistoryEntry {
  id: string
  cwd: string
  title: string
  mtime: string
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
