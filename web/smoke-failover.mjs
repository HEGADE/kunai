// The failover banner, driven through the real client socket.
//
// There is no route that forces a failover state (it only happens when a turn
// dies on a usage wall), so this wraps WebSocket before the app loads, then
// dispatches the same frame the server would send. The app cannot tell the
// difference: it goes through the identical onmessage path.
//
// Worth testing rather than assuming, because the whole bug being fixed was a UI
// that failed to say something. A server that broadcasts correctly into a client
// that renders nothing is the same bug over again.
import { chromium } from 'playwright'

const url = 'http://127.0.0.1:8901/'
const fail = (m) => {
  console.error('FAIL: ' + m)
  process.exit(1)
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 850 } })
const crashes = []
page.on('pageerror', (e) => crashes.push(e.message))

await page.addInitScript(() => {
  const Orig = window.WebSocket
  window.__socks = []
  window.WebSocket = class extends Orig {
    constructor(...args) {
      super(...args)
      window.__socks.push(this)
    }
  }
})

await page.goto(url)
await page.waitForTimeout(1500)
await page.locator(".row:has(.node:not([data-state='past']))").first().click()
await page.locator('textarea[placeholder="Message Claude…"]').first().waitFor({ timeout: 10000 })

const send = (frame) =>
  page.evaluate((f) => {
    const ws = window.__socks.filter((s) => s.url.includes('/ws/app/')).pop()
    if (!ws) throw new Error('no session socket found')
    ws.dispatchEvent(new MessageEvent('message', { data: JSON.stringify(f) }))
  }, frame)

// 1. Deciding: the banner must say work is under way, with its spinner.
await send({ t: 'failover', seq: 90001, failover_state: 'deciding' })
const banner = page.locator('.ratebanner')
await banner.waitFor({ timeout: 4000 }).catch(() => fail('no banner while failing over'))
const text = await banner.innerText()
if (!/finding an account with headroom/i.test(text)) fail(`banner reads ${JSON.stringify(text)}`)
if (!(await banner.locator('.fospin').count())) fail('no progress indicator on the failover banner')
await page.screenshot({ path: '/tmp/failover-deciding.png' })

// 2. Ended with a reason: the reason replaces it, dismissable.
const why = 'No other account has headroom, so this session stays put.'
await send({ t: 'failover', seq: 90002, failover_state: 'ended', message: why })
await page.waitForTimeout(400)
const after = await page.locator('.ratebanner').innerText()
if (!after.includes('No other account has headroom')) fail(`after ending, banner reads ${JSON.stringify(after)}`)
await page.screenshot({ path: '/tmp/failover-ended.png' })

// 3. A stand-down carries no message, so it must leave nothing behind rather
// than explaining a switch the user just made themselves.
await send({ t: 'failover', seq: 90003, failover_state: 'deciding' })
await page.waitForTimeout(200)
await send({ t: 'failover', seq: 90004, failover_state: 'ended' })
await page.waitForTimeout(400)
if (await page.locator('.ratebanner').count()) {
  fail('a silent stand-down left a banner: ' + (await page.locator('.ratebanner').innerText()))
}

if (crashes.length) fail('uncaught page exception: ' + crashes.join(' | '))
console.log('PASS: failover banner appears, reports a reason, and stands down silently')
await browser.close()
