// Shared, pure helpers for rendering tool calls — used by ToolCard,
// PermissionGate, and ToolBody so the collapsed summary and icon are consistent
// everywhere. Everything reads only the tool `input` (the model's request).

import { diffLines } from './diff'

type Obj = Record<string, unknown>
const obj = (v: unknown): Obj => (v && typeof v === 'object' ? (v as Obj) : {})
const str = (v: unknown): string => (typeof v === 'string' ? v : '')
const num = (v: unknown): number | undefined => (typeof v === 'number' ? v : undefined)

export type IconKey =
  | 'terminal'
  | 'edit'
  | 'write'
  | 'read'
  | 'search'
  | 'glob'
  | 'todo'
  | 'web'
  | 'task'
  | 'tool'

export function iconFor(name: string): IconKey {
  switch (name) {
    case 'Bash':
    case 'BashOutput':
    case 'KillShell':
      return 'terminal'
    case 'Edit':
    case 'MultiEdit':
    case 'NotebookEdit':
      return 'edit'
    case 'Write':
      return 'write'
    case 'Read':
      return 'read'
    case 'Grep':
      return 'search'
    case 'Glob':
      return 'glob'
    case 'TodoWrite':
      return 'todo'
    case 'WebFetch':
    case 'WebSearch':
      return 'web'
    case 'Task':
      return 'task'
    default:
      return 'tool'
  }
}

// baseName trims a path to its last segment for compact summaries.
function baseName(path: string): string {
  return path.replace(/\/+$/, '').split('/').slice(-1)[0] || path
}

// ToolLabel is the structured, collapsed-row summary.
//   action  the human verb phrase ("Read 12 lines", "Edit", "Bash")
//   file    a basename to render as a language pill (file ops only)
//   path    the full path, used to resolve the pill's language logo
//   text    an inline detail (command, pattern, url) when there is no file pill
//   mono    render `text` in the monospace font
//   added/removed  drive the green/red diff stats
// Keeping this structured lets the card show a filename pill and colour the
// counts rather than baking everything into one flat string.
export interface ToolLabel {
  action: string
  file?: string
  path?: string
  text?: string
  mono: boolean
  added: number
  removed: number
}

// lineCount counts lines in captured tool output, ignoring a trailing newline.
function lineCount(s: string): number {
  if (!s) return 0
  const t = s.endsWith('\n') ? s.slice(0, -1) : s
  return t.split('\n').length
}

// describe is the one-line label shown while a tool card is collapsed. It may
// use `result` (present once the tool has run) to enrich the action phrase, e.g.
// the number of lines a Read returned.
export function describe(name: string, input: unknown, result?: { content: string }): ToolLabel {
  const i = obj(input)
  const base: ToolLabel = { action: name, mono: false, added: 0, removed: 0 }
  switch (name) {
    case 'Edit': {
      const d = diffLines(str(i.old_string), str(i.new_string))
      return { ...base, action: 'Edit', file: baseName(str(i.file_path)), path: str(i.file_path), added: d.added, removed: d.removed }
    }
    case 'MultiEdit': {
      const edits = Array.isArray(i.edits) ? (i.edits as Obj[]) : []
      let a = 0
      let r = 0
      for (const e of edits) {
        const d = diffLines(str(e.old_string), str(e.new_string))
        a += d.added
        r += d.removed
      }
      const action = edits.length > 1 ? `Edit ${edits.length} spots` : 'Edit'
      return { ...base, action, file: baseName(str(i.file_path)), path: str(i.file_path), added: a, removed: r }
    }
    case 'Write': {
      const lines = str(i.content) ? lineCount(str(i.content)) : 0
      return { ...base, action: lines ? `Wrote ${lines} ${lines === 1 ? 'line' : 'lines'}` : 'Write', file: baseName(str(i.file_path)), path: str(i.file_path) }
    }
    case 'Read': {
      const n = result ? lineCount(result.content) : 0
      const off = num(i.offset)
      const action = n
        ? `Read ${n} ${n === 1 ? 'line' : 'lines'}`
        : off != null
          ? `Read from ${off}`
          : 'Read'
      return { ...base, action, file: baseName(str(i.file_path)), path: str(i.file_path) }
    }
    case 'Bash':
      return { ...base, action: 'Bash', text: str(i.command) || str(i.description), mono: true }
    case 'Grep':
      return { ...base, action: 'Grep', text: str(i.pattern) + (str(i.path) ? `  in ${baseName(str(i.path))}` : ''), mono: true }
    case 'Glob':
      return { ...base, action: 'Glob', text: str(i.pattern), mono: true }
    case 'TodoWrite': {
      const todos = Array.isArray(i.todos) ? (i.todos as Obj[]) : []
      const done = todos.filter((t) => str(t.status) === 'completed').length
      return { ...base, action: 'Todos', text: todos.length ? `${done}/${todos.length} done` : '' }
    }
    case 'WebFetch':
      return { ...base, action: 'Fetch', text: str(i.url), mono: true }
    case 'WebSearch':
      return { ...base, action: 'Search', text: str(i.query) }
    case 'Task':
      return { ...base, action: 'Task', text: str(i.subagent_type) || str(i.description) }
    default:
      return {
        ...base,
        text: str(i.command) || str(i.file_path) || str(i.path) || str(i.pattern) || str(i.url) || '',
        mono: true,
      }
  }
}
