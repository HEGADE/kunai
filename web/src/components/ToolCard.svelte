<script lang="ts">
  import { summaryOf } from '../lib/toolMeta'
  import type { ToolResult } from '../lib/types'
  import ToolIcon from './tools/ToolIcon.svelte'
  import ToolBody from './tools/ToolBody.svelte'
  import ResultView from './tools/ResultView.svelte'

  let { name, input, result }: { name: string; input: unknown; result?: ToolResult } = $props()
  let open = $state(false)
  const summary = $derived(summaryOf(name, input))
</script>

<div class="tool" class:open>
  <button class="head" onclick={() => (open = !open)}>
    <span class="ic"><ToolIcon {name} size={13} /></span>
    <span class="name">{name}</span>
    {#if summary}<span class="sum mono">{summary}</span>{/if}
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
    border-radius: var(--r-sm);
  }
  .head {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 7px 8px;
    text-align: left;
    border-radius: var(--r-sm);
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
  .sum {
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
    min-width: 0;
    font-size: 12px;
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
    margin: 2px 0 4px;
    padding-left: 22px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
</style>
