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
  // A tool has no result until it reports back. For most tools that window is a
  // blink; for an Agent it can be the whole time it works in the background, so
  // the card stays "running" until its result arrives (a later frame, correlated
  // by tool_use_id) — which is exactly how a background agent finishes late.
  const running = $derived(!result)
  const isAgent = $derived(name === 'Agent' || name === 'Task')
</script>

<div class="tool" class:open class:running>
  <button class="head" onclick={() => (open = !open)}>
    <span class="ic"><ToolIcon {name} size={13} /></span>
    <span class="name">{label.action}</span>
    {#if label.agent}<span class="agent" class:hot={running}>{label.agent}</span>{/if}
    {#if label.file}<FileChip path={label.path} name={label.file} />{/if}
    {#if label.text}<span class="sum" class:mono={label.mono}>{label.text}</span>{/if}
    <span class="sp"></span>
    {#if label.added}<span class="stat add">+{label.added}</span>{/if}
    {#if label.removed}<span class="stat del">−{label.removed}</span>{/if}
    {#if result?.isError}
      <span class="errdot" title="Tool reported an error"></span>
    {:else if running}
      <span class="spin" aria-label="Running" title="Running"></span>
    {/if}
    <span class="car" aria-hidden="true">
      <svg width="9" height="9" viewBox="0 0 8 8" fill="currentColor"><path d="M2 0l4 4-4 4z" /></svg>
    </span>
  </button>
  {#if open}
    <div class="body">
      <ToolBody {name} {input} />
      {#if result}
        <ResultView {result} />
      {:else if running}
        <div class="pending">
          <span class="spin"></span>
          <span class="ptext">{isAgent ? 'Working in the background — this can finish after the turn ends.' : 'Running…'}</span>
        </div>
      {/if}
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
  /* The subagent type, as a compact pill. It tints amber while the agent runs so
     the row reads as "this agent is still working" at a glance. */
  .agent {
    flex: none;
    padding: 1px 8px;
    border-radius: 100px;
    background: var(--panel-3);
    color: var(--text-2);
    font-size: 11px;
    font-weight: 500;
    white-space: nowrap;
  }
  .agent.hot {
    color: var(--busy);
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
  /* A quiet amber ring for "still running" — matches the app's busy language
     without a glow. Respects reduced motion by falling back to a soft pulse. */
  .spin {
    flex: none;
    width: 11px;
    height: 11px;
    border-radius: 50%;
    border: 1.5px solid var(--panel-3);
    border-top-color: var(--busy);
    animation: spin 0.7s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .spin {
      animation: soften 1.4s ease-in-out infinite;
      border-color: var(--busy);
    }
  }
  @keyframes soften {
    50% {
      opacity: 0.35;
    }
  }
  .body {
    padding: 10px;
    border-top: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .pending {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 9px 11px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
  }
  .ptext {
    font-size: 12px;
    color: var(--text-3);
  }
</style>
