<script lang="ts">
  import type { DroppedFinding } from '../../lib/api'
  import { proseHtml } from '../../lib/prose'

  // What verification refuted, kept and shown.
  //
  // This is the half of a review that is normally thrown away, and throwing it
  // away is how a reviewer becomes untrustworthy in a way nobody can see. Three
  // findings from a reviewer that considered seven is a different thing from
  // three from one that only managed three, and nothing else can tell them
  // apart. It matters most in the case that looks like nothing happened: a
  // review that found three things and refuted all three reported "Nothing worth
  // reporting", which reads as "I looked and it is fine" when what happened was
  // "I looked, I found three things, and I talked myself out of all of them".
  //
  // Collapsed, because these are not going to be posted and cannot be
  // un-dropped. What a reader wants is the count, and then the reasons only if
  // the count surprises them.
  let { dropped, open = false }: { dropped: DroppedFinding[]; open?: boolean } = $props()

  let show = $state(open)
</script>

{#if dropped.length}
  <div class="wrap">
    <button class="head" onclick={() => (show = !show)}>
      <span class="caret x-mono">{show ? '−' : '+'}</span>
      {dropped.length} considered and dropped
      <span class="sp"></span>
      <span class="meta x-mono">refuted by the check</span>
    </button>
    {#if show}
      <div class="rows">
        {#each dropped as d, i (i)}
          <div class="row">
            <div class="top">
              <span class="title">{d.title}</span>
              <span class="loc x-mono">{d.file}{d.line ? `:${d.line}` : ''}</span>
            </div>
            {#if d.why}
              <p class="why">{@html proseHtml(d.why)}</p>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<style>
  .wrap {
    border-top: 1px solid var(--x-line-panel);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 12px;
    width: 100%;
    padding: 16px 0;
    border: 0;
    border-bottom: 1px solid var(--x-line-panel);
    background: none;
    text-align: left;
    font-size: 13.5px;
    color: var(--x-body);
  }
  .head:hover {
    color: var(--x-ink-2);
  }
  .caret {
    font-size: 12px;
  }
  .sp {
    flex: 1;
  }
  .meta {
    font-size: 11px;
    color: var(--x-dim);
  }
  .rows {
    display: flex;
    flex-direction: column;
    padding: 6px 0 14px;
  }
  .row {
    padding: 14px 0;
    border-bottom: 1px solid var(--x-line-soft);
  }
  .row:last-child {
    border-bottom: 0;
  }
  .top {
    display: flex;
    align-items: baseline;
    gap: 12px;
    flex-wrap: wrap;
  }
  /* Struck through: it was a claim, and it is not one any more. */
  .title {
    font-size: 14.5px;
    line-height: 1.45;
    color: var(--x-body);
    text-decoration: line-through;
    text-decoration-color: var(--x-fainter);
  }
  .loc {
    font-size: 11px;
    color: var(--x-dim);
    unicode-bidi: plaintext;
  }
  .why {
    margin: 8px 0 0;
    max-width: 74ch;
    font-size: 13.5px;
    line-height: 1.7;
    color: var(--x-mute);
  }
  .why :global(code) {
    font-family: var(--x-mono);
    font-size: 0.88em;
    color: var(--x-ink-4);
  }
</style>
