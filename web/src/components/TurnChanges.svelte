<script lang="ts">
  import { SvelteSet } from 'svelte/reactivity'
  import type { Turn } from '../lib/turns'
  import { fileEditsOf } from '../lib/toolMeta'
  import { buildTree, type TreeNode } from '../lib/fileTree'
  import { langFromPath } from '../lib/highlight'
  import { iconForFile } from '../lib/langIcons'
  import DiffView from './tools/DiffView.svelte'
  import CodeView from './tools/CodeView.svelte'

  // What this one query changed, read like a live `git diff --stat`: a folder
  // tree of the files its Edit/Write/MultiEdit calls touched, each with a scaled
  // add/remove gauge so the churn's shape is legible at a glance, and each file
  // expandable to its diff. Fed entirely from the turn's tool inputs (fileEditsOf),
  // so it is per-query, needs no git, and stays correct after the work is committed.
  let {
    turn,
    canRevert = false,
    reverted = false,
    onRevert,
    onUndo,
  }: {
    turn: Turn
    // Whether a pre-turn snapshot exists to revert this turn's file changes to.
    canRevert?: boolean
    // Whether this turn has already been reverted (offer Undo instead).
    reverted?: boolean
    onRevert?: () => Promise<void> | void
    onUndo?: () => Promise<void> | void
  } = $props()

  // Reverting rewrites files on disk, so it takes a second tap to confirm.
  let confirming = $state(false)
  let busy = $state(false)
  async function doRevert() {
    busy = true
    try {
      await onRevert?.()
    } finally {
      busy = false
      confirming = false
    }
  }
  async function doUndo() {
    busy = true
    try {
      await onUndo?.()
    } finally {
      busy = false
    }
  }

  const files = $derived(fileEditsOf(turn.blocks))
  const tree = $derived<TreeNode[]>(buildTree(files))
  const added = $derived(files.reduce((n, f) => n + f.added, 0))
  const removed = $derived(files.reduce((n, f) => n + f.removed, 0))
  const total = $derived(added + removed)
  // Gauge scale: the busiest file fills the bar, the rest read against it, so the
  // longest bar marks where the real work happened.
  const maxChurn = $derived(Math.max(1, ...files.map((f) => f.added + f.removed)))

  // Dir paths that are collapsed, and file paths whose diff is open. Reactive
  // collections so one toggle re-renders only the affected rows.
  const collapsed = new SvelteSet<string>()
  const open = new SvelteSet<string>()

  function toggleDir(path: string) {
    if (collapsed.has(path)) collapsed.delete(path)
    else collapsed.add(path)
  }
  function toggleFile(path: string) {
    if (open.has(path)) open.delete(path)
    else open.add(path)
  }

  function collapseAll() {
    const walk = (nodes: TreeNode[]) => {
      for (const n of nodes)
        if (n.kind === 'dir') {
          collapsed.add(n.path)
          walk(n.children)
        }
    }
    walk(tree)
  }
  const allCollapsed = $derived.by(() => {
    let dirs = 0
    const walk = (nodes: TreeNode[]) => {
      for (const n of nodes)
        if (n.kind === 'dir') {
          dirs++
          walk(n.children)
        }
    }
    walk(tree)
    return dirs > 0 && collapsed.size >= dirs
  })
</script>

{#snippet nums(a: number, r: number)}
  <span class="nums">
    {#if a > 0}<span class="add">+{a}</span>{/if}
    {#if r > 0}<span class="del">−{r}</span>{/if}
  </span>
{/snippet}

<!-- A diffstat gauge: bar length scaled to the busiest file, split green/red by
     the add/remove ratio. `frac` is this row's share of the max churn. -->
{#snippet gauge(a: number, r: number, frac: number)}
  {@const t = Math.max(1, a + r)}
  <span class="gauge" style="--frac:{frac}">
    <span class="fill">
      <span class="g" style="flex:{a / t}"></span>
      <span class="d" style="flex:{r / t}"></span>
    </span>
  </span>
{/snippet}

{#snippet chev(down: boolean)}
  <svg class="chev" class:down width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6" /></svg>
{/snippet}

<!-- The file's brand logo, same as the tool card's chip, so a changed file reads
     by its language at a glance. Unknown types fall back to a neutral glyph. -->
{#snippet fileIcon(path: string)}
  {@const icon = iconForFile(path)}
  {#if icon}
    <svg class="li" viewBox="0 0 24 24" width="13" height="13" style="color:{icon.color}" aria-hidden="true"><path fill="currentColor" d={icon.d} /></svg>
  {:else}
    <svg class="li gen" viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M14 3v5h5" /><path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" /></svg>
  {/if}
{/snippet}

{#snippet row(n: TreeNode, depth: number)}
  {#if n.kind === 'dir'}
    <button class="drow" style="--d:{depth}" onclick={() => toggleDir(n.path)}>
      {@render chev(!collapsed.has(n.path))}
      <span class="nm dir">{n.name}<span class="slash">/</span></span>
      <span class="sp"></span>
      {@render nums(n.added, n.removed)}
    </button>
    {#if !collapsed.has(n.path)}
      {#each n.children as c (c.kind === 'dir' ? c.path : c.file.path)}
        {@render row(c, depth + 1)}
      {/each}
    {/if}
  {:else}
    {@const f = n.file}
    {@const t = f.added + f.removed}
    <button class="frow" class:open={open.has(f.path)} style="--d:{depth}" onclick={() => toggleFile(f.path)}>
      {@render chev(open.has(f.path))}
      {@render fileIcon(f.path)}
      <span class="nm">{f.name}</span>
      <span class="sp"></span>
      {@render nums(f.added, f.removed)}
      {@render gauge(f.added, f.removed, t / maxChurn)}
    </button>
    {#if open.has(f.path)}
      <div class="diffs" style="--d:{depth}">
        {#each f.ops as op, k (k)}
          {#if op.kind === 'edit'}
            <DiffView oldStr={op.oldStr} newStr={op.newStr} replaceAll={op.replaceAll} />
          {:else}
            <CodeView code={op.content} lang={langFromPath(f.path)} />
          {/if}
        {/each}
      </div>
    {/if}
  {/if}
{/snippet}

{#if files.length}
  <div class="tchanges">
    <div class="chead">
      <span class="inv"><span class="prompt">❯</span> diff&nbsp;<span class="stat">--stat</span></span>
      <span class="fc">{files.length} file{files.length === 1 ? '' : 's'}</span>
      <span class="sp"></span>
      <span class="tally">
        {@render nums(added, removed)}
        {#if total > 0}
          <span class="total" title="{added} added, {removed} removed">
            <span class="g" style="flex:{added / total}"></span>
            <span class="d" style="flex:{removed / total}"></span>
          </span>
        {/if}
      </span>
      {#if reverted}
        <button class="tbtn undo" onclick={doUndo} disabled={busy} title="Restore the files this revert undid"
          >Undo revert</button
        >
      {:else if canRevert}
        {#if confirming}
          <button class="tbtn danger" onclick={doRevert} disabled={busy}>Revert — sure?</button>
          <button class="tbtn" onclick={() => (confirming = false)} disabled={busy}>Cancel</button>
        {:else}
          <button
            class="tbtn"
            onclick={() => (confirming = true)}
            title="Restore the working tree to before this turn (undoes its file changes; the conversation stays)"
            >Revert</button
          >
        {/if}
      {/if}
      <button class="tbtn" onclick={collapseAll} disabled={allCollapsed} aria-label="Collapse all folders">Collapse</button>
    </div>
    <div class="tree">
      {#each tree as n (n.kind === 'dir' ? n.path : n.file.path)}
        {@render row(n, 0)}
      {/each}
    </div>
  </div>
{/if}

<style>
  .tchanges {
    margin: 5px 0 2px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg-raised);
    overflow: hidden;
    font-family: var(--mono);
  }

  /* Header reads like a stat command banner: the invocation, the file count, and
     a total add/remove gauge that anchors the whole card. */
  .chead {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 9px 12px;
    border-bottom: 1px solid var(--border);
  }
  .inv {
    display: inline-flex;
    align-items: baseline;
    gap: 2px;
    font-size: 12px;
    text-transform: none;
    letter-spacing: 0;
    color: var(--text-4);
  }
  .inv .prompt {
    color: var(--text-3);
    font-weight: 600;
    margin-right: 4px;
  }
  .inv .stat {
    color: var(--text-2);
  }
  .fc {
    font-size: 11.5px;
    color: var(--text-4);
  }
  .tally {
    display: flex;
    align-items: center;
    gap: 9px;
  }
  .total {
    display: flex;
    width: 108px;
    height: 5px;
    border-radius: 3px;
    overflow: hidden;
    background: var(--panel-2);
  }
  .total .g,
  .tree .g {
    background: color-mix(in oklab, #6fae90 82%, transparent);
  }
  .total .d,
  .tree .d {
    background: color-mix(in oklab, #c98a83 82%, transparent);
  }
  .tbtn {
    flex: none;
    height: 24px;
    padding: 0 10px;
    border: 1px solid var(--border-2);
    border-radius: 100px;
    color: var(--text-4);
    font-size: 11px;
    letter-spacing: 0.02em;
  }
  .tbtn:hover {
    color: var(--text-2);
    border-color: var(--text-4);
  }
  .tbtn:disabled {
    opacity: 0.3;
    border-color: var(--border);
  }
  /* The confirm step of a destructive revert reads in the diff's muted red. */
  .tbtn.danger {
    color: var(--diff-del, #d98b8b);
    border-color: var(--diff-del, #d98b8b);
  }
  .tbtn.danger:hover {
    background: color-mix(in srgb, var(--diff-del, #d98b8b) 12%, transparent);
  }
  .tbtn.undo {
    color: var(--text-2);
  }

  .tree {
    display: flex;
    flex-direction: column;
    padding: 6px 8px 8px;
  }

  /* A row is a light stat line, not a boxed item. Indent guides (a hairline per
     depth level, drawn behind the content) make nesting read without a folder
     icon on every row. */
  .drow,
  .frow {
    position: relative;
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    text-align: left;
    padding: 5px 8px 5px calc(8px + var(--d) * 17px);
    border-radius: var(--r-sm);
    font-size: 12.5px;
    color: var(--text-2);
  }
  .drow::before,
  .frow::before {
    content: '';
    position: absolute;
    left: 12px;
    top: 0;
    bottom: 0;
    width: calc(var(--d) * 17px);
    background: repeating-linear-gradient(
      90deg,
      transparent 0,
      transparent 16px,
      var(--border-2) 16px,
      var(--border-2) 17px
    );
    pointer-events: none;
  }
  .drow:hover,
  .frow:hover,
  .frow.open {
    background: var(--panel);
    color: var(--text);
  }
  .chev {
    flex: none;
    color: var(--text-4);
    transition: transform 0.12s ease;
  }
  .chev.down {
    transform: rotate(90deg);
    color: var(--text-3);
  }
  .li {
    flex: none;
  }
  .li.gen {
    color: var(--text-4);
  }
  .nm {
    flex: none;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 62%;
  }
  .nm.dir {
    color: var(--text-3);
  }
  .drow:hover .nm.dir {
    color: var(--text);
  }
  .slash {
    color: var(--text-4);
  }
  .sp {
    flex: 1;
    min-width: 10px;
  }
  .nums {
    flex: none;
    display: flex;
    gap: 8px;
    font-size: 11.5px;
    font-variant-numeric: tabular-nums;
  }
  .add {
    color: #6fae90;
  }
  .del {
    color: #c98a83;
  }

  /* The signature: a fixed-track gauge whose fill length is this file's share of
     the busiest file, split green/red by the add/remove ratio. The longest bar
     is the file that changed most. */
  .gauge {
    flex: none;
    width: 60px;
    height: 5px;
    border-radius: 3px;
    background: var(--panel-2);
    overflow: hidden;
  }
  .gauge .fill {
    display: flex;
    height: 100%;
    width: calc(max(0.08, var(--frac)) * 100%);
    border-radius: 3px;
    overflow: hidden;
  }

  .diffs {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 3px 6px 9px calc(8px + var(--d) * 17px + 17px);
  }

  @media (max-width: 560px) {
    .nm {
      max-width: 52%;
    }
    /* Narrow: the per-row gauges, the total gauge and the file count all drop, so
       the header stays one clean line — invocation, tally, Collapse. */
    .gauge,
    .total,
    .fc {
      display: none;
    }
  }
  .inv {
    white-space: nowrap;
  }
  .chead {
    flex-wrap: nowrap;
  }
</style>
