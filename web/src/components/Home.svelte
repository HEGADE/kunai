<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { usage } from '../lib/api'
  import type { Usage, UsageWindow } from '../lib/types'
  import { updateAvailable } from '../lib/update'
  import Schedules from './Schedules.svelte'

  let { compact = false }: { compact?: boolean } = $props()

  // Which machine's stats to show. '' = the hub/self. Lets you inspect any
  // machine in the fleet by clicking its tab.
  let picked = $state('')
  const multi = $derived(app.machines.length > 1)
  const sel = $derived(
    app.machines.find((m) => m.id === picked) ??
      app.machines.find((m) => m.self) ??
      app.machines[0] ??
      null,
  )
  const st = $derived(sel?.stats ?? null)
  const outdated = $derived(updateAvailable(st?.kunai_version, app.latestVersion))
  const updating = $derived(sel ? !!app.updating[sel.id] : false)
  const updateErr = $derived(sel ? (app.updateError[sel.id] ?? '') : '')
  const selSessions = $derived(sel ? app.sessions.filter((s) => s.machineId === sel.id).length : 0)
  const selResumable = $derived(sel ? app.history.filter((h) => h.machineId === sel.id).length : 0)

  const greeting = $derived.by(() => {
    const h = new Date().getHours()
    if (h < 5) return 'Good night'
    if (h < 12) return 'Good morning'
    if (h < 17) return 'Good afternoon'
    return 'Good evening'
  })

  // Memory in binary GiB, the convention for RAM (a "16 GB" stick is 16 GiB).
  function gb(n: number): string {
    if (!n) return '—'
    const g = n / 1024 ** 3
    return g >= 100 ? `${Math.round(g)} GB` : `${g.toFixed(1)} GB`
  }
  // Disk in decimal GB (÷10^9), which is what macOS and disk makers show, so the
  // total matches the OS instead of reading ~7% smaller as binary GiB would.
  function gbDisk(n: number): string {
    if (!n) return '—'
    const g = n / 1e9
    return g >= 100 ? `${Math.round(g)} GB` : `${g.toFixed(1)} GB`
  }
  function dur(sec: number): string {
    if (!sec) return '—'
    const d = Math.floor(sec / 86400)
    const h = Math.floor((sec % 86400) / 3600)
    const m = Math.floor((sec % 3600) / 60)
    if (d > 0) return `${d}d ${h}h`
    if (h > 0) return `${h}h ${m}m`
    return `${m}m`
  }
  const memUsedPct = $derived(
    st && st.mem_total ? Math.round(((st.mem_total - st.mem_available) / st.mem_total) * 100) : 0,
  )
  // Apple Silicon reports a pressure level, not degrees; these two levels mean
  // "backing off".
  const pressureHot = $derived(st?.thermal_pressure === 'serious' || st?.thermal_pressure === 'critical')
  const capitalize = (s: string) => (s ? s[0].toUpperCase() + s.slice(1) : s)

  // A vital is only worth reading when it is a problem, so these are the only
  // reason any of them raises its voice. Everything else stays a quiet line.
  const tempHot = $derived(!!st && (st.cpu_temp_c >= 80 || pressureHot))
  const memHigh = $derived(memUsedPct >= 90)
  const diskLow = $derived(!!st && !!st.disk_total && st.disk_free / st.disk_total < 0.1)
  // What the page is actually asked from a phone at 2am: is anything working?
  const running = $derived(
    selSessions === 0
      ? 'Nothing running'
      : `${selSessions} session${selSessions === 1 ? '' : 's'} running`,
  )

  // The selected machine's Claude quota. Depends on the primitives, not on the
  // machine object, so a stats refresh that changes nothing here doesn't refetch.
  const selId = $derived(sel?.id ?? '')
  const selUrl = $derived(sel?.url ?? '')
  const selOnline = $derived(sel?.online ?? false)
  // Quota is per account: a machine can run more than one Claude login and each
  // has its own windows, so one meter could only ever describe one of them.
  // `clis` is sent only when there is a real choice, so a single-account machine
  // keeps exactly one unlabelled meter as before. '' means the default account.
  const accounts = $derived<string[]>(st?.clis?.length ? st.clis : [''])
  let uses = $state<Record<string, Usage | null>>({})
  // Why an account has no numbers, when it has none. Without this a failed read
  // just deleted the rows, which reads as "still loading" forever.
  let usageErrs = $state<Record<string, string>>({})
  // Whether this machine's quota has come back yet, either way. It gates the
  // skeleton, so the skeleton shows once per machine and never again: a refresh
  // updates the numbers in place rather than blinking the rows away and back.
  let usageLoaded = $state(false)
  // Only a real machine switch clears the meters. A transient poll blip (a
  // dropped fetch that flips a machine offline for one tick) must NOT blank
  // numbers that were right a second ago: that blanking collapsed the quota
  // rows to zero height and shoved the whole sidebar list ("dancing").
  let usageFor = ''
  $effect(() => {
    const url = selUrl,
      online = selOnline,
      id = selId,
      names = accounts
    if (id !== usageFor) {
      usageFor = id
      uses = {}
      usageErrs = {}
      usageLoaded = false
    }
    if (!online) {
      usageLoaded = true // an offline machine has no quota to wait for; keep the last-good numbers
      return
    }
    let done = false
    // Each account is asked separately, and one being logged out must not blank
    // the others: settle them independently rather than failing the batch.
    const load = () =>
      Promise.all(
        names.map((cli) =>
          usage(url, cli)
            .then((u) => {
              if (done) return
              // Keep the last good numbers if a later poll comes back empty: a
              // blip should not blank a meter that was right a minute ago.
              if (u?.session || u?.weekly) {
                uses[cli] = u
                usageErrs[cli] = ''
              } else {
                usageErrs[cli] = u?.unavailable || 'unavailable'
              }
            })
            .catch((e) => {
              if (done) return
              usageErrs[cli] = String(e?.message || e)
            }),
        ),
      ).then(() => {
        if (!done) usageLoaded = true
      })
    load()
    // The server caches for a minute; match it rather than poll faster than the
    // number can move.
    const t = setInterval(load, 60_000)
    return () => {
      done = true
      clearInterval(t)
    }
  })
  // The rolling window only carries a reset time once it has usage in it. An idle
  // 5-hour window (0% used) reports no reset, which is not "unknown" — nothing has
  // started the clock — so say that plainly instead of raising a false alarm.
  function resetsIn(w: UsageWindow | null): string {
    if (!w) return ''
    if (!w.resets_at) return w.percent > 0 ? 'reset time unknown' : 'no usage this window'
    const s = w.resets_at - Math.floor(Date.now() / 1000)
    return s <= 0 ? 'resetting' : `resets in ${dur(s)}`
  }

  // With several accounts, two meters each is a wall: six bars that mostly say
  // "fine". The limit that stops you first is the only honest single reading, so
  // an account collapses to one row showing its fuller window, named, with when
  // it frees up. Same argument the loop meter makes for its two caps. Tapping a
  // row opens both windows, so nothing is lost, it is just not all shouted at
  // once.
  type Binding = { pct: number; window: string; when: string }
  function binding(u: Usage | null): Binding | null {
    const s = u?.session ?? null
    const w = u?.weekly ?? null
    if (!s && !w) return null
    const pick = !s ? w! : !w ? s! : w.percent >= s.percent ? w : s
    return { pct: pick.percent, window: pick === w ? 'weekly' : 'session', when: shortReset(pick) }
  }
  // Terse enough for a phone: "2h 39m", not "resets in 2h 39m".
  function shortReset(w: UsageWindow): string {
    if (!w.resets_at) return w.percent > 0 ? '' : 'idle'
    const s = w.resets_at - Math.floor(Date.now() / 1000)
    return s <= 0 ? 'resetting' : dur(s)
  }
  // Which account's detail is open. One at a time: this is a glance, not a table.
  let openAcct = $state('')

  // Quick-start dirs for the selected machine only — so chips don't each repeat
  // the machine name (that's stated once in the section header).
  const selProjects = $derived.by(() => {
    if (!sel) return []
    const active = new Set(app.sessions.filter((s) => s.machineId === sel.id).map((s) => s.cwd))
    const seen = new Set<string>()
    const out: { cwd: string; name: string }[] = []
    for (const h of app.history) {
      if (h.machineId !== sel.id || active.has(h.cwd) || seen.has(h.cwd)) continue
      seen.add(h.cwd)
      out.push({ cwd: h.cwd, name: h.cwd.replace(/\/+$/, '').split('/').slice(-1)[0] || h.cwd })
      if (out.length >= 8) break
    }
    return out
  })
</script>

<div class="home" class:compact>
  <div class="hello">
    <h1>{greeting}.</h1>
    <p class="sub">
      {#if st?.hostname}<span class="host mono">{st.hostname}</span>{:else if sel}<span class="host mono">{sel.label}</span>{/if}
      <span class="mono dim">· direct over tailnet</span>
      {#if st?.claude_version}<span class="mono dim">· claude {st.claude_version}</span>{/if}
    </p>
  </div>

  {#if multi}
    <div class="mpick">
      {#each app.machines as m (m.id)}
        <button
          class="mp"
          class:on={sel?.id === m.id}
          class:off={!m.online}
          title={m.url}
          onclick={() => (picked = m.id)}
        >
          <span class="pdot" class:live={m.online}></span>
          <span class="plabel">{m.label}</span>
        </button>
      {/each}
    </div>
  {/if}

  {#if !st && sel}
    <div class="offline">
      <span class="odot"></span>
      {sel.label} is offline — no stats to show.
    </div>
  {/if}

  {#if outdated && sel}
    <div class="update">
      <span class="udot"></span>
      <div class="utext">
        <span class="uhead">Update available</span>
        <span class="mono usub">{st?.kunai_version} → {app.latestVersion} · restarts {sel.label}, sessions resume</span>
        {#if updateErr}
          <span class="mono uerr">update failed: {updateErr}</span>
        {/if}
      </div>
      <button class="ubtn" disabled={updating} onclick={() => sel && app.updateMachine(sel.id)}>
        {updating ? 'Updating…' : updateErr ? 'Retry' : 'Update'}
      </button>
    </div>
  {/if}

  {#if st}
    <!-- Quota first, and close to alone. It is the only thing on this page that
         can stop you working, so it is the only thing that gets any weight. -->
    {#if !usageLoaded}
      <!-- Hold the rows' exact height while the CLI is asked. The quota takes a
           couple of seconds to arrive, and appearing from nothing shoved the
           whole page down; this reserves the space and fills it in place. -->
      <div class="quota" aria-hidden="true">
        {#each ['Session', 'Weekly'] as k (k)}
          <div class="q skel">
            <span class="q-k">{k}</span>
            <div class="q-track"></div>
            <span class="q-pct mono">—</span>
            <span class="q-when mono"></span>
          </div>
        {/each}
      </div>
    {:else if accounts.length > 1}
      <!-- Several accounts: one line each, the binding window only. Six bars that
           mostly say "fine" is not a dashboard, and on a phone it is a wall. -->
      <div class="qroster">
        {#each accounts as cli (cli)}
          {@const u = uses[cli] ?? null}
          {@const b = binding(u)}
          {@const err = usageErrs[cli] ?? ''}
          {@const open = openAcct === cli}
          <button
            class="qrow"
            class:hot={(b?.pct ?? 0) >= 80}
            class:open
            onclick={() => (openAcct = open ? '' : cli)}
            aria-expanded={open}
            title="{cli} — tap for both windows"
          >
            <i class="qfill" style="width:{Math.min(100, b?.pct ?? 0)}%"></i>
            <span class="qname">{cli}</span>
            {#if b}
              <span class="qwhen mono">{b.window}{b.when ? ` · ${b.when}` : ''}</span>
              <span class="qpct mono">{Math.round(b.pct)}<small>%</small></span>
            {:else}
              <span class="qwhen mono">{err ? 'no quota' : '—'}</span>
              <span class="qpct mono">—</span>
            {/if}
          </button>
          {#if open && u}
            <!-- Both windows, on demand. -->
            <div class="qdetail">
              {#each [{ k: 'Session', w: u.session }, { k: 'Weekly', w: u.weekly }] as { k, w } (k)}
                {#if w}
                  <div class="q">
                    <span class="q-k">{k}</span>
                    <div class="q-track">
                      <i class:hot={w.percent >= 80} style="width:{Math.max(1.5, Math.min(100, w.percent))}%"></i>
                    </div>
                    <span class="q-pct mono" class:hot={w.percent >= 80}>{Math.round(w.percent)}<small>%</small></span>
                    <span class="q-when mono">{resetsIn(w)}</span>
                  </div>
                {/if}
              {/each}
            </div>
          {/if}
        {/each}
      </div>
    {:else}
      <!-- One account: two rows was never the clutter, and it says more. -->
      {#each accounts as cli (cli)}
        {@const u = uses[cli] ?? null}
        {@const session = u?.session ?? null}
        {@const weekly = u?.weekly ?? null}
        {@const err = usageErrs[cli] ?? ''}
        <div class="quota">
          {#if err && !session && !weekly}
            {#each ['Session', 'Weekly'] as k (k)}
              <div class="q skel">
                <span class="q-k">{k}</span>
                <div class="q-track"></div>
                <span class="q-pct mono">—</span>
                <span class="q-when mono">{err === 'unavailable' ? 'no quota reported' : 'unavailable'}</span>
              </div>
            {/each}
          {:else}
            {#if session}
              <div class="q">
                <span class="q-k">Session</span>
                <div class="q-track">
                  <i class:hot={session.percent >= 80} style="width:{Math.max(1.5, Math.min(100, session.percent))}%"></i>
                </div>
                <span class="q-pct mono" class:hot={session.percent >= 80}
                  >{Math.round(session.percent)}<small>%</small></span
                >
                <span class="q-when mono">{resetsIn(session)}</span>
              </div>
            {/if}
            {#if weekly}
              <div class="q">
                <span class="q-k">Weekly</span>
                <div class="q-track">
                  <i class:hot={weekly.percent >= 80} style="width:{Math.max(1.5, Math.min(100, weekly.percent))}%"></i>
                </div>
                <span class="q-pct mono" class:hot={weekly.percent >= 80}
                  >{Math.round(weekly.percent)}<small>%</small></span
                >
                <span class="q-when mono">{resetsIn(weekly)}</span>
              </div>
            {/if}
          {/if}
        </div>
      {/each}
    {/if}

    <!-- The machine, by exception. A vital that is fine is not news, so it stays
         one quiet line; the guard tripping is news, so it says so in words. The
         silence is the signal. -->
    {#if st.thermal_trip}
      <p class="alarm">Ran too hot — the guard stopped every session here.</p>
    {/if}
    <div class="status">
      <p class="state" class:live={selSessions > 0}>
        <span class="sdot" class:live={selSessions > 0} aria-hidden="true"></span>
        {running}
        <!-- A count of what you could reopen is navigation, not status, so it
             rides along quietly rather than sharing the sentence's weight. -->
        {#if selSessions === 0 && selResumable}<span class="sresume">· {selResumable} to resume</span>{/if}
      </p>
      <div class="vitals mono">
      {#if st.cpu_temp_c > 0}
        <span class:warn={tempHot}>{Math.round(st.cpu_temp_c)}°C</span>
      {:else if st.thermal_pressure}
        <span class:warn={tempHot}>{capitalize(st.thermal_pressure)}</span>
      {/if}
      {#if st.mem_total}
        <span class:warn={memHigh} title="{gb(st.mem_total - st.mem_available)} of {gb(st.mem_total)}"
          >{memUsedPct}% memory</span
        >
      {/if}
      {#if st.disk_total}
        <span class:warn={diskLow} title="of {gbDisk(st.disk_total)}">{gbDisk(st.disk_free)} free</span>
      {/if}
        {#if st.uptime_sec}<span>up {dur(st.uptime_sec)}</span>{/if}
      </div>
    </div>
  {/if}

  <div class="start">
    <span class="s-label">{multi && sel ? `Start on ${sel.label}` : 'Start in'}</span>
    <div class="chips">
      {#each selProjects as p (p.cwd)}
        <button class="chip" title={p.cwd} onclick={() => sel && app.quickStart(sel.id, p.cwd)}>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" /></svg>
          {p.name}
        </button>
      {/each}
      <button class="chip browse" onclick={() => app.newSession()}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
        Browse…
      </button>
    </div>
  </div>

  <Schedules />
</div>

<style>
  .home {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }
  /* Full (desktop pane) variant centers a wider column */
  .home:not(.compact) {
    max-width: 720px;
    margin: 0 auto;
    padding: 9vh 32px 32px;
    width: 100%;
  }
  .hello h1 {
    font-size: 26px;
    font-weight: 600;
    letter-spacing: -0.02em;
    margin: 0 0 6px;
  }
  .compact .hello h1 {
    font-size: 21px;
  }
  .sub {
    margin: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    font-size: 11.5px;
  }
  .host {
    color: var(--text-2);
  }
  .dim {
    color: var(--text-4);
  }
  .mpick {
    display: flex;
    gap: 7px;
    flex-wrap: wrap;
  }
  .mp {
    max-width: 220px;
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 7px 13px;
    border-radius: 100px;
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-3);
    font-size: 13px;
    font-weight: 500;
  }
  .mp .pdot {
    flex: none;
  }
  .plabel {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .mp:hover {
    color: var(--text-2);
    border-color: var(--border-2);
  }
  .mp.on {
    color: var(--text);
    background: var(--panel-3);
    border-color: var(--border-2);
  }
  .mp.off {
    opacity: 0.55;
  }
  .pdot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .pdot.live {
    background: var(--live);
  }
  .offline {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 14px 16px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    color: var(--text-3);
    font-size: 13px;
  }
  .odot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--alert);
  }
  .update {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 14px;
    background: var(--panel);
    border: 1px solid var(--border-2);
    border-radius: var(--r-lg);
  }
  .udot {
    flex: none;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--text);
  }
  .utext {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
    flex: 1;
  }
  .uhead {
    font-size: 13px;
    font-weight: 550;
    color: var(--text);
  }
  .usub {
    font-size: 10.5px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .uerr {
    font-size: 10.5px;
    color: var(--alert);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .ubtn {
    flex: none;
    padding: 7px 16px;
    border-radius: 100px;
    background: var(--text);
    color: var(--bg);
    border: none;
    font-size: 13px;
    font-weight: 600;
  }
  .ubtn:disabled {
    opacity: 0.6;
  }
  /* Quota: the page's one piece of weight. Reuses the track/fill and mono
     numerals the context meter already uses, so a budget reads the same
     everywhere in kunai. */
  /* The multi-account roster: one line per account, and the meter IS the row's
     fill, so a bar costs no vertical space at all. Three accounts read as three
     lines rather than six bars. */
  .qroster {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-bottom: 20px;
    max-width: 34rem;
  }
  .qrow {
    position: relative;
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    text-align: left;
    padding: 9px 12px;
    border-radius: 8px;
    overflow: hidden;
    background: var(--panel);
  }
  .qrow:hover,
  .qrow.open {
    background: var(--panel-2);
  }
  /* The fill sits behind the text: positioned so it paints over the row's own
     background, with the labels positioned after it so they stay on top. */
  .qfill {
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    background: var(--panel-3);
    pointer-events: none;
  }
  .qrow.hot .qfill {
    background: color-mix(in oklab, var(--busy) 20%, transparent);
  }
  .qname,
  .qwhen,
  .qpct {
    position: relative;
  }
  .qname {
    flex: 1;
    min-width: 0;
    font-size: 13.5px;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .qwhen {
    flex: none;
    font-size: 11px;
    color: var(--text-4);
  }
  .qpct {
    flex: none;
    font-size: 14px;
    color: var(--text);
    font-variant-numeric: tabular-nums;
    min-width: 2.6rem;
    text-align: right;
  }
  .qrow.hot .qpct {
    color: var(--busy);
  }
  .qpct small {
    font-size: 0.72em;
    opacity: 0.65;
  }
  /* Both windows, revealed under the row that asked for them. */
  .qdetail {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 10px 12px 6px;
  }
  .quota {
    display: flex;
    flex-direction: column;
    gap: 11px;
    margin-bottom: 20px;
    /* A row has to read as one unit. Left to fill the canvas, the eye travels
       from bar to number to reset and the three stop belonging together. */
    max-width: 34rem;
  }
  .q {
    display: grid;
    grid-template-columns: 3.4rem 1fr auto auto;
    align-items: center;
    gap: 12px;
  }
  .q-k {
    font-size: 13px;
    color: var(--text-2);
  }
  .q-track {
    height: 6px;
    border-radius: 100px;
    background: var(--panel-3);
    overflow: hidden;
  }
  .q-track i {
    display: block;
    height: 100%;
    border-radius: 100px;
    background: var(--text-2);
  }
  .q-track i.hot {
    background: var(--busy);
  }
  /* The skeleton is the same row with nothing in it: an empty track and a dash.
     No shimmer — a pulse here would be one more thing moving on a page whose
     whole point is that a quiet machine looks quiet. */
  .q.skel .q-pct {
    color: var(--text-4);
  }
  .q-pct {
    font-size: 15px;
    color: var(--text);
    font-variant-numeric: tabular-nums;
    min-width: 2.8rem;
    text-align: right;
  }
  .q-pct small {
    font-size: 0.72em;
    color: var(--text-3);
    margin-left: 1px;
  }
  .q-pct.hot {
    color: var(--busy);
  }
  .q-when {
    font-size: 12px;
    color: var(--text-3);
    text-align: right;
    white-space: nowrap;
  }
  /* On a phone the reset earns its own row rather than squeezing the track. */
  .home.compact .q {
    grid-template-columns: 1fr auto;
    row-gap: 7px;
  }
  .home.compact .q-k {
    order: 1;
  }
  .home.compact .q-pct {
    order: 2;
  }
  .home.compact .q-track {
    order: 3;
    grid-column: 1 / -1;
  }
  .home.compact .q-when {
    order: 4;
    grid-column: 1 / -1;
    text-align: left;
    min-width: 0;
  }
  .alarm {
    margin: 0 0 8px;
    font-size: 13px;
    color: var(--alert);
  }
  .status {
    display: flex;
    flex-direction: column;
    gap: 2px;
    margin-bottom: 26px;
  }
  .state {
    margin: 0;
    font-size: 13.5px;
    color: var(--text-2);
  }
  .sresume {
    color: var(--text-4);
  }
  .vitals {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0 8px;
    font-size: 12px;
    color: var(--text-4);
  }
  /* On the phone dashboard the vitals sit at the top of the session list, so a
     poll that changes a number's width (uptime ticking, "Nominal"→"45°C") must
     not wrap the line and shove every row below it. Pin it to one line; the
     least-important tail (uptime) is what clips if space runs out. */
  .home.compact .vitals {
    flex-wrap: nowrap;
    overflow: hidden;
  }
  /* The dots come from the layout, not the markup, so a vital that is absent
     never leaves a separator stranded. */
  .vitals span + span::before {
    content: '·';
    margin-right: 8px;
    color: var(--text-4);
    opacity: 0.5;
  }
  .state.live {
    color: var(--text);
  }
  .sdot {
    display: inline-block;
    width: 6px;
    height: 6px;
    margin-right: 7px;
    border-radius: 100px;
    background: var(--text-4);
    vertical-align: 1px;
  }
  .sdot.live {
    background: var(--live);
  }
  .vitals .warn {
    color: var(--busy);
  }

  .start {
    display: flex;
    flex-direction: column;
    gap: 9px;
  }
  .s-label {
    font-size: 11.5px;
    font-weight: 550;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-4);
    padding: 0 2px;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 8px 13px;
    border-radius: 100px;
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-2);
    font-size: 13px;
    font-weight: 500;
    max-width: 100%;
  }
  .chip:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .chip.browse {
    color: var(--text-3);
    border-style: dashed;
  }
</style>
