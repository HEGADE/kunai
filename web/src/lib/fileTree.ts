// Fold the flat changed-file list into the nested tree the sidebar-style view
// renders. Pure and allocation-light: one pass to build, one to collapse
// single-child directory chains (so `src/lib/foo.ts` shows as `src/lib` › file,
// not three nested rows). No reactivity here — the component derives it once.

import type { ChangedFile } from './types'

export interface TreeFile {
  kind: 'file'
  name: string
  file: ChangedFile
}

export interface TreeDir {
  kind: 'dir'
  name: string // may be a joined segment like "src/lib" after collapsing
  path: string // full path from the root, for a stable key + collapse memory
  added: number
  removed: number
  children: TreeNode[]
}

export type TreeNode = TreeDir | TreeFile

interface RawDir {
  children: Map<string, RawDir>
  files: ChangedFile[]
}

function emptyDir(): RawDir {
  return { children: new Map(), files: [] }
}

export function buildTree(files: ChangedFile[]): TreeNode[] {
  const root = emptyDir()
  for (const f of files) {
    const parts = f.path.split('/')
    parts.pop() // the leaf name is derived from the file's full path later
    let dir = root
    for (const p of parts) {
      let next = dir.children.get(p)
      if (!next) {
        next = emptyDir()
        dir.children.set(p, next)
      }
      dir = next
    }
    dir.files.push(f)
  }
  return toNodes(root, '')
}

function toNodes(dir: RawDir, prefix: string): TreeNode[] {
  const dirs: TreeDir[] = []
  for (const [name, child] of dir.children) {
    const path = prefix ? `${prefix}/${name}` : name
    dirs.push(collapse({ kind: 'dir', name, path, added: 0, removed: 0, children: toNodes(child, path) }))
  }
  dirs.sort((a, b) => (a.name < b.name ? -1 : 1))

  const files: TreeFile[] = dir.files
    .map((f) => ({ kind: 'file', name: f.path.split('/').pop() as string, file: f }) as TreeFile)
    .sort((a, b) => (a.name < b.name ? -1 : 1))

  for (const d of dirs) rollUp(d)
  // Directories first, then files, each alphabetical — the file-explorer order.
  return [...dirs, ...files]
}

// collapse merges a directory that holds exactly one subdirectory (and no files)
// into it: "a" › "b" › file becomes "a/b" › file.
function collapse(d: TreeDir): TreeDir {
  while (d.children.length === 1 && d.children[0].kind === 'dir') {
    const only = d.children[0]
    d = { kind: 'dir', name: `${d.name}/${only.name}`, path: only.path, added: 0, removed: 0, children: only.children }
  }
  return d
}

// rollUp sums a directory's descendant add/remove counts, bottom-up.
function rollUp(d: TreeDir): void {
  let added = 0
  let removed = 0
  for (const c of d.children) {
    if (c.kind === 'dir') {
      rollUp(c)
      added += c.added
      removed += c.removed
    } else {
      added += c.file.added
      removed += c.file.removed
    }
  }
  d.added = added
  d.removed = removed
}
