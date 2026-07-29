// Smoke test for the sidebar lifecycle work: snooze via the row menu, the
// snoozed shelf, persistence across reload, and wake-now. Runs against the
// scratch server on 127.0.0.1:8901 (fake home, fixture transcripts).
import { chromium } from 'playwright'

const url = 'http://127.0.0.1:8901/'
const fail = (msg) => {
  console.error('FAIL: ' + msg)
  process.exit(1)
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 850 } })
await page.goto(url)

// All three fixture sessions render, grouped by folder.
await page.waitForSelector('text=Refactor the session store', { timeout: 8000 })
for (const t of ['Fix the login bug', 'Write the report generator']) {
  if (!(await page.locator(`text=${t}`).count())) fail(`missing row: ${t}`)
}
const groups = await page.locator('.glabel').allInnerTexts()
if (!groups.includes('alpha') || !groups.includes('beta')) fail(`groups = ${groups}`)

// Snooze "Refactor the session store" for an hour through its row menu.
const row = page.locator('.row', { hasText: 'Refactor the session store' }).first()
await row.hover()
await row.locator('button[aria-label="Session actions"]').click()
await page.locator('button:has-text("Snooze")').first().click()
await page.locator('button:has-text("In 1 hour")').click()

// The row leaves its group for the shelf.
await page.waitForSelector('.snhead', { timeout: 5000 })
const shelfCount = await page.locator('.sncount').innerText()
if (shelfCount.trim() !== '1') fail(`shelf count = ${shelfCount}`)
if (await page.locator('.kids .row', { hasText: 'Refactor the session store' }).count())
  fail('snoozed row still sits in its group')

// Expand the shelf: the slim row shows the return ticket.
await page.locator('.snhead').click()
const shelfRow = page.locator('.row.snz', { hasText: 'Refactor the session store' })
if (!(await shelfRow.count())) fail('snoozed row not on the shelf')
const ticket = await shelfRow.locator('.snin').innerText()
if (!/in 1h|in 60m|in 59m/.test(ticket)) fail(`return ticket = ${ticket}`)

// Survives a reload: the snooze is server-side.
await page.reload()
await page.waitForSelector('.snhead', { timeout: 8000 })

// Wake it via the shelf row's menu.
await page.locator('.snhead').click()
const shelfRow2 = page.locator('.row.snz', { hasText: 'Refactor the session store' })
await shelfRow2.hover()
await shelfRow2.locator('button[aria-label="Session actions"]').click()
await page.locator('button:has-text("Wake now")').click()
await page.waitForSelector('.snhead', { state: 'detached', timeout: 5000 })
if (!(await page.locator('.kids .row', { hasText: 'Refactor the session store' }).count()))
  fail('woken row did not return to its group')

await page.screenshot({ path: '/tmp/kunai-nav-sidebar.png' })

// The other half of the rule: a snooze whose time has PASSED does not sit on
// the shelf waiting to be noticed. The row returns to its own group with a Woke
// pill, and the shelf disappears when nothing is parked. Planted through the
// API because waiting an hour is not a test.
const beta = '11111111-aaaa-bbbb-cccc-000000000003'
await fetch(`${url}api/sessions/${beta}`, {
  method: 'PATCH',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ snoozed_until: Date.now() - 5000 }),
})
await page.reload()
await page.waitForSelector('text=Write the report generator', { timeout: 8000 })
const expired = page.locator('.row', { hasText: 'Write the report generator' }).first()
if ((await expired.locator('.status.wokep').count()) !== 1) fail('an expired snooze shows no Woke pill')
if (await page.locator('.snhead').count()) fail('an expired snooze still holds the shelf open')
if (!(await page.locator('.kids .row', { hasText: 'Write the report generator' }).count()))
  fail('the woken row is not back in its group')
await page.screenshot({ path: '/tmp/kunai-nav-woke.png' })

console.log('PASS: snooze -> shelf -> persist -> wake, and an expired snooze wakes in place')
await browser.close()
