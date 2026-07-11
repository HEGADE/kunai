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

function editCounts(oldStr: string, newStr: string): string {
  const { added, removed } = diffLines(oldStr, newStr)
  const parts: string[] = []
  if (added) parts.push(`+${added}`)
  if (removed) parts.push(`−${removed}`)
  return parts.join(' ')
}

// summaryOf is the one-line label shown while a tool card is collapsed.
export function summaryOf(name: string, input: unknown): string {
  const i = obj(input)
  switch (name) {
    case 'Edit': {
      const stat = editCounts(str(i.old_string), str(i.new_string))
      return [baseName(str(i.file_path)), stat].filter(Boolean).join('  ')
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
      const stat = [a ? `+${a}` : '', r ? `−${r}` : ''].filter(Boolean).join(' ')
      return [baseName(str(i.file_path)), stat].filter(Boolean).join('  ')
    }
    case 'Write': {
      const lines = str(i.content) ? str(i.content).split('\n').length : 0
      return [baseName(str(i.file_path)), lines ? `${lines} lines` : ''].filter(Boolean).join('  ')
    }
    case 'Bash':
      return str(i.command) || str(i.description)
    case 'Read': {
      const off = num(i.offset)
      const lim = num(i.limit)
      const range = off != null ? `  ${off}–${lim != null ? off + lim : ''}` : ''
      return baseName(str(i.file_path)) + range
    }
    case 'Grep':
      return str(i.pattern) + (str(i.path) ? `  in ${baseName(str(i.path))}` : '')
    case 'Glob':
      return str(i.pattern)
    case 'TodoWrite': {
      const todos = Array.isArray(i.todos) ? (i.todos as Obj[]) : []
      const done = todos.filter((t) => str(t.status) === 'completed').length
      return todos.length ? `${done}/${todos.length} done` : ''
    }
    case 'WebFetch':
      return str(i.url)
    case 'WebSearch':
      return str(i.query)
    case 'Task':
      return str(i.subagent_type) || str(i.description)
    default:
      return (
        str(i.command) || str(i.file_path) || str(i.path) || str(i.pattern) || str(i.url) || ''
      )
  }
}
