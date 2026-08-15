<script lang="ts">
  import type { HunkLine } from '../../lib/api'

  // The code a finding is about.
  //
  // The anchored lines are marked twice: a wash across the row and two pixels of
  // accent down its left edge. That is the only place the accent appears in the
  // pane, and it is the right place for it, because "which of these lines is the
  // claim actually about" is the question a reader asks of every hunk.
  //
  // Comments are dimmed a step below code. In this codebase that is not a small
  // thing: the comments are the reasoning, so they are frequent and long, and
  // left at the same weight as code they are most of what the eye lands on.
  let {
    lines,
    file,
    stat,
    side,
  }: {
    lines: HunkLine[]
    file: string
    stat: string
    side: string
  } = $props()

  const num = (l: HunkLine) => (side === 'LEFT' ? l.old : l.new) || ''
  const isComment = (t: string) => {
    const s = t.trim()
    return s.startsWith('//') || s.startsWith('#') || s.startsWith('*') || s.startsWith('/*')
  }
</script>

<div class="panel">
  <div class="head">
    <span class="x-cap">Diff</span>
    <span class="file x-mono">{file}</span>
    <div class="sp"></div>
    <span class="stat x-mono">{stat}</span>
  </div>
  <div class="code x-mono">
    {#each lines as l, i (i)}
      <div class="ln" class:hit={l.focus}>
        <span class="n">{num(l)}</span>
        <span class="sign" class:add={l.kind === '+'} class:del={l.kind === '-'}>
          {l.kind === ' ' ? '' : l.kind}
        </span>
        <span class="t" class:cmt={isComment(l.text)}>{l.text}</span>
      </div>
    {/each}
  </div>
</div>

<style>
  .panel {
    border: 1px solid var(--x-line-panel);
    border-radius: 10px;
    overflow: hidden;
    background: var(--x-panel);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    min-width: 0;
    padding: 9px 13px;
    border-bottom: 1px solid var(--x-line-panel);
    background: var(--x-panel-2);
  }
  .file,
  .stat {
    font-size: 11px;
    color: var(--x-dim);
  }
  .file {
    unicode-bidi: plaintext;
  }
  .sp {
    flex: 1;
  }
  .code {
    font-size: 12.5px;
    line-height: 1.85;
    padding: 8px 0;
    overflow-x: auto;
  }
  .ln {
    display: flex;
    padding: 0 12px;
  }
  /* The lines the claim is about, on two channels: a wash and an edge. */
  .ln.hit {
    background: var(--x-accent-wash);
    box-shadow: inset 2px 0 0 var(--x-accent);
  }
  .n {
    width: 34px;
    flex: none;
    text-align: right;
    color: #5a5c65;
  }
  .ln.hit .n {
    color: var(--x-accent-dim);
  }
  .sign {
    width: 16px;
    flex: none;
    text-align: center;
    color: var(--x-fainter);
  }
  .sign.add {
    color: var(--x-add);
  }
  .sign.del {
    color: var(--x-accent-dim);
  }
  .t {
    white-space: pre;
    color: var(--x-ink-4);
  }
  /* The reasoning, a step back from the code it explains. */
  .t.cmt {
    color: #7a7c86;
  }
</style>
