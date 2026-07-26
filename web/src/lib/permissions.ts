// The permission modes a session can run in, and what each one means.
//
// Kept here rather than inside the composer because two places now offer the
// choice and they must not describe it differently: the composer switches a
// running session, and the worktree dialog picks the mode a session is SPAWNED
// in. The second is not a convenience -- the CLI takes the mode as a spawn flag,
// so one set afterwards arrives too late to govern the first tool call, which
// for an agent working unattended is the one that decides whether it gets
// anywhere at all.
//
// Mirrors internal/session.PermissionModes; keep the two in step by hand, as
// with the rest of the wire contract.

import type { PermissionMode } from './types'

export interface PermissionOption {
  id: PermissionMode
  label: string
  // hint reads as a sentence fragment, because the dialog uses it as prose under
  // the row as well as a tooltip on the chip.
  hint: string
}

export const PERMISSION_MODES: PermissionOption[] = [
  { id: 'default', label: 'Ask', hint: 'Stops for every tool call' },
  { id: 'auto', label: 'Auto', hint: 'Runs safe actions, asks about the rest' },
  { id: 'acceptEdits', label: 'Accept edits', hint: 'Writes files without asking' },
  { id: 'plan', label: 'Plan', hint: 'Reads and plans, changes nothing' },
]

// permissionLabel is the short word for a mode, for a pill with no room for more.
export function permissionLabel(id: string): string {
  return PERMISSION_MODES.find((m) => m.id === id)?.label ?? id
}
