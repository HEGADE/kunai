// The running screen, in the three phases it has, with the data it really gets.
import { chromium } from 'playwright'
const b = await chromium.launch()
const p = await b.newPage({ viewport: { width: 1280, height: 1000 } })

const AREAS = [
  { what: 'Can a guest write a file it should not?', files: ['internal/server/shareupload.go', 'internal/server/sharegate.go', 'internal/server/attachments.go'],
    why: 'The share upload listener answers the public internet, and the guest writes the prompt frame that decides what happens to the bytes. Worth knowing whether images-only, id ownership and the file cap are each enforced on the value actually consulted.' },
  { what: 'Can an applied fix land on the wrong file?', files: ['internal/server/prreviewapply.go', 'internal/review/apply.go'],
    why: 'Applying a suggested patch writes into the real checkout, the one place in a review where Write is not withheld. The client sends an index; the server resolves it separately.' },
  { what: 'Can a comment land on code it was never about?', files: ['internal/server/prreviewpost.go', 'internal/review/reanchor.go'],
    why: 'When the pull request has moved, old line anchors are stale, so comments are re-anchored by matching quoted text instead.' },
]
const FILES = [
  ['web/src/components/Settings.svelte', 740, 784], ['web/src/components/ReviewView.svelte', 571, 264],
  ['web/src/components/review/DetailRail.svelte', 555, 0], ['web/src/components/review/RunningReview.svelte', 551, 0],
  ['CLAUDE.md', 472, 1], ['internal/review/phase_test.go', 375, 0], ['internal/review/phaseprompt.go', 364, 0],
  ['web/src/Share.svelte', 349, 10], ['internal/review/phase.go', 344, 0], ['web/src/lib/app.svelte.ts', 250, 71],
  ['web/src/components/GitHubApp.svelte', 231, 178], ['web/src/components/Accounts.svelte', 86, 287],
  ['web/src/components/Providers.svelte', 73, 238], ['web/src/components/ReviewDraft.svelte', 0, 355],
  ['internal/server/shareupload.go', 60, 12], ['internal/server/prreviewapi.go', 40, 8],
].map(([path, additions, deletions]) => ({ path, additions, deletions }))

let phase = 'find'
const t0 = Date.now()
await p.route('**/api/sessions/*/review', (r) => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({
  owner: 'HEGADE', repo: 'kunai', number: 7, title: 'Nightly into main', head_sha: 'abc1234', base_ref: 'main',
  from_fork: false, phase, surveyed: true, findings: [], running: true, stopped: false, files: FILES,
  survey: { intent: 'Merges the nightly line into main', areas: AREAS },
  timeline: [
    { phase: 'survey', at: new Date(t0 - 300000).toISOString() },
    { phase: 'find', at: new Date(t0 - 240000).toISOString() },
    ...(phase === 'verify' ? [{ phase: 'verify', at: new Date(t0 - 90000).toISOString() }] : []),
  ],
}) }))
const row = { id: 'rev7', cwd: '/tmp/x', model: 'opus', effort: 'high', cli: 'Claude', review: true,
  title: 'Review #7', state: 'running', created_at: new Date(t0 - 300000).toISOString() }
await p.route((u) => new URL(u).pathname === '/api/sessions', (r) =>
  r.request().method() === 'GET' ? r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([row]) }) : r.continue())

await p.goto('http://127.0.0.1:8901/')
await p.waitForTimeout(1400)
await p.locator(".row:has(.node:not([data-state='past']))").first().click()
await p.waitForTimeout(2000)
await p.screenshot({ path: '/tmp/run-find.png', fullPage: true })
console.log((await p.locator('.run').innerText()).slice(0, 420))

// Open both disclosures to see the folded half.
for (const r of await p.locator('.row').all()) await r.click()
await p.waitForTimeout(400)
await p.screenshot({ path: '/tmp/run-open.png', fullPage: true })

phase = 'verify'
await p.reload(); await p.waitForTimeout(2200)
await p.screenshot({ path: '/tmp/run-verify.png' })
console.log('--- verify ---')
console.log((await p.locator('.run').innerText()).slice(0, 300))
await b.close()
