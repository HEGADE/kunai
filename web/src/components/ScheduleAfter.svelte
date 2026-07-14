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
    <span class="k">Schedule this session</span>
    <button class="x" onclick={onClose} aria-label="Close">✕</button>
  </div>
  <textarea bind:value={prompt} rows="2" placeholder="Prompt to run when it fires…"></textarea>

  <div class="seg">
    <button class:on={kind === 'reset'} onclick={() => (kind = 'reset')}>After reset</button>
    <button class:on={kind === 'at'} onclick={() => (kind = 'at')}>At a time</button>
  </div>
  {#if kind === 'reset'}
    <div class="row">
      <label class="off"><input type="number" min="0" bind:value={offsetMin} /> min after {winLabel} reset</label>
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
    max-width: 720px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    gap: 9px;
    padding: 13px 20px calc(var(--safe-bottom) + 12px);
  }
  .sa-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .k {
    font-size: 11.5px;
    font-weight: 550;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .x {
    width: 24px;
    height: 24px;
    border-radius: 50%;
    color: var(--text-4);
    font-size: 11px;
  }
  .x:hover {
    color: var(--text-2);
    background: var(--panel-3);
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
  textarea:focus,
  input:focus {
    border-color: var(--border-2);
  }
  .seg {
    display: flex;
    gap: 6px;
  }
  .seg button {
    flex: 1;
    padding: 8px;
    border-radius: var(--r-sm);
    background: var(--panel-2);
    border: 1px solid var(--border);
    color: var(--text-3);
    font-size: 12.5px;
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
    gap: 7px;
    font-size: 12.5px;
    color: var(--text-3);
  }
  .off input {
    width: 54px;
  }
  .hint {
    font-size: 11px;
    color: var(--text-4);
  }
  .check {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    font-size: 12.5px;
    color: var(--text-3);
  }
  .go {
    padding: 11px;
    border-radius: var(--r);
    background: var(--white);
    color: #0b0b0c;
    font-weight: 600;
    font-size: 14px;
  }
  .go:disabled {
    opacity: 0.45;
  }
</style>
