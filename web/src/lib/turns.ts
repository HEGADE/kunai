// Grouping the flat item stream into turns for the chat view. A turn is one user
// message plus every assistant message that follows it until the next user
// message. Per turn we split the assistant blocks into the tool "activity" (which
// the view collapses) and the trailing "answer" text (which stays visible), and
// aggregate the files it changed for the footer.

import type { Item } from './chat.svelte'
import type { Attachment, Block, ProjectInfo } from './types'
import { fileChangesOf, type FileChange } from './toolMeta'

export interface Turn {
  user?: string
  // Files sent with the user message (metadata only).
  userFiles?: Attachment[]
  // Set when this entry is a codebase joining the session rather than a message.
  project?: ProjectInfo
  // All assistant blocks in the turn, flattened in arrival order.
  blocks: Block[]
  hasAssistant: boolean
  toolCalls: number
  messages: number
  // Distinct tool names in the activity, capped for the header icon cluster.
  toolNames: string[]
  // Everything up to and including the last tool call (collapsed when idle).
  activity: Block[]
  // Trailing text blocks after the last tool call (the visible reply).
  answer: Block[]
  files: FileChange[]
  durationMs?: number
  tokens?: number
  newTokens?: number
  cachedTokens?: number
  outputTokens?: number
  costUsd?: number
}

const isText = (b: Block): boolean => b.type === 'text' && !!b.text

export function groupTurns(items: Item[]): Turn[] {
  const turns: Turn[] = []
  let cur: Turn | null = null
  const start = (user?: string, userFiles?: Attachment[]): Turn => {
    cur = {
      user,
      userFiles,
      blocks: [],
      hasAssistant: false,
      toolCalls: 0,
      messages: 0,
      toolNames: [],
      activity: [],
      answer: [],
      files: [],
    }
    turns.push(cur)
    return cur
  }
  for (const it of items) {
    if (it.role === 'project') {
      // A project joins between turns; what Claude says next is its own turn.
      start(undefined).project = it.project
      cur = null
      continue
    }
    if (it.role === 'user') {
      start(it.text, it.attachments)
    } else {
      const t = cur ?? start(undefined)
      t.hasAssistant = true
      t.blocks.push(...it.blocks)
      if (it.durationMs != null) t.durationMs = it.durationMs
      if (it.tokens != null) t.tokens = it.tokens
      if (it.newTokens != null) t.newTokens = it.newTokens
      if (it.cachedTokens != null) t.cachedTokens = it.cachedTokens
      if (it.outputTokens != null) t.outputTokens = it.outputTokens
      if (it.costUsd != null) t.costUsd = it.costUsd
    }
  }
  for (const t of turns) finalize(t)
  return turns
}

function finalize(t: Turn): void {
  const blocks = t.blocks
  let lastTool = -1
  for (let i = 0; i < blocks.length; i++) if (blocks[i].type === 'tool_use') lastTool = i
  t.toolCalls = blocks.filter((b) => b.type === 'tool_use').length
  if (lastTool >= 0) {
    t.activity = blocks.slice(0, lastTool + 1)
    t.answer = blocks.slice(lastTool + 1).filter(isText)
  } else {
    t.answer = blocks.filter(isText)
  }
  t.messages = t.activity.filter(isText).length
  const names: string[] = []
  for (const b of t.activity) {
    if (b.type === 'tool_use' && b.name && !names.includes(b.name)) names.push(b.name)
  }
  t.toolNames = names.slice(0, 5)
  t.files = fileChangesOf(blocks)
}
