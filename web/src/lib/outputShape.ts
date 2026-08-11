// What a tool's output actually is, so it can be rendered as that.
//
// Tool output was one grey `<pre>` regardless of content, which is a waste of
// three things kunai already has: a syntax highlighter, a diff renderer, and the
// path the tool was given. Reading a Go file looked like reading a log; reading a
// unified diff looked like nothing at all, which matters now that a review's
// whole job starts by reading one.
//
// Pure and free of Svelte, so the sniffing rules can be read (and corrected) in
// one place rather than inside a component.

// Shape is how a body should be rendered.
export type Shape =
  | { kind: 'diff' }
  | { kind: 'code'; lang?: string }
  | { kind: 'text' }

// EXT_LANG maps a file extension to a highlight.js language. Deliberately the
// same curated set the rest of the app highlights: an unknown extension is
// better rendered as plain text than as a guess, since a wrong guess colours
// tokens that are not tokens.
const EXT_LANG: Record<string, string> = {
  go: 'go',
  ts: 'typescript',
  tsx: 'typescript',
  js: 'javascript',
  jsx: 'javascript',
  svelte: 'xml',
  html: 'xml',
  xml: 'xml',
  css: 'css',
  scss: 'css',
  json: 'json',
  py: 'python',
  rs: 'rust',
  java: 'java',
  kt: 'kotlin',
  rb: 'ruby',
  php: 'php',
  c: 'c',
  h: 'c',
  cc: 'cpp',
  cpp: 'cpp',
  hpp: 'cpp',
  cs: 'csharp',
  swift: 'swift',
  sh: 'bash',
  bash: 'bash',
  zsh: 'bash',
  yml: 'yaml',
  yaml: 'yaml',
  toml: 'ini',
  ini: 'ini',
  sql: 'sql',
  md: 'markdown',
  markdown: 'markdown',
  diff: 'diff',
  patch: 'diff',
}

// langFor returns the highlight language for a path, or undefined when the
// extension is not one we highlight.
export function langFor(path?: string): string | undefined {
  if (!path) return undefined
  const base = path.split('/').pop() ?? ''
  const ext = base.includes('.') ? base.split('.').pop()!.toLowerCase() : ''
  return EXT_LANG[ext]
}

// looksLikeDiff recognises unified diff output regardless of where it came from.
//
// Sniffed rather than taken from the path alone, because a diff arrives from
// several places that do not agree on naming: a `.diff` file, `git diff` through
// Bash, and `git show`. Requires a hunk header AND a changed line, so a file that
// merely contains a line starting with "+" (a changelog, a markdown list) is not
// mistaken for one.
export function looksLikeDiff(text: string): boolean {
  if (!text) return false
  const head = text.slice(0, 4000)
  if (!/^@@ -\d+(,\d+)? \+\d+(,\d+)? @@/m.test(head)) return false
  return /^[+-]/m.test(head)
}

// shapeOf decides how to render a tool's output.
//
// The path wins over sniffing for code, because a `.go` file whose first lines
// happen to look like something else is still Go. Sniffing wins for diffs,
// because the path is often absent (Bash output) and a diff is unambiguous.
export function shapeOf(text: string, path?: string): Shape {
  if (looksLikeDiff(text)) return { kind: 'diff' }
  const lang = langFor(path)
  if (lang) return { kind: 'code', lang }
  return { kind: 'text' }
}

// stripLineNumbers removes the `   12→` gutter the Read tool prefixes to every
// line. Without this, highlighting a read file colours the numbers as part of
// the code and every line begins with an arrow that is not in the file.
//
// Conservative: applied only when nearly every line carries the marker, so a file
// that genuinely contains an arrow is left alone.
export function stripLineNumbers(text: string): string {
  const lines = text.split('\n')
  if (lines.length < 2) return text
  const marked = lines.filter((l) => /^\s*\d+→/.test(l)).length
  if (marked < lines.length * 0.8) return text
  return lines.map((l) => l.replace(/^\s*\d+→/, '')).join('\n')
}
