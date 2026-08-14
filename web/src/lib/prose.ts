// Making a reviewer's prose scannable.
//
// A finding's argument is technical writing about code, and it is dense with the
// things a reader is actually hunting for: the function that is wrong, the file
// and line it is on, the call that proves it. Rendered as flat text they vanish
// into the paragraph, so a twelve-line argument reads as a grey wall you skim
// and give up on. That is not a typography problem, it is an information one:
// the structure is already in the sentence and the rendering was throwing it
// away.
//
//   SetAnswerHook fires at the end of every turn with whatever the model said
//   out loud, including turns a person started (internal/session/answer.go:23).
//   advanceReview cannot tell the two apart.
//
// Three identifiers and a location in two sentences. Marked up, the eye lands on
// them and the sentence becomes a map. Left plain, you have to read every word
// to find out whether this finding is about code you recognise.
//
// The rules are deliberately conservative, because over-matching is worse than
// under-matching: prose peppered with false code spans is harder to read than
// prose with none, and a reader who cannot trust the marking stops seeing it.

/** One run of the input: prose, or something that should be set as code. */
interface Span {
  text: string
  kind: 'text' | 'code' | 'loc'
}

// A file path with a line or a range: internal/server/foo.go:196-203, or the
// bare :204 the model uses when it has already named the file. Matched before
// anything else, since a path contains dots that the identifier rules want.
const LOCATION = /\b[\w./-]+\.[a-z]{1,5}:\d+(?:-\d+)?\b|(?<=\s)::?\d+(?:-\d+)?\b/g

// A call: `safeName(a.Name)`, `g.files.has(token, a.ID)`. No nesting, on
// purpose. Matching balanced parentheses with a regular expression is where this
// kind of thing goes wrong, and a nested call simply stays prose, which is a
// cost worth paying to never swallow half a sentence.
const CALL = /\b[A-Za-z_][\w.]*\([^()]{0,80}\)/g

// camelCase and dotted.Identifiers. English almost never produces either, which
// is what makes this safe: a hump inside a word is a reliable tell that the word
// came from a program rather than from a sentence.
const IDENT = /\b(?:[a-z]+[A-Z]\w*|[A-Za-z_]\w*\.[A-Za-z_]\w+)\b/g

/**
 * Split prose into runs, marking the parts that are code.
 *
 * Exported for its own tests: the rules are guesses about how a model writes,
 * and guesses that nobody can exercise are guesses that quietly rot.
 */
export function spans(text: string): Span[] {
  if (!text) return []

  // Backticks first and absolutely: when the model has said "this is code", that
  // beats every heuristic below, and the content inside must not be re-scanned.
  const out: Span[] = []
  const parts = text.split(/`([^`]+)`/g)
  parts.forEach((part, i) => {
    if (i % 2 === 1) {
      out.push({ text: part, kind: 'code' })
      return
    }
    out.push(...scan(part))
  })
  return out.filter((s) => s.text !== '')
}

// scan applies the heuristics to one run of un-backticked prose, most specific
// pattern first so a location is never re-cut by the identifier rule.
function scan(text: string): Span[] {
  let runs: Span[] = [{ text, kind: 'text' }]
  runs = split(runs, LOCATION, 'loc')
  runs = split(runs, CALL, 'code')
  runs = split(runs, IDENT, 'code')
  return runs
}

// split cuts every prose run on a pattern, leaving already-marked runs alone.
function split(runs: Span[], re: RegExp, kind: 'code' | 'loc'): Span[] {
  const out: Span[] = []
  for (const run of runs) {
    if (run.kind !== 'text') {
      out.push(run)
      continue
    }
    let last = 0
    // A fresh RegExp per run: a /g pattern carries lastIndex between calls, and
    // sharing one across runs silently skips matches in every run after the
    // first. The classic way this kind of code half-works.
    const p = new RegExp(re.source, re.flags)
    for (let m = p.exec(run.text); m; m = p.exec(run.text)) {
      if (m.index > last) out.push({ text: run.text.slice(last, m.index), kind: 'text' })
      out.push({ text: m[0], kind })
      last = m.index + m[0].length
    }
    if (last < run.text.length) out.push({ text: run.text.slice(last), kind: 'text' })
  }
  return out
}

const escapes: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
}

/** Escape for insertion as HTML. */
export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => escapes[c])
}

/**
 * Model prose as HTML, with the code in it set as code.
 *
 * The order is what makes this safe: the RAW text is tokenised, and each token
 * is escaped as it is emitted. Escaping first and pattern-matching afterwards
 * would run the regular expressions over `&lt;` and friends, which is how a
 * marker ends up inside an entity and the escaping stops meaning anything.
 */
export function proseHtml(text: string): string {
  return spans(text)
    .map((s) => (s.kind === 'text' ? escapeHtml(s.text) : `<code class="${s.kind}">${escapeHtml(s.text)}</code>`))
    .join('')
}
