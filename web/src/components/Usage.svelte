<script lang="ts">
  // What all of this cost.
  //
  // The dashboard already answers "can I keep going" (how full the subscription
  // windows are). This answers the other question, which kunai has never been
  // able to: what have I actually been spending it ON. Which model ate the
  // month, whether the Codex provider is earning its place, what a heavy day
  // looks like next to a quiet one.
  //
  // The headline is deliberately not a bill and says so directly underneath.
  // Everything here runs on subscriptions, so nobody was charged per token; the
  // number is the counterfactual, what this work would have cost through the
  // API. That is the only figure that compares a Claude session to a Codex one,
  // and it is also the one most likely to be misread as an invoice, so the
  // caption is part of the number rather than a footnote.
  import { app } from '../lib/app.svelte'
  import { fetchUsageStats } from '../lib/api'
  import SegMenu from './SegMenu.svelte'
  import Spinner from './Spinner.svelte'
  import {
    PERIODS,
    byAgent,
    byModel,
    cacheWrite,
    compact,
    dailyCost,
    dailyTokens,
    dayLabel,
    money,
    percent,
    pricedShare,
    totalTokens,
    totals,
    window_,
    type Period,
    type UsageReport,
  } from '../lib/usage'

  let machineId = $state(app.activeMachineId ?? app.machines[0]?.id ?? '')
  let period = $state<Period>(30)
  let metric = $state<'cost' | 'tokens'>('cost')
  let split = $state<'model' | 'day'>('model')

  let report = $state<UsageReport | null>(null)
  let error = $state('')
  let loading = $state(true)

  const machine = $derived(app.machines.find((m) => m.id === machineId))
  const base = $derived(app.baseForMachine(machineId))
  // A loopback install labels itself from its own origin, which reads as "127."
  // once the sidebar has truncated it. In prose that is worse than useless, so
  // the machine you are standing at is called what it is.
  const where = $derived(!machine || machine.self ? 'this machine' : machine.label)

  async function load() {
    error = ''
    try {
      report = await fetchUsageStats(base)
    } catch (e) {
      error = e instanceof Error ? e.message : 'could not read usage'
    } finally {
      loading = false
    }
  }

  $effect(() => {
    void machineId
    loading = true
    report = null
    load()
  })

  // A first scan over a large corpus takes seconds, and the server answers
  // immediately with whatever it has rather than blocking. So while it is
  // scanning, keep asking: the numbers grow into place instead of the page
  // sitting on a spinner with no idea whether anything is happening.
  $effect(() => {
    if (!report?.scanning) return
    const t = setTimeout(load, 1500)
    return () => clearTimeout(t)
  })

  const days = $derived(window_(report, period))
  const total = $derived(totals(days))
  const agents = $derived(byAgent(days))
  const models = $derived(byModel(days))
  const priced = $derived(pricedShare(days))
  const series = $derived(metric === 'cost' ? dailyCost(days) : dailyTokens(days))
  const peak = $derived(Math.max(1, ...series))
  const empty = $derived(!loading && !error && total.n === 0)

  const periodOptions = PERIODS.map((d) => ({ id: String(d), label: `${d} days` }))
</script>

<div class="backdrop" onclick={() => app.closeUsage()} role="presentation">
<section class="sheet" role="dialog" aria-label="Usage" onclick={(e) => e.stopPropagation()}>
  <header class="top">
    <button class="back" onclick={() => app.closeUsage()} aria-label="Back">
      <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6" /></svg>
    </button>
    <h1>Usage</h1>
    {#if app.machines.length > 1}
      <label class="mpick">
        <select bind:value={machineId} aria-label="Machine">
          {#each app.machines as m (m.id)}
            <option value={m.id}>{m.label}{m.self ? ' · this machine' : ''}</option>
          {/each}
        </select>
        <svg class="mchev" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6" /></svg>
      </label>
    {/if}
    <SegMenu
      value={String(period)}
      options={periodOptions}
      align="right"
      up={false}
      onpick={(id) => (period = Number(id) as Period)}
    />
  </header>

  {#if error}
    <p class="state err">{error}</p>
  {:else if loading && !report}
    <div class="loading"><Spinner /><span>Reading transcripts…</span></div>
  {:else if empty}
    <p class="state">
      No recorded work on {where} in the last {period} days.
    </p>
  {:else}
    {#if report?.scanning}
      <p class="scanning">
        <Spinner />
        <span>Still reading transcripts — these numbers are still growing.</span>
      </p>
    {/if}

    <!-- The headline, and immediately the caveat. Split across two elements so
         the caveat is never cropped away from the number it qualifies. -->
    <div class="hero">
      <div class="hlabel">Raw token cost</div>
      <div class="hval">{money(total.cost)}</div>
      <p class="hnote">
        What these tokens would have cost at API rates. Not what you were billed —
        this work ran on subscriptions.
      </p>
    </div>

    <!-- Which brain the money went on. One bar, because the question is a
         proportion and a proportion is a length.
         Shown only when there is more than one agent to compare: on a
         Claude-only machine this block says the hero's own figure, then "100%",
         then a token count the stat row repeats below it. Three restatements of
         something already on screen is worse than no panel at all. -->
    {#if agents.length > 1}
      {@const work = totalTokens(total)}
      <!-- The bar is TOKEN share, not cost share, and that is the difference
           between informative and misleading. An unpriced agent contributes zero
           cost, so a cost bar would show Claude at 100% on a machine that also
           ran millions of Codex tokens -- erasing exactly the comparison this
           panel exists to make. Tokens are known for every agent, always. -->
      <div class="split">
        {#each agents as a (a.model)}
          {@const share = work > 0 ? totalTokens(a) / work : 0}
          <div
            class="seg"
            style="flex: {Math.max(share, 0.015)}"
            title="{a.model} · {compact(totalTokens(a))} tokens"
          ></div>
        {/each}
      </div>
      <div class="agents">
        {#each agents as a (a.model)}
          <div class="agent">
            <div class="aname">{a.model}</div>
            <div class="acost">{a.priced ? money(a.cost) : 'not priced'}</div>
            <div class="ameta">
              {compact(totalTokens(a))} tokens{#if work > 0} · {percent(totalTokens(a) / work)} of work{/if}
            </div>
          </div>
        {/each}
      </div>
    {:else}
      <div class="gap"></div>
    {/if}

    <!-- Daily. Bars, not an area: an area over 90 sparse days implies a
         continuous quantity between the points, and this is a per-day total. -->
    <section class="card">
      <div class="chd">
        <h2>Daily {metric === 'cost' ? 'cost' : 'tokens'}</h2>
        <div class="toggle">
          <button class:on={metric === 'cost'} onclick={() => (metric = 'cost')}>Cost</button>
          <button class:on={metric === 'tokens'} onclick={() => (metric = 'tokens')}>Tokens</button>
        </div>
      </div>
      <div class="chart" style="--n: {days.length}">
        {#each days as d, i (d.day)}
          {@const v = series[i]}
          <div
            class="bar"
            class:zero={v === 0}
            style="height: {v === 0 ? 1.5 : Math.max(2, (v / peak) * 100)}%"
            title="{dayLabel(d.day)} · {metric === 'cost' ? money(v) : compact(v) + ' tokens'}"
          ></div>
        {/each}
      </div>
      <!-- Three labels on a spread row, not one per column. A per-column axis
           needs each label to fit a 1fr cell, which at 90 days is about four
           pixels, so they overlap into an unreadable smear exactly when the
           window is long enough to need them. The ends plus the middle is all a
           reader takes from a date axis anyway. -->
      <div class="axis">
        <span>{days.length ? dayLabel(days[0].day) : ''}</span>
        <span>{days.length > 2 ? dayLabel(days[Math.floor(days.length / 2)].day) : ''}</span>
        <span>{days.length ? dayLabel(days[days.length - 1].day) : ''}</span>
      </div>
    </section>

    <!-- The token split. Kept apart from the money because it is the thing that
         explains it: an agent re-reads its whole context on every tool call, so
         cache reads dwarf everything and the headline only makes sense next to
         them. -->
    <div class="stats">
      <div class="stat">
        <span class="sl">Processed</span><span class="sv">{compact(totalTokens(total))}</span>
      </div>
      <div class="stat">
        <span class="sl">Cached in</span><span class="sv">{compact(total.r)}</span>
      </div>
      <div class="stat">
        <span class="sl">Cache writes</span><span class="sv">{compact(cacheWrite(total))}</span>
      </div>
      <div class="stat">
        <span class="sl">Uncached in</span><span class="sv">{compact(total.in)}</span>
      </div>
      <div class="stat">
        <span class="sl">Output</span><span class="sv">{compact(total.out)}</span>
      </div>
      <div class="stat">
        <span class="sl">Responses</span><span class="sv">{compact(total.n)}</span>
      </div>
    </div>

    <section class="card">
      <div class="chd">
        <h2>Breakdown</h2>
        <div class="toggle">
          <button class:on={split === 'model'} onclick={() => (split = 'model')}>Model</button>
          <button class:on={split === 'day'} onclick={() => (split = 'day')}>Day</button>
        </div>
      </div>
      <table class="tbl">
        <thead>
          <tr>
            <th>{split === 'model' ? 'Model' : 'Day'}</th>
            <th class="num">Cost</th>
            <th class="num">Share</th>
            <th class="num">Tokens</th>
          </tr>
        </thead>
        <tbody>
          {#if split === 'model'}
            {#each models as m (m.model)}
              <tr>
                <td class="mono">{m.model}</td>
                <td class="num">{m.priced ? money(m.cost) : '—'}</td>
                <td class="num dim">{m.priced && total.cost > 0 ? percent(m.cost / total.cost) : '—'}</td>
                <td class="num dim">{compact(totalTokens(m))}</td>
              </tr>
            {/each}
          {:else}
            <!-- Newest first here, unlike the chart: a table is read from the
                 top and the day you care about is today. -->
            {#each [...days].reverse().filter((d) => d.models.length) as d (d.day)}
              {@const t = totals([d])}
              <tr>
                <td class="mono">{dayLabel(d.day)}</td>
                <td class="num">{money(t.cost)}</td>
                <td class="num dim">{total.cost > 0 ? percent(t.cost / total.cost) : '—'}</td>
                <td class="num dim">{compact(totalTokens(t))}</td>
              </tr>
            {/each}
          {/if}
        </tbody>
      </table>
    </section>

    <!-- How much of the above to believe. A cost covering 98% of the tokens is a
         different claim from one covering all of them, so the page states its own
         coverage rather than letting the headline imply completeness. -->
    <section class="card quality">
      <h2>Cost quality</h2>
      <div class="qrow">
        <span class="ql">Model priced</span><span class="qv">{percent(priced)}</span>
      </div>
      <div class="qrow">
        <span class="ql">Unpriced</span>
        <span class="qv" class:warn={priced < 1}>{percent(1 - priced)}</span>
      </div>
      <div class="qrow">
        <span class="ql">Cache savings</span><span class="qv good">{money(total.savings)}</span>
      </div>
      <p class="qnote">
        Unpriced tokens are counted but not costed — kunai has no published rate for
        that model, and guessing one would put a confident figure on a guess. Cache
        savings is what the cached reads would have cost at the full input rate.
      </p>
    </section>

    <p class="foot">
      Read from {report?.files ?? 0} transcript{(report?.files ?? 0) === 1 ? '' : 's'} across every
      account on {where}.
    </p>
  {/if}
</section>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 60;
    background: rgba(0, 0, 0, 0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 20px;
  }
  .sheet {
    width: 100%;
    max-width: 720px;
    max-height: min(92dvh, 900px);
    display: flex;
    flex-direction: column;
    background: var(--bg-raised, var(--bg));
    border: 1px solid var(--border-2);
    border-radius: 20px;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    box-shadow: 0 30px 80px -30px rgba(0, 0, 0, 0.8);
    padding: 20px 22px 24px;
  }
  /* Load-bearing, not tidiness. This page is taller than the sheet, so the
     column overflows and every child gets shrunk by the default flex-shrink: 1.
     A child sized by CSS rather than by text has nothing to push back with, so
     it collapses -- the agent bar rendered as a literal 0px line, and the chart
     silently lost height, before this was here. Any fixed-height block added
     later would hit the same thing, so the rule is on the container. */
  .sheet > * {
    flex: none;
  }

  .top {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .back {
    flex: none;
    width: 34px;
    height: 34px;
    margin-left: -6px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 10px;
    color: var(--text-3);
  }
  .back:hover {
    background: var(--panel);
    color: var(--text);
  }
  .top h1 {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: 19px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }
  .mpick {
    flex: none;
    position: relative;
    display: inline-flex;
    align-items: center;
  }
  .mpick select {
    appearance: none;
    -webkit-appearance: none;
    height: 32px;
    padding: 0 28px 0 12px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 100px;
    color: var(--text-2);
    font-size: 12.5px;
    max-width: 150px;
  }
  .mchev {
    position: absolute;
    right: 10px;
    color: var(--text-4);
    pointer-events: none;
  }

  .state {
    margin: 24px 2px;
    font-size: 13px;
    line-height: 1.6;
    color: var(--text-3);
  }
  .state.err {
    color: var(--danger, #d66);
  }
  .loading,
  .scanning {
    display: flex;
    align-items: center;
    gap: 9px;
    margin: 18px 2px;
    font-size: 12.5px;
    color: var(--text-3);
  }
  .scanning {
    margin: 14px 2px 0;
  }

  .hero {
    margin: 22px 2px 4px;
  }
  .hlabel {
    font-family: var(--mono);
    font-size: 10.5px;
    letter-spacing: 0.09em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .hval {
    font-family: var(--mono);
    font-size: 40px;
    line-height: 1.1;
    letter-spacing: -0.02em;
    margin-top: 6px;
    color: var(--text);
  }
  .hnote {
    margin: 8px 0 0;
    max-width: 46ch;
    font-size: 12px;
    line-height: 1.55;
    color: var(--text-4);
  }

  .split {
    display: flex;
    gap: 2px;
    height: 5px;
    margin: 20px 2px 10px;
  }
  .seg {
    border-radius: 100px;
    background: var(--text-3);
  }
  .seg:first-child {
    background: var(--text);
  }
  .seg:nth-child(3) {
    background: var(--text-4);
  }

  .gap {
    height: 22px;
  }
  .agents {
    display: flex;
    flex-wrap: wrap;
    gap: 10px 26px;
    margin: 0 2px 22px;
  }
  .aname {
    font-size: 12.5px;
    color: var(--text-2);
  }
  .acost {
    font-family: var(--mono);
    font-size: 17px;
    margin-top: 2px;
    color: var(--text);
  }
  .ameta {
    font-family: var(--mono);
    font-size: 11px;
    margin-top: 2px;
    color: var(--text-4);
  }

  .card {
    border: 1px solid var(--border);
    border-radius: 14px;
    padding: 14px 15px 15px;
    margin-bottom: 14px;
  }
  .chd {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 14px;
  }
  .chd h2,
  .quality h2 {
    flex: 1;
    margin: 0;
    font-size: 12.5px;
    font-weight: 600;
    color: var(--text-2);
  }
  .quality h2 {
    margin-bottom: 12px;
  }
  .toggle {
    display: inline-flex;
    gap: 2px;
    padding: 2px;
    background: var(--panel);
    border-radius: 100px;
  }
  .toggle button {
    padding: 4px 11px;
    border-radius: 100px;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .toggle button.on {
    background: var(--bg-raised, var(--bg));
    color: var(--text);
  }

  .chart {
    display: grid;
    grid-template-columns: repeat(var(--n), 1fr);
    align-items: end;
    gap: 2px;
    height: 120px;
  }
  .bar {
    background: var(--text-2);
    border-radius: 2px 2px 0 0;
    min-height: 1.5px;
  }
  /* A day with no work is a hairline rather than nothing, so the gap reads as
     "measured, and empty" instead of as missing data. */
  .bar.zero {
    background: var(--border-2);
  }
  .axis {
    display: flex;
    justify-content: space-between;
    margin-top: 7px;
    font-family: var(--mono);
    font-size: 9.5px;
    color: var(--text-4);
  }

  .stats {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(96px, 1fr));
    gap: 12px 8px;
    margin: 0 2px 20px;
  }
  .stat {
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .sl {
    font-size: 11px;
    color: var(--text-4);
  }
  .sv {
    font-family: var(--mono);
    font-size: 15px;
    color: var(--text);
  }

  .tbl {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  .tbl th {
    text-align: left;
    font-family: var(--mono);
    font-size: 10px;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    font-weight: 500;
    color: var(--text-4);
    padding-bottom: 7px;
    border-bottom: 1px solid var(--border);
  }
  .tbl td {
    padding: 7px 0;
    border-bottom: 1px solid var(--border);
    color: var(--text-2);
  }
  .tbl tr:last-child td {
    border-bottom: none;
    padding-bottom: 0;
  }
  .tbl .num {
    text-align: right;
    font-family: var(--mono);
  }
  .tbl .mono {
    font-family: var(--mono);
    color: var(--text);
  }
  .tbl .dim {
    color: var(--text-4);
  }

  .qrow {
    display: flex;
    align-items: baseline;
    gap: 10px;
    padding: 5px 0;
  }
  .ql {
    flex: 1;
    font-size: 12px;
    color: var(--text-3);
  }
  .qv {
    font-family: var(--mono);
    font-size: 13px;
    color: var(--text);
  }
  .qv.warn {
    color: var(--warn, #d9a441);
  }
  .qv.good {
    color: var(--ok, #6fae7d);
  }
  .qnote {
    margin: 10px 0 0;
    font-size: 11.5px;
    line-height: 1.55;
    color: var(--text-4);
  }

  .foot {
    margin: 4px 2px 0;
    font-size: 11.5px;
    color: var(--text-4);
  }

  @media (max-width: 560px) {
    .backdrop {
      padding: 0;
    }
    .sheet {
      max-width: none;
      max-height: 100dvh;
      height: 100dvh;
      border: none;
      border-radius: 0;
      padding-top: max(20px, var(--safe-top));
    }
    .hval {
      font-size: 32px;
    }
  }
</style>
