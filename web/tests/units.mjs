// Unit tests for the sidebar's pure helpers.
//
// Plain node, no test runner: node strips the types off a .ts import by itself,
// so this costs no dependency and runs anywhere the repo does. Run it with
//   node web/tests/units.mjs
//
// These functions were written as pure precisely so they could be exercised
// directly, and until now nothing did. Every threshold below is an off-by-one
// waiting to happen, and the group summary decides what the sidebar claims about
// agents nobody is watching, which is the worst thing to be wrong about quietly.

import { shortAgo, longAgo, secondsSince } from '../src/lib/reltime.ts'
import { groupSessions, groupStartTarget, projectName, visibleGroups } from '../src/lib/grouping.ts'
import { summarise, isAwaiting, isWorking } from '../src/lib/sidebar.ts'
import {
  chosenCli, isProvider, providerModelChoices, providerModelToSend, showEffort,
} from '../src/lib/spawnoptions.ts'
import { ordered, decide, postLabel, asPosted } from '../src/lib/review.ts'
import { clampTTL, splitDuration, expiryWords, MIN_TTL, MAX_TTL } from '../src/lib/duration.ts'
import {
  byAgent, byModel, compact, dailyCost, money, percent, pricedShare, totals,
  totalTokens, window_,
} from '../src/lib/usage.ts'

let pass = 0
const fails = []
function eq(name, got, want) {
  const ok = JSON.stringify(got) === JSON.stringify(want)
  if (ok) pass++
  else fails.push(`${name}\n     got  ${JSON.stringify(got)}\n     want ${JSON.stringify(want)}`)
}

// --- reltime ----------------------------------------------------------------
// A fixed "now" rather than Date.now(), so a slow machine cannot fail the suite.
const NOW = Date.parse('2026-07-26T12:00:00Z')
const at = (ms) => new Date(NOW - ms).toISOString()
const S = 1000, M = 60 * S, H = 60 * M, D = 24 * H

eq('shortAgo under a minute', shortAgo(at(59 * S), NOW), 'now')
eq('shortAgo at exactly a minute', shortAgo(at(60 * S), NOW), '1m')
eq('shortAgo just under an hour', shortAgo(at(59 * M + 59 * S), NOW), '59m')
eq('shortAgo at exactly an hour', shortAgo(at(60 * M), NOW), '1h')
eq('shortAgo just under a day', shortAgo(at(23 * H + 59 * M), NOW), '23h')
eq('shortAgo at exactly a day', shortAgo(at(24 * H), NOW), '1d')
eq('shortAgo at 29 days stays in days', shortAgo(at(29 * D), NOW), '29d')
eq('shortAgo at 30 days becomes months', shortAgo(at(30 * D), NOW), '1mo')
eq('shortAgo just under a year', shortAgo(at(359 * D), NOW), '11mo')
eq('shortAgo at a year', shortAgo(at(360 * D), NOW), '1y')
// A peer's clock can be ahead of ours; a negative age must not print "-3m".
eq('a future timestamp reads as now, not negative', shortAgo(at(-5 * M), NOW), 'now')
eq('an unparseable timestamp is empty, not NaN', shortAgo('not a date', NOW), '')
eq('secondsSince reports -1 for junk', secondsSince('', NOW), -1)
eq('longAgo says just now', longAgo(at(10 * S), NOW), 'just now')
eq('longAgo appends ago', longAgo(at(3 * D), NOW), '3d ago')

// --- grouping ---------------------------------------------------------------
const live = (over) => ({ machineId: 'm', cwd: '/home/a/kunai', state: 'idle', ...over })

eq('projectName takes the last segment', projectName('/home/a/kunai/'), 'kunai')
eq(
  'a worktree groups under its repository, not its own directory',
  groupSessions([live({ cwd: '/data/worktrees/kunai/fix', repo: '/home/a/kunai' })]).map((g) => g.label),
  ['kunai'],
)
eq(
  'a named workspace wins over the directory',
  groupSessions([live({ workspace: 'Platform' })]).map((g) => g.label),
  ['Platform'],
)
eq(
  'a group spanning two directories has no start target',
  groupStartTarget({
    key: 'w', label: 'w', named: true,
    items: [live({ workspace: 'w' }), live({ workspace: 'w', cwd: '/home/a/other' })],
  }),
  null,
)
eq(
  'a worktree does not cost its group the start button',
  groupStartTarget({
    key: 'kunai', label: 'kunai', named: false,
    items: [live(), live({ cwd: '/data/worktrees/kunai/fix', repo: '/home/a/kunai' })],
  }),
  { machineId: 'm', cwd: '/home/a/kunai' },
)

// --- group state summary ----------------------------------------------------
// Only running and awaiting_permission are counted. `starting` is deliberately
// not: a resumed session reads `starting` until its first prompt, so a summary
// that trusted it would announce work on a session sitting there doing nothing.
// A badge built on that exact field was tried once and reverted for lying.
eq('an idle folder says nothing', summarise([live(), live()]), '')
eq('a resumed session is not reported as working', summarise([live({ state: 'starting' })]), '')
eq('one running agent', summarise([live({ state: 'running' })]), '1 working')
eq('two running agents', summarise([live({ state: 'running' }), live({ state: 'running' })]), '2 working')
eq(
  'needing you is said even when something else is working',
  summarise([live({ state: 'running' }), live({ state: 'awaiting_permission' })]),
  '1 working · 1 needs you',
)
eq('needing you alone', summarise([live({ state: 'awaiting_permission' })]), '1 needs you')
eq('past sessions carry no state and count for nothing', summarise([{ machineId: 'm', cwd: '/x' }]), '')
eq('isAwaiting only for the gate', [isAwaiting(live({ state: 'awaiting_permission' })), isAwaiting(live({ state: 'running' }))], [true, false])
eq('isWorking only for a live turn', [isWorking(live({ state: 'running' })), isWorking(live({ state: 'starting' }))], [true, false])

// --- spawn options ----------------------------------------------------------
// The rules that decide what a "start a session" control offers. Written inline
// once, they ended up correct in the worktree dialog and absent from the New
// Session dialog, which went on offering Claude tiers beside a Codex account.
const CLIS = ['Claude', 'work', 'Codex', 'Grok']
const PM = { Codex: 'gpt-5.4', Grok: 'grok-4.5' }

eq('no account chosen means the machine default', chosenCli('', CLIS), 'Claude')
eq('an empty machine yields no account', chosenCli('', []), '')
eq('a provider is recognised from the model map alone', isProvider('Codex', PM), true)
eq('a Claude account is not', isProvider('work', PM), false)
eq('and neither is nothing', isProvider('', PM), false)
eq('a provider drops the effort control', showEffort('Grok', PM), false)
eq('a Claude account keeps it', showEffort('Claude', PM), true)

// A lapsed login or a proxy still starting returns nothing, and an empty control
// is the worst outcome there.
eq('an empty served list still shows the model it is on', providerModelChoices('grok-4.5', []), ['grok-4.5'])
eq('the current model is not listed twice', providerModelChoices('gpt-5.4', ['gpt-5.5', 'gpt-5.4']), ['gpt-5.5', 'gpt-5.4'])
eq('one the proxy omitted is put first', providerModelChoices('gpt-5.3', ['gpt-5.5']), ['gpt-5.3', 'gpt-5.5'])
eq('with nothing chosen, the served list stands', providerModelChoices('', ['a', 'b']), ['a', 'b'])

// Sending the provider model pins it for that provider's NEXT session too, so it
// must only be sent when something was actually chosen.
eq('unchanged provider model is not sent back', providerModelToSend('Codex', PM, 'gpt-5.4'), undefined)
eq('a changed one is', providerModelToSend('Codex', PM, 'gpt-5.5'), 'gpt-5.5')
eq('and never for a Claude account', providerModelToSend('Claude', PM, 'gpt-5.5'), undefined)
eq('nor when nothing is chosen', providerModelToSend('Codex', PM, ''), undefined)

// --- duration: how long a share link lives ---------------------------------
// The bounds have to match what the server enforces, or the dialog shows a
// number the server quietly changes underneath it.
eq("a too-short link is pulled up to the floor", clampTTL(60), MIN_TTL)
eq("zero too", clampTTL(0), MIN_TTL)
eq("a too-long one is pulled down to the ceiling", clampTTL(400 * 86400), MAX_TTL)
eq("a legal one is left alone", clampTTL(86400 + 4 * 3600 + 5 * 60), 101100)
eq("nonsense does not become NaN", clampTTL(NaN), MIN_TTL)

// The picker composes days/hours/minutes, so a stored share has to come apart
// into the same units it was made in.
eq("a composed duration round-trips", splitDuration(86400 + 4 * 3600 + 15 * 60), { days: 1, hours: 4, mins: 15 })
eq("zero splits to zero", splitDuration(0), { days: 0, hours: 0, mins: 0 })
eq("exact days carry no remainder", splitDuration(5 * 86400), { days: 5, hours: 0, mins: 0 })

// An expiry is stated as an instant, because "5 days" has to be added to the
// current time before it means anything and a weekday does not.
const NOON = new Date(2026, 6, 27, 12, 0, 0).getTime()
const starts = (s, p) => eq(s, expiryWords(p[0], NOON).startsWith(p[1]), true)
starts("a few hours out is today", [NOON + 3 * 3600e3, "expires today at"])
starts("overnight is tomorrow", [NOON + 20 * 3600e3, "expires tomorrow at"])
eq("within the week it names the weekday", /^expires \w+day at /.test(expiryWords(NOON + 3 * 86400e3, NOON)), true)
eq("and not today or tomorrow", /today|tomorrow/.test(expiryWords(NOON + 3 * 86400e3, NOON)), false)
eq("a link already gone says so", expiryWords(NOON - 1000, NOON), "expired")

// --- usage ------------------------------------------------------------------
// The arithmetic behind a page that claims to say what things cost. Every number
// here is one somebody could act on, and the failure mode is a plausible wrong
// total rather than a crash, so it is exactly the shape that needs pinning.
const tok = (o) => ({ priced: true, cost: 0, savings: 0, in: 0, w5: 0, w1: 0, r: 0, out: 0, n: 0, ...o })
const REPORT = {
  files: 2, scanned_at: 0, scanning: false, models: [],
  days: [
    { day: "2026-07-24", models: [
      tok({ model: "claude-opus-5", agent: "Claude", cost: 10, savings: 4, r: 1000, out: 10, n: 2 }),
      tok({ model: "gpt-5.5", agent: "Codex", priced: false, in: 500, n: 1 }),
    ] },
    { day: "2026-07-26", models: [
      tok({ model: "claude-opus-5", agent: "Claude", cost: 5, savings: 1, r: 500, out: 5, n: 1 }),
    ] },
  ],
}
const TODAY = new Date(2026, 6, 26)

// A window covers every day in it, including the ones nothing happened on: a
// chart that silently drops empty days compresses time and makes an idle week
// look as busy as a hard one.
eq("a window has one row per day", window_(REPORT, 7, TODAY).length, 7)
eq("and it ends on today", window_(REPORT, 7, TODAY)[6].day, "2026-07-26")
eq("an empty day is present but empty", window_(REPORT, 7, TODAY)[5].models.length, 0)
eq("a day outside the window is dropped", window_(REPORT, 1, TODAY).length, 1)
eq("...and it is today's", totals(window_(REPORT, 1, TODAY)).cost, 5)

const W = window_(REPORT, 7, TODAY)
eq("totals add across days", totals(W).cost, 15)
eq("responses add too", totals(W).n, 4)
eq("models roll up across days", byModel(W).map((m) => m.model), ["claude-opus-5", "gpt-5.5"])
eq("the biggest spender leads", byModel(W)[0].cost, 15)
eq("agents roll up by family", byAgent(W).map((a) => a.model), ["Claude", "Codex"])

// Unpriced is contagious: a bucket holding one unpriced model has a cost that is
// a floor, not a total, and rolling it up as priced would launder that.
eq("an unpriced model stays unpriced", byModel(W)[1].priced, false)
eq("and it makes its total a floor", totals(W).priced, false)
eq("a priced-only rollup stays priced", byAgent(W)[0].priced, true)

// The page states its own coverage rather than letting the headline imply it.
eq("priced share is by tokens", percent(pricedShare(W)), "75%")
eq("nothing measured is fully priced", pricedShare([]), 1)

eq("daily cost is per day, in order", dailyCost(W), [0, 0, 0, 0, 10, 0, 5])

// Money and counts both span several orders of magnitude on one page, so neither
// can use a fixed precision.
eq("thousands lose the cents", money(12500.95), "$12,501")
eq("dollars keep them", money(4.5), "$4.50")
eq("a fraction of a cent still shows", money(0.0031), "$0.0031")
eq("zero is plain", money(0), "$0")
eq("billions are readable", compact(10_600_000_000), "10.6B")
eq("so are millions", compact(130_000_000), "130M")
eq("and small counts are exact", compact(65_193), "65K")
eq("a tiny share does not round to nothing", percent(0.0004), "<0.1%")
// ...and neither end may round to a certainty it does not have. 99.93% shown as
// "100%" beside its own complement as "<0.1%" reads as a contradiction, and on
// the cost-quality panel it claims exactly the completeness being audited.
eq("an almost-whole share does not round to all", percent(0.9993), ">99.9%")
eq("a genuinely whole share is whole", percent(1), "100%")
// Exactly "0%", not "0.0%": the decimal implies a rounding that did not happen,
// and this panel's whole job is to be honest about what it could not price.
eq("and nothing is nothing", percent(0), "0%")

eq("total tokens count every tier", totalTokens(tok({ in: 1, w5: 2, w1: 3, r: 4, out: 5 })), 15)

console.log(`${pass}/${pass + fails.length} passed`)
for (const f of fails) console.log(`FAIL ${f}`)
process.exit(fails.length ? 1 : 0)

// --- how many folders the sidebar shows -------------------------------------
// A quiet folder is one holding nothing but past work. Those are capped; a
// folder with something LIVE in it is never counted against the cap and never
// dropped, because hiding a running agent to make room for one somebody last
// opened on Tuesday is the wrong way round.
const grp = (label, kinds) => ({ key: label, label, named: false, items: kinds.map((k) => ({ kind: k })) })
const isLive = (r) => r.kind === 'live'
const quiet = (label) => grp(label, ['recent'])
const busy = (label) => grp(label, ['live'])

eq(
  'quiet folders are capped',
  visibleGroups([quiet('a'), quiet('b'), quiet('c'), quiet('d'), quiet('e')], isLive, 3).shown.map((g) => g.label),
  ['a', 'b', 'c'],
)
eq(
  'the cut folders are counted, so the link can say so',
  visibleGroups([quiet('a'), quiet('b'), quiet('c'), quiet('d'), quiet('e')], isLive, 3).hidden,
  2,
)
eq(
  'a live folder is never dropped, however far down it sits',
  visibleGroups([quiet('a'), quiet('b'), quiet('c'), quiet('d'), busy('live-one')], isLive, 3).shown.map((g) => g.label),
  ['a', 'b', 'c', 'live-one'],
)
eq(
  'live folders do not spend the quiet budget',
  visibleGroups([busy('L1'), busy('L2'), quiet('a'), quiet('b'), quiet('c'), quiet('d')], isLive, 3)
    .shown.map((g) => g.label),
  ['L1', 'L2', 'a', 'b', 'c'],
)
eq(
  'a folder holding one live session among past ones stays',
  visibleGroups([quiet('a'), quiet('b'), quiet('c'), grp('mixed', ['recent', 'live'])], isLive, 3)
    .shown.map((g) => g.label),
  ['a', 'b', 'c', 'mixed'],
)
eq('nothing hidden when the list is short', visibleGroups([quiet('a')], isLive, 3).hidden, 0)

// --- review triage ----------------------------------------------------------
// The numbers a reviewer decides on must be the numbers that get posted, and the
// way that goes wrong is silent: the headline once counted the severity the
// model wrote rather than the one the reader overruled it to, so demoting the
// only blocker to a minor still announced "1 blocker".

const F = (index, severity, extra = {}) => ({
  index, severity, file: 'a.go', line: 1, side: 'RIGHT', title: 't', body: 'b',
  confidence: 'high', inline: true, ...extra,
})
const FS = [F(0, 'minor'), F(1, 'blocker'), F(2, 'major'), F(3, 'minor', { inline: false })]

eq(
  'findings are ordered worst first',
  ordered(FS, {}, 'all').map((f) => f.index),
  [1, 2, 0, 3],
)
eq(
  'an overruled severity moves the finding immediately',
  ordered(FS, { 1: { title: 't', body: 'b', severity: 'minor' } }, 'all').map((f) => f.index),
  [2, 0, 3, 1],
)
eq(
  'a filter is applied at the overruled severity, not the model’s',
  ordered(FS, { 1: { title: 't', body: 'b', severity: 'minor' } }, 'blocker').map((f) => f.index),
  [],
)

eq('nothing dropped means everything posts', decide(FS, new Set(), {}).keep, 4)
eq('dropping is counted', decide(FS, new Set([1, 2]), {}).drop, 2)
// The delivery split is a promise about what lands where on the pull request.
eq('the inline/summary split counts only what is kept', 
  [decide(FS, new Set([0]), {}).inline, decide(FS, new Set([0]), {}).summary], [2, 1])

// The bug a screenshot caught and every selector assertion walked past.
eq(
  'overruling the only blocker removes it from the headline',
  decide(FS, new Set(), { 1: { title: 't', body: 'b', severity: 'minor' } }).blockers,
  0,
)
eq(
  'a dropped blocker is not counted either, because it is not being sent',
  decide(FS, new Set([1]), {}).blockers,
  0,
)
// An unrecognised severity must not vanish from the counts: it still gets
// posted, so it is counted as the mildest thing rather than as nothing.
eq('an unknown severity still counts', decide([F(0, 'critical')], new Set(), {}).counts.minor, 1)

// Dropping every finding is a decision, not a dead end: "I looked, there is
// nothing worth flagging" is a review worth sending.
eq('the button says what it will send', postLabel(decide(FS, new Set(), {}), false), 'Post 4 findings')
eq('one finding is not pluralised', postLabel(decide([F(0, 'minor')], new Set(), {}), false), 'Post 1 finding')
eq('dropping everything offers the summary', postLabel(decide(FS, new Set([0, 1, 2, 3]), {}), false), 'Post summary')
eq('a review that found nothing still posts', postLabel(decide([], new Set(), {}), false), 'Post summary')

// A rewrite is what gets posted, anchor untouched.
eq(
  'asPosted uses the reader’s words',
  asPosted(F(0, 'minor'), { 0: { title: 'mine', body: 'why', severity: 'blocker' } }).title,
  'mine',
)
eq(
  'asPosted never moves the anchor',
  asPosted(F(0, 'minor'), { 0: { title: 'mine', body: 'why', severity: 'blocker' } }).line,
  1,
)
