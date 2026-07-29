<script lang="ts">
  import type { Block } from '../lib/types'
  import type { ChatConnection } from '../lib/chat.svelte'
  import { describe } from '../lib/toolMeta'
  import ToolIcon from './tools/ToolIcon.svelte'
  import BlockView from './BlockView.svelte'

  // What the agent is doing, while it is doing it.
  //
  // A finished turn already collapses its tool calls behind one summary line, but
  // a RUNNING turn rendered every block inline, which is the one case where that
  // matters most: watching an agent read forty files meant forty cards pushing
  // the work off the screen, and the thing you actually wanted to see (what it is
  // doing NOW) was at the bottom of a column you had to chase.
  //
  // So while it runs this is one line: the current call, named, with a count of
  // what came before. Open it and you get the full stream, which is what the
  // inline rendering was for. It closes itself when the turn ends and the
  // ordinary collapsed group takes over.
  let {
    blocks,
    chat,
    open = $bindable(false),
  }: { blocks: Block[]; chat: ChatConnection; open?: boolean } = $props()

  // Tool calls in order, so "the current one" is simply the last.
  const calls = $derived(blocks.filter((b) => b.type === 'tool_use'))
  const current = $derived(calls.length ? calls[calls.length - 1] : null)
  // Whether that last call has come back yet. An answered call means the agent is
  // thinking rather than working, and saying "Reading x" then would be a lie.
  const answered = $derived(!!current?.id && !!chat.toolResults[current.id])
  const label = $derived(current ? describe(current.name ?? '', current.input) : null)
  const done = $derived(answered ? calls.length : calls.length - 1)
</script>

{#if calls.length}
  <div class="la" class:open>
    <button class="head" onclick={() => (open = !open)}>
      <span class="car" aria-hidden="true">
        <svg width="9" height="9" viewBox="0 0 8 8" fill="currentColor"><path d="M2 0l4 4-4 4z" /></svg>
      </span>
      {#if current && !answered}
        <span class="ic"><ToolIcon name={current.name ?? ''} size={13} /></span>
        <span class="now">{label?.action ?? current.name}</span>
        {#if label?.file}<span class="file mono">{label.file}</span>{/if}
      {:else}
        <span class="now quiet">Thinking</span>
      {/if}
      {#if done > 0}
        <!-- What it already did, as a number rather than as a column. -->
        <span class="count mono">{done} done</span>
      {/if}
    </button>
    {#if open}
      <div class="body">
        {#each blocks as b, j (j)}
          <BlockView block={b} {chat} />
        {/each}
      </div>
    {/if}
  </div>
{/if}

<style>
  .la {
    margin: 2px 0 6px;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 5px 8px 5px 4px;
    border-radius: var(--r-sm);
    color: var(--text-3);
    font-size: 12.5px;
    text-align: left;
    min-width: 0;
  }
  .head:hover {
    background: var(--panel);
    color: var(--text-2);
  }
  .car {
    flex: none;
    display: flex;
    color: var(--text-4);
    transform: rotate(0deg);
    transition: transform var(--t) var(--ease);
  }
  .la.open .car {
    transform: rotate(90deg);
  }
  .ic {
    flex: none;
    display: flex;
    color: var(--text-4);
  }
  /* The verb, and the only thing here at full weight: it is the answer to "what
     is it doing". */
  .now {
    flex: none;
    color: var(--text-2);
    font-weight: 500;
  }
  .now.quiet {
    color: var(--text-3);
    font-weight: 400;
  }
  .file {
    flex: 1;
    min-width: 0;
    font-size: 11.5px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    /* A path clipped from the left keeps its leading slash where it belongs. */
    unicode-bidi: plaintext;
  }
  .count {
    flex: none;
    margin-left: auto;
    font-size: 11px;
    font-variant-numeric: tabular-nums;
    color: var(--text-4);
  }
  .body {
    padding-left: 4px;
  }
</style>
