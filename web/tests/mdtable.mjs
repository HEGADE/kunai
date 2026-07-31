// A markdown table on a phone: does it keep its shape, or get crushed?
//
// The bug this pins is specific and was invisible on a laptop. The container
// sets `overflow-wrap: anywhere` so a long URL cannot push the chat wider, and
// the table was `width: 100%`. Together those say "fit this seven-column table
// into 360px, and you may break a line between any two characters" -- so the
// browser did, stacking "Panel score" one letter per line and making a header
// row taller than the screen.
//
// Run against the REAL shipped stylesheet and the REAL marked output rather than
// a hand-written approximation, so it fails if either the CSS or the markdown
// pipeline stops producing what this assumes. Needs `npm run build` first.
//
//   node tests/mdtable.mjs
import { chromium } from 'playwright'
import { readFileSync, readdirSync } from 'fs'
import { marked } from 'marked'

const DIST = '../internal/webui/dist/assets'
const PHONE = 360 // the narrow end of what kunai is actually read on

const fail = (msg) => {
  console.error('FAIL: ' + msg)
  process.exit(1)
}

const chunks = (ext) =>
  readdirSync(DIST)
    .filter((f) => f.endsWith(ext))
    .map((f) => readFileSync(`${DIST}/${f}`, 'utf8'))
    .join('\n')

// The Markdown component is code-split, so read whichever chunk carries its CSS
// rather than pinning a fingerprinted filename that changes on every build.
const css = chunks('.css')
if (!/\.md[^{]*table\{/.test(css)) fail('no table rules in the built CSS: run `npm run build` first')

// The layout below is only reachable if render() actually wraps its tables, and
// this file builds that markup by hand (it cannot import a .svelte module).
// Asserting the shipped bundle still emits the wrapper is what stops the two
// halves drifting apart into a test that passes against markup nobody produces.
if (!chunks('.js').includes('tablewrap')) {
  fail('the built app never emits .tablewrap, so the rules under test apply to nothing')
}

// The table from the report that showed the bug: many narrow columns and one
// prose column, which is the shape that has nowhere to go on a phone.
const table = [
  '| # | Candidate | Panel score | Code | Theory | Raw test % | One-line read |',
  '|---|---|---|---|---|---|---|',
  '| 1 | Rohil Chaturved | 84 | 7/10 | 9/10 | 95.9% | Elite fundamentals (60/60 MCQ), genuine iterative work, one real chunking bug |',
  '| 2 | Meera Iyer | 71 | 6/10 | 8/10 | 88.2% | Strong theory, thin on delivery |',
].join('\n')

// What render() does to a table, mirrored here because a .svelte module cannot be
// imported into plain node. The assertion above keeps this honest.
const wrapTables = (html) =>
  html.replace(/<table>/g, '<div class="tablewrap"><table>').replace(/<\/table>/g, '</table></div>')

const page = await (await chromium.launch()).newPage({ viewport: { width: PHONE, height: 780 } })
await page.setContent(
  `<style>
     :root { --text:#eee; --text-4:#888; --panel:#1a1a1c; --panel-2:#222; --border:#333;
             --live:#7ec; --sans:system-ui; --r-sm:6px }
     html,body { margin:0; background:#0b0b0c }
     ${css}
   </style>
   <!-- .frame is the message column: the table must not widen it. -->
   <div class="frame" style="width:${PHONE}px; overflow:hidden">
     <div class="md svelte-m5kjgf" style="overflow-wrap:anywhere">${wrapTables(marked.parse(table))}</div>
   </div>`,
)

const th = page.locator('th', { hasText: 'Panel score' })
const box = await th.boundingBox()

// One line of 13px text is about 22px tall. A cell that has stacked its
// characters vertically is many times that, which is the whole symptom.
if (box.height > 44) {
  fail(`the "Panel score" header is ${Math.round(box.height)}px tall: its text is stacking vertically`)
}
// And it must be wider than a single glyph, which is what the crush produced.
if (box.width < 60) fail(`the "Panel score" column is only ${Math.round(box.width)}px wide`)

// The table keeps its real width and scrolls inside its wrapper...
const scroll = await page.locator('.tablewrap').evaluate((w) => ({
  clientWidth: w.clientWidth,
  scrollWidth: w.scrollWidth,
  overflowX: getComputedStyle(w).overflowX,
}))
if (scroll.overflowX !== 'auto') fail(`wrapper overflow-x = ${scroll.overflowX}, want auto`)
if (scroll.scrollWidth <= scroll.clientWidth) {
  fail('the table did not exceed the viewport, so this fixture is no longer testing the crush')
}

// ...and the prose column keeps a readable measure rather than collapsing to its
// longest word. This is the difference between the wrapper scrolling and the
// table scrolling itself, and it is worth a number: squeezed, this row was 120px
// tall, which is most of a phone screen for one candidate.
const rowHeight = await page.locator('tbody tr').first().evaluate((tr) => tr.getBoundingClientRect().height)
if (rowHeight > 80) {
  fail(`a row is ${Math.round(rowHeight)}px tall: the prose column has been squeezed to its longest word`)
}

// ...without dragging the message column wider than the screen, which would put
// a horizontal scrollbar on the whole conversation.
const frame = await page.locator('.frame').evaluate((f) => f.scrollWidth)
if (frame > PHONE) fail(`the table widened the message column to ${frame}px on a ${PHONE}px screen`)

console.log(
  `ok: header ${Math.round(box.width)}x${Math.round(box.height)}, row ${Math.round(rowHeight)}px, ` +
    `table scrolls ${scroll.clientWidth} -> ${scroll.scrollWidth}, column stays ${frame}px`,
)
process.exit(0)
