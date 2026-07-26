// Drives the worktree UI against a real kunai and a real git repository.
//
// This is here rather than as a unit test because the thing worth checking is
// the seam: that what the launcher shows matches what the server would do, and
// that a session started from it actually lands in a separate checkout. Both
// halves are already unit-tested on their own; only the join is not.
//
// Run against a dev server (never the 8443 stable service):
//   kunai -addr 127.0.0.1:8899 -data /tmp/kunai-wt
//   node web/tests/worktree.spec.mjs [repoPath]

import { chromium } from 'playwright'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { homedir } from 'node:os'

const ORIGIN = process.env.KUNAI_ORIGIN || 'http://localhost:8899'
const REPO = process.argv[2] || '/tmp/e2erepo'
// A second repository, so the sidebar has more than one group and therefore
// renders headings. It used to get that from whatever history happened to be
// lying around, which stopped being true once this suite started clearing up
// after itself.
const REPO_B = '/tmp/kunai-e2e-other'
const SHOT_DIR = '/tmp/kunai-wt-shots'

const results = []
function check(name, ok, detail = '') {
  results.push({ name, ok, detail })
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`)
}

const git = (args, cwd = REPO) => execFileSync('git', args, { cwd, encoding: 'utf8' }).trim()

const worktreeCount = () =>
  git(['worktree', 'list', '--porcelain'])
    .split('\n')
    .filter((l) => l.startsWith('worktree ')).length

async function shot(page, name) {
  await page.screenshot({ path: `${SHOT_DIR}/${name}.png`, fullPage: false })
}

// The fixture is built here rather than by hand. Two of the checks below read
// what the launcher suggests, and a suggestion is derived from what is in the
// repository: a repo missing its lockfile or its .env produces a different (and
// correct) answer, which then looks exactly like a regression.
function buildFixture() {
  if (REPO !== '/tmp/e2erepo') return // a repo the caller chose; leave it alone
  rmSync(REPO, { recursive: true, force: true })
  mkdirSync(REPO, { recursive: true })
  git(['init', '-q', '-b', 'main'])
  git(['config', 'user.email', 'test@localhost'])
  git(['config', 'user.name', 'test'])
  writeFileSync(`${REPO}/README.md`, 'hello\n')
  writeFileSync(`${REPO}/package.json`, '{"name":"fixture","version":"1.0.0"}\n')
  writeFileSync(`${REPO}/package-lock.json`, '{"lockfileVersion":3}\n') // -> npm ci
  writeFileSync(`${REPO}/.gitignore`, '.env\n')
  writeFileSync(`${REPO}/.env`, 'SECRET=1\n') // ignored, so it is carried by a symlink
  git(['add', '-A'])
  git(['commit', '-q', '-m', 'fixture'])

  rmSync(REPO_B, { recursive: true, force: true })
  mkdirSync(REPO_B, { recursive: true })
  git(['init', '-q', '-b', 'main'], REPO_B)
  git(['config', 'user.email', 'test@localhost'], REPO_B)
  git(['config', 'user.name', 'test'], REPO_B)
  writeFileSync(`${REPO_B}/README.md`, 'second\n')
  git(['add', '-A'], REPO_B)
  git(['commit', '-q', '-m', 'fixture'], REPO_B)
}

async function main() {
  rmSync(SHOT_DIR, { recursive: true, force: true })
  mkdirSync(SHOT_DIR, { recursive: true })
  buildFixture()

  // Seed a session in the repository under test so the launcher lists it. Done
  // here rather than by hand so the run is repeatable from a clean data dir.
  const seeded = await fetch(`${ORIGIN}/api/sessions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ cwd: REPO }),
  }).then((r) => r.json())
  // The second one only exists so the sidebar has two groups and shows headings.
  await fetch(`${ORIGIN}/api/sessions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ cwd: REPO_B }),
  })
  check('seeded a session in the repository under test', !!seeded.id, seeded.id || JSON.stringify(seeded))

  const browser = await chromium.launch()
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  page.on('console', (m) => {
    if (m.type() === 'error') console.log('  [console error]', m.text())
  })

  await page.goto(ORIGIN)
  // The sidebar renders its own compact Home, so every selector below is scoped
  // to the full one. Measuring the compact instance by accident is a mistake this
  // repo has made before, and it fails in a way that looks like a real bug.
  const home = page.locator('.home:not(.compact)')
  await home.locator('.launch').waitFor({ state: 'visible', timeout: 15000 })

  // Pick the test repository explicitly. The recent list is the real account's
  // history, so whatever happens to be most recent is not the repo under test,
  // and a suggestion read off the wrong folder looks exactly like a bug.
  await home.locator('.lbar .pick').first().click()
  await home.locator('.dirpop').first().waitFor({ timeout: 5000 })
  const repoOption = home.locator('.dirpop .dp').filter({ hasText: new RegExp(`^${REPO}$`) })
  if ((await repoOption.count()) === 0) {
    console.log(`the launcher does not know ${REPO}; start a session there first`)
    await browser.close()
    return report()
  }
  await repoOption.first().click()
  check('the launcher is pointed at the repository under test', true, REPO)

  // --- the pill exists and is off by default ---------------------------------
  // Unarmed, the pill names the branch the work will run on, which is the thing
  // you would check before starting. It falls back to "this checkout" only for a
  // folder whose branch could not be read.
  // The branch is fetched after the folder is chosen, so wait for it rather than
  // racing it.
  const pill = home.locator('.lbar .wtpick')
  await pill.first().waitFor({ timeout: 10000 })
  await page.waitForFunction(
    () => document.querySelector('.home:not(.compact) .lbar .wtpick')?.textContent?.includes('main'),
    { timeout: 10000 },
  ).catch(() => {})
  const pillText = (await pill.first().innerText()).trim()
  check('the launcher names the branch the work will run on', pillText === 'main', pillText || '(no pill)')
  await shot(page, '01-launcher')

  if ((await pill.count()) === 0) {
    await browser.close()
    return report()
  }

  // --- open it and read what it offers ---------------------------------------
  await pill.first().click()
  await home.locator('.wtpop').waitFor({ timeout: 5000 })
  await shot(page, '02-picker-closed-state')

  const modes = home.locator('.wtpop .mode')
  await modes.nth(1).waitFor({ timeout: 5000 })
  const modeLabels = (await modes.allInnerTexts()).map((t) => t.split('\n')[0].trim())
  check(
    'the choice reads as two places, not a checkbox',
    modeLabels.includes('This checkout') && modeLabels.includes('New worktree'),
    modeLabels.join(' / '),
  )

  await home.locator('.wtpop .mode', { hasText: 'New worktree' }).click()
  await home.locator('.wtpop .fields').waitFor({ timeout: 5000 })
  await shot(page, '03-picker-open')

  const baseBtn = home.locator('.wtpop .basebtn')
  const baseText = (await baseBtn.innerText()).trim()
  check('a base branch is preselected, so the fast path needs no choice', baseText.length > 0, baseText)

  const setupLine = home.locator('.wtpop .setupline')
  const setupText = (await setupLine.count()) ? (await setupLine.innerText()).trim() : ''
  check(
    'the setup command is shown before it can run',
    setupText.includes('npm ci') || setupText.includes('ln -sf'),
    setupText,
  )

  // The branch preview is what makes the choice concrete rather than abstract.
  await home.locator('.wtpop .nameinput').fill('Fix Login')
  const preview = await home.locator('.wtpop .branchprev').innerText()
  check('the branch name is previewed from what you typed', preview === 'kunai/fix-login', preview)
  await shot(page, '04-picker-named')

  // Enter finishes the form. Without it the scrim the popover opened sits over
  // the Start button, so the primary action needs a stray click on empty space
  // first: fine for picking one item from a list, wrong after filling in a form.
  await home.locator('.wtpop .nameinput').press('Enter')
  await home.locator('.wtpop').waitFor({ state: 'detached', timeout: 5000 })
  check('Enter closes the picker so Start is reachable', true)

  const armed = (await home.locator('.lbar .pick.armed').innerText()).trim()
  check('the pill states the choice once armed', armed.includes('worktree of'), armed)

  // --- start the work ---------------------------------------------------------
  const before = git(['worktree', 'list', '--porcelain']).split('\n').filter((l) => l.startsWith('worktree ')).length
  await home.locator('.launch .brief').fill('say hello and stop')
  await home.locator('.launch .go').click()

  // The session opens; the card under the header is the proof it landed in a
  // worktree rather than the main checkout.
  await page.waitForSelector('.wtcard', { timeout: 30000 })
  await shot(page, '05-session-card')

  // The branch may carry a collision suffix if an earlier run left one behind,
  // which is correct behaviour rather than a failure, so assert the shape.
  const cardBranch = (await page.locator('.wtcard .branch').innerText()).trim()
  check('the session says which worktree it is in', /^fix-login(-\d+)?$/.test(cardBranch), cardBranch)

  const after = git(['worktree', 'list', '--porcelain']).split('\n').filter((l) => l.startsWith('worktree ')).length
  check('git actually gained a worktree', after === before + 1, `${before} -> ${after}`)

  const branches = git(['branch', '--list', 'kunai/*'])
  check('the branch exists and is namespaced', branches.includes(`kunai/${cardBranch}`), `kunai/${cardBranch}`)

  // The main checkout must be untouched by any of this: that is the whole point.
  const mainStatus = git(['status', '--porcelain'])
  check('the main checkout is still clean', mainStatus === '', mainStatus || '(clean)')
  const mainBranch = git(['rev-parse', '--abbrev-ref', 'HEAD'])
  check('the main checkout is still on its own branch', mainBranch === 'main', mainBranch)

  // --- the card's detail ------------------------------------------------------
  await page.locator('.wtcard .head').click()
  await page.waitForSelector('.wtcard .body', { timeout: 5000 })
  await shot(page, '06-card-open')

  const bodyText = await page.locator('.wtcard .body').innerText()
  check(
    'the card names both checkouts',
    bodyText.includes(REPO) && bodyText.includes('worktrees'),
  )
  check(
    'the card flags what is shared with the main checkout',
    bodyText.toLowerCase().includes('shared with the main checkout'),
  )

  // --- the sidebar groups it under its repository -----------------------------
  // A worktree lives in kunai's data directory, so grouping by its own folder
  // would give every worktree a heading of its own and scatter one repository
  // across the list. It belongs to the codebase it came from.
  const repoLeaf = REPO.replace(/\/+$/, '').split('/').slice(-1)[0]
  const groupLabels = (await page.locator('.sb .grp .glabel').allInnerTexts()).map((t) => t.trim())
  check(
    'the sidebar groups the worktree session under its repository',
    groupLabels.includes(repoLeaf),
    groupLabels.join(' / '),
  )
  check(
    'the worktree does not get a heading of its own',
    !groupLabels.includes('fix-login'),
    groupLabels.join(' / '),
  )
  // The chip earns its place only when it adds something. An untitled session is
  // already named after its worktree directory, so a chip there would say the
  // same word twice; give the session a title and the branch becomes the thing
  // that tells it apart from a session in the main checkout.
  const untitled = await page.locator('.sb .wtchip').allInnerTexts()
  check(
    'no branch chip while the row is already named after the worktree',
    !untitled.includes(cardBranch),
    untitled.join(',') || '(none)',
  )

  const sessions = await fetch(`${ORIGIN}/api/sessions`).then((r) => r.json())
  const wtSession = sessions.find((m) => m.cwd.includes('/worktrees/'))
  await fetch(`${ORIGIN}/api/sessions/${wtSession.id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: 'Login rework' }),
  })
  await page.reload()
  await page.waitForSelector('.sb .row', { timeout: 15000 })
  const titled = await page.locator('.sb .wtchip').allInnerTexts()
  check('a titled worktree row carries its branch', titled.includes(cardBranch), titled.join(','))
  await shot(page, '08-sidebar-grouping')

  // Discard is confirmed by naming what it destroys, not by asking "are you sure".
  // The reload above collapsed the card, so open it again first.
  if ((await page.locator('.wtcard .body').count()) === 0) {
    await page.locator('.wtcard .head').click()
    await page.waitForSelector('.wtcard .body', { timeout: 5000 })
  }
  await page.locator('.wtcard .quiet', { hasText: 'Discard' }).click()
  const danger = await page.locator('.wtcard .danger').innerText()
  check('discard says what would be lost', danger.includes('Delete this worktree'), danger.trim())
  await shot(page, '07-discard-confirm')

  // --- the sidebar's worktree composer ----------------------------------------
  // The plus keeps its promise of starting work here with no questions asked, so
  // the worktree is a second button beside it rather than a dialog in front of
  // it. What it opens is a composer, not a menu: a worktree exists to hold a
  // piece of work, so the first thing to say about it is what that work is, and
  // the branch it cuts from is a setting on that rather than the whole question.
  await page.goto(ORIGIN)
  await home.locator('.launch').waitFor({ state: 'visible', timeout: 15000 })
  const heading = page.locator('.sb .grp', { hasText: repoLeaf }).first()
  await heading.waitFor({ timeout: 15000 })
  const buttons = heading.locator('.gadd')
  await buttons.first().waitFor({ timeout: 15000 })
  const buttonCount = await buttons.count()
  check('a group heading offers both a plus and a worktree button', buttonCount === 2, `${buttonCount}`)

  const wtBefore = worktreeCount()
  await buttons.first().click() // the worktree one sits before the plus
  await page.waitForSelector('.panel textarea', { timeout: 10000 })
  check(
    'the worktree button opens a composer, not a bare branch list',
    (await page.locator('.panel textarea').count()) === 1,
  )
  // Preselected to where you are standing. Silently cutting from main while you
  // worked on a feature branch is the complaint this panel exists to answer, so
  // the preselection is the check that matters, not merely that a picker exists.
  const onBranch = git(['rev-parse', '--abbrev-ref', 'HEAD'])
  const shownBase = (await page.locator('.panel .basebtn .bl').innerText()).trim()
  check('the panel offers to cut from the branch you are on', shownBase === onBranch, `${shownBase} vs ${onBranch}`)

  await page.locator('.panel .basebtn').click()
  await page.waitForSelector('.panel .pop .opt', { timeout: 10000 })
  const branchOpts = (await page.locator('.panel .pop .opt').allInnerTexts()).map((t) => t.replace(/\n/g, ' '))
  check('the base can still be changed from the header', branchOpts.length > 0, branchOpts.join(' | '))
  check('and the branch you are on is listed first', /you are here/.test(branchOpts[0] ?? ''), branchOpts[0] ?? '(none)')
  await page.keyboard.press('Escape') // closes the picker, keeps the composer
  check('escape closes the picker before the panel', (await page.locator('.panel textarea').count()) === 1)
  await shot(page, '09-worktree-composer')

  // The prompt names the branch: nobody should have to name a branch before
  // describing the work, because describing the work is the name.
  await page.locator('.panel textarea').fill('tidy the login form and stop')
  check(
    'with no name typed, nothing claims to know the branch yet',
    (await page.locator('.panel .prev').count()) === 0,
  )
  // Type one and the branch it makes is previewed, so the name is not a guess.
  await page.locator('.panel .name').fill('Login Tidy')
  const namePreview = (await page.locator('.panel .prev').innerText()).trim()
  check('a typed name previews the branch it becomes', namePreview === 'kunai/login-tidy', namePreview)
  await page.locator('.panel .name').fill('')
  await page.locator('.panel .go').click()
  await page.waitForSelector('.wtcard', { timeout: 60000 })
  check('starting from the composer lands in a worktree', worktreeCount() === wtBefore + 1, `${wtBefore} -> ${worktreeCount()}`)

  const composedBranch = (await page.locator('.wtcard .branch').innerText()).trim()
  check(
    'and the branch is named from what was typed',
    /login/.test(composedBranch),
    composedBranch,
  )
  const composedSetup = await page
    .locator('.wtcard .head')
    .innerText()
    .then((t) => t.trim())
  check('the composed worktree still ran the repository\'s own setup', !composedSetup.includes('Setup failed'), composedSetup)
  await shot(page, '10-worktree-composed')

  // --- one list, grouped by folder --------------------------------------------
  // A session used to move out of its project into an Active section the moment
  // it started running, which is exactly backwards for worktrees: you start
  // several agents on one repository so you can watch them side by side. The
  // presence dot says which are live; the folder says where they belong.
  const sectionLabels = (await page.locator('.sb .sec').allInnerTexts()).map((t) => t.trim())
  check(
    'running sessions are not split off into their own section',
    !sectionLabels.includes('Active') && !sectionLabels.includes('Recent'),
    sectionLabels.join(' / ') || '(none)',
  )
  const repoGroup = page.locator('.sb .grp', { hasText: repoLeaf }).first()
  const rowsUnderRepo = await repoGroup
    .evaluate((el) => {
      let n = 0
      for (let s = el.nextElementSibling; s && !s.classList.contains('grp') && !s.classList.contains('sec'); s = s.nextElementSibling) {
        if (s.classList.contains('row')) n++
      }
      return n
    })
  check('the repository heading holds its sessions', rowsUnderRepo > 0, `${rowsUnderRepo} rows`)
  const liveDots = await repoGroup.evaluate((el) => {
    let n = 0
    for (let s = el.nextElementSibling; s && !s.classList.contains('grp') && !s.classList.contains('sec'); s = s.nextElementSibling) {
      if (s.querySelector('.live')) n++
    }
    return n
  })
  check('and the live ones keep their presence dot', liveDots > 0, `${liveDots} live`)

  // --- the branch names itself -------------------------------------------------
  // Nobody should have to name a branch before describing the work, because
  // describing the work is the name. The launcher has the brief at the moment the
  // worktree is made, so no name is typed here at all.
  await page.goto(ORIGIN)
  await home.locator('.launch').waitFor({ state: 'visible', timeout: 15000 })
  await home.locator('.lbar .pick').first().click()
  await home.locator('.dirpop').first().waitFor({ timeout: 5000 })
  await home.locator('.dirpop .dp').filter({ hasText: new RegExp(`^${REPO}$`) }).first().click()

  await home.locator('.lbar .wtpick').first().click()
  await home.locator('.wtpop .mode', { hasText: 'New worktree' }).click()
  await home.locator('.wtpop .fields').waitFor({ timeout: 5000 })
  await home.locator('.wtpop .nameinput').press('Enter') // left empty on purpose
  await home.locator('.wtpop').waitFor({ state: 'detached', timeout: 5000 })

  await home.locator('.launch .brief').fill('please fix the login redirect loop')
  await home.locator('.launch .go').click()
  await page.waitForSelector('.wtcard', { timeout: 30000 })

  const named = (await page.locator('.wtcard .branch').innerText()).trim()
  check(
    'an unnamed worktree names itself from the task',
    /^fix-login-redirect-loop(-\d+)?$/.test(named),
    named,
  )
  const namedBranches = git(['branch', '--list', 'kunai/fix-login-redirect-loop*'])
  check('and that is the real branch', namedBranches.trim() !== '', namedBranches.replace(/\s+/g, ' '))
  await shot(page, '10-self-named')

  await browser.close()
  await cleanup()
  report()
}

// cleanup closes every session in a worktree and removes the worktrees kunai
// made, through the same API a user would, then removes the transcripts those
// sessions left behind.
//
// The transcripts matter. A session's cwd decides where the CLI writes its
// transcript, and the throwaway data dir has no account config, so these land in
// the real ~/.claude alongside actual work and show up in Recent. Twenty-three
// rows of "say hello and stop" in someone's session list is this test's mess, and
// it is this test's job to clear it.
async function cleanup() {
  const paths = new Set([REPO, REPO_B])
  try {
    const worktrees = await fetch(`${ORIGIN}/api/worktrees`).then((r) => r.json())
    for (const w of worktrees) paths.add(w.path)

    const sessions = await fetch(`${ORIGIN}/api/sessions`).then((r) => r.json())
    for (const m of sessions) {
      paths.add(m.cwd)
      await fetch(`${ORIGIN}/api/sessions/${m.id}`, { method: 'DELETE' })
    }
    await new Promise((r) => setTimeout(r, 800))
    for (const w of worktrees) {
      const q = new URLSearchParams({ path: w.path, force: '1' })
      await fetch(`${ORIGIN}/api/worktrees?${q}`, { method: 'DELETE' })
    }
    console.log(`cleaned up ${worktrees.length} worktree(s)`)
  } catch (e) {
    console.log('cleanup failed:', e.message)
  }
  removeTranscripts(paths)
}

// removeTranscripts deletes the CLI transcript folders belonging to directories
// this run used. Scoped by exact path, and refuses anything outside /tmp, so it
// can only ever remove what a throwaway directory produced.
function removeTranscripts(paths) {
  const root = `${homedir()}/.claude/projects`
  let gone = 0
  for (const p of paths) {
    if (!p || !p.startsWith('/tmp/')) continue // never a real project
    const folder = `${root}/${p.replace(/\//g, '-')}`
    if (existsSync(folder)) {
      rmSync(folder, { recursive: true, force: true })
      gone++
    }
  }
  if (gone) console.log(`removed ${gone} transcript folder(s) this run created`)
}

function report() {
  const failed = results.filter((r) => !r.ok)
  console.log(`\n${results.length - failed.length}/${results.length} passed`)
  console.log(`screenshots in ${SHOT_DIR}`)
  process.exit(failed.length ? 1 : 0)
}

// Cleanup runs whether or not the run succeeded. It used to run only on the
// success path, so every failure left its sessions, worktrees and transcripts
// behind: the next run then started against the leftovers of the last, and
// failed for reasons that had nothing to do with the code. A test that only
// tidies up when it passes is a test that guarantees its own flakiness.
main().catch(async (e) => {
  console.error(e)
  await cleanup()
  process.exit(1)
})
