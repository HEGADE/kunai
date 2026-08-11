// Where an image written by the agent is fetched from.
//
// A session's conversation can contain ![shot](/tmp/x.png), and only the machine
// running that session can turn that path into pixels. The chat therefore has to
// rewrite it to that machine's own endpoint, which means whatever renders the
// markdown needs to know which session it is inside.
//
// Passed through context rather than props because Markdown is rendered from
// several places that have no session in scope (a tool card, a subagent trace,
// the streaming reply), and threading it down each of those chains would be a
// lot of plumbing to reach a tag that appears rarely.

export const FILE_BASE = Symbol('kunai:file-base')

// FileBase returns the URL prefix an image path is appended to, or '' when there
// is no session in scope and a local path simply cannot be resolved. A function
// rather than a string so a tab switch cannot leave a stale session's id baked
// into a component that was set up under it.
export type FileBase = () => string

// fileBaseFor builds the prefix for one session on one machine. The path itself
// is appended by the caller, encoded, as the `path` query parameter.
export function fileBaseFor(origin: string, sessionId: string): string {
  if (!sessionId) return ''
  return `${origin.replace(/\/+$/, '')}/api/sessions/${encodeURIComponent(sessionId)}/file?path=`
}

// sharedImageBase is the guest equivalent, and it points at a DIFFERENT route on
// a different listener for a reason worth stating.
//
// The owner's route above reads files inside the session's own folders, and it
// is deliberately absent from the share gate: a share link is a public URL, and
// that route would hand whoever holds it every image in the project. So a guest
// gets a route confined to the generated-images directory instead, which holds
// nothing but pictures the model drew in a conversation they are already
// watching. Same shape of prefix, so the markdown renderer needs no idea which
// of the two it was given.
export function sharedImageBase(token: string): string {
  if (!token) return ''
  return `/api/share/${encodeURIComponent(token)}/image?path=`
}
