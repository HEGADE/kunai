<script lang="ts">
  import { SvelteSet, SvelteMap } from 'svelte/reactivity'
  import type { ChatConnection } from '../lib/chat.svelte'
  import type { ChangesResp, FileDiff } from '../lib/types'
  import { fetchChanges, fetchDiff } from '../lib/api'
  import { buildTree, type TreeNode } from '../lib/fileTree'
  import FileDiffView from './FileDiff.svelte'

  // What the session's agent changed on disk: a file tree with per-file and
  // per-folder line counts, each file expanding to its diff (fetched lazily, so
  // opening the view costs one small request and reading a file costs one more).
  let { chat }: { chat: ChatConnection } = $props()

  let data = $state<ChangesResp | null>(null)

  // dir paths that are collapsed, file paths whose diff is open, and the diff
  // cache (a FileDiff, or the sentinels while it loads / if it failed). Reactive
  // collections so a single add/delete re-renders the affected rows only.
  const collapsed = new SvelteSet<string>()
  const open = new SvelteSet<string>()
  const diffs = new SvelteMap<string, FileDiff | 'loading' | 'error'>()

  const tree = $derived<TreeNode[]>(data?.files ? buildTree(data.files) : [])

  async function load() {
    try {
      data = await fetchChanges(chat.origin, chat.sessionId)
    } catch {
      // A failed read just leaves the panel hidden; the chat is unaffected.
    }
  }
  $effect(() => {
    // Refetch when the session changes (chat prop swaps between tabs); clear
    // first so the panel never shows the previous session's changes mid-load.
    void chat.sessionId
    data = null
    load()
  })
  // Refresh the moment a turn ends: the agent has just finished touching files,
  // so the panel updates itself instead of waiting for a manual refresh.
  let wasRunning = false
  $effect(() => {
    const runningNow = chat.sessionState === 'running'
    if (wasRunning && !runningNow) load()
    wasRunning = runningNow
  })

  function toggleDir(path: string) {
    if (collapsed.has(path)) collapsed.delete(path)
    else collapsed.add(path)
  }

  async function toggleFile(path: string) {
    if (open.has(path)) {
      open.delete(path)
      return
    }
    open.add(path)
    if (diffs.has(path)) return
    diffs.set(path, 'loading')
    try {
      const res = await fetchDiff(chat.origin, chat.sessionId, path)
      diffs.set(path, res.files[0] ?? 'error')
    } catch {
      diffs.set(path, 'error')
    }
  }

  function collapseAll() {
    collapsed.clear()
    const walk = (nodes: TreeNode[]) => {
      for (const n of nodes)
        if (n.kind === 'dir') {
          collapsed.add(n.path)
          walk(n.children)
        }
    }
    walk(tree)
  }
  const anyCollapsed = $derived(collapsed.size > 0)

  const statusMark: Record<string, string> = { added: 'A', modified: 'M', deleted: 'D', renamed: 'R' }
</script>

{#snippet counts(added: number, removed: number)}
  <span class="counts mono">
    {#if added > 0}<span class="add">+{added}</span>{/if}
    {#if removed > 0}<span class="del">−{removed}</span>{/if}
  </span>
{/snippet}

{#snippet chevron(down: boolean)}
  <svg class="chev" class:down width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6" /></svg>
{/snippet}

{#snippet row(n: TreeNode, depth: number)}
  {#if n.kind === 'dir'}
    <button class="drow" style="--d:{depth}" onclick={() => toggleDir(n.path)}>
      {@render chevron(!collapsed.has(n.path))}
      <svg class="ic" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" /></svg>
      <span class="nm">{n.name}</span>
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
      <span class="mark" data-s={f.status} title={f.status}>{statusMark[f.status] ?? 'M'}</span>
      <span class="nm">{n.name}</span>
      {@render counts(f.added, f.removed)}
    </button>
    {#if open.has(f.path)}
      {@const d = diffs.get(f.path)}
      <div class="diffwrap" style="--d:{depth}">
        {#if d === 'loading' || d === undefined}
          <p class="dnote">Loading diff…</p>
        {:else if d === 'error'}
          <p class="dnote err">Couldn't load this diff.</p>
        {:else}
          <FileDiffView diff={d} />
        {/if}
      </div>
    {/if}
  {/if}
{/snippet}

<!-- Always present in a git repo, at the end of the chat: the full tree when
     the agent changed files, a quiet clean state otherwise. A non-git session
     has nothing to review, so it shows nothing at all. -->
{#if data?.repo}
  {@const has = data.files.length > 0}
  <div class="changes" class:empty={!has}>
    <div class="chead">
      <div class="ctitle">
        <span class="eyebrow">Changed files</span>
        {#if has}
          <span class="n mono">{data.files.length}</span>
          {@render counts(data.added, data.removed)}
        {:else}
          <span class="clean">Clean</span>
        {/if}
      </div>
      <div class="cbtns">
        {#if has}
          <button class="tbtn" onclick={collapseAll} disabled={anyCollapsed && collapsed.size >= tree.length}>Collapse all</button>
        {/if}
        <button class="tbtn" onclick={load} aria-label="Refresh" title="Refresh">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 11-3-6.7" /><path d="M21 3v5h-5" /></svg>
        </button>
      </div>
    </div>

    {#if has}
      <div class="body">
        <div class="tree">
          {#each tree as n (n.kind === 'dir' ? n.path : n.file.path)}
            {@render row(n, 0)}
          {/each}
        </div>
        {#if data.truncated}<p class="state sub">Showing the first {data.files.length} files.</p>{/if}
      </div>
    {:else}
      <p class="cleanmsg">No uncommitted changes — the working tree matches the last commit.</p>
    {/if}
  </div>
{/if}

<style>
  /* An inline card at the end of the conversation, like a tool card but wider:
     it holds the review where the work finished. */
  .changes {
    display: flex;
    flex-direction: column;
    margin-top: 6px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg-raised);
    overflow: hidden;
  }
  .chead {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    padding: 9px 12px;
    border-bottom: 1px solid var(--border);
  }
  /* Clean state: one quiet row, no divider or tree beneath it. */
  .changes.empty .chead {
    border-bottom: none;
  }
  .clean {
    font-size: 11.5px;
    color: var(--text-4);
  }
  .cleanmsg {
    margin: 0;
    padding: 0 12px 11px;
    font-size: 12px;
    color: var(--text-4);
  }
  .ctitle {
    display: flex;
    align-items: center;
    gap: 9px;
    min-width: 0;
  }
  .n {
    font-size: 12px;
    color: var(--text-3);
  }
  .cbtns {
    display: flex;
    align-items: center;
    gap: 4px;
  }
  .tbtn {
    display: flex;
    align-items: center;
    gap: 6px;
    height: 28px;
    padding: 0 10px;
    border-radius: var(--r-sm);
    color: var(--text-3);
    font-size: 12px;
    font-weight: 500;
  }
  .tbtn:hover {
    color: var(--text);
    background: var(--panel-2);
  }
  .tbtn:disabled {
    opacity: 0.4;
  }

  .body {
    max-height: 460px;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    padding: 6px 8px 8px;
  }
  .tree {
    display: flex;
    flex-direction: column;
  }
  .drow,
  .frow {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    text-align: left;
    padding: 6px 8px;
    padding-left: calc(8px + var(--d) * 15px);
    border-radius: var(--r-sm);
    font-size: 13px;
    color: var(--text-2);
  }
  .drow:hover,
  .frow:hover {
    background: var(--panel);
    color: var(--text);
  }
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
  .ic {
    flex: none;
    color: var(--text-4);
  }
  .drow:hover .ic {
    color: var(--text-3);
  }
  /* A file's change kind as a single muted letter, not a colour block. */
  .mark {
    flex: none;
    width: 16px;
    text-align: center;
    font-family: var(--mono);
    font-size: 11px;
    color: var(--text-4);
  }
  .mark[data-s='added'] {
    color: var(--live);
  }
  .mark[data-s='deleted'] {
    color: var(--alert);
  }
  .mark[data-s='modified'],
  .mark[data-s='renamed'] {
    color: var(--busy);
  }
  .nm {
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
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
  .diffwrap {
    padding: 3px 0 8px;
    padding-left: calc(8px + var(--d) * 15px + 8px);
  }
  .state {
    margin: 18px 8px;
    font-size: 13px;
    color: var(--text-3);
    line-height: 1.55;
  }
  .state.sub {
    margin: 8px;
    font-size: 12px;
    color: var(--text-4);
  }
  .dnote.err {
    color: var(--alert);
  }
  .dnote {
    margin: 0;
    padding: 8px 4px;
    font-size: 12px;
    color: var(--text-4);
  }
</style>
