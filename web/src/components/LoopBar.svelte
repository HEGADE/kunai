<script lang="ts">
  import type { ChatConnection } from '../lib/chat.svelte'
  import { loopProgress, usd } from '../lib/loop'

  // The loop while it runs, sitting where the queue sits: just above the composer,
  // because it is the same kind of fact (work that is going to happen whether or
  // not you are here).
  //
  // One meter, not two. A loop ends at whichever limit arrives first, so the only
  // honest reading of "how close is this to over" is the nearer of the two. The
  // line underneath names which one, and roughly when, which is the whole reason
  // to trust it enough to go to sleep.
  let { chat }: { chat: ChatConnection } = $props()

  const p = $derived(chat.loop ? loopProgress(chat.loop) : null)
</script>

{#if chat.loop && chat.loop.state === 'running' && p}
  <div class="bar">
    <div class="top">
      <span class="dot" aria-hidden="true"></span>
      <span class="lbl">Loop</span>
      <span class="nums mono">
        <span class="n" class:binds={p.binding === 'iterations'}>#{chat.loop.iteration}/{chat.loop.max_iters}</span>
        <span class="sep">·</span>
        <span class="n" class:binds={p.binding === 'spend'}>{usd(chat.loop.spent_usd)}/{usd(chat.loop.max_usd)}</span>
      </span>
      <button class="stop" onclick={() => chat.stopLoop()}>Stop</button>
    </div>
    <div class="track"><span class="fill" style="width:{Math.max(1.5, p.frac * 100)}%"></span></div>
    {#if p.note}
      <span class="note mono">{p.note}</span>
    {/if}
  </div>
{/if}

<style>
  .bar {
    max-width: 720px;
    margin: 0 auto 6px;
    width: 100%;
    display: flex;
    flex-direction: column;
    gap: 7px;
    padding: 9px 11px 10px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
  }
  .top {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  /* Amber is "working", the same as a busy tab. It does not pulse: pulsing is
     reserved for a session that actually wants you, and a loop wants nothing. */
  .dot {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--busy);
  }
  .lbl {
    flex: none;
    font-size: 12.5px;
    font-weight: 500;
    color: var(--text-2);
  }
  .nums {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: baseline;
    gap: 6px;
    font-size: 11.5px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
  }
  /* The limit that is going to end this reads at full strength; the other recedes.
     Which number matters changes as it runs, so the emphasis follows it. */
  .n.binds {
    color: var(--text-2);
  }
  .sep {
    color: var(--text-4);
  }
  .stop {
    flex: none;
    height: 24px;
    padding: 0 11px;
    border-radius: 6px;
    border: 1px solid var(--border-2);
    color: var(--text-2);
    font-size: 11.5px;
    font-weight: 500;
  }
  .stop:hover {
    color: var(--text);
    background: var(--panel-3);
  }
  .track {
    height: 4px;
    border-radius: 100px;
    background: var(--panel-3);
    overflow: hidden;
  }
  .fill {
    display: block;
    height: 100%;
    border-radius: 100px;
    background: var(--text-3);
    transition: width 0.4s ease;
  }
  @media (prefers-reduced-motion: reduce) {
    .fill {
      transition: none;
    }
  }
  .note {
    font-size: 10.5px;
    color: var(--text-4);
  }
</style>
