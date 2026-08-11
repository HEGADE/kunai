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

  // The activity is bounded and scrolls itself, so a turn that makes forty calls
  // does not grow the page by forty rows: the conversation under it stays where
  // the reader left it. Pinned to the newest call, which is the one worth seeing,
  // and only while the reader is already at the bottom -- scrolling up to read an
  // earlier command must not be undone by the next one arriving.
  let body = $state<HTMLElement | null>(null)
  $effect(() => {
    void calls.length
    const el = body
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    if (atBottom) el.scrollTop = el.scrollHeight
  })
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
      <!-- The tool calls ONLY, never the prose.
           This rendered every block, which did two things wrong at once. The
           reply was already rendered below by Chat.svelte, so opening this
           printed the whole answer a second time; and interleaving the two meant
           a paragraph, a command, a paragraph, a command, each arrival shoving
           the rest of the conversation down the page. Keeping the calls here and
           the prose below means the answer reads as one continuous column with
           the activity beside it, rather than as a transcript of two things
           taking turns. -->
      <div class="body" bind:this={body}>
        {#each calls as b, j (b.id ?? j)}
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
  /* Capped rather than unbounded. A running turn's activity is something you
     glance at, not the document: letting it grow is what pushed the work off the
     screen and made the reply chase the bottom of a column. In px rather than vh
     so a phone's address bar hiding cannot resize it mid-turn. */
  .body {
    padding-left: 4px;
    max-height: 260px;
    overflow: auto;
    overscroll-behavior: contain;
  }
</style>
