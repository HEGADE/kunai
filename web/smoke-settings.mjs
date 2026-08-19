// Settings, against a scratch server on 127.0.0.1:8902.
//
// Settings is the one place configuration lives. Accounts, Providers and
// Channels were pages of their own, and Accounts was the tell that it was
// wrong: it existed twice, once as a page with the real sign-in flow and once
// as a section inside Settings listing the same accounts with a link across to
// the page. Most of what is asserted here is that there is exactly one of each
// thing, reachable by URL.
//
// Start one with:
//   go build -o /tmp/kunai-s ./cmd/kunai
//   /tmp/kunai-s -addr 127.0.0.1:8902 -data /tmp/ks-data
import { chromium } from 'playwright'

const url = 'http://127.0.0.1:8902/'
const fail = (m) => {
  console.error('FAIL: ' + m)
  process.exit(1)
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const crashes = []
page.on('pageerror', (e) => crashes.push(e.message))

await page.goto(url)
await page.waitForTimeout(1200)

// 1. Settings is a place with an address, and its sections are addresses too.
// The old modal left the URL on whatever was behind it, so a reload threw you
// back there and the link was worth nothing.
await page.locator('button[aria-label="Settings"]').first().click()
await page.waitForTimeout(700)
if (!/\/settings\//.test(page.url())) fail(`Settings did not take a section URL: ${page.url()}`)
if (await page.locator('.backdrop').count()) fail('Settings still renders a modal backdrop')

// A section survives a reload. That is the whole difference between a route and
// a view flag that happens to be set.
await page.goto(url + 'settings/unattended')
await page.waitForTimeout(1400)
if (!(await page.locator('.rlink.on:has-text("Unattended")').count())) {
  fail('a section URL did not open that section')
}

// 2. ACCOUNTS EXISTS ONCE. The duplicate had two tells: a second roster of the
// same accounts, and a link from one to the other. Neither may come back.
await page.goto(url + 'settings/accounts')
await page.waitForTimeout(1500)
const accountsText = await page.locator('.panel').innerText()
if (/is on the\s+Accounts\s+page/i.test(accountsText)) {
  fail('Settings still links across to a separate Accounts page')
}
// The real sign-in flow is here, not a form asking for a config folder path.
if (!/Sign in another Claude subscription/i.test(accountsText)) {
  fail('the Accounts section is not the real sign-in flow')
}
if (await page.locator('input[placeholder^="Config folder"]').count()) {
  fail('the old raw config-folder form is still in Settings')
}
// And there is no separate page to reach it at any more.
await page.goto(url + 'accounts')
await page.waitForTimeout(1200)
if (await page.locator('.roster').count()) fail('/accounts still serves a second Accounts page')

// 3. Every section renders, and each one says what it decides rather than only
// naming a noun.
for (const s of ['notifications', 'machines', 'accounts', 'providers', 'channels', 'network', 'unattended', 'reviews']) {
  await page.goto(url + 'settings/' + s)
  await page.waitForTimeout(900)
  const head = page.locator('.shead')
  if (!(await head.count())) fail(`${s} has no section header`)
  const blurb = (await head.locator('p').innerText()).trim()
  if (blurb.length < 20) fail(`${s} has no line saying what it decides: "${blurb}"`)
}

// 3b. Every section is built from the ONE shared card, and none of them runs off
// the side. Both are what went wrong when each section styled itself: Reviews
// was a wall of bare paragraphs and inputs next to Accounts' cards, and its
// status line, an unwrapped flex row carrying an App name, a list of orgs and a
// numeric id, pushed the page wider than the window.
for (const s of ['accounts', 'providers', 'network', 'unattended', 'reviews', 'machines']) {
  await page.goto(url + 'settings/' + s)
  await page.waitForTimeout(1000)
  if (!(await page.locator('.st-card').count())) {
    fail(`${s} does not use the shared settings card`)
  }
  const over = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  if (over > 0) fail(`${s} overflows the window by ${over}px`)
}

// 4. The rail says whose settings each section changes. This is the fix for the
// actual clutter: one column mixed a browser-scoped switch with machine-scoped
// ones and nothing said which followed the picker.
const railText = (await page.locator('.rail').innerText()).toLowerCase()
for (const want of ['this device', 'notifications', 'fleet', 'machines']) {
  if (!railText.includes(want)) fail(`the rail is missing "${want}": ${railText}`)
}
// The machine group is headed by the machine's own name, not the word "Machine".
const groups = await page.locator('.rgroup').allInnerTexts()
if (!groups.some((g) => !['this device', 'fleet'].includes(g.trim().toLowerCase()))) {
  fail(`no rail group is named after a machine: ${groups.join(' | ')}`)
}

// 5. Finding and adding machines is fleet management, so it belongs to the
// Machines section and nowhere else.
await page.goto(url + 'settings/machines')
await page.waitForTimeout(1000)
if (!(await page.locator('.foot:has-text("Find machines")').count())) {
  fail('Machines does not offer a way to find machines')
}
await page.goto(url + 'settings/unattended')
await page.waitForTimeout(900)
if (await page.locator('.foot:has-text("Find machines")').count()) {
  fail('Find machines followed a section it has nothing to do with')
}

// 6. The sidebar shortcuts are doors into Settings, and each marks itself
// current. A nav item that opens a page then claims nothing is open is how you
// end up building the same page twice.
for (const [label, section] of [
  ['Accounts', 'accounts'],
  ['Providers', 'providers'],
  ['Channels', 'channels'],
]) {
  await page.goto(url)
  await page.waitForTimeout(800)
  await page.locator(`button[aria-label="${label}"]`).first().click()
  await page.waitForTimeout(900)
  if (!page.url().endsWith('/settings/' + section)) {
    fail(`${label} did not open /settings/${section}: ${page.url()}`)
  }
  if (!(await page.locator(`button[aria-label="${label}"][aria-current="page"]`).count())) {
    fail(`${label} opened its section without marking itself current`)
  }
  // Exactly one nav item may claim the page.
  const current = await page.locator('nav button[aria-current="page"]').count()
  if (current !== 1) fail(`${current} nav items claim the page at once on ${section}`)
}

await page.screenshot({ path: '/tmp/settings.png' })

// 7. Back leaves the place. On a phone the sidebar is hidden while a place is
// open, so this is the only way out.
await page.locator('button[aria-label="Back"]').first().click()
await page.waitForTimeout(700)
if (/\/settings/.test(page.url())) fail(`Back did not leave Settings: ${page.url()}`)

// 8. On a phone the rail becomes a strip of chips and the page still works.
const phone = await browser.newPage({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })
await phone.goto(url + 'settings/unattended')
await phone.waitForTimeout(1600)
const chip = phone.locator('.rlink').first()
const box = await chip.boundingBox()
if (!box || box.height < 30) fail(`a settings chip is only ${box?.height}px tall, too small to tap`)
// The card must not run off the side.
const card = await phone.locator('.st-card').first().boundingBox()
if (!card || card.x < 0 || card.x + card.width > 390) {
  fail(`the settings card overflows the phone: x=${card?.x} w=${card?.width}`)
}
await phone.screenshot({ path: '/tmp/settings-phone.png' })
await phone.close()

if (crashes.length) fail('uncaught page exception: ' + crashes.join(' | '))
console.log('PASS: one Settings, one Accounts, every section addressable and scoped')
await browser.close()
