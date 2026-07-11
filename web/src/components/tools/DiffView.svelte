<script lang="ts">
  import { diffLines } from '../../lib/diff'

  let {
    oldStr,
    newStr,
    path,
    replaceAll = false,
    maxRows = 40,
  }: {
    oldStr: string
    newStr: string
    path?: string
    replaceAll?: boolean
    maxRows?: number
  } = $props()

  let expanded = $state(false)
  const diff = $derived(diffLines(oldStr, newStr))
  const clamped = $derived(!expanded && diff.rows.length > maxRows)
  const rows = $derived(clamped ? diff.rows.slice(0, maxRows) : diff.rows)
  const sign = (k: string) => (k === 'add' ? '+' : k === 'del' ? '−' : ' ')
</script>

<div class="diff">
  {#if path}
    <div class="dhead">
      <span class="dpath mono">{path}</span>
      <span class="sp"></span>
      {#if replaceAll}<span class="badge">replace all</span>{/if}
      {#if diff.added}<span class="stat add">+{diff.added}</span>{/if}
      {#if diff.removed}<span class="stat del">−{diff.removed}</span>{/if}
    </div>
  {/if}
  <div class="body mono">
    {#each rows as r, i (i)}
      <div class="row {r.kind}"><span class="g">{sign(r.kind)}</span><span class="t">{r.text || ' '}</span></div>
    {/each}
  </div>
  {#if clamped}
    <button class="more" onclick={() => (expanded = true)}>Show full diff ({diff.rows.length - maxRows} more)</button>
  {:else if expanded && diff.rows.length > maxRows}
    <button class="more" onclick={() => (expanded = false)}>Collapse</button>
  {/if}
</div>

<style>
  .diff {
    --diff-add-bg: rgba(79, 158, 127, 0.08);
    --diff-del-bg: rgba(207, 111, 102, 0.09);
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    overflow: hidden;
  }
  .dhead {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    border-bottom: 1px solid var(--border);
  }
  .dpath {
    font-size: 11.5px;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    unicode-bidi: plaintext;
    text-align: left;
  }
  .sp {
    flex: 1;
  }
  .badge {
    font-size: 10px;
    color: var(--text-3);
    border: 1px solid var(--border-2);
    border-radius: 4px;
    padding: 0 5px;
  }
  .stat {
    font-family: var(--mono);
    font-size: 11px;
  }
  .stat.add {
    color: var(--live);
  }
  .stat.del {
    color: var(--alert);
  }
  .body {
    overflow-x: auto;
    padding: 4px 0;
    font-size: 12.5px;
    line-height: 1.5;
  }
  .row {
    display: flex;
    padding: 0 10px;
    white-space: pre;
  }
  .row.add {
    background: var(--diff-add-bg);
  }
  .row.del {
    background: var(--diff-del-bg);
  }
  .g {
    flex: none;
    width: 14px;
    color: var(--text-4);
    user-select: none;
  }
  .row.add .g {
    color: var(--live);
  }
  .row.del .g {
    color: var(--alert);
  }
  .t {
    color: var(--text);
  }
  .row.ctx .t {
    color: var(--text-2);
  }
  .more {
    width: 100%;
    padding: 6px;
    border-top: 1px solid var(--border);
    font-size: 11.5px;
    color: var(--text-3);
  }
  .more:hover {
    color: var(--text);
    background: var(--panel);
  }
</style>
