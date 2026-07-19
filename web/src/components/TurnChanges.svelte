<script lang="ts">
  import { SvelteSet } from 'svelte/reactivity'
  import type { Turn } from '../lib/turns'
  import { fileEditsOf } from '../lib/toolMeta'
  import { buildTree, type TreeNode } from '../lib/fileTree'
  import { langFromPath } from '../lib/highlight'
  import DiffView from './tools/DiffView.svelte'
  import CodeView from './tools/CodeView.svelte'

  // What this one query changed: a folder tree of the files its Edit/Write/
  // MultiEdit calls touched, each file expandable to its diff. Fed entirely from
  // the turn's tool inputs (fileEditsOf), so it is per-query, needs no git, and
  // stays correct after the work is committed.
  let { turn }: { turn: Turn } = $props()

  const files = $derived(fileEditsOf(turn.blocks))
  const tree = $derived<TreeNode[]>(buildTree(files))
  const added = $derived(files.reduce((n, f) => n + f.added, 0))
  const removed = $derived(files.reduce((n, f) => n + f.removed, 0))

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

{#snippet counts(a: number, r: number)}
  <span class="counts mono">
    {#if a > 0}<span class="add">+{a}</span>{/if}
    {#if r > 0}<span class="del">−{r}</span>{/if}
  </span>
{/snippet}

{#snippet chev(down: boolean)}
  <svg class="chev" class:down width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6" /></svg>
{/snippet}

{#snippet row(n: TreeNode, depth: number)}
  {#if n.kind === 'dir'}
    <button class="drow" style="--d:{depth}" onclick={() => toggleDir(n.path)}>
      {@render chev(!collapsed.has(n.path))}
      <svg class="fic" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" /></svg>
      <span class="nm dir">{n.name}</span>
      <span class="sp"></span>
      {@render counts(n.added, n.removed)}
    </button>
    {#if !collapsed.has(n.path)}
      {#each n.children as c (c.kind === 'dir' ? c.path : c.file.path)}
        {@render row(c, depth + 1)}
      {/each}
    {/if}
  {:else}
    {@const f = n.file}
    <button class="frow" class:open={open.has(f.path)} style="--d:{depth}" onclick={() => toggleFile(f.path)}>
      {@render chev(open.has(f.path))}
      <span class="nm">{f.name}</span>
      <span class="sp"></span>
      {@render counts(f.added, f.removed)}
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
      <span class="eyebrow">Changed files</span>
      <span class="n mono">{files.length}</span>
      {@render counts(added, removed)}
      <span class="sp"></span>
      <button class="tbtn" onclick={collapseAll} disabled={allCollapsed}>Collapse all</button>
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
    margin: 4px 0 2px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg-raised);
    overflow: hidden;
  }
  .chead {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 8px 11px;
    border-bottom: 1px solid var(--border);
  }
  .eyebrow {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .n {
    font-size: 12px;
    color: var(--text-3);
  }
  .tbtn {
    flex: none;
    height: 24px;
    padding: 0 9px;
    border-radius: var(--r-sm);
    color: var(--text-4);
    font-size: 11.5px;
    font-weight: 500;
  }
  .tbtn:hover {
    color: var(--text-2);
    background: var(--panel);
  }
  .tbtn:disabled {
    opacity: 0.35;
  }
  .tree {
    display: flex;
    flex-direction: column;
    padding: 5px 6px 6px;
  }
  .drow,
  .frow {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    text-align: left;
    padding: 5px 7px;
    padding-left: calc(7px + var(--d) * 16px);
    border-radius: var(--r-sm);
    font-size: 13px;
    color: var(--text-2);
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
  }
  .fic {
    flex: none;
    color: var(--text-4);
  }
  .drow:hover .fic {
    color: var(--text-3);
  }
  .nm {
    flex: none;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 100%;
  }
  .nm.dir {
    font-family: var(--mono);
    font-size: 12px;
    color: var(--text-2);
  }
  .sp {
    flex: 1;
    min-width: 8px;
  }
  .counts {
    flex: none;
    display: flex;
    gap: 7px;
    font-size: 11.5px;
  }
  .add {
    color: #6fae90;
  }
  .del {
    color: #c98a83;
  }
  .diffs {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 2px 6px 8px calc(7px + var(--d) * 16px + 18px);
  }
</style>
