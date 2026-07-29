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

// 1. With no App configured the dashboard still works, and the card is ABSENT
// rather than reporting a capability nobody asked for as a failure.
//
// The second half is the assertion that matters. Without it this shipped an
// error onto the dashboard of every user who does not review pull requests,
// and the test passed because it only checked that the page rendered at all.
// A placeholder is an attribute, not text, so it is matched as one.
if (!(await page.locator('textarea[placeholder="What should Claude work on?"]').count())) {
  fail('the dashboard did not render')
}
if (await page.locator('.prs').count()) {
  fail('the pull requests card appeared with no GitHub App configured: ' +
    (await page.locator('.prs').first().innerText()))
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

// 4. A review session opens on its FINDINGS, not on the transcript, and each one
// carries the code it is about. Driven by stubbing the endpoint, because a real
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
          hunk: [
            { kind: ' ', new: 211, text: 'func stop() {' },
            { kind: '+', new: 212, text: '\tstopLoopLocked(s)', focus: true },
            { kind: ' ', new: 213, text: '}' },
          ],
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

// The review view replaces the chat for a review session. The chat is still
// reachable, which is the point of the Conversation button.
const view = page.locator('.rv')
if (!(await view.count())) fail('a review session did not open on its findings')
if (await page.locator('textarea[placeholder="Message Claude…"]').count()) {
  fail('the review opened on the transcript instead of the findings')
}

const head = (await page.locator('.top').innerText()).toLowerCase()
if (!head.includes('lyzr/kunai#128')) fail(`the header does not name the pull request: ${head}`)
if (!head.includes('conversation')) fail('there is no way through to the conversation')

// Two findings, each a self-contained card, badged with where it will land.
const cards = view.locator('.card')
if ((await cards.count()) !== 2) fail(`rendered ${await cards.count()} cards, want 2`)
const first = (await cards.first().innerText()).toLowerCase()
if (!first.includes('inline')) fail('the first finding is not badged inline')
if (!first.includes('stoplooplocked')) fail('the finding does not carry the code it is about')
if (!(await cards.nth(1).innerText()).toLowerCase().includes('summary')) {
  fail('the unanchorable finding is not badged as going to the summary')
}

// Post names what it will send, and dropping one changes that number: the
// header is a promise about what lands on the pull request.
if (!(await page.locator('.post').innerText()).includes('2')) {
  fail('Post does not say how many findings it will send')
}
await cards.first().locator('button:has-text("Drop")').click()
await page.waitForTimeout(300)
if (!(await page.locator('.post').innerText()).includes('1')) {
  fail('dropping a finding did not change what Post promises')
}

// Keyboard, because a review is a rhythm rather than a page you scroll.
// Dropping the last one is a decision, not a dead end: the summary is still a
// review, so the button changes what it promises rather than going grey.
await page.keyboard.press('j')
await page.keyboard.press('d')
await page.waitForTimeout(300)
if (!(await page.locator('.post').innerText()).toLowerCase().includes('summary')) {
  fail('with every finding dropped, Post does not say it will send the summary alone')
}

await page.screenshot({ path: '/tmp/review-draft.png' })
if (crashes.length) fail('uncaught page exception: ' + crashes.join(' | '))
console.log('PASS: no App is invisible, a bad key is refused, and a review opens on its findings')
await browser.close()
