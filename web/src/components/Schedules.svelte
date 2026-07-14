<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { MODELS } from '../lib/models'
  import type { Job } from '../lib/types'

  const MODES = [
    { id: 'acceptEdits', label: 'Accept edits' },
    { id: 'auto', label: 'Auto' },
    { id: 'default', label: 'Ask (interactive)' },
    { id: 'plan', label: 'Plan (read-only)' },
  ]
  const WINDOWS = [
    { id: 'five_hour', label: '5-hour limit' },
    { id: 'seven_day', label: 'weekly limit' },
  ]

  let show = $state(false)
  const machineOf = (id: string) => app.machines.find((m) => m.id === id)

  // --- new-job form ---
  let f = $state({
    machineId: '',
    targetKind: 'new' as 'new' | 'resume',
    cwd: '',
    sessionId: '',
    model: 'opus',
    mode: 'acceptEdits',
    prompt: '',
    triggerKind: 'reset' as 'at' | 'reset',
    at: '',
    window: 'five_hour',
    offsetMin: 1,
    rearm: true,
  })
  function openForm() {
    f.machineId = app.machines[0]?.id ?? ''
    show = true
  }

  const projectsFor = $derived(app.projects.filter((p) => p.machineId === f.machineId))
  const sessionsFor = $derived(app.history.filter((h) => h.machineId === f.machineId))

  const valid = $derived(
    !!f.prompt.trim() &&
      (f.targetKind === 'new' ? !!f.cwd.trim() : !!f.sessionId) &&
      (f.triggerKind === 'at' ? !!f.at : f.offsetMin >= 0),
  )

  function submit() {
    if (!valid) return
    const target =
      f.targetKind === 'new'
        ? { kind: 'new' as const, cwd: f.cwd.trim(), model: f.model, mode: f.mode }
        : {
            kind: 'resume' as const,
            session_id: f.sessionId,
            cwd: sessionsFor.find((h) => h.id === f.sessionId)?.cwd ?? '',
            mode: f.mode,
          }
    const trigger =
      f.triggerKind === 'at'
        ? { kind: 'at' as const, at: new Date(f.at).toISOString() }
        : { kind: 'reset' as const, window: f.window as Job['trigger']['window'], offset_sec: Math.round(f.offsetMin * 60) }
    const job: Partial<Job> = {
      name: f.prompt.trim().slice(0, 48),
      rearm: f.rearm,
      target,
      prompt: f.prompt.trim(),
      trigger,
    }
    app.createSchedule(f.machineId, job)
    show = false
    f.prompt = ''
    f.cwd = ''
    f.sessionId = ''
  }

  // --- display helpers ---
  function rel(iso?: string): string {
    if (!iso) return ''
    const t = new Date(iso).getTime()
    if (isNaN(t) || t < 946684800000) return '' // < year 2000 => zero/pending
    let s = Math.round((t - Date.now()) / 1000)
    const past = s < 0
    s = Math.abs(s)
    const d = Math.floor(s / 86400)
    const h = Math.floor((s % 86400) / 3600)
    const m = Math.floor((s % 3600) / 60)
    const parts = d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`
    return past ? `${parts} ago` : `in ${parts}`
  }
  function when(job: Job): string {
    const nf = rel(job.next_fire)
    if (job.trigger.kind === 'reset' && !nf) return 'waiting for quota reading'
    return nf || '—'
  }
  function summary(job: Job): string {
    const t = job.trigger
    const trig =
      t.kind === 'at'
        ? `at ${new Date(t.at ?? '').toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}`
        : `${Math.round((t.offset_sec ?? 0) / 60)} min after ${t.window === 'seven_day' ? 'weekly' : '5-hour'} reset`
    const tgt =
      job.target.kind === 'resume'
        ? `resume ${sessionsFor.find((h) => h.id === job.target.session_id)?.title ?? 'session'}`
        : `new session in ${(job.target.cwd ?? '').split('/').slice(-1)[0]}`
    return `${trig} · ${tgt}${job.rearm ? ' · repeats' : ''}`
  }
</script>

<div class="sched">
  <div class="s-head">
    <span class="s-label">Schedules</span>
    <button class="add" onclick={() => (show ? (show = false) : openForm())}>{show ? 'Cancel' : '+ New'}</button>
  </div>

  {#if show}
    <div class="form">
      {#if app.machines.length > 1}
        <label class="row"><span>Machine</span>
          <select bind:value={f.machineId}>
            {#each app.machines as m (m.id)}<option value={m.id}>{m.label}</option>{/each}
          </select>
        </label>
      {/if}

      <div class="seg">
        <button class:on={f.targetKind === 'new'} onclick={() => (f.targetKind = 'new')}>New session</button>
        <button class:on={f.targetKind === 'resume'} onclick={() => (f.targetKind = 'resume')}>Resume</button>
      </div>

      {#if f.targetKind === 'new'}
        <label class="row"><span>Folder</span>
          <input list="sched-dirs" placeholder="/path/to/project" bind:value={f.cwd} class="mono" />
          <datalist id="sched-dirs">{#each projectsFor as p (p.cwd)}<option value={p.cwd}></option>{/each}</datalist>
        </label>
        <label class="row"><span>Model</span>
          <select bind:value={f.model}>{#each MODELS as m (m.id)}<option value={m.id}>{m.label}</option>{/each}</select>
        </label>
      {:else}
        <label class="row"><span>Session</span>
          <select bind:value={f.sessionId}>
            <option value="" disabled>pick a session…</option>
            {#each sessionsFor as h (h.id)}<option value={h.id}>{h.title}</option>{/each}
          </select>
        </label>
      {/if}

      <label class="row"><span>Mode</span>
        <select bind:value={f.mode}>{#each MODES as m (m.id)}<option value={m.id}>{m.label}</option>{/each}</select>
      </label>

      <textarea placeholder="Prompt to run…" bind:value={f.prompt} rows="2"></textarea>

      <div class="seg">
        <button class:on={f.triggerKind === 'reset'} onclick={() => (f.triggerKind = 'reset')}>After reset</button>
        <button class:on={f.triggerKind === 'at'} onclick={() => (f.triggerKind = 'at')}>At a time</button>
      </div>
      {#if f.triggerKind === 'reset'}
        <div class="row2">
          <select bind:value={f.window}>{#each WINDOWS as w (w.id)}<option value={w.id}>{w.label}</option>{/each}</select>
          <label class="off"><input type="number" min="0" bind:value={f.offsetMin} /> min after</label>
        </div>
        {#if machineOf(f.machineId)?.stats?.rate_resets?.[f.window]}
          <span class="hint">next {f.window === 'seven_day' ? 'weekly' : '5-hour'} reset {rel(new Date((machineOf(f.machineId)!.stats!.rate_resets![f.window]) * 1000).toISOString())}</span>
        {/if}
      {:else}
        <input type="datetime-local" bind:value={f.at} />
      {/if}

      <label class="check"><input type="checkbox" bind:checked={f.rearm} /> Repeat (re-arm after each run)</label>
      <button class="create" disabled={!valid} onclick={submit}>Create schedule</button>
    </div>
  {/if}

  {#each app.schedules as job (job.machineId + ':' + job.id)}
    <div class="job" class:off={!job.enabled}>
      <button class="tog" class:on={job.enabled} onclick={() => app.toggleSchedule(job)} aria-label="Toggle" title={job.enabled ? 'Enabled' : 'Paused'}></button>
      <div class="jmeta">
        <span class="jname">{job.name || 'Scheduled prompt'}</span>
        <span class="jsub">{summary(job)}</span>
        {#if job.last_status}<span class="jstatus" class:err={job.last_status.startsWith('error')}>last: {job.last_status}</span>{/if}
      </div>
      <span class="jwhen">{job.enabled ? when(job) : 'paused'}</span>
      <button class="del" onclick={() => app.removeSchedule(job)} aria-label="Delete">✕</button>
    </div>
  {/each}
  {#if app.schedules.length === 0 && !show}
    <p class="empty">Run a prompt at a set time, or right after your quota resets.</p>
  {/if}
</div>

<style>
  .sched { display: flex; flex-direction: column; gap: 8px; }
  .s-head { display: flex; align-items: center; justify-content: space-between; padding: 0 2px; }
  .s-label {
    font-size: 11.5px; font-weight: 550; letter-spacing: 0.05em; text-transform: uppercase; color: var(--text-4);
  }
  .add {
    font-size: 12px; color: var(--text-2); padding: 4px 10px; border-radius: 100px;
    border: 1px solid var(--border); background: var(--panel-2);
  }
  .add:hover { color: var(--text); border-color: var(--border-2); }
  .job {
    display: flex; align-items: center; gap: 11px; padding: 11px 13px;
    background: var(--panel); border: 1px solid var(--border); border-radius: var(--r-lg);
  }
  .job.off { opacity: 0.6; }
  .tog { flex: none; width: 30px; height: 18px; border-radius: 100px; background: var(--panel-3); border: 1px solid var(--border); position: relative; }
  .tog::after { content: ''; position: absolute; top: 1px; left: 1px; width: 14px; height: 14px; border-radius: 50%; background: var(--text-4); transition: transform 0.14s; }
  .tog.on { background: var(--white); border-color: var(--white); }
  .tog.on::after { transform: translateX(12px); background: #0b0b0c; }
  .jmeta { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
  .jname { font-size: 13.5px; color: var(--text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .jsub { font-size: 11px; color: var(--text-4); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .jstatus { font-size: 10px; color: var(--text-4); }
  .jstatus.err { color: var(--alert); }
  .jwhen { flex: none; font-size: 11.5px; color: var(--text-3); white-space: nowrap; }
  .del { flex: none; width: 24px; height: 24px; border-radius: 50%; color: var(--text-4); font-size: 11px; }
  .del:hover { color: var(--text-2); background: var(--panel-3); }
  .empty { margin: 0; padding: 4px 2px; font-size: 12px; color: var(--text-4); }

  .form {
    display: flex; flex-direction: column; gap: 9px; padding: 13px;
    background: var(--panel); border: 1px solid var(--border-2); border-radius: var(--r-lg);
  }
  .row { display: flex; align-items: center; gap: 10px; }
  .row > span { flex: none; width: 62px; font-size: 12px; color: var(--text-3); }
  .row2 { display: flex; gap: 8px; align-items: center; }
  .off { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--text-3); }
  .off input { width: 56px; }
  .form input, .form select, .form textarea {
    flex: 1; min-width: 0; background: var(--panel-2); border: 1px solid var(--border);
    border-radius: var(--r-sm); padding: 8px 10px; font-size: 13px; color: var(--text); outline: none;
  }
  .form input:focus, .form select:focus, .form textarea:focus { border-color: var(--border-2); }
  .form textarea { resize: vertical; }
  .seg { display: flex; gap: 6px; }
  .seg button { flex: 1; padding: 8px; border-radius: var(--r-sm); background: var(--panel-2); border: 1px solid var(--border); color: var(--text-3); font-size: 12.5px; }
  .seg button.on { color: var(--text); background: var(--panel-3); border-color: var(--border-2); }
  .hint { font-size: 11px; color: var(--text-4); }
  .check { display: inline-flex; align-items: center; gap: 8px; font-size: 12.5px; color: var(--text-3); }
  .check input { flex: none; width: auto; }
  .create { padding: 11px; border-radius: var(--r); background: var(--white); color: #0b0b0c; font-weight: 600; font-size: 13.5px; }
  .create:disabled { opacity: 0.45; }
</style>
