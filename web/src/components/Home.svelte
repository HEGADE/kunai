<script lang="ts">
  import { app } from '../lib/app.svelte'
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
  // Load average relative to core count is a decent at-a-glance CPU pressure gauge.
  const cpuPct = $derived(st && st.cores ? Math.min(100, Math.round((st.load1 / st.cores) * 100)) : 0)
  // Temperature meter is scaled to a 100°C full bar; most CPUs throttle near there.
  const tempPct = $derived(st ? Math.min(100, Math.round(st.cpu_temp_c)) : 0)
  // Apple Silicon reports a pressure level, not degrees. Map it to a coarse meter
  // and flag the two levels that mean "backing off".
  const pressureLevels: Record<string, number> = { nominal: 20, fair: 55, serious: 85, critical: 100 }
  const pressurePct = $derived(st ? (pressureLevels[st.thermal_pressure] ?? 0) : 0)
  const pressureHot = $derived(st?.thermal_pressure === 'serious' || st?.thermal_pressure === 'critical')
  const capitalize = (s: string) => (s ? s[0].toUpperCase() + s.slice(1) : s)

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
      </div>
      <button class="ubtn" disabled={updating} onclick={() => sel && app.updateMachine(sel.id)}>
        {updating ? 'Updating…' : 'Update'}
      </button>
    </div>
  {/if}

  {#if st}
    <div class="tiles">
      {#if st.mem_total}
        <div class="tile">
          <div class="t-top">
            <span class="t-label">Memory</span>
            <span class="t-val">{memUsedPct}<small>%</small></span>
          </div>
          <div class="meter"><i style="width:{memUsedPct}%"></i></div>
          <span class="t-foot mono">{gb(st.mem_total - st.mem_available)} of {gb(st.mem_total)}</span>
        </div>
      {/if}
      {#if !compact && st.cores}
        <div class="tile">
          <div class="t-top">
            <span class="t-label">CPU</span>
            <span class="t-val">{cpuPct}<small>%</small></span>
          </div>
          <div class="meter"><i style="width:{cpuPct}%"></i></div>
          <span class="t-foot mono">{st.cores} cores · load {st.load1.toFixed(2)}</span>
        </div>
      {/if}
      {#if st.cpu_temp_c > 0}
        <div class="tile">
          <div class="t-top">
            <span class="t-label">Temperature</span>
            <span class="t-val" class:hot={tempPct >= 80} class:tripped={st.thermal_trip}
              >{Math.round(st.cpu_temp_c)}<small>°C</small></span
            >
          </div>
          <div class="meter"><i class:hot={tempPct >= 80} style="width:{tempPct}%"></i></div>
          <span class="t-foot mono">{st.thermal_trip ? 'guard tripped · stopped' : 'CPU'}</span>
        </div>
      {:else if st.thermal_pressure}
        <div class="tile">
          <div class="t-top">
            <span class="t-label">Thermal</span>
            <span class="t-val sm" class:hot={pressureHot} class:tripped={st.thermal_trip}
              >{capitalize(st.thermal_pressure)}</span
            >
          </div>
          <div class="meter"><i class:hot={pressureHot} style="width:{pressurePct}%"></i></div>
          <span class="t-foot mono">{st.thermal_trip ? 'guard tripped · stopped' : 'pressure'}</span>
        </div>
      {/if}
      <div class="tile">
        <div class="t-top">
          <span class="t-label">Disk free</span>
          <span class="t-val">{gbDisk(st.disk_free).split(' ')[0]}<small> GB</small></span>
        </div>
        {#if st.disk_total}
          <div class="meter">
            <i style="width:{Math.round(((st.disk_total - st.disk_free) / st.disk_total) * 100)}%"></i>
          </div>
        {/if}
        <span class="t-foot mono">of {gbDisk(st.disk_total)}</span>
      </div>
      {#if st.uptime_sec}
        <div class="tile">
          <div class="t-top">
            <span class="t-label">Uptime</span>
            <span class="t-val sm">{dur(st.uptime_sec)}</span>
          </div>
          <span class="t-foot mono">load {st.load1.toFixed(2)}</span>
        </div>
      {/if}
      <div class="tile">
        <div class="t-top">
          <span class="t-label">Sessions</span>
          <span class="t-val">{selSessions}</span>
        </div>
        <span class="t-foot mono">kunai up {dur(st.kunai_uptime_sec)}</span>
      </div>
      {#if !compact}
        <div class="tile">
          <div class="t-top">
            <span class="t-label">Resumable</span>
            <span class="t-val">{selResumable}</span>
          </div>
          <span class="t-foot mono">past sessions</span>
        </div>
      {/if}
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
  .tiles {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 8px;
  }
  .home:not(.compact) .tiles {
    grid-template-columns: repeat(3, 1fr);
  }
  .tile {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    padding: 13px 14px 12px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
  }
  .t-top {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 8px;
  }
  .t-label {
    font-size: 11.5px;
    color: var(--text-3);
  }
  .t-val {
    font-size: 19px;
    font-weight: 600;
    letter-spacing: -0.01em;
    color: var(--text);
    white-space: nowrap;
  }
  .t-val small {
    font-size: 12px;
    font-weight: 500;
    color: var(--text-3);
  }
  .t-val.sm {
    font-size: 15px;
  }
  .meter {
    height: 3px;
    border-radius: 3px;
    background: var(--panel-3);
    overflow: hidden;
  }
  .meter i {
    display: block;
    height: 100%;
    border-radius: 3px;
    background: var(--text-2);
  }
  /* Heat is the one stat where the number itself is a warning, so it earns the
     status colours the rest of the dashboard keeps for dots. */
  .t-val.hot {
    color: var(--busy);
  }
  .t-val.tripped {
    color: var(--alert);
  }
  .meter i.hot {
    background: var(--busy);
  }
  .t-foot {
    font-size: 10px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
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
