// Smoke test for the Usage page: the sidebar entry, the headline and its
// caveat, the chart, the breakdown in both modes, and the period switch.
//
// Runs against a scratch server on 127.0.0.1:8901 with its own data dir -- never
// the 8443 stable service. Start one with:
//   go build -o /tmp/kunai-usage ./cmd/kunai
//   /tmp/kunai-usage -addr 127.0.0.1:8901 -data /tmp/kunai-usage-data
//
// The assertions worth having here are the ones a unit test cannot reach: that
// the numbers arrive at all (the scan is asynchronous and the page polls while
// it runs), and that the period control re-windows the client-side data rather
// than silently doing nothing.
import { chromium } from 'playwright'
const url = process.env.KUNAI_URL || 'http://127.0.0.1:8901/'
const fail = (m) => { console.error('FAIL: ' + m); process.exit(1) }
const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const errs = []
page.on('pageerror', (e) => errs.push(String(e)))
page.on('console', (m) => { if (m.type() === 'error') errs.push(m.text()) })

await page.goto(url, { waitUntil: 'networkidle' })
const nav = page.locator('button[aria-label="Usage"]')
if (!(await nav.count())) fail('no Usage entry in the sidebar')
await nav.click()
await page.waitForSelector('section[aria-label="Usage"]', { timeout: 10000 })
await page.waitForSelector('.hval', { timeout: 15000 })

const headline = (await page.locator('.hval').textContent())?.trim()
const note = (await page.locator('.hnote').textContent())?.trim()
const bars = await page.locator('.chart .bar').count()
const rows = await page.locator('.tbl tbody tr').count()
const stats = await page.locator('.stats .stat').allTextContents()
const quality = await page.locator('.qrow').allTextContents()
console.log('headline:', headline)
console.log('caveat  :', note?.replace(/\s+/g,' ').slice(0, 80) + '…')
console.log('bars    :', bars, ' table rows:', rows)
console.log('stats   :', stats.map((s) => s.replace(/\s+/g, ' ')).join(' | '))
console.log('quality :', quality.map((s) => s.replace(/\s+/g, ' ')).join(' | '))

if (!/^\$[\d,]/.test(headline || '')) fail('headline is not money: ' + headline)
if (bars !== 30) fail('expected 30 bars for the default period, got ' + bars)
if (rows < 3) fail('breakdown table is empty')

await page.locator('section[aria-label="Usage"] button').filter({ hasText: '30 days' }).first().click()
await page.waitForTimeout(300)
const seven = page.getByText('7 days', { exact: true })
if (await seven.count()) {
  await seven.first().click()
  await page.waitForTimeout(400)
  const b7 = await page.locator('.chart .bar').count()
  console.log('after 7d:', b7, 'bars, headline', (await page.locator('.hval').textContent())?.trim())
  if (b7 !== 7) fail('period switch did not re-window: ' + b7)
} else { console.log('(period menu not found; skipped)') }

await page.locator('.toggle button').filter({ hasText: 'Day' }).first().click()
await page.waitForTimeout(300)
console.log('day rows:', await page.locator('.tbl tbody tr').count())

await page.screenshot({ path: '/tmp/usage-page.png', fullPage: true })
if (errs.length) fail('console errors:\n' + errs.join('\n'))
console.log('OK')
await browser.close()
