<script lang="ts">
  import { contextUsage } from '../lib/context'

  // tokens/model come straight from the chat store, so the meter updates live on
  // every result event — no polling or manual refresh. tokens is 0 until the
  // first turn of a session reports usage.
  let { tokens, model }: { tokens: number; model: string } = $props()

  let open = $state(false)
  const u = $derived(contextUsage(tokens, model))
  const empty = $derived(tokens <= 0)

  // Trigger ring geometry: a track circle plus a used-arc drawn from the top.
  const R = 7
  const CIRC = 2 * Math.PI * R
</script>

<div class="ctx">
  <button class="trigger" class:on={open} onclick={() => (open = !open)} aria-label="Context usage" title="Context usage">
    <svg width="20" height="20" viewBox="0 0 20 20" aria-hidden="true">
      <circle cx="10" cy="10" r={R} fill="none" stroke="var(--border-2)" stroke-width="2" />
      {#if !empty}
        <circle
          cx="10"
          cy="10"
          r={R}
          fill="none"
          stroke="var(--text-2)"
          stroke-width="2"
          stroke-linecap="round"
          stroke-dasharray={CIRC}
          stroke-dashoffset={CIRC * (1 - u.usedFrac)}
          transform="rotate(-90 10 10)"
        />
      {/if}
    </svg>
  </button>

  {#if open}
    <button class="scrim" onclick={() => (open = false)} aria-label="Close"></button>
    <div class="pop">
      <div class="top">
        <span class="title">Context</span>
        <span class="amt mono">{empty ? '—' : u.label}</span>
      </div>
      {#if empty}
        <p class="empty">Send a message to load context usage.</p>
      {:else}
        <div class="track"><span class="fill" style="width:{Math.max(1.5, u.usedPct)}%"></span></div>
        <div class="rows">
          <div class="row"><span>Free space</span><span class="mono">{u.freePct.toFixed(1)}%</span></div>
          <div class="row"><span>In use</span><span class="mono">{u.usedPct.toFixed(1)}%</span></div>
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .ctx {
    position: relative;
    display: inline-flex;
  }
  .trigger {
    width: 32px;
    height: 32px;
    border-radius: var(--r-sm);
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--text-3);
  }
  .trigger:hover,
  .trigger.on {
    color: var(--text);
    background: var(--panel-2);
  }
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 30;
  }
  .pop {
    position: absolute;
    z-index: 31;
    bottom: calc(100% + 8px);
    right: 0;
    width: 260px;
    padding: 12px 13px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
  }
  .top {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
  }
  .title {
    font-size: 13.5px;
    font-weight: 600;
    color: var(--text);
  }
  .amt {
    font-size: 12px;
    color: var(--text-2);
  }
  .empty {
    margin: 9px 0 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--text-4);
  }
  .track {
    margin: 11px 0 12px;
    height: 6px;
    border-radius: 100px;
    background: var(--panel-3);
    overflow: hidden;
  }
  .fill {
    display: block;
    height: 100%;
    border-radius: 100px;
    background: var(--text-2);
  }
  .rows {
    display: flex;
    flex-direction: column;
    gap: 7px;
  }
  .row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    font-size: 12.5px;
    color: var(--text-3);
  }
  .row .mono {
    color: var(--text-2);
  }
</style>
