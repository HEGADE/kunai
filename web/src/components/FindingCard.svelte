<script lang="ts">
  import type { ReviewFinding } from '../lib/api'
  import { langFor } from '../lib/outputShape'
  import { highlightToHtml } from '../lib/highlight'

  // One finding, carrying its own evidence.
  //
  // Self-contained on purpose: the claim, the code it is about, and the two
  // decisions you can make about it, all in one card. That is what lets the same
  // layout work on a laptop and on a phone, which a list-plus-detail split cannot
  // do, and kunai is used from a phone.
  let {
    f,
    dropped,
    selected,
    onToggle,
    onAsk,
  }: {
    f: ReviewFinding
    dropped: boolean
    selected: boolean
    onToggle: () => void
    onAsk: () => void
  } = $props()

  const lang = $derived(langFor(f.file))
  const location = $derived(
    !f.file ? '' : !f.line ? f.file : f.end_line ? `${f.file}:${f.line}-${f.end_line}` : `${f.file}:${f.line}`,
  )
  // The gutter number is the one the finding quotes: the new file normally, the
  // old file for a finding about a deleted line.
  const num = (l: { old?: number; new?: number }) => (f.side === 'LEFT' ? l.old : l.new) || ''
</script>

<article class="card" class:dropped class:selected>
  <header>
    <span class="loc mono">{location}</span>
    <span class="where" class:sum={!f.inline} title={f.why ?? 'Posted as a comment on this line'}>
      {f.inline ? 'inline' : 'summary'}
    </span>
  </header>

  <h3 class="claim">{f.title}</h3>
  {#if f.body}<p class="why">{f.body}</p>{/if}
  {#if f.why}<p class="note">{f.why}</p>{/if}

  {#if f.hunk?.length}
    <!-- The code, in the diff's own vocabulary. Highlighted per line so a change
         reads as a change AND as code, which a plain red/green block does not. -->
    <div class="hunk mono">
      {#each f.hunk as l, i (i)}
        <div class="hl" class:add={l.kind === '+'} class:del={l.kind === '-'} class:focus={l.focus}>
          <span class="n">{num(l)}</span>
          <span class="k">{l.kind === ' ' ? '' : l.kind}</span>
          <span class="t">{@html highlightToHtml(l.text, lang)}</span>
        </div>
      {/each}
    </div>
  {/if}

  {#if f.suggestion}
    <div class="sug">
      <span class="slbl mono">suggested change</span>
      <pre class="scode mono">{f.suggestion}</pre>
    </div>
  {/if}

  <footer>
    <button class="act" class:on={!dropped} onclick={onToggle}>
      {dropped ? 'Include' : 'Drop'}
    </button>
    <button class="ask" onclick={onAsk}>Ask about this →</button>
  </footer>
</article>

<style>
  .card {
    border: 1px solid var(--border);
    border-radius: var(--r);
    background: var(--panel);
    padding: 13px 15px 11px;
    transition: opacity 0.15s, border-color 0.15s;
  }
  /* Dropped recedes rather than disappearing, so undoing is one click and the
     count in the header stays honest about what changed. */
  .dropped {
    opacity: 0.38;
  }
  /* Keyboard focus is a border, not a glow: this theme has no glows. */
  .selected {
    border-color: var(--border-2);
  }
  header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
    margin-bottom: 7px;
  }
  .loc {
    font-size: 11.5px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    /* A clipped path keeps its leading slash where it belongs. */
    unicode-bidi: plaintext;
  }
  .where {
    flex: none;
    font-size: 10.5px;
    letter-spacing: 0.03em;
    color: var(--text-4);
  }
  .where.sum {
    color: var(--busy);
  }
  .claim {
    margin: 0;
    font-size: 14px;
    font-weight: 550;
    line-height: 1.4;
    color: var(--text);
  }
  .why {
    margin: 6px 0 0;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--text-2);
    white-space: pre-wrap;
  }
  .note {
    margin: 6px 0 0;
    font-size: 11.5px;
    color: var(--text-4);
  }

  /* The evidence. A diff gutter, not a code block: the numbers are the point,
     because they are what the finding is quoting. */
  .hunk {
    margin-top: 10px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg);
    overflow-x: auto;
    font-size: 12px;
    line-height: 1.55;
  }
  .hl {
    display: flex;
    gap: 0;
    white-space: pre;
  }
  .n {
    flex: none;
    width: 44px;
    padding-right: 9px;
    text-align: right;
    color: var(--text-4);
    opacity: 0.6;
    user-select: none;
  }
  .k {
    flex: none;
    width: 12px;
    color: var(--text-4);
    user-select: none;
  }
  .t {
    flex: 1;
    padding-right: 10px;
  }
  /* The same muted green and red the changed-files card uses, at the same low
     opacity: a diff should read as a diff without shouting. */
  .add {
    background: color-mix(in srgb, var(--live) 11%, transparent);
  }
  .del {
    background: color-mix(in srgb, var(--alert) 10%, transparent);
  }
  .add .k {
    color: var(--live);
  }
  .del .k {
    color: var(--alert);
  }
  /* The lines the finding is actually about, marked so context can be generous
     without the point getting lost in it. */
  .focus {
    box-shadow: inset 2px 0 0 var(--text-3);
  }

  .sug {
    margin-top: 10px;
  }
  .slbl {
    display: block;
    margin-bottom: 4px;
    font-size: 10.5px;
    color: var(--text-4);
  }
  .scode {
    margin: 0;
    padding: 9px 11px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg);
    font-size: 12px;
    line-height: 1.5;
    color: var(--text-2);
    overflow-x: auto;
    white-space: pre;
  }

  footer {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 11px;
  }
  .act {
    padding: 4px 12px;
    border-radius: var(--r-sm);
    background: var(--panel-2);
    color: var(--text-2);
    font-size: 12px;
    font-weight: 500;
  }
  .act:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .ask {
    margin-left: auto;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .ask:hover {
    color: var(--text-2);
  }
</style>
