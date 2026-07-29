// The pull-request review surfaces, against the scratch server on :8901.
//
// No GitHub App is configured there, which is the point of most of this: the
// dashboard must stay usable, Settings must explain what is missing, and nothing
// may throw. The draft card is then driven directly against the API so its
// rendering is exercised without needing a real pull request.
import { chromium } from 'playwright'

const url = 'http://127.0.0.1:8901/'
const fail = (m) => {
  console.error('FAIL: ' + m)
  process.exit(1)
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const crashes = []
page.on('pageerror', (e) => crashes.push(e.message))

await page.goto(url)
await page.waitForTimeout(1500)

// 1. With no App configured, the dashboard still works and the card stays out of
// the way rather than showing an error nobody can act on from here.
// A placeholder is an attribute, not text, so it is matched as one.
if (!(await page.locator('textarea[placeholder="What should Claude work on?"]').count())) {
  fail('the dashboard did not render')
}

// 2. Settings explains what to do, and never shows key material. Opened from the
// nav rather than by URL: settings is a view, not a route.
await page.locator('button[aria-label="Settings"]').first().click()
await page.waitForTimeout(1200)
const gh = page.locator('text=Reviews are posted by a GitHub App')
if (!(await gh.count())) fail('the GitHub section is missing from Settings')
const body = await page.locator('body').innerText()
if (/BEGIN RSA PRIVATE KEY-----\n/.test(body)) fail('Settings rendered key material')
if (!/pull requests read and write/.test(body)) fail('Settings does not say what permissions the App needs')

// 3. A bad key is refused at the moment somebody can fix it, not at first use.
await page.locator('input[placeholder="App id"]').first().fill('123456')
await page.locator('textarea[placeholder^="-----BEGIN"]').first().fill('not a key at all')
await page.locator('button:has-text("Save")').first().click()
await page.waitForTimeout(800)
const afterSave = await page.locator('body').innerText()
if (!/PEM|RSA|could not be parsed/i.test(afterSave)) {
  fail('a malformed key was not refused with a readable reason')
}

// 4. The draft card renders from the API's shape, including the badge that says
// where each finding lands. Driven by stubbing the endpoint, because a real
// draft needs a real pull request and a real review.
await page.route('**/api/sessions/*/review', (route) =>
  route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      owner: 'lyzr',
      repo: 'kunai',
      number: 128,
      title: 'Snooze the sidebar rows',
      head_sha: 'abc123',
      from_fork: false,
      requester: '@shorya',
      summary: 'Sound overall, with one caller left behind.',
      total: 2,
      inline: 1,
      summary_count: 1,
      findings: [
        {
          index: 0, file: 'internal/session/loop.go', line: 212, side: 'RIGHT',
          title: 'Interrupt leaves the loop record on disk', body: 'The file outlives the run.',
          inline: true, suggestion: 'stopLoopLocked()',
        },
        {
          index: 1, file: 'internal/server/history.go', line: 88, side: 'RIGHT',
          title: 'The caller still expects the old shape', body: 'It will not compile.',
          inline: false, why: 'this pull request does not change internal/server/history.go',
        },
      ],
    }),
  }),
)

await page.goto(url)
await page.waitForTimeout(1200)
await page.locator(".row:has(.node:not([data-state='past']))").first().click()
await page.waitForTimeout(1500)

const card = page.locator('.draft')
if (!(await card.count())) fail('the review draft card did not render')
// Compared case-insensitively: the label is upper-cased by CSS, and innerText
// reports it as rendered.
const text = (await card.innerText()).toLowerCase()
for (const want of ['review draft', 'lyzr/kunai#128', '@shorya', 'inline', 'summary']) {
  if (!text.includes(want)) fail(`the draft card is missing ${JSON.stringify(want)}:\n${text}`)
}
if (!/2 findings . 1 inline . 1 in the summary/.test(text)) {
  fail(`the counts line does not promise what will land:\n${text}`)
}

// Dropping a finding updates the promise, which is the whole point of pruning
// before posting.
await card.locator('button[aria-label="Drop this finding"]').first().click()
await page.waitForTimeout(300)
if (!/1 finding . 0 inline . 1 in the summary/.test(await card.innerText())) {
  fail('dropping a finding did not update the counts')
}

await page.screenshot({ path: '/tmp/review-draft.png' })
if (crashes.length) fail('uncaught page exception: ' + crashes.join(' | '))
console.log('PASS: dashboard survives no App, Settings refuses a bad key, draft card renders and prunes')
await browser.close()
