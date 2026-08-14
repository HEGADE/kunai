<script lang="ts">
  import type { HunkLine } from '../../lib/api'
  import { highlightToHtml } from '../../lib/highlight'

  // The code a finding is quoting.
  //
  // Two rules, both learned from a real review that filled a whole screen with
  // one finding. Context is TRIMMED to a few lines either side of the lines the
  // claim is actually about: the anchor generously included four lines of
  // comment before the code, which is exactly the material a reader skips, and
  // it pushed the decision buttons off the bottom of the screen. And the block
  // is height-capped and scrolls inside itself, so a finding spanning forty
  // lines cannot make the card taller than the list it lives in.
  //
  // The cap is in px rather than vh on purpose: a phone's address bar hiding
  // must not resize the evidence out from under somebody reading it.
  let {
    lines,
    lang,
    side,
  }: { lines: HunkLine[]; lang?: string; side: string } = $props()

  // Lines of context kept either side of the focus. Three is enough to see the
  // shape of the block without reprinting the function.
  const CONTEXT = 3

  let full = $state(false)

  const trimmed = $derived.by(() => {
    const first = lines.findIndex((l) => l.focus)
    if (first < 0) return { shown: lines, hidden: 0 }
    let last = lines.length - 1
    while (last > 0 && !lines[last].focus) last--
    const from = Math.max(0, first - CONTEXT)
    const to = Math.min(lines.length, last + 1 + CONTEXT)
    return { shown: lines.slice(from, to), hidden: lines.length - (to - from) }
  })

  const shown = $derived(full ? lines : trimmed.shown)

  // The gutter number is the one the finding quotes: the new file normally, the
  // old file for a finding about a line the pull request deletes.
  const num = (l: HunkLine) => (side === 'LEFT' ? l.old : l.new) || ''
</script>

<div class="wrap">
  <div class="code mono">
    {#each shown as l, i (i)}
      <div class="ln" class:add={l.kind === '+'} class:del={l.kind === '-'} class:focus={l.focus}>
        <span class="n">{num(l)}</span>
        <span class="k">{l.kind === ' ' ? '' : l.kind}</span>
        <span class="t">{@html highlightToHtml(l.text, lang)}</span>
      </div>
    {/each}
  </div>
  {#if trimmed.hidden > 0}
    <button class="more" onclick={() => (full = !full)}>
      {full ? 'Trim to the quoted lines' : `Show ${trimmed.hidden} more line${trimmed.hidden === 1 ? '' : 's'} of context`}
    </button>
  {/if}
</div>

<style>
  .wrap {
    margin-top: 12px;
  }
  .code {
    max-height: 230px;
    overflow: auto;
    border-radius: var(--r-sm);
    background: var(--bg);
    padding: 7px 0;
    font-size: 12px;
    line-height: 1.6;
    /* Ligatures are off globally, but this is the one surface where the reader is
       judging a claim against exact characters, so it says so for itself too. */
    font-variant-ligatures: none;
  }
  .ln {
    display: flex;
    white-space: pre;
  }
  .n {
    flex: none;
    width: 46px;
    padding-right: 10px;
    text-align: right;
    color: var(--text-4);
    opacity: 0.55;
    user-select: none;
    font-variant-numeric: tabular-nums;
  }
  .k {
    flex: none;
    width: 13px;
    color: var(--text-4);
    user-select: none;
  }
  .t {
    flex: 1;
    padding-right: 12px;
  }
  .add {
    background: color-mix(in srgb, var(--live) 10%, transparent);
  }
  .del {
    background: color-mix(in srgb, var(--alert) 9%, transparent);
  }
  .add .k {
    color: var(--live);
  }
  .del .k {
    color: var(--alert);
  }
  /* The lines the claim is actually about. Marked on two channels, because the
     tint alone is easy to lose against an added line's own green. */
  .focus {
    box-shadow: inset 2px 0 0 var(--sev-ink, var(--text-3));
  }
  .focus .n {
    opacity: 1;
    color: var(--text-3);
  }

  .more {
    margin-top: 6px;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .more:hover {
    color: var(--text-2);
  }
</style>
