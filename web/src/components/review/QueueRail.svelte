<script lang="ts">
  import type { ReviewFinding } from '../../lib/api'
  import type { Verdicts, Edits } from '../../lib/reviewDeck'
  import { effectiveSeverity, pad } from '../../lib/reviewDeck'

  // The queue: every finding as one row, and where you are in them.
  //
  // Collapsible to a 44px strip of numbered stubs rather than to nothing, which
  // is the detail that makes collapsing worth offering: the review is a fixed
  // list you are working through, so losing your place in it costs more than the
  // 272px the rail takes. Collapsed, you can still see how many are left and
  // jump to one.
  //
  // The rail also carries what was checked and found CLEAN, which is the half of
  // a review that normally goes unsaid. A reviewer that only ever lists problems
  // is one you cannot tell from a reviewer that stopped looking.
  let {
    findings,
    active,
    verdicts,
    edits,
    clean,
    open,
    onpick,
  }: {
    findings: ReviewFinding[]
    active: number
    verdicts: Verdicts
    edits: Edits
    clean: string[]
    open: boolean
    onpick: (i: number) => void
  } = $props()

  const fileOf = (path: string) => path.split('/').pop() || path
</script>

<aside class="rail" class:open>
  {#if open}
    <div class="head">
      <span class="x-cap">Queue</span>
      <span class="x-cap keys">J / K</span>
    </div>

    <div class="rows">
      {#each findings as f, i (f.index)}
        {@const v = verdicts[f.index]}
        <button class="row" class:on={i === active} class:done={!!v} onclick={() => onpick(i)}>
          <span class="num x-mono">{pad(i + 1)}</span>
          <span class="text">
            <span class="short">{f.short || f.title}</span>
            <span class="meta x-mono">
              <span class="tag" data-v={v ?? 'todo'} data-sev={effectiveSeverity(f, edits)}>
                {v === 'accept' ? 'ACCEPTED' : v === 'dismiss' ? 'DISMISSED' : effectiveSeverity(f, edits).toUpperCase()}
              </span>
              <span class="tsep">|</span>
              <span class="file">{fileOf(f.file)}</span>
            </span>
          </span>
        </button>
      {/each}
    </div>

    {#if clean.length}
      <div class="clean">
        <div class="x-cap">Checked &middot; clean</div>
        <ul>
          {#each clean as c (c)}
            <li><span class="tick" aria-hidden="true">✓</span>{c}</li>
          {/each}
        </ul>
      </div>
    {/if}
  {:else}
    <!-- Collapsed. Numbered stubs, not nothing: the point of the rail is knowing
         where you are in a fixed list, and that survives at 44px. -->
    <div class="stubs">
      {#each findings as f, i (f.index)}
        <button
          class="stub x-mono"
          class:on={i === active}
          title={f.short || f.title}
          onclick={() => onpick(i)}
        >
          {pad(i + 1)}
        </button>
      {/each}
    </div>
  {/if}
</aside>

<style>
  .rail {
    display: flex;
    flex-direction: column;
    min-height: 0;
    overflow: hidden;
    border-right: 1px solid var(--x-line);
    background: var(--x-rail);
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 10px 12px;
    border-bottom: 1px solid var(--x-line-soft);
  }
  .keys {
    letter-spacing: 0.06em;
  }

  .rows {
    flex: 1;
    overflow-y: auto;
    padding: 6px 0;
  }
  .row {
    display: grid;
    grid-template-columns: 16px minmax(0, 1fr);
    gap: 10px;
    width: 100%;
    text-align: left;
    padding: 13px 14px 13px 12px;
    border: 0;
    /* The active edge, which is the one place the accent appears in this rail.
       Two pixels of colour is enough to hold a place in a list. */
    border-left: 2px solid transparent;
    background: none;
    transition: background 160ms, border-color 160ms;
  }
  .row:hover {
    background: #0f1114;
  }
  .row.on {
    background: var(--x-row);
    border-left-color: var(--x-accent);
  }
  .num {
    font-size: 10.5px;
    color: var(--x-dim);
    padding-top: 2px;
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }
  .short {
    font-size: 13px;
    line-height: 1.4;
    color: var(--x-mute);
  }
  .row.on .short {
    color: var(--x-ink);
  }
  /* A decided row recedes: the list is a queue, and what is left in it is the
     thing worth seeing. */
  .row.done .short {
    color: var(--x-dim);
  }
  .meta {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    font-size: 10px;
    letter-spacing: 0.06em;
    color: var(--x-dim);
  }
  .tag {
    color: var(--x-accent);
  }
  .tag[data-v='accept'] {
    color: var(--x-go);
  }
  .tag[data-v='dismiss'] {
    color: #55565f;
  }
  .tsep {
    color: #4a4c55;
  }
  .file {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    unicode-bidi: plaintext;
  }

  .clean {
    border-top: 1px solid var(--x-line-soft);
    padding: 12px;
  }
  .clean ul {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin: 10px 0 0;
    padding: 0;
    list-style: none;
  }
  .clean li {
    display: flex;
    gap: 8px;
    font-size: 12px;
    line-height: 1.4;
    color: var(--x-dim);
  }
  .tick {
    flex: none;
  }

  .stubs {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 16px;
    width: 44px;
    padding: 14px 0;
  }
  .stub {
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    border: 1px solid var(--x-edge);
    border-radius: 6px;
    background: none;
    color: var(--x-dim);
    font-size: 10.5px;
    transition: all 140ms;
  }
  .stub:hover {
    border-color: var(--x-edge-lit);
  }
  .stub.on {
    background: var(--x-accent-chip);
    border-color: var(--x-accent-edge);
    color: var(--x-accent-lit);
  }
</style>
