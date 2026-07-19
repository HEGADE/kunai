<script lang="ts">
  import type { Turn } from '../lib/turns'
  import { formatDuration, formatTokens, formatCost } from '../lib/format'

  let { turn }: { turn: Turn } = $props()

  const duration = $derived(turn.durationMs != null ? formatDuration(turn.durationMs) : '')
  const cost = $derived(turn.costUsd ? formatCost(turn.costUsd) : '')
  // A turn re-sends the conversation on every tool call, so its total is
  // dominated by re-reads. Split them out: "new" is what the model read fresh
  // and pays full price for, "cached" is the same context read back cheaply.
  const fresh = $derived(turn.newTokens ? formatTokens(turn.newTokens) : '')
  const cached = $derived(turn.cachedTokens ? formatTokens(turn.cachedTokens) : '')
  const meta = $derived(
    [duration, fresh && `${fresh} new`, cached && `${cached} cached`, cost].filter(Boolean).join(' · '),
  )
  const hasSplit = $derived(!!(turn.newTokens || turn.cachedTokens || turn.outputTokens))
  let explain = $state(false)
</script>

{#if meta}
  <div class="footer">
    {#if meta}<span class="dur mono">{meta}</span>{/if}
    {#if hasSplit}
      <span class="info">
        <button class="ibtn" onclick={() => (explain = !explain)} aria-label="What these numbers mean" title="What these numbers mean">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9" /><path d="M12 11v5" /><path d="M12 7.6v.1" /></svg>
        </button>
        {#if explain}
          <button class="scrim" onclick={() => (explain = false)} aria-label="Close"></button>
          <div class="pop">
            <div class="prow"><span>New</span><span class="mono">{formatTokens(turn.newTokens ?? 0)}</span></div>
            <div class="prow"><span>Cached</span><span class="mono">{formatTokens(turn.cachedTokens ?? 0)}</span></div>
            <div class="prow"><span>Output</span><span class="mono">{formatTokens(turn.outputTokens ?? 0)}</span></div>
            <p class="note">
              Claude re-sends the whole conversation on every tool call, so a long
              turn reads the same context many times over. Those re-reads are
              cached and cost a fraction of new input, which is why the cached
              number runs far ahead of the price.
            </p>
          </div>
        {/if}
      </span>
    {/if}
  </div>
{/if}

<style>
  .footer {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 7px;
    padding-top: 2px;
  }
  .dur {
    flex: none;
    font-size: 11.5px;
    color: var(--text-3);
    padding-right: 2px;
  }
  .info {
    position: relative;
    display: inline-flex;
    margin-left: -3px;
  }
  .ibtn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: 50%;
    color: var(--text-4);
  }
  .ibtn:hover {
    color: var(--text-2);
  }
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 30;
  }
  .pop {
    position: absolute;
    z-index: 31;
    bottom: calc(100% + 7px);
    left: -8px;
    width: 262px;
    padding: 11px 12px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
    text-align: left;
  }
  .prow {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    font-size: 12.5px;
    color: var(--text-3);
    padding-bottom: 5px;
  }
  .prow .mono {
    color: var(--text-2);
  }
  .note {
    margin: 6px 0 0;
    padding-top: 9px;
    border-top: 1px solid var(--border);
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-4);
  }
</style>
