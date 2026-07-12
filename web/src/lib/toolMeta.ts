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

// ToolLabel is the structured, collapsed-row summary. `text` is the primary
// label (a filename, command, or pattern); `mono` picks the font (sans for
// filenames and prose, mono for code and paths); `added`/`removed` drive the
// green/red diff stats. Keeping this structured lets the card colour the counts
// rather than baking them into one flat string.
export interface ToolLabel {
  text: string
  mono: boolean
  added: number
  removed: number
}

const label = (text: string, mono = false, added = 0, removed = 0): ToolLabel => ({
  text,
  mono,
  added,
  removed,
})

// describe is the one-line label shown while a tool card is collapsed.
export function describe(name: string, input: unknown): ToolLabel {
  const i = obj(input)
  switch (name) {
    case 'Edit': {
      const d = diffLines(str(i.old_string), str(i.new_string))
      return label(baseName(str(i.file_path)), false, d.added, d.removed)
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
      return label(baseName(str(i.file_path)), false, a, r)
    }
    case 'Write': {
      const lines = str(i.content) ? str(i.content).split('\n').length : 0
      return label(baseName(str(i.file_path)), false, lines, 0)
    }
    case 'Bash':
      return label(str(i.command) || str(i.description), true)
    case 'Read': {
      const off = num(i.offset)
      const lim = num(i.limit)
      const range = off != null ? `  ${off}–${lim != null ? off + lim : ''}` : ''
      return label(baseName(str(i.file_path)) + range, false)
    }
    case 'Grep':
      return label(str(i.pattern) + (str(i.path) ? `  in ${baseName(str(i.path))}` : ''), true)
    case 'Glob':
      return label(str(i.pattern), true)
    case 'TodoWrite': {
      const todos = Array.isArray(i.todos) ? (i.todos as Obj[]) : []
      const done = todos.filter((t) => str(t.status) === 'completed').length
      return label(todos.length ? `${done}/${todos.length} done` : '', false)
    }
    case 'WebFetch':
      return label(str(i.url), true)
    case 'WebSearch':
      return label(str(i.query), false)
    case 'Task':
      return label(str(i.subagent_type) || str(i.description), false)
    default:
      return label(
        str(i.command) || str(i.file_path) || str(i.path) || str(i.pattern) || str(i.url) || '',
        true,
      )
  }
}
