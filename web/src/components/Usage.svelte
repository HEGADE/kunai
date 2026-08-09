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
  //
  // This is a PAGE, at /usage, not a dialog. It was a dialog first and that was
  // the wrong container for it: a modal is for a decision you are making right
  // now, on top of the thing you were doing, and it takes the whole screen
  // hostage to say so. Usage is a place you go to read, compare and come back
  // to. Being a route means it survives a reload, the back button works on it,
  // it can be linked to, and it gets the full width its charts want instead of
  // a 720px sheet with the app greyed out behind it.
  import { app } from '../lib/app.svelte'
  import { fetchUsageStats } from '../lib/api'
  import SegMenu from './SegMenu.svelte'
  import Spinner from './Spinner.svelte'
  import { AGENT_ORDER, agentColor, agentRank } from '../lib/agentColors'
  import {
    PERIODS,
    byAgent,
    byModel,
    cacheWrite,
    compact,
    comparable,
    dayLabel,
    deltaPct,
    firstDay,
    money,
    niceMax,
    percent,
    pricedShare,
    signedPct,
    stack,
    priorWindow,
    totalTokens,
    totals,
    valueOf,
    window_,
    type Metric,
    type Period,
    type UsageReport,
  } from '../lib/usage'

  let machineId = $state(app.activeMachineId ?? app.machines[0]?.id ?? '')
  let period = $state<Period>(30)
  let metric = $state<Metric>('cost')
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
  const agents = $derived(byAgent(days).sort((a, b) => agentRank(a.model) - agentRank(b.model)))
  const models = $derived(byModel(days))
  const priced = $derived(pricedShare(days))
  const empty = $derived(!loading && !error && total.n === 0)

  // The same window, one period earlier. A headline number nobody can place is
  // the reason "$412" tells you nothing; "$412, up a third on last month" is a
  // fact you can act on.
  const before = $derived(totals(priorWindow(report, period)))
  const fair = $derived(comparable(report, period))
  const dCost = $derived(fair ? deltaPct(total.cost, before.cost) : null)
  const dWork = $derived(fair ? deltaPct(totalTokens(total), totalTokens(before)) : null)
  const since = $derived(firstDay(report))

  const columns = $derived(stack(days, metric, AGENT_ORDER))
  const scale = $derived(niceMax(Math.max(...columns.map((c) => c.total), 0)))
  const busiest = $derived(
    columns.reduce((best, c) => (c.total > best.total ? c : best), columns[0] ?? { total: 0 }),
  )

  const periodOptions = PERIODS.map((d) => ({ id: String(d), label: `Last ${d} days` }))

  // --- the chart's hover layer ---
  //
  // A tooltip enhances and never gates: every number it shows is also in the
  // Breakdown table below, by day, which is what a screen reader and a keyboard
  // reach. The arrow keys move the readout for the same reason.
  let hover = $state(-1)
  let tipX = $state(0)
  let plotW = $state(0)
  let plotEl: HTMLElement | null = $state(null)

  function track(e: PointerEvent, i: number) {
    hover = i
    const r = plotEl?.getBoundingClientRect()
    if (r) tipX = e.clientX - r.left
  }

  function arrows(e: KeyboardEvent) {
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
    e.preventDefault()
    const at = hover < 0 ? columns.length - 1 : hover + (e.key === 'ArrowRight' ? 1 : -1)
    hover = Math.max(0, Math.min(columns.length - 1, at))
    tipX = GUTTER + (plotW - GUTTER) * ((hover + 0.5) / Math.max(1, columns.length))
  }

  const TIP = 176 // tooltip width, so it can be kept inside the plot
  const GUTTER = 46 // must match --gutter below; the columns start after the ruler
  const tipLeft = $derived(Math.max(TIP / 2, Math.min(plotW - TIP / 2, tipX)))
  const shown = $derived(hover >= 0 && hover < columns.length ? columns[hover] : null)

  function fmt(v: number): string {
    return metric === 'cost' ? money(v) : compact(v)
  }

  // The axis is a ruler and wants round marks. money() prints cents below $1000
  // and drops them above it, so the two gridlines came out as "$1,000" over
  // "$500.00" -- the same scale wearing two different notations.
  function tick(v: number): string {
    if (metric !== 'cost') return compact(v)
    return v >= 1 ? '$' + Math.round(v).toLocaleString('en-US') : money(v)
  }
</script>

<section class="page" aria-label="Usage">
  <header class="top">
    <button class="back" onclick={() => app.closeUsage()} aria-label="Back" title="Back">
      <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6" /></svg>
    </button>
    <div class="ttl">
      <h1>Usage</h1>
      <!-- The machine only. The period is named by the control two inches away,
           and on a phone the two sat on adjacent lines saying the same thing. -->
      <p class="sub">{where}</p>
    </div>
    <!-- One filter row, above everything it scopes, so every number on the page
         is answering the same question. -->
    <div class="filters">
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
    </div>
  </header>

  <div class="scroll">
    <div class="wrap">
      {#if error}
        <p class="state err">{error}</p>
      {:else if loading && !report}
        <div class="loading"><Spinner /><span>Reading transcripts…</span></div>
      {:else if empty}
        <div class="blank">
          <h2>Nothing recorded yet</h2>
          <p>
            No work on {where} in the last {period} days. Every assistant reply carries its
            own token counts, so this page fills in on its own as soon as a session runs —
            there is nothing to turn on.
          </p>
        </div>
      {:else}
        {#if report?.scanning}
          <p class="scanning">
            <Spinner />
            <span>Still reading transcripts — these numbers are still growing.</span>
          </p>
        {/if}

        <!-- The headline, and immediately the caveat. Split across two elements so
             the caveat is never cropped away from the number it qualifies. -->
        <div class="band">
          <div class="hero">
            <div class="hlabel">Raw token cost</div>
            <div class="hval">{money(total.cost)}</div>
            {#if dCost !== null}
              <div class="delta">
                <span class="arrow" class:up={dCost > 0} class:down={dCost < 0}>
                  {dCost > 0.005 ? '↑' : dCost < -0.005 ? '↓' : '·'}
                </span>
                {signedPct(dCost)} <span class="dim">vs previous {period} days</span>
              </div>
            {:else if since}
              <div class="delta dim">Records begin {dayLabel(since)}</div>
            {/if}
            <div class="spacer"></div>
            <p class="hnote">
              What these tokens would have cost at API rates. Not what you were billed —
              this work ran on subscriptions.
            </p>
          </div>

          <div class="tiles">
            <div class="tile">
              <span class="tl">Tokens processed</span>
              <span class="tv">{compact(totalTokens(total))}</span>
              <span class="td"
                >{dWork !== null
                  ? `${signedPct(dWork)} vs previous`
                  : `across ${compact(total.n)} replies`}</span
              >
            </div>
            <div class="tile">
              <span class="tl">Saved by caching</span>
              <span class="tv">{money(total.savings)}</span>
              <span class="td">not spent re-reading context</span>
            </div>
          </div>
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
          <section class="card">
            <div class="chd">
              <h2>Where the work went</h2>
              <span class="hint">share of tokens</span>
            </div>
            <div class="split">
              {#each agents as a (a.model)}
                {@const share = work > 0 ? totalTokens(a) / work : 0}
                <div
                  class="seg"
                  style="flex: {Math.max(share, 0.015)}; background: {agentColor(a.model)}"
                  title="{a.model} · {compact(totalTokens(a))} tokens"
                ></div>
              {/each}
            </div>
            <div class="agents">
              {#each agents as a (a.model)}
                <div class="agent">
                  <div class="aname">
                    <span class="swatch" style="background: {agentColor(a.model)}"></span>
                    {a.model}
                  </div>
                  <div class="acost">{a.priced ? money(a.cost) : 'not priced'}</div>
                  <!-- One expression rather than an inline {#if}: the block form
                       swallows the space in front of the separator, which reads
                       as "12.6B tokens· >99.9%". -->
                  <div class="ameta">
                    {`${compact(totalTokens(a))} tokens${work > 0 ? ` · ${percent(totalTokens(a) / work)}` : ''}`}
                  </div>
                </div>
              {/each}
            </div>
          </section>
        {/if}

        <!-- Daily. Columns, not an area: an area over 90 sparse days implies a
             continuous quantity between the points, and this is a per-day total.
             Split by agent rather than totalled, because "Tuesday was heavy" is
             half an answer and "Tuesday was heavy and it was all Codex" is the
             whole one. -->
        <section class="card">
          <div class="chd">
            <h2>Daily {metric === 'cost' ? 'cost' : 'tokens'}</h2>
            <div class="toggle">
              <button class:on={metric === 'cost'} onclick={() => (metric = 'cost')}>Cost</button>
              <button class:on={metric === 'tokens'} onclick={() => (metric = 'tokens')}>Tokens</button>
            </div>
          </div>

          <!-- The chart is focusable and arrow-key readable, which is why it
               takes a tabindex a figure would not normally carry. The lint is
               right in general and wrong here: this ADDS a keyboard path to
               numbers that would otherwise only be reachable by hover, and the
               Breakdown table below is still the non-visual route, so nothing is
               gated behind it either way. -->
          <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <figure
            class="plot"
            bind:this={plotEl}
            bind:clientWidth={plotW}
            style="--n: {columns.length}"
            tabindex="0"
            role="group"
            aria-label="Daily {metric} by agent. Use the arrow keys to read each day, or the Breakdown table below."
            onkeydown={arrows}
            onpointerleave={() => (hover = -1)}
            onblur={() => (hover = -1)}
          >
            <!-- Two hairlines and their labels. Recessive on purpose: they are a
                 ruler, not data. -->
            <div class="rule" style="top: 0"><span>{tick(scale)}</span></div>
            <div class="rule half" style="bottom: 50%"><span>{tick(scale / 2)}</span></div>

            {#each columns as c, i (c.day)}
              <div
                class="col"
                class:dim={hover >= 0 && hover !== i}
                onpointerenter={(e) => track(e, i)}
                onpointermove={(e) => track(e, i)}
                role="presentation"
              >
                {#if c.total > 0}
                  <div class="stk" style="height: {Math.max(1.5, (c.total / scale) * 100)}%">
                    {#each c.parts as p (p.agent)}
                      <div class="sq" style="flex: {p.value}; background: {agentColor(p.agent)}"></div>
                    {/each}
                  </div>
                {:else}
                  <!-- A day with no work is a hairline rather than nothing, so the
                       gap reads as "measured, and empty" instead of as missing
                       data. -->
                  <div class="zero"></div>
                {/if}
              </div>
            {/each}

            {#if shown}
              <div class="tip" style="left: {tipLeft}px; width: {TIP}px">
                <div class="tday">{dayLabel(shown.day)}</div>
                <div class="ttotal">{fmt(shown.total)}</div>
                {#each shown.parts as p (p.agent)}
                  <div class="trow">
                    <span class="key" style="background: {agentColor(p.agent)}"></span>
                    <span class="tname">{p.agent}</span>
                    <span class="tval">{fmt(p.value)}</span>
                  </div>
                {/each}
                {#if !shown.parts.length}<div class="trow dim">nothing recorded</div>{/if}
              </div>
            {/if}
          </figure>

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

          <p class="cap">
            {#if busiest && busiest.total > 0}
              Busiest day was {dayLabel(busiest.day)} at {fmt(busiest.total)}.
            {/if}
            {#if metric === 'cost' && priced < 1}
              Unpriced models add nothing to these bars — switch to Tokens to see all the
              work.
            {/if}
          </p>
        </section>

        <!-- The token split. Kept apart from the money because it is the thing that
             explains it: an agent re-reads its whole context on every tool call, so
             cache reads dwarf everything and the headline only makes sense next to
             them. -->
        <section class="card">
          <div class="chd">
            <h2>Tokens</h2>
            <span class="hint">what the money was made of</span>
          </div>
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
        </section>

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
                <th class="share">Share</th>
                <th class="num">Tokens</th>
              </tr>
            </thead>
            <tbody>
              {#if split === 'model'}
                {#each models as m (m.model)}
                  {@const f = m.priced && total.cost > 0 ? m.cost / total.cost : 0}
                  <tr>
                    <td class="mono name">
                      <span class="swatch sm" style="background: {agentColor(m.agent)}"></span>
                      {m.model}
                    </td>
                    <td class="num">{m.priced ? money(m.cost) : '—'}</td>
                    <td class="share">
                      <div class="shbox">
                        <span class="track"
                          ><span
                            class="fill"
                            style="width: {(f * 100).toFixed(2)}%; background: {agentColor(m.agent)}"
                          ></span></span
                        >
                        <span class="pct">{m.priced && total.cost > 0 ? percent(f) : '—'}</span>
                      </div>
                    </td>
                    <td class="num dim">{compact(totalTokens(m))}</td>
                  </tr>
                {/each}
              {:else}
                <!-- Newest first here, unlike the chart: a table is read from the
                     top and the day you care about is today. -->
                {#each [...days].reverse().filter((d) => d.models.length) as d (d.day)}
                  {@const t = totals([d])}
                  {@const f = total.cost > 0 ? t.cost / total.cost : 0}
                  <tr>
                    <td class="mono name">{dayLabel(d.day)}</td>
                    <td class="num">{money(t.cost)}</td>
                    <td class="share">
                      <div class="shbox">
                        <span class="track"
                          ><span class="fill neutral" style="width: {(f * 100).toFixed(2)}%"></span
                          ></span
                        >
                        <span class="pct">{total.cost > 0 ? percent(f) : '—'}</span>
                      </div>
                    </td>
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
        <section class="card">
          <div class="chd">
            <h2>Cost quality</h2>
            <span class="hint">how much of this is measured</span>
          </div>
          <div class="cover">
            <div class="cbar">
              <div class="cfill" style="width: {(priced * 100).toFixed(2)}%"></div>
            </div>
            <div class="clegend">
              <span><span class="ckey on"></span>Priced {percent(priced)}</span>
              <span><span class="ckey"></span>Unpriced {percent(1 - priced)}</span>
            </div>
          </div>
          <p class="qnote">
            Unpriced tokens are counted but not costed — kunai has no published rate for
            that model, and guessing one would put a confident figure on a guess. Add one
            in <code>pricing.json</code> and it joins the total.
          </p>
        </section>

        <p class="foot">
          Read from {report?.files ?? 0} transcript{(report?.files ?? 0) === 1 ? '' : 's'} across
          every account on {where}.
        </p>
      {/if}
    </div>
  </div>
</section>

<style>
  .page {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  /* The header is chrome over a scrolling canvas, so it takes the same hairline
     treatment the chat header does: no band, no fill, just a seam. */
  .top {
    flex: none;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 16px 11px;
    padding-top: max(10px, var(--safe-top));
    border-bottom: 1px solid var(--border);
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
  .ttl {
    flex: 1;
    min-width: 0;
  }
  .ttl h1 {
    margin: 0;
    font-size: 16.5px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }
  .sub {
    margin: 1px 0 0;
    font-size: 11.5px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .filters {
    flex: none;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .mpick {
    position: relative;
    display: inline-flex;
    align-items: center;
  }
  .mpick select {
    appearance: none;
    -webkit-appearance: none;
    height: 30px;
    padding: 0 26px 0 11px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 100px;
    color: var(--text-2);
    font-size: 12.5px;
    max-width: 170px;
  }
  .mchev {
    position: absolute;
    right: 9px;
    color: var(--text-4);
    pointer-events: none;
  }

  .scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
  }
  .wrap {
    max-width: 940px;
    margin: 0 auto;
    padding: 22px 16px calc(40px + var(--safe-bottom));
  }

  .state {
    margin: 24px 2px;
    font-size: 13px;
    line-height: 1.6;
    color: var(--text-3);
  }
  .state.err {
    color: var(--alert);
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
    margin: 0 2px 18px;
  }
  .blank {
    margin: 60px auto;
    max-width: 44ch;
    text-align: center;
  }
  .blank h2 {
    margin: 0 0 8px;
    font-size: 15px;
    font-weight: 600;
    color: var(--text-2);
  }
  .blank p {
    margin: 0;
    font-size: 12.5px;
    line-height: 1.65;
    color: var(--text-4);
  }

  /* --- headline band --- */
  .band {
    display: grid;
    grid-template-columns: minmax(0, 1.35fr) minmax(0, 1fr);
    gap: 14px;
    margin-bottom: 14px;
  }
  .hero {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    padding: 18px 18px 16px;
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
    font-size: 44px;
    line-height: 1.05;
    letter-spacing: -0.03em;
    margin-top: 8px;
    color: var(--text);
  }
  .delta {
    display: flex;
    align-items: baseline;
    gap: 5px;
    margin-top: 9px;
    font-family: var(--mono);
    font-size: 12px;
    color: var(--text-2);
  }
  /* Deliberately not green-good / red-bad. Spending more is not a failure and
     spending less is not a win -- an overnight loop that finished the job is the
     most expensive day on the chart and the best one. The arrow states the
     direction and stops there. */
  .arrow {
    color: var(--text-4);
  }
  .delta .dim,
  .delta.dim {
    color: var(--text-4);
  }
  .spacer {
    flex: 1;
    min-height: 12px;
  }
  /* Pinned to the bottom of the card rather than trailing the number: the band's
     height is set by the two tiles beside it, and left to follow the delta the
     caveat sat in the middle with a hole under it. */
  .hnote {
    margin: 16px 0 0;
    padding-top: 12px;
    align-self: stretch;
    max-width: 46ch;
    font-size: 12px;
    line-height: 1.55;
    color: var(--text-4);
  }

  .tiles {
    display: grid;
    grid-template-rows: 1fr 1fr;
    gap: 14px;
    min-width: 0;
  }
  .tile {
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 4px;
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    padding: 14px 16px;
    min-width: 0;
  }
  .tl {
    font-size: 11.5px;
    color: var(--text-4);
  }
  .tv {
    font-family: var(--mono);
    font-size: 22px;
    letter-spacing: -0.02em;
    color: var(--text);
  }
  .td {
    font-size: 11px;
    color: var(--text-4);
  }

  /* --- cards --- */
  .card {
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    padding: 15px 16px 16px;
    margin-bottom: 14px;
  }
  .chd {
    display: flex;
    align-items: baseline;
    gap: 10px;
    margin-bottom: 15px;
  }
  .chd h2 {
    flex: 1;
    margin: 0;
    font-size: 12.5px;
    font-weight: 600;
    color: var(--text-2);
  }
  .hint {
    font-size: 11px;
    color: var(--text-4);
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
    transition: color var(--t-fast) var(--ease);
  }
  .toggle button:hover {
    color: var(--text-2);
  }
  .toggle button.on {
    background: var(--panel-3);
    color: var(--text);
  }

  /* --- agent split --- */
  .split {
    display: flex;
    /* The 2px gap is the separator, in the surface colour. Neighbouring hues read
       as distinct because of the gap, not because of a stroke drawn round them:
       a border would be data-weight ink that is not data. */
    gap: 2px;
    height: 9px;
    margin-bottom: 15px;
  }
  .seg {
    border-radius: 100px;
  }
  .agents {
    display: flex;
    flex-wrap: wrap;
    gap: 12px 30px;
  }
  .aname {
    display: flex;
    align-items: center;
    gap: 7px;
    font-size: 12.5px;
    color: var(--text-2);
  }
  .swatch {
    flex: none;
    width: 9px;
    height: 9px;
    border-radius: 3px;
  }
  .swatch.sm {
    width: 7px;
    height: 7px;
    border-radius: 2px;
    display: inline-block;
    margin-right: 7px;
    vertical-align: 1px;
  }
  .acost {
    font-family: var(--mono);
    font-size: 18px;
    margin-top: 4px;
    color: var(--text);
  }
  .ameta {
    font-family: var(--mono);
    font-size: 11px;
    margin-top: 2px;
    color: var(--text-4);
  }

  /* --- daily chart --- */
  /* The gutter is not decoration. With the tick labels sitting over the plot they
     landed on top of whichever bar happened to be tall there, which is exactly
     the region of the chart a reader is looking at. Giving the ruler its own
     column costs 44px and makes the collision impossible rather than unlikely. */
  .plot {
    --gutter: 46px;
    position: relative;
    display: grid;
    grid-template-columns: repeat(var(--n), minmax(0, 1fr));
    align-items: end;
    gap: 2px;
    height: 168px;
    margin: 0;
    padding-left: var(--gutter);
    outline: none;
  }
  .plot:focus-visible {
    outline: 1px solid var(--border-2);
    outline-offset: 6px;
    border-radius: 4px;
  }
  .rule {
    position: absolute;
    left: var(--gutter);
    right: 0;
    height: 1px;
    background: var(--border);
    pointer-events: none;
  }
  .rule span {
    position: absolute;
    right: calc(100% + 9px);
    top: 50%;
    transform: translateY(-50%);
    font-family: var(--mono);
    font-variant-numeric: tabular-nums;
    font-size: 9.5px;
    color: var(--text-4);
    white-space: nowrap;
  }
  .rule.half {
    background: color-mix(in srgb, var(--border) 60%, transparent);
  }
  /* The whole column is the hit target, including the empty space above the bar:
     aiming at a 6px-wide 3px-tall mark is a game, not an interface. */
  .col {
    height: 100%;
    display: flex;
    align-items: flex-end;
    min-width: 0;
  }
  .stk {
    width: 100%;
    /* Capped rather than filling the slot, so a 7-day window does not render six
       fat slabs. The leftover is air. */
    max-width: 22px;
    margin: 0 auto;
    display: flex;
    flex-direction: column-reverse;
    gap: 2px;
    border-radius: 4px 4px 0 0;
    overflow: hidden;
    transition: opacity var(--t-fast) var(--ease);
  }
  .sq {
    min-height: 2px;
  }
  .zero {
    width: 100%;
    max-width: 22px;
    margin: 0 auto;
    height: 1.5px;
    background: var(--border-2);
  }
  /* The hovered column stays lit and everything else steps back, which is a
     cheaper and steadier way to say "this one" than moving anything. */
  .col.dim .stk,
  .col.dim .zero {
    opacity: 0.35;
  }

  /* Inside the plot, not floating above it: anchored above, a 110px readout
     cleared the card entirely and landed on the panel overhead, which reads as a
     stray popover rather than as part of the chart. The dimmed columns keep it
     legible over whatever it covers. */
  .tip {
    position: absolute;
    top: 4px;
    transform: translateX(-50%);
    z-index: 5;
    padding: 9px 11px 10px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: 10px;
    box-shadow: 0 12px 30px -14px rgba(0, 0, 0, 0.9);
    pointer-events: none;
  }
  .tday {
    font-size: 10.5px;
    color: var(--text-4);
  }
  /* Value leads, label follows: the reader already has the day, they came for
     the number. That is the legend's hierarchy inverted, on purpose. */
  .ttotal {
    font-family: var(--mono);
    font-size: 17px;
    line-height: 1.2;
    margin: 2px 0 7px;
    color: var(--text);
  }
  .trow {
    display: flex;
    align-items: center;
    gap: 7px;
    font-size: 11.5px;
    color: var(--text-3);
    padding: 1.5px 0;
  }
  .trow.dim {
    color: var(--text-4);
  }
  /* A short line key rather than a filled box: at tooltip density a swatch is
     data-weight ink doing a label's job. */
  .key {
    flex: none;
    width: 10px;
    height: 2px;
    border-radius: 2px;
  }
  .tname {
    flex: 1;
    min-width: 0;
  }
  .tval {
    font-family: var(--mono);
    color: var(--text-2);
  }

  .axis {
    display: flex;
    justify-content: space-between;
    margin-top: 8px;
    padding-left: 46px; /* the plot's gutter, so Jul 11 sits over its own column */
    font-family: var(--mono);
    font-size: 9.5px;
    color: var(--text-4);
  }
  .cap {
    margin: 10px 0 0;
    font-size: 11.5px;
    line-height: 1.55;
    color: var(--text-4);
  }
  .cap:empty {
    display: none;
  }

  /* --- token tiles --- */
  .stats {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(104px, 1fr));
    gap: 14px 10px;
  }
  .stat {
    display: flex;
    flex-direction: column;
    gap: 3px;
    min-width: 0;
  }
  .sl {
    font-size: 11px;
    color: var(--text-4);
  }
  .sv {
    font-family: var(--mono);
    font-size: 16px;
    color: var(--text);
  }

  /* --- breakdown --- */
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
    padding-bottom: 8px;
    border-bottom: 1px solid var(--border);
  }
  .tbl td {
    padding: 8px 0;
    border-bottom: 1px solid var(--border);
    color: var(--text-2);
  }
  /* Columns need air between them or the cost runs straight into the share bar,
     which is what content-sized numeric columns will always do. */
  .tbl th + th,
  .tbl td + td {
    padding-left: 20px;
  }
  .tbl tbody tr:last-child td {
    border-bottom: none;
    padding-bottom: 0;
  }
  .tbl tbody tr:hover td {
    color: var(--text);
  }
  .tbl .num {
    text-align: right;
    font-family: var(--mono);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .tbl .mono {
    font-family: var(--mono);
    color: var(--text);
  }
  /* The model column takes the width; the three numeric ones are sized by their
     own content. Left to the browser, a full-length model id was crushed to
     "claude-opus-4…" while the Cost column carried 200px of nothing. */
  .tbl .name {
    width: 46%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .tbl .dim {
    color: var(--text-4);
  }
  /* The share column is a length as well as a number: a table of percentages is
     read one row at a time, and the point of a share is the comparison between
     rows, which only a shape gives you for free.
     The flex lives on an inner box rather than on the cell, because a
     display:flex <td> leaves the table's column algorithm entirely -- which is
     what was starving the model column while padding the cost one. */
  th.share,
  td.share {
    text-align: right;
    width: 1%;
    white-space: nowrap;
  }
  .shbox {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 9px;
  }
  .track {
    flex: none;
    width: 78px;
    height: 4px;
    border-radius: 100px;
    background: var(--panel-3);
    overflow: hidden;
  }
  .fill {
    display: block;
    height: 100%;
    border-radius: 100px;
  }
  .fill.neutral {
    background: var(--text-3);
  }
  .pct {
    font-family: var(--mono);
    font-variant-numeric: tabular-nums;
    font-size: 11.5px;
    color: var(--text-4);
    min-width: 42px;
    text-align: right;
  }

  /* --- cost quality --- */
  .cbar {
    height: 7px;
    border-radius: 100px;
    background: var(--panel-3);
    overflow: hidden;
  }
  .cfill {
    height: 100%;
    border-radius: 100px;
    background: var(--text-2);
  }
  .clegend {
    display: flex;
    gap: 20px;
    margin-top: 11px;
    font-size: 11.5px;
    color: var(--text-3);
  }
  .clegend span {
    display: inline-flex;
    align-items: center;
    gap: 7px;
  }
  .ckey {
    width: 9px;
    height: 9px;
    border-radius: 3px;
    background: var(--panel-3);
  }
  .ckey.on {
    background: var(--text-2);
  }
  .qnote {
    margin: 12px 0 0;
    font-size: 11.5px;
    line-height: 1.6;
    color: var(--text-4);
  }
  .qnote code {
    font-family: var(--mono);
    font-size: 11px;
    color: var(--text-3);
  }

  .foot {
    margin: 4px 2px 0;
    font-size: 11.5px;
    color: var(--text-4);
  }

  @media (max-width: 720px) {
    .band {
      grid-template-columns: 1fr;
    }
    .tiles {
      grid-template-rows: none;
      grid-template-columns: 1fr 1fr;
    }
    .hval {
      font-size: 34px;
    }
    .wrap {
      padding: 16px 13px calc(36px + var(--safe-bottom));
    }
    /* The hero's caveat is pinned to the bottom of the card only while the card
       is sharing a row with the tiles. Stacked, there is nothing to line up with
       and the spacer is just a hole. */
    .spacer {
      display: none;
    }
    .hnote {
      margin-top: 12px;
      padding-top: 0;
    }
    /* A table with a bar in it needs width the phone does not have, and the
       number is the part that matters. */
    td.share .track {
      display: none;
    }
    .tbl .name {
      width: auto;
    }
  }
</style>
