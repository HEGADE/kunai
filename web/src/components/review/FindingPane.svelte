<script lang="ts">
  import type { ReviewDraft, ReviewFinding } from '../../lib/api'
  import { effectiveSeverity, pad, type Edits, type Verdicts } from '../../lib/reviewDeck'
  import { proseHtml } from '../../lib/prose'
  import DiffPanel from './DiffPanel.svelte'

  // Every finding, stacked, with the summary above them.
  //
  // Stacked rather than one-at-a-time, and that is the design's real move: a
  // review READS, and a deck that shows one card at a time makes you click to
  // find out whether the next one is worse than this one. Scrolling past a
  // finding you have decided about is cheaper than clicking to reach one you
  // have not, and the queue rail carries your place either way.
  //
  // Which finding is "active" therefore comes from scroll position rather than
  // from a click, and that observation lives here because this is the element
  // that scrolls.
  let {
    draft,
    headline,
    findings,
    verdicts,
    edits,
    active,
    onactive,
    scroller = $bindable(),
  }: {
    draft: ReviewDraft
    // The review's verdict in a sentence. Passed in rather than derived here,
    // because it is counted at the EDITED severity and the deck owns the edits.
    headline: string
    findings: ReviewFinding[]
    verdicts: Verdicts
    edits: Edits
    active: number
    onactive: (i: number) => void
    scroller?: HTMLElement
  } = $props()

  let open = $state<Record<number, boolean>>({})

  // Scroll-spy. The topmost finding whose head has passed the reading line is
  // the one being read; anything else (nearest to centre, most visible area)
  // flickers on a long finding, because a single block can be taller than the
  // window and then "most visible" changes as you scroll THROUGH it.
  const READING_LINE = 220

  function spy() {
    const el = scroller
    if (!el) return
    let next = 0
    findings.forEach((f, i) => {
      const node = document.getElementById(`x-f-${f.index}`)
      if (node && node.getBoundingClientRect().top < READING_LINE) next = i
    })
    if (next !== active) onactive(next)
  }

  const stat = (f: ReviewFinding) => {
    const n = f.hunk?.length ?? 0
    if (!n) return ''
    const lang = f.file.split('.').pop()?.toUpperCase() ?? ''
    return `${n} lines${lang ? ' · ' + lang : ''}`
  }
  const lineRange = (f: ReviewFinding) => (f.end_line ? `:${f.line}-${f.end_line}` : `:${f.line}`)
</script>

<div class="pane" bind:this={scroller} onscroll={spy}>
  <header class="summary">
    <div class="eyebrow x-mono">
      <span class="lit">SUMMARY</span><span>·</span><span>{draft.owner}/{draft.repo}</span><span>·</span
      ><span>kunai[bot]</span>
    </div>
    <h1 class="head">{headline}</h1>
    {#if draft.summary}
      <p class="lede">{@html proseHtml(draft.summary)}</p>
    {/if}
  </header>

  {#each findings as f, i (f.index)}
    {@const v = verdicts[f.index]}
    <section id="x-f-{f.index}" class="block" class:on={i === active} class:done={!!v}>
      <div class="top">
        <span class="chip" data-sev={effectiveSeverity(f, edits)} data-v={v ?? 'todo'}>
          {v === 'accept' ? 'ACCEPTED' : v === 'dismiss' ? 'DISMISSED' : effectiveSeverity(f, edits).toUpperCase()}
        </span>
        <span class="loc x-mono">{f.file}<span class="lines">{lineRange(f)}</span></span>
        <div class="sp"></div>
        <span class="pos x-mono">{pad(i + 1)} / {pad(findings.length)}</span>
      </div>

      <h2 class="claim">{f.title}</h2>

      {#if f.body}
        <p class="body">{@html proseHtml(f.body)}</p>
      {/if}

      <!-- The verifier's full working, behind a disclosure because it runs to a
           dozen lines on every finding now that verification actually happens.
           Gated on the prose existing and NOT on the rail having rows: a review
           with no rows is exactly the one whose paragraph has nowhere else to
           go, and gating it the other way round left that text unreachable. -->
      {#if f.evidence}
        {#if open[f.index]}
          <p class="body more">{@html proseHtml(f.evidence)}</p>
        {/if}
        <button class="expand x-mono" onclick={() => (open = { ...open, [f.index]: !open[f.index] })}>
          {open[f.index] ? '— less' : '+ the rest of the argument'}
        </button>
      {/if}

      {#if f.hunk?.length}
        <DiffPanel lines={f.hunk} file={f.file.split('/').pop() ?? f.file} stat={stat(f)} side={f.side} />
      {/if}

      {#if !f.inline && f.why}
        <p class="aside x-mono">In the summary, not on the line: {f.why}</p>
      {/if}
    </section>
  {/each}

  <!-- The last finding has to be able to reach the reading line, or the queue
       can never make it active and the rail's last row is unreachable. -->
  <div class="tail"></div>
</div>

<style>
  .pane {
    min-width: 0;
    min-height: 0;
    overflow-y: auto;
    overflow-x: hidden;
    background: var(--x-bg);
  }

  .summary {
    padding: 26px 34px 20px;
    border-bottom: 1px solid var(--x-line-soft);
  }
  .eyebrow {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 10.5px;
    letter-spacing: 0.14em;
    color: var(--x-dim);
    margin-bottom: 12px;
  }
  .eyebrow .lit {
    color: var(--x-accent);
  }
  .head {
    margin: 0;
    font-size: 22px;
    font-weight: 600;
    letter-spacing: -0.02em;
    color: var(--x-ink);
  }
  .lede {
    font-size: 14px;
    line-height: 1.7;
    color: var(--x-body);
    max-width: 78ch;
    margin: 10px 0 0;
  }
  .lede :global(code) {
    font-family: var(--x-mono);
    font-size: 12.5px;
    color: var(--x-ink-4);
  }

  .block {
    border-bottom: 1px solid var(--x-line-soft);
    padding: 28px 34px 32px;
    background: var(--x-bg);
    transition: background 200ms, opacity 200ms;
  }
  /* The one being read lifts a step. Barely: it marks a place without making
     the others look switched off, which is what a heavier treatment does to a
     list you are meant to keep reading through. */
  .block.on {
    background: var(--x-rail);
  }
  .block.done {
    opacity: 0.55;
  }

  .top {
    display: flex;
    align-items: center;
    gap: 10px 12px;
    flex-wrap: wrap;
    min-width: 0;
    margin-bottom: 14px;
  }
  .chip {
    padding: 3px 8px;
    border-radius: 4px;
    font-family: var(--x-mono);
    font-size: 10px;
    letter-spacing: 0.14em;
    background: var(--x-accent-chip);
    color: var(--x-accent-lit);
  }
  .chip[data-v='accept'],
  .chip[data-v='dismiss'] {
    background: rgba(255, 255, 255, 0.05);
    color: var(--x-body);
  }
  .loc {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 11.5px;
    color: var(--x-body);
    unicode-bidi: plaintext;
  }
  .lines {
    color: var(--x-accent);
  }
  .sp {
    flex: 1;
  }
  .pos {
    font-size: 10.5px;
    color: var(--x-dim);
  }

  .claim {
    margin: 0 0 14px;
    font-size: 19px;
    font-weight: 500;
    line-height: 1.35;
    letter-spacing: -0.015em;
    color: var(--x-ink);
    max-width: 62ch;
  }
  .body {
    font-size: 14.5px;
    line-height: 1.75;
    color: var(--x-body);
    max-width: 78ch;
    margin: 0 0 12px;
  }
  .body :global(code) {
    font-family: var(--x-mono);
    font-size: 0.88em;
    color: var(--x-ink-4);
  }
  .body :global(code.loc) {
    color: var(--x-accent-lit);
  }
  .more {
    animation: fade 180ms ease both;
  }
  @keyframes fade {
    from {
      opacity: 0;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .more {
      animation: none;
    }
  }
  .expand {
    margin-bottom: 18px;
    padding: 0;
    border: 0;
    background: none;
    color: var(--x-dim);
    font-size: 11px;
    letter-spacing: 0.06em;
  }
  .expand:hover {
    color: var(--x-ink-4);
  }
  .aside {
    margin: 12px 0 0;
    font-size: 11px;
    color: var(--x-dim);
  }

  .tail {
    height: 40vh;
  }

  @media (max-width: 900px) {
    .summary,
    .block {
      padding-left: 18px;
      padding-right: 18px;
    }
  }
</style>
