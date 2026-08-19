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

// 2. Settings explains what to do, and never shows key material. Settings is a
// route now and its sections are a rail, so the GitHub App lives under Reviews
// rather than at the bottom of one long column.
await page.locator('button[aria-label="Settings"]').first().click()
await page.waitForTimeout(1200)
await page.locator('.rlink:has-text("Reviews")').click()
await page.waitForTimeout(600)
const gh = page.locator('text=Reviews post as a bot')
if (!(await gh.count())) fail('the GitHub section is missing from Settings')
const body = await page.locator('body').innerText()
if (/BEGIN RSA PRIVATE KEY-----\n/.test(body)) fail('Settings rendered key material')
if (!/pull requests read and write/.test(body)) fail('Settings does not say what permissions the App needs')

// 3. A bad key is refused at the moment somebody can fix it, not at first use.
// The App id field is labelled rather than placeholder-labelled now, and its
// placeholder is an example id. Matched by its label so a copy change to the
// example cannot break this again.
await page.locator('.st-field:has-text("App id") input').first().fill('123456')
await page.locator('textarea[placeholder^="-----BEGIN"]').first().fill('not a key at all')
await page.locator('button:has-text("Save")').first().click()
await page.waitForTimeout(800)
const afterSave = await page.locator('body').innerText()
if (!/PEM|RSA|could not be parsed/i.test(afterSave)) {
  fail('a malformed key was not refused with a readable reason')
}

// 4. The review workspace. Three columns, each answering a different question:
// the queue says where you are in a fixed list, the pane is the reading, and the
// rail is what to do about the one you are reading.
const FIND = (i, over = {}) => ({
  index: i, file: 'internal/session/loop.go', line: 210 + i, end_line: 212 + i, side: 'RIGHT',
  severity: i === 0 ? 'blocker' : 'major', confidence: 'high', verified: true, inline: true,
  short: `Short claim ${i}`, title: `The full claim for finding number ${i}`,
  body: 'Session.Interrupt returns before stopLoopLocked runs (internal/session/loop.go:212).',
  hunk: [{ kind: '+', new: 212, text: '\tstopLoopLocked(s)', focus: true }],
  ...over,
})
await page.route('**/api/sessions/*/review', (route) =>
  route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      owner: 'lyzr', repo: 'kunai', number: 128, title: 'Snooze the sidebar rows',
      head_sha: 'abc123', base_ref: 'main', from_fork: false, phase: 'done',
      summary: 'Sound overall, with one caller left behind.',
      files: [{ path: 'a.go', additions: 1, deletions: 1 }, { path: 'b.go', additions: 2, deletions: 0 }],
      clean: ['Review index mapping', 'Driver lifetime'],
      findings: [
        FIND(0, {
          grounds: [
            { key: 'TRACE', value: 'Interrupt returns before stopLoopLocked' },
            { key: 'CALLERS', value: 'two call sites, neither guards it' },
          ],
          impact: { who: 'any loop', radius: 'a resurrected run at boot', size: '3 lines' },
          patch: {
            title: 'Stop the loop before the driver goes',
            lines: [{ sign: '-', text: 'drv.Close()' }, { sign: '+', text: 'stopLoopLocked(s)' }],
          },
        }),
        FIND(1),
        FIND(2, { inline: false, why: 'this pull request does not change that file' }),
      ],
      dropped: [{ file: 'm.go', line: 17, severity: 'blocker', title: 'Create races with Close', why: 'Both take mu.' }],
    }),
  }),
)

const sessionRow = {
  id: 'review-smoke', cwd: '/tmp/review-demo', model: 'opus', effort: 'high', cli: 'Claude',
  title: 'Review #128 Snooze the sidebar rows', state: 'idle',
  created_at: new Date(Date.now() - 60000).toISOString(), project: '/tmp/review-demo',
}
await page.route((u) => new URL(u).pathname === '/api/sessions', (route) =>
  route.request().method() === 'GET'
    ? route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([sessionRow]) })
    : route.continue(),
)

await page.goto(url)
await page.waitForTimeout(1200)
await page.locator(".row:has(.node:not([data-state='past']))").first().click()
await page.waitForTimeout(1600)

const view = page.locator('.rvx')
if (!(await view.count())) fail('a review session did not open on its findings')
if (await page.locator('textarea[placeholder="Message Claude…"]').count()) {
  fail('the review opened on the transcript instead of the findings')
}
// The review takes the whole window: the session list beside it is a list of
// things you are deliberately not doing.
if (await page.locator('aside.sidebar').isVisible().catch(() => false)) {
  fail('the session sidebar is still taking a third of the review')
}

// The bar names the pull request and where it lands.
const bar = (await page.locator('header.bar').innerText()).toLowerCase()
if (!bar.includes('lyzr/kunai')) fail(`the bar does not name the repository: ${bar}`)
if (!bar.includes('#128')) fail('the bar does not name the pull request')
if (!bar.includes('main')) fail('the bar does not say what this merges into')
if (!bar.includes('conversation')) fail('there is no way through to the conversation')

// 5. THE QUEUE. Every finding as one row, numbered, worst first, each carrying
// the SHORT claim rather than a title truncated mid-clause.
const rows = page.locator('.rail .row')
if ((await rows.count()) !== 3) fail(`the queue has ${await rows.count()} rows, want 3`)
if (!(await rows.first().innerText()).includes('01')) fail('queue rows are not numbered')
if (!(await rows.first().innerText()).includes('Short claim 0')) {
  fail('the queue shows the full title rather than the short one')
}
// One pip per finding: the whole review's state in the width of a word.
if ((await page.locator('.pips .pip').count()) !== 3) fail('the bar does not carry a pip per finding')
// What was checked and found CLEAN: the half of a review that normally goes
// unsaid, and the only thing that tells a thorough reviewer from one that stopped.
if (!(await page.locator('.rail .clean').innerText()).includes('Driver lifetime')) {
  fail('the queue does not say what was checked and found clean')
}

// 6. THE DETAIL RAIL answers the four questions a reader has once they believe a
// claim, in order: what would I change, what checked it, who can reach it, what
// do I do.
const rail = page.locator('aside.rail').last()
const railText = (await rail.innerText()).toLowerCase()
if (!railText.includes('finding 01')) fail('the detail rail does not say which finding it is showing')
if (!railText.includes('suggested patch')) fail('the detail rail shows no patch')
if (!railText.includes('stop the loop before the driver goes')) fail('the patch has no title')
if (!railText.includes('what checked it')) fail('the detail rail does not show what checked the claim')
if (!railText.includes('trace')) fail('the grounds are not labelled')
if (!railText.includes('impact')) fail('the detail rail does not show impact')
if (!railText.includes('a resurrected run at boot')) fail('impact does not carry the blast radius')

// The patch is COPIED, not applied. A review runs with Write, Edit and Bash
// withheld, which is the property that lets it run unattended on somebody
// else's branch; a button that writes to the tree would undo it.
if (railText.includes('apply as a commit')) {
  fail('the review offers to write to the tree, which its toolset deliberately forbids')
}
if (!railText.includes('copy the patch')) fail('there is no way to take the patch')

// 7. ACCEPT and DISMISS, and the rule that matters most: an UNDECIDED finding is
// SENT. Silence is not a dismissal, and a reviewer that quietly dropped
// everything you had not got to would be worse than one that posted too much.
const cta = page.locator('.cta')
if (!(await cta.innerText()).includes('3')) fail(`nothing decided should still send all three: ${await cta.innerText()}`)
await rail.locator('.accept').click()
await page.waitForTimeout(250)
if (!(await cta.innerText()).includes('3')) fail('accepting changed what is sent, and it must not')
if (!(await page.locator('.status').innerText()).includes('1 of 3')) fail('accepting did not count as resolved')

// Dismissing is the only thing that holds one back.
await page.keyboard.press('j')
await page.waitForTimeout(300)
await page.keyboard.press('x')
await page.waitForTimeout(300)
if (!(await cta.innerText()).includes('2')) fail(`dismissing did not hold a finding back: ${await cta.innerText()}`)
// And it can be undone.
await rail.locator('.undo').click()
await page.waitForTimeout(250)
if (!(await cta.innerText()).includes('3')) fail('a dismissal could not be undone')

// 8. The rails collapse, and the queue collapses to numbered STUBS rather than
// to nothing: the point of it is knowing where you are in a fixed list, and that
// survives at 44px.
await page.keyboard.press('[')
await page.waitForTimeout(400)
if (await page.locator('.rail .rows').isVisible().catch(() => false)) fail('[ did not collapse the queue')
if ((await page.locator('.rail .stub').count()) !== 3) fail('the collapsed queue lost its place markers')
await page.keyboard.press('[')
await page.waitForTimeout(400)

// 9. Posting. The whole screen changes, so nothing floats over the top of it.
await page.route('**/api/sessions/*/review/post', (route) =>
  route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ url: 'https://example.test/r/1' }) }),
)
await page.screenshot({ path: '/tmp/review-workspace.png' })

// 10. A failure is a TOAST, where the reader is looking, and never a line at the
// end of a scrolling column.
await page.unroute('**/api/sessions/*/review/post')
await page.route('**/api/sessions/*/review/post', (route) =>
  route.fulfill({ status: 409, contentType: 'application/json', body: JSON.stringify({ error: 'the bot is not installed here' }) }),
)
await cta.click()
await page.waitForTimeout(700)
const toast = page.locator('.toast.error')
if (!(await toast.count())) fail('a failed post said nothing where the reader was looking')
if (!(await toast.innerText()).toLowerCase().includes('not installed')) fail('the toast does not carry the reason')
const tBox = await toast.boundingBox()
if (!tBox || tBox.y > 200) fail(`the toast is not where the reader is looking (y=${tBox?.y})`)
await page.locator('.toast .x').click()
await page.waitForTimeout(250)

// 11. A review IN PROGRESS: a page, not a progress line. Everything on it
// already existed and none of it was being shown.
await page.route('**/api/sessions/*/review', (route) =>
  route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      owner: 'lyzr', repo: 'kunai', number: 128, title: 'T', head_sha: 'a', from_fork: false,
      phase: 'verify', surveyed: true, findings: [],
      files: [{ path: 'internal/session/loop.go', additions: 120, deletions: 8 }],
      timeline: [
        { phase: 'survey', at: new Date(Date.now() - 300000).toISOString() },
        { phase: 'find', at: new Date(Date.now() - 200000).toISOString() },
        { phase: 'verify', at: new Date(Date.now() - 40000).toISOString() },
      ],
      survey: { intent: 'x', areas: [{ what: 'The loop record lifetime', files: ['internal/session/loop.go'] }] },
    }),
  }),
)
await page.route((u) => new URL(u).pathname === '/api/sessions', (route) =>
  route.request().method() === 'GET'
    ? route.fulfill({
        status: 200, contentType: 'application/json',
        body: JSON.stringify([{ ...sessionRow, state: 'running', turn_started_at: Date.now() - 92000 }]),
      })
    : route.continue(),
)
await page.goto(url)
await page.waitForTimeout(1200)
await page.locator(".row:has(.node:not([data-state='past']))").first().click()
await page.waitForTimeout(1600)
const runText = (await page.locator('.run').innerText()).toLowerCase()
if (!runText.includes('refute')) fail(`the running screen does not say what the phase is doing: ${runText}`)
if (!runText.includes('the loop record lifetime')) fail('it does not show what the reviewer decided to look at')
if (!runText.includes('loop.go')) fail('it does not show the change under review')
if (!/\d+m/.test(await page.locator('.trail').innerText())) fail('the trail does not say how long each phase took')
// Nothing to decide yet, so no rails and no queue.
if (await page.locator('.rail .rows').count()) fail('a running review offered a queue of findings it has not found')
await page.screenshot({ path: '/tmp/review-running.png' })

if (crashes.length) fail('uncaught page exception: ' + crashes.join(' | '))
console.log('PASS: no App is invisible, a bad key is refused, and the review workspace holds together')
await browser.close()
