<script lang="ts">
  import type { ToolResult } from '../../lib/types'
  let { result, maxLines = 18 }: { result: ToolResult; maxLines?: number } = $props()

  let expanded = $state(false)
  const text = $derived(result.content.replace(/\n$/, ''))
  const lines = $derived(text ? text.split('\n') : [])
  const clamped = $derived(!expanded && lines.length > maxLines)
  const shown = $derived(clamped ? lines.slice(0, maxLines).join('\n') : text)
</script>

<div class="rv" class:err={result.isError}>
  <div class="bar">
    <span class="dot"></span>
    <span class="label">{result.isError ? 'Error' : 'Output'}</span>
    {#if lines.length}<span class="meta mono">{lines.length} lines</span>{/if}
  </div>
  {#if text}
    <pre class="out mono">{shown}</pre>
    {#if clamped}
      <button class="more" onclick={() => (expanded = true)}>Show {lines.length - maxLines} more lines</button>
    {:else if expanded && lines.length > maxLines}
      <button class="more" onclick={() => (expanded = false)}>Collapse</button>
    {/if}
    {#if result.truncated}<div class="trunc">output truncated</div>{/if}
  {:else}
    <div class="empty mono">(no output)</div>
  {/if}
</div>

<style>
  .rv {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    overflow: hidden;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 11px;
    border-bottom: 1px solid var(--border);
  }
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .rv.err .dot {
    background: var(--alert);
  }
  .label {
    font-size: 11px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .rv.err .label {
    color: var(--alert);
  }
  .meta {
    margin-left: auto;
    font-size: 10.5px;
    color: var(--text-4);
  }
  .out {
    margin: 0;
    padding: 10px 12px;
    overflow-x: auto;
    font-size: 12.5px;
    line-height: 1.5;
    color: var(--text-2);
    white-space: pre;
  }
  .empty {
    padding: 9px 12px;
    font-size: 12px;
    color: var(--text-4);
  }
  .more {
    width: 100%;
    padding: 6px;
    border-top: 1px solid var(--border);
    font-size: 11.5px;
    color: var(--text-3);
  }
  .more:hover {
    color: var(--text);
    background: var(--panel);
  }
  .trunc {
    padding: 5px 12px;
    font-size: 10.5px;
    color: var(--text-4);
    border-top: 1px solid var(--border);
  }
</style>
