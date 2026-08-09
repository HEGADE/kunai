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
  // grave marks the one option that is different in kind rather than in degree,
  // so every surface offering the list can set it apart without knowing which id
  // is the dangerous one.
  grave?: boolean
  // more is the longer explanation behind an info affordance, for a mode whose
  // one-line hint cannot carry the whole answer. Only one mode needs it, and it
  // needs it because the short version is easy to misread as a mode that already
  // exists.
  more?: string
}

export const PERMISSION_MODES: PermissionOption[] = [
  { id: 'default', label: 'Ask', hint: 'Stops for every tool call' },
  { id: 'auto', label: 'Auto', hint: 'Runs safe actions, asks about the rest' },
  { id: 'acceptEdits', label: 'Accept edits', hint: 'Writes files without asking' },
  { id: 'plan', label: 'Plan', hint: 'Reads and plans, changes nothing' },
  // Last, and set apart, because it is not the end of a scale. The others differ
  // in WHICH calls stop for a person; this one removes the stopping, so there is
  // no category of action left that gets a second look -- any command, any file
  // the process can reach.
  //
  // It is named rather than described, which is the opposite of the rule the
  // other four follow, and that is the point: "Never ask" reads as one more
  // setting on the same dial, and this is not on the dial. A name you have to
  // learn is a name you cannot pick by accident, and the composer turns yellow
  // while it is on, so the state is legible from across the room rather than
  // from a word in a menu you closed a minute ago.
  //
  // The hint has to distinguish it from Auto, and the obvious phrasing does not:
  // "runs commands without asking" describes Auto too, which already runs safe
  // ones on its own. The difference is not WHETHER it runs commands, it is that
  // nothing is left that makes it stop.
  {
    id: 'bypassPermissions',
    label: 'Yolo mode',
    hint: 'Never asks, not even about risky commands',
    grave: true,
    more:
      'Every other mode stops somewhere. This one does not: deleting files, ' +
      'git push, installing packages and network calls all run with no prompt, ' +
      'and it is not limited to this folder. The composer turns yellow while it is on.',
  },
]

// The word for the mode that asks nothing, kept here so the composer, the
// confirmation and any later surface agree on which id that is.
export const BYPASS: PermissionMode = 'bypassPermissions'

// permissionLabel is the short word for a mode, for a pill with no room for more.
export function permissionLabel(id: string): string {
  return PERMISSION_MODES.find((m) => m.id === id)?.label ?? id
}
