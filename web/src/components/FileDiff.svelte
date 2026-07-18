<script lang="ts">
  import type { FileDiff } from '../lib/types'

  // Renders a file's diff from the server's structured rows — no client-side
  // diffing, just a single pass over typed lines. Deliberately unhighlighted:
  // the green/red is the signal, and skipping per-line syntax highlighting keeps
  // even a large hunk cheap to mount.
  let { diff }: { diff: FileDiff } = $props()
</script>

<div class="fd">
  {#if diff.binary}
    <p class="note">Binary file — no text diff.</p>
  {:else if !diff.lines || diff.lines.length === 0}
    <p class="note">No changes to show.</p>
  {:else}
    <div class="rows mono">
      {#each diff.lines as l, i (i)}
        {#if l.kind === 'hunk'}
          <div class="row hunk"><code>{l.text}</code></div>
        {:else}
          <div class="row {l.kind}">
            <span class="gut">{l.old ?? ''}</span>
            <span class="gut">{l.new ?? ''}</span>
            <span class="sign">{l.kind === 'add' ? '+' : l.kind === 'del' ? '−' : ''}</span>
            <code>{l.text || ' '}</code>
          </div>
        {/if}
      {/each}
    </div>
    {#if diff.truncated}<p class="note">Diff truncated — file is very large.</p>{/if}
  {/if}
</div>

<style>
  .fd {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    overflow: hidden;
  }
  .rows {
    overflow-x: auto;
    font-size: 11.5px;
    line-height: 1.6;
    -webkit-overflow-scrolling: touch;
  }
  .row {
    display: flex;
    align-items: baseline;
    white-space: pre;
    min-width: max-content;
    width: 100%;
  }
  .gut {
    flex: none;
    width: 34px;
    padding: 0 6px;
    text-align: right;
    color: var(--text-4);
    user-select: none;
  }
  .sign {
    flex: none;
    width: 12px;
    text-align: center;
    color: var(--text-4);
    user-select: none;
  }
  code {
    flex: 1;
    padding-right: 12px;
    color: var(--text-2);
    font-family: inherit;
  }
  /* Muted green/red at low opacity, the app's diff convention. */
  .row.add {
    background: rgba(79, 158, 127, 0.09);
  }
  .row.add .sign,
  .row.add code {
    color: #8fc3ac;
  }
  .row.del {
    background: rgba(207, 111, 102, 0.09);
  }
  .row.del .sign,
  .row.del code {
    color: #d79a94;
  }
  .row.hunk {
    background: var(--panel);
    color: var(--text-4);
    padding: 2px 12px;
  }
  .row.hunk code {
    color: var(--text-4);
    padding: 0;
  }
  .note {
    margin: 0;
    padding: 10px 12px;
    font-size: 12px;
    color: var(--text-4);
  }
</style>
