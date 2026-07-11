// Minimal line diff for rendering Edit/MultiEdit tool calls. Inputs are the
// model's old_string -> new_string, which are small, so a straightforward LCS
// over lines is plenty — no dependency, no streaming concerns.

export type DiffRow = { kind: 'add' | 'del' | 'ctx'; text: string }
export type LineDiff = { rows: DiffRow[]; added: number; removed: number }

export function diffLines(a: string, b: string): LineDiff {
  const A = a.split('\n')
  const B = b.split('\n')
  // Longest common subsequence table over lines.
  const n = A.length
  const m = B.length
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = A[i] === B[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    }
  }

  const rows: DiffRow[] = []
  let added = 0
  let removed = 0
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (A[i] === B[j]) {
      rows.push({ kind: 'ctx', text: A[i] })
      i++
      j++
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      rows.push({ kind: 'del', text: A[i] })
      removed++
      i++
    } else {
      rows.push({ kind: 'add', text: B[j] })
      added++
      j++
    }
  }
  while (i < n) {
    rows.push({ kind: 'del', text: A[i++] })
    removed++
  }
  while (j < m) {
    rows.push({ kind: 'add', text: B[j++] })
    added++
  }
  return { rows, added, removed }
}
