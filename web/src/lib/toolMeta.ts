// Shared, pure helpers for rendering tool calls — used by ToolCard,
// PermissionGate, and ToolBody so the collapsed summary and icon are consistent
// everywhere. Everything reads only the tool `input` (the model's request).

import { diffLines } from './diff'
import type { Block } from './types'

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
  | 'ask'
  | 'skill'
  | 'plan'
  | 'clock'
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
    // The subagent launcher (current CLIs name it `Agent`; older ones `Task`)
    // plus the background-task family that manages long-running work.
    case 'Agent':
    case 'Task':
    case 'TaskCreate':
    case 'TaskUpdate':
    case 'TaskStop':
    case 'TaskGet':
    case 'TaskList':
    case 'TaskOutput':
    case 'Monitor':
      return 'task'
    case 'AskUserQuestion':
      return 'ask'
    case 'Skill':
      return 'skill'
    case 'ExitPlanMode':
      return 'plan'
    case 'ToolSearch':
      return 'search'
    case 'Artifact':
      return 'write'
    case 'ScheduleWakeup':
      return 'clock'
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
  agent?: string // subagent type, shown as a pill (Agent tool only)
  mono: boolean
  added: number
  removed: number
}

// A tool result made only of image blocks. The driver renders each one as this
// placeholder rather than shipping the bytes back (claude/toolresult.go).
const IMAGE_RESULT = /^(\s*\[image\]\s*)+$/

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
      const content = result?.content ?? ''
      // Reading an image comes back as image blocks, which the driver flattens to
      // a placeholder (claude/toolresult.go). Counting its "lines" reported the
      // nonsense "Read 1 line" for a picture, so name what actually happened.
      if (content && IMAGE_RESULT.test(content)) {
        return { ...base, action: 'Read image', file: baseName(str(i.file_path)), path: str(i.file_path) }
      }
      const n = result ? lineCount(content) : 0
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
    // A subagent launch. The type (Explore, general-purpose, …) reads as a pill;
    // the description is the human summary of what it was asked to do.
    case 'Agent':
    case 'Task':
      return { ...base, action: 'Agent', agent: str(i.subagent_type), text: str(i.description) }
    case 'TaskCreate':
      return { ...base, action: 'Start task', text: str(i.description) || str(i.prompt) }
    case 'TaskUpdate':
      return { ...base, action: 'Update task', text: str(i.status) || str(i.description) }
    case 'TaskStop':
      return { ...base, action: 'Stop task', text: str(i.task_id), mono: true }
    case 'TaskOutput':
      return { ...base, action: 'Read task output', text: str(i.task_id), mono: true }
    case 'TaskGet':
    case 'TaskList':
      return { ...base, action: name === 'TaskList' ? 'List tasks' : 'Task status', text: str(i.task_id), mono: true }
    case 'Monitor': {
      const ids = Array.isArray(i.agentIds) ? i.agentIds.length : 0
      const wait = str(i.wait_for)
      return { ...base, action: 'Monitor', text: ids ? `${ids} agent${ids === 1 ? '' : 's'}${wait ? ` · ${wait}` : ''}` : str(i.command) }
    }
    case 'AskUserQuestion': {
      const qs = Array.isArray(i.questions) ? (i.questions as Obj[]) : []
      return { ...base, action: 'Ask', text: qs.length === 1 ? str(qs[0].question) : qs.length ? `${qs.length} questions` : '' }
    }
    case 'Skill':
      return { ...base, action: 'Skill', text: str(i.skill), mono: true }
    case 'ToolSearch':
      return { ...base, action: 'Tool search', text: str(i.query), mono: true }
    case 'ExitPlanMode':
      return { ...base, action: 'Present plan' }
    case 'Artifact':
      return { ...base, action: 'Artifact', text: str(i.title) || baseName(str(i.file_path)) }
    case 'ScheduleWakeup':
      return { ...base, action: i.stop ? 'Cancel wake-up' : 'Schedule wake-up', text: str(i.reason) }
    default:
      return {
        ...base,
        text: str(i.command) || str(i.file_path) || str(i.path) || str(i.pattern) || str(i.url) || '',
        mono: true,
      }
  }
}

// FileChange is one file a turn touched, with its net line delta. Used by the
// per-turn footer chips.
export interface FileChange {
  path: string
  name: string
  added: number
  removed: number
}

// fileChangesOf aggregates the Edit/MultiEdit/Write tool calls in a turn's blocks
// into one entry per file (summed if the same file is touched more than once),
// preserving first-seen order. Reads: Edit/MultiEdit diff their old->new strings;
// Write counts its whole content as additions (there is no prior content on the
// wire). Non-file tools contribute nothing.
export function fileChangesOf(blocks: Block[]): FileChange[] {
  const byPath = new Map<string, FileChange>()
  const bump = (path: string, added: number, removed: number) => {
    if (!path) return
    const prev = byPath.get(path)
    if (prev) {
      prev.added += added
      prev.removed += removed
    } else {
      byPath.set(path, { path, name: baseName(path), added, removed })
    }
  }
  for (const b of blocks) {
    if (b.type !== 'tool_use') continue
    const i = obj(b.input)
    if (b.name === 'Edit') {
      const d = diffLines(str(i.old_string), str(i.new_string))
      bump(str(i.file_path), d.added, d.removed)
    } else if (b.name === 'MultiEdit') {
      const edits = Array.isArray(i.edits) ? (i.edits as Obj[]) : []
      let a = 0
      let r = 0
      for (const e of edits) {
        const d = diffLines(str(e.old_string), str(e.new_string))
        a += d.added
        r += d.removed
      }
      bump(str(i.file_path), a, r)
    } else if (b.name === 'Write') {
      bump(str(i.file_path), lineCount(str(i.content)), 0)
    }
  }
  return [...byPath.values()]
}

// One edit a turn made to a file: an Edit/MultiEdit hunk (old -> new) or a Write
// (whole content). Rendered by the per-query changed-files card via the same
// DiffView/CodeView the tool cards use.
export type EditOp =
  | { kind: 'edit'; oldStr: string; newStr: string; replaceAll: boolean }
  | { kind: 'write'; content: string }

export interface FileEdits extends FileChange {
  ops: EditOp[]
}

// fileEditsOf is fileChangesOf plus the actual operations per file, in order, so
// the per-query card can show what each query changed (tree + expandable diffs)
// straight from the conversation — no git, and it survives commits. Same source
// (Edit/MultiEdit/Write tool calls), same order-preserving grouping by path.
export function fileEditsOf(blocks: Block[]): FileEdits[] {
  const byPath = new Map<string, FileEdits>()
  const at = (path: string): FileEdits | null => {
    if (!path) return null
    let e = byPath.get(path)
    if (!e) {
      e = { path, name: baseName(path), added: 0, removed: 0, ops: [] }
      byPath.set(path, e)
    }
    return e
  }
  for (const b of blocks) {
    if (b.type !== 'tool_use') continue
    const i = obj(b.input)
    if (b.name === 'Edit') {
      const e = at(str(i.file_path))
      if (!e) continue
      const d = diffLines(str(i.old_string), str(i.new_string))
      e.added += d.added
      e.removed += d.removed
      e.ops.push({ kind: 'edit', oldStr: str(i.old_string), newStr: str(i.new_string), replaceAll: i.replace_all === true })
    } else if (b.name === 'MultiEdit') {
      const e = at(str(i.file_path))
      if (!e) continue
      const edits = Array.isArray(i.edits) ? (i.edits as Obj[]) : []
      for (const ed of edits) {
        const d = diffLines(str(ed.old_string), str(ed.new_string))
        e.added += d.added
        e.removed += d.removed
        e.ops.push({ kind: 'edit', oldStr: str(ed.old_string), newStr: str(ed.new_string), replaceAll: ed.replace_all === true })
      }
    } else if (b.name === 'Write') {
      const e = at(str(i.file_path))
      if (!e) continue
      const content = str(i.content)
      e.added += lineCount(content)
      e.ops.push({ kind: 'write', content })
    }
  }
  return [...byPath.values()]
}
