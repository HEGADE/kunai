<script lang="ts">
  // In-chat scheduling for the CURRENT session: type a prompt and arm it to run
  // after the quota resets (or at a time), resuming this session. Opened from the
  // rate-limit banner and the chat menu. Reuses app.createSchedule.
  import { app } from '../lib/app.svelte'
  import type { Job } from '../lib/types'

  let {
    machineId,
    sessionId,
    cwd,
    window: win = 'five_hour',
    resetsAt = 0,
    onClose,
  }: {
    machineId: string
    sessionId: string
    cwd: string
    window?: string
    resetsAt?: number
    onClose: () => void
  } = $props()

  let prompt = $state('')
  let kind = $state<'reset' | 'at'>('reset')
  let offsetMin = $state(1)
  let at = $state('')
  let rearm = $state(false)
  let busy = $state(false)

  const valid = $derived(!!prompt.trim() && (kind === 'reset' ? offsetMin >= 0 : !!at))
  const winLabel = win === 'seven_day' ? 'weekly' : '5-hour'

  function rel(unixSec: number): string {
    if (!unixSec) return ''
    let s = Math.round(unixSec - Date.now() / 1000)
    const past = s < 0
    s = Math.abs(s)
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    const p = h ? `${h}h ${m}m` : `${m}m`
    return past ? `${p} ago` : `in ${p}`
  }

  async function submit() {
    if (!valid || busy) return
    busy = true
    const trigger =
      kind === 'reset'
        ? { kind: 'reset' as const, window: win as Job['trigger']['window'], offset_sec: Math.round(offsetMin * 60) }
        : { kind: 'at' as const, at: new Date(at).toISOString() }
    try {
      await app.createSchedule(machineId, {
        name: prompt.trim().slice(0, 48),
        rearm,
        target: { kind: 'resume', session_id: sessionId, cwd, mode: 'acceptEdits' },
        prompt: prompt.trim(),
        trigger,
      })
      onClose()
    } finally {
      busy = false
    }
  }
</script>

<div class="sa">
  <div class="sa-top">
    <span class="title">Schedule this session</span>
    <button class="x" onclick={onClose} aria-label="Close">✕</button>
  </div>
  <p class="lede">Arm a prompt to run later. When it fires, this session resumes and picks it up, even if you've closed the tab.</p>

  <textarea bind:value={prompt} rows="2" placeholder="Prompt to run when it fires…"></textarea>

  <div class="seg">
    <button class:on={kind === 'reset'} onclick={() => (kind = 'reset')}>After reset</button>
    <button class:on={kind === 'at'} onclick={() => (kind = 'at')}>At a time</button>
  </div>
  {#if kind === 'reset'}
    <div class="row">
      <label class="off"><input type="number" class="mono" min="0" bind:value={offsetMin} /> min after {winLabel} reset</label>
      {#if resetsAt}<span class="hint">resets {rel(resetsAt)}</span>{/if}
    </div>
  {:else}
    <input type="datetime-local" bind:value={at} />
  {/if}

  <label class="check"><input type="checkbox" bind:checked={rearm} /> Repeat after each reset</label>
  <button class="go" disabled={!valid || busy} onclick={submit}>{busy ? 'Scheduling…' : 'Schedule'}</button>
</div>

<style>
  .sa {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 15px 17px 14px;
  }
  .sa-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .title {
    font-size: 14px;
    font-weight: 600;
    color: var(--text);
  }
  .x {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    margin: -4px -6px -4px 0;
    border-radius: 50%;
    color: var(--text-4);
    font-size: 12px;
  }
  .x:hover {
    color: var(--text);
    background: var(--panel-3);
  }
  .lede {
    margin: -6px 0 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--text-4);
  }
  textarea,
  input,
  .off input {
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    padding: 9px 11px;
    font-size: 13px;
    color: var(--text);
    outline: none;
  }
  textarea {
    resize: vertical;
  }
  textarea::placeholder {
    color: var(--text-4);
  }
  textarea:focus,
  input:focus {
    border-color: var(--border-2);
  }
  /* Spinners add chrome and invite fiddling; the number is the point. */
  input[type='number']::-webkit-outer-spin-button,
  input[type='number']::-webkit-inner-spin-button {
    appearance: none;
    margin: 0;
  }
  input[type='number'] {
    -moz-appearance: textfield;
    appearance: textfield;
  }
  .seg {
    display: flex;
    gap: 6px;
  }
  .seg button {
    flex: 1;
    padding: 9px;
    border-radius: var(--r-sm);
    background: var(--panel-2);
    border: 1px solid var(--border);
    color: var(--text-3);
    font-size: 12.5px;
  }
  .seg button:hover {
    color: var(--text-2);
  }
  .seg button.on {
    color: var(--text);
    background: var(--panel-3);
    border-color: var(--border-2);
  }
  .row {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .off {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    font-size: 12.5px;
    color: var(--text-3);
  }
  .off input {
    width: 52px;
    text-align: center;
    padding: 8px 6px;
  }
  .hint {
    font-size: 11px;
    color: var(--text-4);
  }
  .check {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    margin-top: 1px;
    font-size: 12.5px;
    color: var(--text-3);
  }
  .go {
    height: 40px;
    margin-top: 2px;
    border-radius: var(--r);
    background: var(--white);
    color: #0b0b0c;
    font-weight: 600;
    font-size: 13.5px;
  }
  .go:disabled {
    background: var(--panel-2);
    color: var(--text-4);
  }
</style>
