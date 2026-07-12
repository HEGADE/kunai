<script lang="ts">
  import type { Turn } from '../lib/turns'
  import { formatDuration, formatTokens, formatCost } from '../lib/format'
  import FileChip from './tools/FileChip.svelte'

  let { turn }: { turn: Turn } = $props()

  const MAX = 4
  const shown = $derived(turn.files.slice(0, MAX))
  const rest = $derived(turn.files.slice(MAX))
  const restAdded = $derived(rest.reduce((n, f) => n + f.added, 0))
  const restRemoved = $derived(rest.reduce((n, f) => n + f.removed, 0))
  const duration = $derived(turn.durationMs != null ? formatDuration(turn.durationMs) : '')
  const tokens = $derived(turn.tokens ? formatTokens(turn.tokens) : '')
  const cost = $derived(turn.costUsd ? formatCost(turn.costUsd) : '')
  const meta = $derived([duration, tokens && `${tokens} tokens`, cost].filter(Boolean).join(' · '))
</script>

{#if meta || turn.files.length}
  <div class="footer">
    {#if meta}<span class="dur mono">{meta}</span>{/if}
    {#each shown as f (f.path)}
      <FileChip path={f.path} name={f.name} added={f.added} removed={f.removed} />
    {/each}
    {#if rest.length}
      <span class="more">
        +{rest.length} more
        {#if restAdded}<span class="stat add">+{restAdded}</span>{/if}
        {#if restRemoved}<span class="stat del">−{restRemoved}</span>{/if}
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
  .more {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 1px 8px;
    border: 1px solid var(--border-2);
    border-radius: 6px;
    background: var(--panel);
    font-size: 12px;
    color: var(--text-3);
  }
  .stat {
    font-family: var(--mono);
    font-size: 11px;
    font-weight: 500;
  }
  .stat.add {
    color: var(--live);
  }
  .stat.del {
    color: var(--alert);
  }
</style>
