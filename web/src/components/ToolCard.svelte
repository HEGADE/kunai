<script lang="ts">
  import { describe } from '../lib/toolMeta'
  import type { ToolResult } from '../lib/types'
  import ToolIcon from './tools/ToolIcon.svelte'
  import ToolBody from './tools/ToolBody.svelte'
  import ResultView from './tools/ResultView.svelte'
  import FileChip from './tools/FileChip.svelte'

  let { name, input, result }: { name: string; input: unknown; result?: ToolResult } = $props()
  let open = $state(false)
  const label = $derived(describe(name, input, result))
</script>

<div class="tool" class:open>
  <button class="head" onclick={() => (open = !open)}>
    <span class="ic"><ToolIcon {name} size={13} /></span>
    <span class="name">{label.action}</span>
    {#if label.file}<FileChip path={label.path} name={label.file} />{/if}
    {#if label.text}<span class="sum" class:mono={label.mono}>{label.text}</span>{/if}
    <span class="sp"></span>
    {#if label.added}<span class="stat add">+{label.added}</span>{/if}
    {#if label.removed}<span class="stat del">−{label.removed}</span>{/if}
    {#if result?.isError}<span class="errdot" title="Tool reported an error"></span>{/if}
    <span class="car" aria-hidden="true">
      <svg width="9" height="9" viewBox="0 0 8 8" fill="currentColor"><path d="M2 0l4 4-4 4z" /></svg>
    </span>
  </button>
  {#if open}
    <div class="body">
      <ToolBody {name} {input} />
      {#if result}<ResultView {result} />{/if}
    </div>
  {/if}
</div>

<style>
  .tool {
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg-raised);
    overflow: hidden;
  }
  .head {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 10px;
    text-align: left;
    font-size: 13px;
    color: var(--text-2);
  }
  .head:hover {
    background: var(--panel);
  }
  .ic {
    flex: none;
    display: inline-flex;
    color: var(--text-4);
  }
  .name {
    flex: none;
    font-weight: 550;
    color: var(--text);
  }
  /* Primary label (filename or command). Truncates only when it actually
     overflows, and the spacer + gap keep it clear of the stats and caret. */
  .sum {
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    color: var(--text-2);
    font-size: 12.5px;
  }
  .sum.mono {
    font-family: var(--mono);
    font-size: 12px;
    letter-spacing: 0;
  }
  .sp {
    flex: 1;
    min-width: 6px;
  }
  .stat {
    flex: none;
    font-family: var(--mono);
    font-size: 11.5px;
    font-weight: 500;
    letter-spacing: 0;
  }
  .stat.add {
    color: var(--live);
  }
  .stat.del {
    color: var(--alert);
  }
  .car {
    flex: none;
    display: inline-flex;
    color: var(--text-4);
    transition: transform 0.15s;
  }
  .open .car {
    transform: rotate(90deg);
    color: var(--text-3);
  }
  .errdot {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--alert);
  }
  .body {
    padding: 10px;
    border-top: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
</style>
