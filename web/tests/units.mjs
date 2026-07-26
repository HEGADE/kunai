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
import { groupSessions, groupStartTarget, projectName } from '../src/lib/grouping.ts'
import { summarise, isAwaiting, isWorking } from '../src/lib/sidebar.ts'
import {
  chosenCli, isProvider, providerModelChoices, providerModelToSend, showEffort,
} from '../src/lib/spawnoptions.ts'

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

console.log(`${pass}/${pass + fails.length} passed`)
for (const f of fails) console.log(`FAIL ${f}`)
process.exit(fails.length ? 1 : 0)
