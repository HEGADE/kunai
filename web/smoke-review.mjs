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
          title: 'Interrupt leaves the loop record on disk',
          // Written the way a reviewer actually writes: dense with the
          // identifiers and locations a reader is hunting for.
          body: 'Session.Interrupt returns before stopLoopLocked runs (internal/session/loop.go:212), so loops/<id>.json outlives the run and resumeLoops resurrects it at the next boot.',
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

// Three findings. Rows now, not cards: one finding used to fill a laptop screen
// with its body, its evidence and a thirteen-line hunk, so the review could not
// be seen at all. Collapsed rows are what make it a list you work through.
const rows = view.locator('.row')
if ((await rows.count()) !== 3) fail(`rendered ${await rows.count()} rows, want 3`)

// The review has a SHAPE. Most serious first, regardless of the order they
// arrived in, and every row names its severity as text beside the colour: a
// reader who cannot separate red from amber must still be able to read it.
const sevOrder = await rows.locator('.sev').allInnerTexts()
if (sevOrder.join(',').toLowerCase() !== 'blocker,major,minor') {
  fail(`findings are not sorted most serious first: ${sevOrder.join(',')}`)
}
const first = (await rows.first().innerText()).toLowerCase()
if (!first.includes('on the line')) fail('the first finding does not say where it will land')
if (!(await rows.nth(1).innerText()).toLowerCase().includes('in the summary')) {
  fail('the unanchorable finding is not badged as going to the summary')
}

// Exactly one finding is open, and it is the worst one: the review opens
// already reading rather than as a list of closed lines. The rest are single
// lines, which is the whole point of the rewrite.
if ((await view.locator('.row.open').count()) !== 1) {
  fail(`${await view.locator('.row.open').count()} findings are open at once, want exactly 1`)
}
if (!(await rows.first().getAttribute('class')).includes('open')) {
  fail('the review did not open on its worst finding')
}
// The open one carries the code it is about, at full size.
if (!first.includes('stoplooplocked')) fail('the open finding does not carry the code it is about')

// The verifier's working is NAMED and folded away rather than printed under
// every claim. It runs to a dozen lines of dense prose now that verification
// actually happens, and that is the wall this card exists to avoid: the reader
// who doubts a finding knows exactly which control to open, and the reader who
// does not is never made to scroll past it.
if (first.includes('followed interrupt')) fail('the verifier’s working is printed under the claim by default')
await rows.first().locator('.ex:has-text("What checked it")').click()
await page.waitForTimeout(250)
if (!(await rows.first().innerText()).toLowerCase().includes('followed interrupt')) {
  fail('a verified finding will not show what checked it')
}

// The identifiers in the argument are set as code, because they are what a
// reader is hunting for and flat text hides them.
if (!(await rows.first().locator('.why code').count())) {
  fail('the argument renders its identifiers as flat prose')
}

// Deciding must never require opening anything: Drop is on every row at every
// state, which is what lets a twelve-finding review be triaged at all.
for (let i = 0; i < 3; i++) {
  if (!(await rows.nth(i).locator('.drop').count())) fail(`row ${i} has no reachable decision`)
}

// An unchecked claim says so, on the row itself. Verification runs on
// everything postable now, so this is the exception rather than the norm it
// used to be when the phase never ran at all.
if (!(await rows.nth(2).innerText()).toLowerCase().includes('unchecked')) {
  fail('an unverified finding does not say so where it can be seen')
}

// Opening another closes the first. A deck, not a pile: a list where everything
// can be open is a screen and a half tall again by the third click.
await rows.nth(2).locator('.disc').click()
await page.waitForTimeout(250)
if ((await view.locator('.row.open').count()) !== 1) {
  fail('opening a second finding left the first one open')
}

// 5. The verdict is the shape of the review before any of it is read.
const verdict = (await page.locator('.verdict').innerText()).toLowerCase()
if (!/1 blocker/.test(verdict)) fail(`the verdict does not lead with the blocker: ${verdict}`)
if (!/refuted/.test(verdict)) fail(`the verdict does not mention what was refuted: ${verdict}`)
if (!verdict.includes('sound overall')) fail('the review summary is not shown with the verdict')

// 6. What the review considered and threw away, with the reason. A reviewer you
// can audit is one you will trust: three findings from a reviewer that refuted
// four is a different thing from three findings from one that found three.
await page.locator('.refuted .head').click()
await page.waitForTimeout(250)
const audit = (await page.locator('.refuted').innerText()).toLowerCase()
if (!audit.includes('create races with close')) fail('the refuted finding is not listed')
if (!audit.includes('both take mu')) fail('the refuted finding does not say why it was dropped')

// 7. Filtering, because a twelve-finding review judged one at a time is why
// people stop reading at the fourth.
await page.locator('.chip.sev-minor').click()
await page.waitForTimeout(250)
if ((await rows.count()) !== 1) fail(`filtering to minor showed ${await rows.count()} rows, want 1`)

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
await rows.first().locator('.disc').click()
await page.waitForTimeout(250)
await rows.first().locator('button:has-text("Edit the wording")').click()
await page.waitForTimeout(250)
await page.locator('.editor .t').fill('Interrupt leaves the loop file behind')
await page.locator('.pick:has-text("Minor")').click()
await page.locator('.editor .save').click()
await page.waitForTimeout(350)
const edited = (await view.innerText()).toLowerCase()
if (!edited.includes('leaves the loop file behind')) fail('an edited finding did not keep the new wording')
if (!edited.includes('your wording')) fail('an edited finding is not marked as the reader’s own words')
// Lowering its severity has to move the row, or the sort is a lie.
const afterEdit = await rows.locator('.sev').allInnerTexts()
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

// The action bar names what it will send, and dropping one changes that number:
// it is a promise about what lands on the pull request.
await page.locator('.bact:has-text("Keep all")').click()
await page.waitForTimeout(250)
if (!(await page.locator('.post').innerText()).includes('3')) {
  fail(`Post does not say how many findings it will send: ${await page.locator('.post').innerText()}`)
}
if (!(await page.locator('.bar').innerText()).toLowerCase().includes('on the line')) {
  fail('the bar does not say how the review will be delivered')
}
await rows.first().locator('.drop').click()
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

// 9. The phone. kunai is used from one, and the review is the surface where a
// split layout would have been easiest and most wrong.
await page.setViewportSize({ width: 390, height: 844 })
await page.waitForTimeout(400)
await page.locator('.bact:has-text("Keep all")').click()
await page.waitForTimeout(300)
const barBox = await page.locator('.bar').boundingBox()
if (!barBox || barBox.width > 390) fail('the action bar overflows a phone')
await page.screenshot({ path: '/tmp/review-phone.png', fullPage: true })

// 9b. Arguing with the reviewer is not a one-way door.
//
// "Ask about this" swaps the findings for the transcript, and nothing swapped
// them back: the only way to return to the thing you were deciding about was to
// close the session and reopen it. The whole reason to argue with a finding is
// to then act on it.
await page.setViewportSize({ width: 1280, height: 900 })
await page.waitForTimeout(300)
await rows.first().locator('.disc').click()
await page.waitForTimeout(250)
await rows.first().locator('button:has-text("Ask about this")').click()
await page.waitForTimeout(600)
if (await page.locator('.rv').count()) fail('Ask about this did not open the conversation')
const back = page.locator('.abtn.findings')
if (!(await back.count())) fail('there is no way back to the findings from the conversation')
await back.click()
await page.waitForTimeout(600)
if (!(await page.locator('.rv').count())) fail('the way back to the findings did not work')

// 10. A failure is a TOAST, not a line at the end of the list.
//
// Post is a button in the bar at the bottom and the findings above it scroll, so
// the reason it failed was being rendered somewhere the reader was not looking
// and styled like a footnote. "This pull request has moved on" is the most
// important sentence on the screen at that moment.
await page.locator('.bact:has-text("Keep all")').click()
await page.waitForTimeout(250)
await page.route('**/api/sessions/*/review/post', (route) =>
  route.fulfill({ status: 409, contentType: 'application/json', body: JSON.stringify({ error: 'the bot is not installed here' }) }),
)
await page.locator('.post').click()
await page.waitForTimeout(700)
const toast = page.locator('.toast.error')
if (!(await toast.count())) fail('a failed post said nothing where the reader was looking')
if (!(await toast.innerText()).toLowerCase().includes('not installed')) {
  fail(`the toast does not carry the reason: ${await toast.innerText()}`)
}
// Above the fold, which is the entire point: it must not need scrolling to.
const tBox = await toast.boundingBox()
if (!tBox || tBox.y > 200) fail(`the toast is not where the reader is looking (y=${tBox?.y})`)
// An error waits to be read rather than removing itself mid-sentence.
await page.waitForTimeout(5000)
if (!(await toast.count())) fail('the error dismissed itself while it was being read')
// Pressing a failing button again is one problem, not two.
await page.locator('.post').click()
await page.waitForTimeout(700)
if ((await page.locator('.toast').count()) !== 1) {
  fail(`pressing a failing button twice stacked ${await page.locator('.toast').count()} toasts`)
}
await page.locator('.toast .x').click()
await page.waitForTimeout(300)
if (await page.locator('.toast').count()) fail('a toast could not be dismissed')
await page.unroute('**/api/sessions/*/review/post')

// 11. A review IN PROGRESS, which is the screen somebody actually stares at:
// the phases run for minutes and there is nothing else on the page. A spinner
// and a clock cannot tell working from hung, so the wait reads as a hang.
await page.setViewportSize({ width: 1280, height: 900 })
await page.route('**/api/sessions/*/review', (route) =>
  route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      owner: 'lyzr', repo: 'kunai', number: 128, title: 'Snooze the sidebar rows',
      head_sha: 'abc123', from_fork: false, phase: 'verify', surveyed: true, findings: [],
      // Everything the running screen is made of, and every bit of it already
      // existed before it was shown: the change under review, when each phase
      // began, and what the reviewer decided to look at.
      files: [
        { path: 'internal/session/loop.go', additions: 120, deletions: 8 },
        { path: 'internal/server/history.go', additions: 40, deletions: 12 },
      ],
      timeline: [
        { phase: 'survey', at: new Date(Date.now() - 300000).toISOString() },
        { phase: 'find', at: new Date(Date.now() - 200000).toISOString() },
        { phase: 'verify', at: new Date(Date.now() - 40000).toISOString() },
      ],
      survey: {
        intent: 'Snoozes a sidebar row until it asks for attention again.',
        areas: [{ what: 'The loop record lifetime', files: ['internal/session/loop.go'], why: 'stopLoopLocked may not run.' }],
      },
    }),
  }),
)
await page.route((u) => new URL(u).pathname === '/api/sessions', (route) =>
  route.request().method() === 'GET'
    ? route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([{ ...sessionRow, state: 'running', turn_started_at: Date.now() - 92000 }]),
      })
    : route.continue(),
)
await page.goto(url)
await page.waitForTimeout(1200)
await page.locator(".row:has(.node:not([data-state='past']))").first().click()
await page.waitForTimeout(1500)

const trail = page.locator('.trail')
if (!(await trail.count())) fail('a running review shows no sense of where it has got to')
const runText = (await page.locator('.run').innerText()).toLowerCase()
if (!runText.includes('refute')) fail(`the running screen does not say what the phase is doing: ${runText}`)

// It is a PAGE, not a progress line. A review runs for minutes and the screen
// for those minutes used to say less than a progress bar would: not what is
// under review, not where the reviewer decided to look, not how far in it was.
// Every number here already existed and none of it was being shown.
if (!runText.includes('the loop record lifetime')) {
  fail('the running screen does not show what the reviewer decided to look at')
}
if (!runText.includes('loop.go')) fail('the running screen does not show the change under review')
if (!/files opened/.test(runText)) fail('the running screen does not report progress through the change')
// Each phase carries its own elapsed time: "how long has it been reading" is a
// different question from "how long has it been going", and the running turn's
// clock restarts at every phase so it could answer neither.
if (!/\d+m/.test(await trail.innerText())) fail('the trail does not say how long each phase took')
// Every step is named, so the wait has a length as well as a position.
if ((await trail.locator('li').count()) !== 3) {
  fail(`the trail drew ${await trail.locator('li').count()} steps, want 3`)
}
if ((await trail.locator('li.on').count()) !== 1) fail('the trail does not light exactly one step')
if ((await trail.locator('li.done').count()) !== 2) fail('the trail does not show the finished steps as done')
// And no action bar, because there is nothing yet to decide about.
if (await page.locator('.bar').count()) fail('a running review offered a Post button')
await page.screenshot({ path: '/tmp/review-running.png', fullPage: true })

// 12. The dashboard row tells the truth about a review it did not start.
//
// It used to know about one only from state held in the component that started
// it, so a refresh, or going to a session and coming back, put "Review" back on
// a pull request that already had a review. Clicking it then started another
// whole reading: minutes of work and real quota, spent because a button forgot.
// The row reads the server now, so a fresh page load is enough.
const fresh = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const ROWS = [
  { number: 5, title: 'Running', author: 'a', base_ref: 'main', head_sha: 'x', draft: false, from_fork: false,
    additions: 1, deletions: 1,
    review: { session_id: 's1', phase: 'find', running: true, findings: 0, posted: false, failed: false, stale: false } },
  { number: 6, title: 'Ready', author: 'a', base_ref: 'main', head_sha: 'x', draft: false, from_fork: false,
    additions: 1, deletions: 1,
    review: { session_id: 's2', phase: 'done', running: false, findings: 3, posted: false, failed: false, stale: false } },
  { number: 7, title: 'Found nothing', author: 'a', base_ref: 'main', head_sha: 'x', draft: false, from_fork: false,
    additions: 1, deletions: 1,
    review: { session_id: 's3', phase: 'done', running: false, findings: 0, posted: false, failed: false, stale: false } },
  { number: 8, title: 'Moved on', author: 'a', base_ref: 'main', head_sha: 'x', draft: false, from_fork: false,
    additions: 1, deletions: 1,
    review: { session_id: 's4', phase: 'done', running: false, findings: 2, posted: false, failed: false, stale: true } },
]
await fresh.route((u) => new URL(u).pathname === '/api/github/app', (r) =>
  r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ configured: true, app_id: '1' }) }))
await fresh.route((u) => new URL(u).pathname === '/api/github/pulls', (r) =>
  r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(ROWS) }))
await fresh.goto(url)
await fresh.waitForTimeout(2500)
const rowText = async (n) => (await fresh.locator(`main .prs .row:has-text("#${n}")`).first().innerText()).toLowerCase()
if (!(await rowText(5)).includes('reviewing')) fail('a running review is not reported on its row after a reload')
// "Ready" says nothing about whether it is worth opening; the count does.
if (!(await rowText(6)).includes('3 findings')) fail('a finished review does not say what it found')
if (!(await rowText(7)).includes('nothing found')) fail('a review that found nothing does not say so')
// A review of an older commit is not this pull request's review any more, so the
// row offers a fresh reading rather than pointing at a stale draft.
if (!/\breview\b/.test(await rowText(8)) || (await rowText(8)).includes('findings')) {
  fail(`a stale review does not offer a fresh reading: ${await rowText(8)}`)
}
await fresh.close()

if (crashes.length) fail('uncaught page exception: ' + crashes.join(' | '))
console.log('PASS: no App is invisible, a bad key is refused, and a review reads as a deck you triage')
await browser.close()
