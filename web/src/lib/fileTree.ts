// Fold a flat list of changed files into a nested folder tree for the per-query
// card: group by directory, collapse single-child directory chains the way an
// editor does (web -> src -> lib shown as one "web/src/lib" row), and roll each
// folder's line counts up from its files. Pure and dependency-free.

import type { FileEdits } from './toolMeta'

export type TreeNode =
  | { kind: 'dir'; name: string; path: string; children: TreeNode[]; added: number; removed: number }
  | { kind: 'file'; name: string; file: FileEdits }

type Dir = Extract<TreeNode, { kind: 'dir' }>

export function buildTree(files: FileEdits[]): TreeNode[] {
  if (!files.length) return []
  const split = files.map((f) => f.path.split('/').filter(Boolean))

  // Strip the directory prefix shared by every file, so the tree roots at the
  // first level that actually branches (the repo, not "/home/you/...").
  const minDir = Math.min(...split.map((p) => p.length - 1))
  let prefix = 0
  for (; prefix < minDir; prefix++) {
    const seg = split[0][prefix]
    if (!split.every((p) => p[prefix] === seg)) break
  }

  const root: Dir = { kind: 'dir', name: '', path: '', children: [], added: 0, removed: 0 }
  files.forEach((f, i) => {
    let cur = root
    let acc = ''
    for (const seg of split[i].slice(prefix, -1)) {
      acc = acc ? `${acc}/${seg}` : seg
      let child = cur.children.find((c): c is Dir => c.kind === 'dir' && c.name === seg)
      if (!child) {
        child = { kind: 'dir', name: seg, path: acc, children: [], added: 0, removed: 0 }
        cur.children.push(child)
      }
      cur = child
    }
    cur.children.push({ kind: 'file', name: f.name, file: f })
  })

  // Collapse single-child directory chains and roll counts up, depth first.
  const collapse = (node: Dir): Dir => {
    node.children = node.children.map((c) => (c.kind === 'dir' ? collapse(c) : c))
    while (node.children.length === 1 && node.children[0].kind === 'dir') {
      const only = node.children[0]
      node.name = node.name ? `${node.name}/${only.name}` : only.name
      node.path = only.path
      node.children = only.children
    }
    node.added = node.children.reduce((n, c) => n + (c.kind === 'dir' ? c.added : c.file.added), 0)
    node.removed = node.children.reduce((n, c) => n + (c.kind === 'dir' ? c.removed : c.file.removed), 0)
    return node
  }
  return root.children.map((c) => (c.kind === 'dir' ? collapse(c) : c))
}
