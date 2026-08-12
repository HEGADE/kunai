// The configuration pages, against a scratch server on 127.0.0.1:8902.
//
// Settings, Accounts, Providers and Channels used to be modals. They are routes
// now, and most of what is worth asserting here follows from that: each one has
// a URL, survives a reload, and hands the back button somewhere sensible. A
// modal that merely looks like a page passes none of those.
//
// Start one with:
//   go build -o /tmp/kunai-scratch ./cmd/kunai
//   /tmp/kunai-scratch -addr 127.0.0.1:8902 -data /tmp/kunai-set-data
import { chromium } from 'playwright'

const url = 'http://127.0.0.1:8902/'
const fail = (m) => {
  console.error('FAIL: ' + m)
  process.exit(1)
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const crashes = []
page.on('pageerror', (e) => crashes.push(e.message))

await page.goto(url)
await page.waitForTimeout(1200)

// 1. Settings is a place with an address. The old modal left the URL on
// whatever was behind it, so a reload threw you back there and the link was
// worth nothing.
await page.locator('button[aria-label="Settings"]').first().click()
await page.waitForTimeout(600)
if (!/\/settings$/.test(page.url())) fail(`Settings did not take the URL: ${page.url()}`)
if (await page.locator('.backdrop').count()) fail('Settings still renders a modal backdrop')

// It survives a reload, which is the whole difference between a route and a
// view flag that happens to have been set.
await page.reload()
await page.waitForTimeout(1200)
if (!(await page.locator('.rail').count())) fail('Settings did not survive a reload')

// 2. The rail says whose settings each section changes. This is the fix for the
// actual clutter: the old column mixed a browser-scoped switch with six
// machine-scoped ones and nothing on screen said which followed the picker.
const railText = (await page.locator('.rail').innerText()).toLowerCase()
for (const want of ['this device', 'notifications', 'fleet', 'machines']) {
  if (!railText.includes(want)) fail(`the rail is missing "${want}": ${railText}`)
}

// 3. Sections switch without leaving the page, and the subtitle follows, so the
// scope is legible on a phone too, where the rail's headings cannot fit.
await page.locator('.rlink:has-text("Machines")').click()
await page.waitForTimeout(300)
if (!(await page.locator('.mcard').count())) fail('the Machines section listed no machines')

// Discover and Add are actions on the Machines section rather than controls
// buried among the switches: finding a machine is fleet management, not a
// setting.
const acts = (await page.locator('header .acts').innerText()).toLowerCase()
if (!acts.includes('discover') || !acts.includes('add')) {
  fail(`Machines does not offer Discover and Add as page actions: ${acts}`)
}
// And they are absent from every other section, or they would read as global.
await page.locator('.rlink:has-text("Notifications")').click()
await page.waitForTimeout(250)
if ((await page.locator('header .acts').innerText().catch(() => '')).toLowerCase().includes('discover')) {
  fail('Discover followed a section it has nothing to do with')
}

// 4. The machine-scoped group is named after the machine, not "Machine". A
// static word is one more thing that does not say which one.
const groups = await page.locator('.rgroup').allInnerTexts()
const named = groups.some((g) => !['this device', 'fleet'].includes(g.trim().toLowerCase()))
if (!named) fail(`no group is named after a machine: ${groups.join(' | ')}`)

// 5. Every machine-scoped section opens without throwing. Each one used to be a
// block in one long column, so a mistake in any of them broke the whole page.
for (const s of ['Network', 'Unattended', 'Accounts', 'Reviews']) {
  const link = page.locator(`.rlink:has-text("${s}")`)
  if (!(await link.count())) continue // needs a machine that is online
  await link.click()
  await page.waitForTimeout(250)
  if (!(await page.locator('.panel').count())) fail(`${s} rendered no panel`)
}

await page.screenshot({ path: '/tmp/settings.png', fullPage: true })

// 6. The other three are routes too, reachable from the nav and addressable.
for (const [label, path] of [
  ['Accounts', '/accounts'],
  ['Providers', '/providers'],
  ['Channels', '/channels'],
]) {
  await page.goto(url)
  await page.waitForTimeout(700)
  await page.locator(`button[aria-label="${label}"]`).first().click()
  await page.waitForTimeout(700)
  if (!page.url().endsWith(path)) fail(`${label} did not take ${path}: ${page.url()}`)
  if (await page.locator('.backdrop').count()) fail(`${label} still renders a modal backdrop`)
  if (!(await page.locator('.wrap').count())) fail(`${label} did not render its column`)
}

// 7. Back leaves the place rather than the app. On a phone the sidebar is
// hidden while a place is open, so this button is the only way out.
await page.locator('button[aria-label="Back"]').first().click()
await page.waitForTimeout(600)
if (/\/(settings|accounts|providers|channels)$/.test(page.url())) {
  fail(`Back did not leave the place: ${page.url()}`)
}

// 8. On a phone the rail becomes a strip of chips and the page still works.
const phone = await browser.newPage({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })
await phone.goto(url + 'settings')
await phone.waitForTimeout(1500)
if (!(await phone.locator('.rail').count())) fail('the settings rail is missing on a phone')
const chip = phone.locator('.rlink').first()
const box = await chip.boundingBox()
if (!box || box.height < 30) fail(`a settings chip is only ${box?.height}px tall, too small to tap`)
await phone.screenshot({ path: '/tmp/settings-phone.png', fullPage: true })
await phone.close()

if (crashes.length) fail('uncaught page exception: ' + crashes.join(' | '))
console.log('PASS: settings, accounts, providers and channels are routes, and the rail names its scopes')
await browser.close()
