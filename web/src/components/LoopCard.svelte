<script lang="ts">
  import type { LoopStatus } from '../lib/types'
  import { loopEnding, usd } from '../lib/loop'

  // The loop's three beats in the log. It is one component because they are one
  // story, and which beat this is falls out of the snapshot itself: iteration 0
  // is the start, a running loop past that is another lap, and anything not
  // running is the ending.
  //
  // The laps are hairlines rather than cards. Over fifty iterations a card each
  // would drown the work they exist to mark.
  let { loop }: { loop: LoopStatus } = $props()

  const started = $derived(loop.state === 'running' && loop.iteration === 0)
  // A seam replayed from a transcript is the same mark as a live lap: on resume
  // the work is still there, and without it the log shows turns nobody asked for.
  const lap = $derived(loop.state === 'seam' || (loop.state === 'running' && loop.iteration > 0))
</script>

{#if lap}
  <div class="rule" role="separator">
    <span class="label mono">#{loop.iteration}</span>
  </div>
{:else}
  <div class="card" class:ended={!started}>
    <div class="top">
      <span class="eyebrow">{started ? 'Loop started' : loopEnding(loop)}</span>
      {#if !started}
        <span class="ran mono">{loop.iteration} of {loop.max_iters} · {usd(loop.spent_usd)}</span>
      {/if}
    </div>

    {#if started}
      <span class="task">{loop.prompt}</span>
      <span class="facts mono">
        stops after {loop.max_iters} iterations, {usd(loop.max_usd)}{loop.promise
          ? `, or "${loop.promise}"`
          : ''}
      </span>
      <span class="note">
        Claude re-reads the task each time a turn ends, and accepts its own file edits until the
        loop stops. It keeps running if you close the tab.
      </span>
    {:else if loop.reason}
      <span class="note">{loop.reason}.</span>
    {/if}
  </div>
{/if}

<style>
  /* Same seam as a compaction boundary: the conversation passes through it. */
  .rule {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 2px 0;
  }
  .rule::before,
  .rule::after {
    content: '';
    flex: 1;
    height: 1px;
    background: var(--border);
  }
  .label {
    font-size: 10.5px;
    letter-spacing: 0.04em;
    color: var(--text-4);
    white-space: nowrap;
  }

  /* Context, not conversation: a card, like a project joining the session. */
  .card {
    display: flex;
    flex-direction: column;
    gap: 7px;
    padding: 12px 14px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-left: 2px solid var(--busy);
    border-radius: var(--r-sm);
  }
  /* A finished loop stops being live, and the left edge stops being amber. */
  .card.ended {
    border-left-color: var(--border-2);
  }
  .top {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
  }
  .eyebrow {
    font-size: 9.5px;
    letter-spacing: 0.11em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .ran {
    font-size: 11px;
    color: var(--text-4);
  }
  .task {
    font-size: 13px;
    line-height: 1.5;
    color: var(--text);
    white-space: pre-wrap;
  }
  .facts {
    font-size: 11px;
    color: var(--text-3);
  }
  .note {
    font-size: 11.5px;
    line-height: 1.45;
    color: var(--text-3);
  }
</style>
