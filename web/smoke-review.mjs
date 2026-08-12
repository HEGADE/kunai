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
      phase: 'done',
      total: 3,
      inline: 2,
      summary_count: 1,
      findings: [
        {
          index: 0, file: 'internal/session/loop.go', line: 212, side: 'RIGHT',
          title: 'Interrupt leaves the loop record on disk', body: 'The file outlives the run.',
          severity: 'blocker', confidence: 'high', category: 'correctness', verified: true,
          evidence: 'Followed Interrupt: it returns before stopLoopLocked runs.',
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
          severity: 'major', confidence: 'medium', category: 'compatibility', verified: true,
          inline: false, why: 'this pull request does not change internal/server/history.go',
        },
        {
          index: 2, file: 'web/src/lib/sidebar.ts', line: 40, side: 'RIGHT',
          title: 'shortAgo rounds down at the minute boundary', body: 'Cosmetic.',
          severity: 'minor', confidence: 'low', category: 'correctness', verified: false,
          inline: true,
        },
      ],
      // What verification refuted, kept with the reason so the filtering can be
      // audited rather than taken on trust.
      dropped: [
        {
          file: 'internal/session/manager.go', line: 17, severity: 'blocker',
          title: 'Create races with Close',
          why: 'Both take mu; the race described cannot happen.',
        },
      ],
    }),
  }),
)

// The session list is stubbed too, reporting idle. The view reads the session's
// live state to decide between "still working" and the finished verdict, and the
// scratch server's session never leaves `starting` because nothing ever prompts
// it over the socket. Stubbing the state is the difference between exercising
// the progress line and exercising the review.
const sessionRow = {
  id: 'review-smoke',
  cwd: '/tmp/review-demo',
  model: 'opus',
  effort: 'high',
  cli: 'Claude',
  title: 'Review #128 Snooze the sidebar rows',
  state: 'idle',
  created_at: new Date(Date.now() - 60000).toISOString(),
  project: '/tmp/review-demo',
}
await page.route((u) => new URL(u).pathname === '/api/sessions', (route) =>
  route.request().method() === 'GET'
    ? route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([sessionRow]) })
    : route.continue(),
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

// Three findings, each a self-contained card, badged with where it will land.
const cards = view.locator('.card')
if ((await cards.count()) !== 3) fail(`rendered ${await cards.count()} cards, want 3`)
const first = (await cards.first().innerText()).toLowerCase()
if (!first.includes('inline')) fail('the first finding is not badged inline')
if (!first.includes('stoplooplocked')) fail('the finding does not carry the code it is about')
if (!(await cards.nth(1).innerText()).toLowerCase().includes('summary')) {
  fail('the unanchorable finding is not badged as going to the summary')
}

// 5. The review has a SHAPE. This is the fix for the thing that made it
// unreadable: a wall of identical cards in emission order, with nothing saying
// which one was the data-loss bug.
//
// Most serious first, regardless of the order they arrived in, and every card
// names its severity as text beside the colour: a reader who cannot separate
// red from amber must still be able to read the review.
const sevOrder = await cards.locator('.sev').allInnerTexts()
if (sevOrder.join(',').toLowerCase() !== 'blocker,major,minor') {
  fail(`findings are not sorted most serious first: ${sevOrder.join(',')}`)
}

// The verdict line is the one thing worth reading before any of the findings.
const verdict = (await page.locator('.verdict').innerText()).toLowerCase()
if (!/1 blocker/.test(verdict)) fail(`the verdict does not lead with the blocker: ${verdict}`)
if (!/refuted/.test(verdict)) fail(`the verdict does not mention what was refuted: ${verdict}`)

// An unchecked claim says so. A reviewer that hedges when it is unsure is what
// earns belief when it is not.
if (!(await cards.nth(2).innerText()).toLowerCase().includes('not independently checked')) {
  fail('an unverified finding does not say that it is unverified')
}
if (!first.includes('followed interrupt')) fail('a verified finding does not show its evidence')

// 6. What the review considered and threw away, with the reason. A reviewer you
// can audit is one you will trust: three findings from a reviewer that refuted
// four is a different thing from three findings from one that found three.
await page.locator('.rhead').click()
await page.waitForTimeout(200)
const audit = (await page.locator('.refuted').innerText()).toLowerCase()
if (!audit.includes('create races with close')) fail('the refuted finding is not listed')
if (!audit.includes('both take mu')) fail('the refuted finding does not say why it was dropped')

// 7. Filtering, because a twelve-finding review judged one card at a time is why
// people stop reading at the fourth.
await page.locator('.chip.sev-minor').click()
await page.waitForTimeout(250)
if ((await cards.count()) !== 1) fail(`filtering to minor showed ${await cards.count()} cards, want 1`)

// Bulk is scoped to what is SHOWN, so "drop all" under a filter cannot silently
// drop the blockers that were filtered away.
await page.locator('.bact:has-text("Drop all")').click()
await page.waitForTimeout(250)
await page.locator('.chip:has-text("All")').click()
await page.waitForTimeout(250)
if (!(await page.locator('.post').innerText()).includes('2')) {
  fail(`drop-all under a filter did not stay scoped to the filter: ${await page.locator('.post').innerText()}`)
}

// 8. Editing, so a finding whose point is right and whose wording is wrong does
// not have to be thrown away to get rid of the sentence.
await cards.first().locator('button:has-text("Edit")').click()
await page.waitForTimeout(200)
await page.locator('.ed-title').fill('Interrupt leaves the loop file behind')
await page.locator('.sevpick:has-text("Minor")').click()
await page.locator('.ed-save').click()
await page.waitForTimeout(300)
const edited = (await view.innerText()).toLowerCase()
if (!edited.includes('leaves the loop file behind')) fail('an edited finding did not keep the new wording')
if (!edited.includes('rewritten by you')) fail('an edited finding does not say it was rewritten')
// Lowering its severity has to move the card, or the sort is a lie.
const afterEdit = await cards.locator('.sev').allInnerTexts()
if (afterEdit[0].toLowerCase() === 'blocker') fail('an edited severity did not re-sort the list')

// And it has to move the HEADLINE. Overruling the only blocker down to a minor
// while the verdict still announces a blocker is the summary lying about the
// thing it exists to summarise, which a screenshot caught and every selector
// assertion above walked straight past.
const afterVerdict = (await page.locator('.verdict').innerText()).toLowerCase()
if (afterVerdict.includes('blocker')) {
  fail(`the verdict still claims a blocker after the only one was overruled: ${afterVerdict}`)
}
// The filter chips count the same way, or a chip offers a count its own filter
// then shows nothing for.
if (await page.locator('.chip.sev-blocker').count()) {
  fail('a Blocker filter chip survived the last blocker being overruled')
}

await page.screenshot({ path: '/tmp/review-draft.png', fullPage: true })

// Post names what it will send, and dropping one changes that number: the
// header is a promise about what lands on the pull request.
await page.locator('.bact:has-text("Keep all")').click()
await page.waitForTimeout(250)
if (!(await page.locator('.post').innerText()).includes('3')) {
  fail(`Post does not say how many findings it will send: ${await page.locator('.post').innerText()}`)
}
await cards.first().locator('button:has-text("Drop")').click()
await page.waitForTimeout(300)
if (!(await page.locator('.post').innerText()).includes('2')) {
  fail('dropping a finding did not change what Post promises')
}

// Dropping every one is a decision, not a dead end: the summary is still a
// review, so the button changes what it promises rather than going grey.
await page.locator('.bact:has-text("Drop all")').click()
await page.waitForTimeout(300)
if (!(await page.locator('.post').innerText()).toLowerCase().includes('summary')) {
  fail('with every finding dropped, Post does not say it will send the summary alone')
}
if (crashes.length) fail('uncaught page exception: ' + crashes.join(' | '))
console.log('PASS: no App is invisible, a bad key is refused, and a review opens on its findings')
await browser.close()
