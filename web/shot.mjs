import { chromium } from 'playwright'
const b = await chromium.launch()
const p = await b.newPage({ viewport: { width: 1100, height: 900 } })
p.on('pageerror', (e) => console.log('PAGEERROR:', e.message))

// Count every call the dashboard makes to the pull-request endpoints. The bug
// was that these fired on the app store's poll beat, for ever.
let calls = 0
p.on('request', (r) => {
  if (/\/api\/github\/(pulls|app)/.test(r.url())) calls++
})

await p.goto('http://127.0.0.1:8902/')
await p.waitForTimeout(4000)
const first = calls
console.log('github calls in the first 4s:', first)

// Sit on the dashboard doing nothing. Nothing may be fetched, and the layout
// must not move.
const h1 = await p.evaluate(() => document.body.scrollHeight)
await p.waitForTimeout(25000)
const h2 = await p.evaluate(() => document.body.scrollHeight)
console.log('github calls over the next 25 idle seconds:', calls - first)
console.log('page height before/after:', h1, h2)
console.log('pull requests card present:', await p.locator('.prs').count())
await b.close()
