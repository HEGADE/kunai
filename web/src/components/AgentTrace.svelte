<script lang="ts">
  // What a subagent actually did, threaded under its Agent tool card. The CLI
  // streams a subagent's whole inner life tagged with the Agent call that spawned
  // it, so this is real activity, not a reconstruction: before it was read, that
  // work was rendered as the MAIN agent's own tool calls and text.
  //
  // Deliberately a compact trace, not a full nested chat: a subagent can run a
  // dozen tools, and at that size each one needs to read as a line you can scan.
  // Tool inputs stay collapsed behind the line; the agent's own words render as
  // quiet prose, because they are the answer it hands back.
  import ToolIcon from './tools/ToolIcon.svelte'
  import { describe } from '../lib/toolMeta'
  import Markdown from './Markdown.svelte'
  import type { Block, ToolResult } from '../lib/types'

  let {
    blocks,
    streaming = '',
    results = {},
  }: { blocks: Block[]; streaming?: string; results?: Record<string, ToolResult> } = $props()

  const isText = (b: Block) => b.type === 'text' && !!b.text?.trim()
</script>

{#if blocks.length || streaming}
  <div class="trace">
    {#each blocks as b, i (b.id ?? i)}
      {#if b.type === 'tool_use'}
        {@const label = describe(b.name ?? '', b.input)}
        {@const res = b.id ? results[b.id] : undefined}
        <div class="row">
          <span class="ic"><ToolIcon name={b.name ?? ''} size={11} /></span>
          <span class="act">{label.action}</span>
          {#if label.file}<span class="f mono">{label.file}</span>{/if}
          {#if label.text}<span class="t" class:mono={label.mono}>{label.text}</span>{/if}
          <span class="sp"></span>
          {#if res?.isError}
            <span class="err" title="This step reported an error">failed</span>
          {:else if !res}
            <span class="spin" aria-label="Running"></span>
          {/if}
        </div>
      {:else if isText(b)}
        <div class="says"><Markdown text={b.text!} /></div>
      {/if}
    {/each}
    {#if streaming}
      <p class="says live">{streaming}</p>
    {/if}
  </div>
{/if}

<style>
  /* A hairline rail on the left says "this happened inside the agent above"
     without drawing a box around it (the tool card is already the box). */
  .trace {
    margin: 6px 0 2px;
    padding-left: 10px;
    border-left: 1px solid var(--border-2);
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    font-size: 11.5px;
    color: var(--text-3);
  }
  .ic {
    flex: none;
    display: flex;
    color: var(--text-4);
  }
  .act {
    flex: none;
    color: var(--text-2);
    font-weight: 500;
  }
  .f,
  .t {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .mono {
    font-family: var(--mono);
    font-size: 11px;
  }
  .sp {
    flex: 1;
  }
  .err {
    flex: none;
    font-size: 10.5px;
    color: var(--red, #d66);
  }
  .spin {
    flex: none;
    width: 9px;
    height: 9px;
    border: 1.5px solid var(--border-2);
    border-top-color: var(--text-3);
    border-radius: 50%;
    animation: sp 0.7s linear infinite;
  }
  @keyframes sp {
    to {
      transform: rotate(360deg);
    }
  }
  /* The agent's own words: the answer it reports back, quieter than the main
     conversation because it is a step inside one turn, not a reply to you. */
  .says {
    margin: 2px 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--text-3);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .says.live {
    color: var(--text-4);
  }
</style>
