// Wire types mirroring the Go server's internal/session protocol.

export type SessionState = 'idle' | 'running' | 'awaiting_permission'

export interface Meta {
  id: string
  cwd: string
  model: string
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
    | 'result'
    | 'state'
    | 'error'
  // hello
  id?: string
  cwd?: string
  model?: string
  title?: string
  state?: SessionState
  high_seq?: number
  pending?: AppEvent[]
  // delta / thinking / user / error
  text?: string
  message?: string
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
  // result
  is_error?: boolean
  duration_ms?: number
}

export interface Attachment {
  id: string
  name: string
  media_type: string
}

export type Command =
  | { t: 'prompt'; text: string; attachments?: Attachment[] }
  | { t: 'permission'; request_id: string; behavior: 'allow' | 'deny'; always?: boolean }
  | { t: 'interrupt' }
  | { t: 'set_model'; model: string }

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
