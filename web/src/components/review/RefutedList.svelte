<script lang="ts">
  import type { DroppedFinding } from '../../lib/api'
  import { severityLabel } from '../../lib/severity'

  // What the review considered and threw away, with the reason.
  //
  // Kept and shown because a reviewer you can audit is one you will trust:
  // three findings from a reviewer that refuted four is a different thing from
  // three findings from one that only found three, and nothing else can tell
  // those apart. Collapsed, because it is evidence about the reviewer rather
  // than about the code.
  let { items }: { items: DroppedFinding[] } = $props()

  let open = $state(false)
</script>

<section class="refuted">
  <button class="head" onclick={() => (open = !open)} aria-expanded={open}>
    <span class="chev" class:open aria-hidden="true">›</span>
    {items.length} claim{items.length === 1 ? '' : 's'} checked and refuted
  </button>

  {#if open}
    <ul>
      {#each items as d, i (i)}
        <li>
          <div class="top">
            <span class="sev">{severityLabel(d.severity)}</span>
            <span class="loc mono">{d.file}{d.line ? ':' + d.line : ''}</span>
          </div>
          <p class="title">{d.title}</p>
          <p class="why">{d.why}</p>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .refuted {
    margin-top: 18px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 7px;
    font-size: 11.5px;
    color: var(--text-4);
    font-variant-numeric: tabular-nums;
  }
  .head:hover {
    color: var(--text-2);
  }
  .chev {
    display: inline-block;
    transition: transform 0.15s;
    font-size: 13px;
    line-height: 1;
  }
  .chev.open {
    transform: rotate(90deg);
  }
  @media (prefers-reduced-motion: reduce) {
    .chev {
      transition: none;
    }
  }
  ul {
    margin: 12px 0 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .top {
    display: flex;
    align-items: baseline;
    gap: 9px;
  }
  .sev {
    font-size: 10px;
    font-weight: 650;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .loc {
    font-size: 11px;
    color: var(--text-4);
    unicode-bidi: plaintext;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .title {
    margin: 3px 0 0;
    font-size: 12.5px;
    color: var(--text-3);
    text-decoration: line-through;
    text-decoration-color: var(--text-4);
  }
  .why {
    margin: 4px 0 0;
    max-width: 72ch;
    font-size: 11.5px;
    line-height: 1.6;
    color: var(--text-4);
  }
</style>
