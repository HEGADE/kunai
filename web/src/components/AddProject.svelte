<script lang="ts">
  import { browse } from '../lib/api'
  import type { ChatConnection } from '../lib/chat.svelte'
  import type { Listing } from '../lib/types'

  // Picking a second codebase for a session already in progress, so it stays in
  // the chat rather than sending you somewhere else to do it.
  let { chat, onClose }: { chat: ChatConnection; onClose: () => void } = $props()

  let listing = $state<Listing | null>(null)
  let loading = $state(true)
  let error = $state('')

  async function go(path: string) {
    loading = true
    error = ''
    try {
      listing = await browse(chat.origin, path)
    } catch (e) {
      error = (e as Error).message
    } finally {
      loading = false
    }
  }
  // Start beside the session's own project: the next one is usually a sibling.
  $effect(() => {
    go(chat.cwd.replace(/\/[^/]+\/?$/, '') || chat.cwd)
  })

  const dirs = $derived(listing?.entries.filter((e) => e.dir) ?? [])
  const already = (p: string) => chat.projects.some((x) => x.path === p) || p === chat.cwd

  // The path is the material here, so it leads: a segmented crumb where each
  // parent is a jump target and the leaf — the folder Add would take — is white.
  const crumbs = $derived.by(() => {
    const parts = (listing?.path ?? '').split('/').filter(Boolean)
    let acc = ''
    return parts.map((name, i) => {
      acc += '/' + name
      return { name, path: acc, leaf: i === parts.length - 1 }
    })
  })
  const leaf = $derived(crumbs.at(-1)?.name ?? listing?.path ?? '')

  function add() {
    if (!listing || already(listing.path)) return
    chat.addProject(listing.path)
    onClose()
  }
</script>

<div class="sheet">
  <div class="head">
    <span class="title">Add a project</span>
    <button class="x" onclick={onClose} aria-label="Close">✕</button>
  </div>
  <p class="lede">Claude gets its layout and path for context. Nothing is read until it needs it.</p>

  <nav class="path mono" aria-label="Current path">
    {#if listing}
      <button class="sep root" onclick={() => go('/')} aria-label="Root">/</button>
      {#each crumbs as c, i (c.path)}
        {#if i > 0}<span class="sep">/</span>{/if}
        {#if c.leaf}
          <span class="seg leaf">{c.name}</span>
        {:else}
          <button class="seg" onclick={() => go(c.path)}>{c.name}</button>
        {/if}
      {/each}
    {:else}
      <span class="seg">…</span>
    {/if}
  </nav>

  <div class="list">
    {#if error}
      <p class="err">{error}</p>
    {:else if loading && !listing}
      <p class="dim">Loading…</p>
    {:else if listing}
      {#if listing.parent}
        <button class="row up" onclick={() => go(listing!.parent)}>
          <span class="ic"
            ><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5M5 12l7-7 7 7" /></svg></span
          ><span class="nm">Up a level</span>
        </button>
      {/if}
      {#each dirs as d (d.path)}
        <button class="row" onclick={() => go(d.path)}>
          <span class="ic">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" /></svg>
          </span>
          <span class="nm">{d.name}</span>
          <span class="go" aria-hidden="true">→</span>
        </button>
      {/each}
      {#if dirs.length === 0}
        <p class="dim">No folders here. Go up a level, or add this one.</p>
      {/if}
    {/if}
  </div>

  <button class="add" disabled={!listing || already(listing.path)} onclick={add}>
    {#if listing && already(listing.path)}
      Already in this session
    {:else if listing}
      Add <span class="leafname mono">{leaf}</span>
    {:else}
      Add
    {/if}
  </button>
</div>

<style>
  .sheet {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 15px 17px 14px;
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .title {
    font-size: 14px;
    font-weight: 600;
    color: var(--text);
  }
  .x {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    margin: -4px -6px -4px 0;
    border-radius: 50%;
    color: var(--text-4);
    font-size: 12px;
  }
  .x:hover {
    color: var(--text);
    background: var(--panel-3);
  }
  .lede {
    margin: -6px 0 0;
    font-size: 12px;
    color: var(--text-4);
  }

  /* Path is the hero: a segmented crumb, mono, on its own rule so it reads as
     "where you are / what Add takes", not a caption. */
  .path {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 3px;
    padding: 9px 11px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    font-size: 12.5px;
  }
  .seg {
    padding: 1px 4px;
    border-radius: 5px;
    color: var(--text-3);
  }
  button.seg:hover {
    color: var(--text);
    background: var(--panel-3);
  }
  .seg.leaf {
    color: var(--text);
    font-weight: 500;
  }
  .sep {
    color: var(--text-4);
    padding: 1px 2px;
    border-radius: 5px;
  }
  button.sep.root:hover {
    color: var(--text-2);
    background: var(--panel-3);
  }

  .list {
    max-height: 232px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 1px;
    margin: 0 -5px;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 9px 9px;
    border-radius: 7px;
    color: var(--text-2);
    font-size: 13px;
    text-align: left;
  }
  .row:hover {
    background: var(--panel-2);
    color: var(--text);
  }
  .up {
    color: var(--text-3);
  }
  .ic {
    flex: none;
    display: flex;
    color: var(--text-4);
  }
  .row:hover .ic {
    color: var(--text-3);
  }
  .nm {
    min-width: 0;
    flex: 1;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .go {
    flex: none;
    color: var(--text-4);
    font-size: 13px;
    opacity: 0;
    transform: translateX(-3px);
    transition:
      opacity 0.1s,
      transform 0.1s;
  }
  .row:hover .go {
    opacity: 1;
    transform: none;
  }
  .dim,
  .err {
    margin: 0;
    padding: 12px 9px;
    font-size: 12px;
    color: var(--text-4);
  }
  .err {
    color: var(--alert);
  }

  .add {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    height: 40px;
    margin-top: 2px;
    border-radius: var(--r);
    background: var(--white);
    color: #0b0b0c;
    font-weight: 600;
    font-size: 13.5px;
  }
  .leafname {
    font-weight: 600;
    font-size: 12.5px;
  }
  .add:disabled {
    background: var(--panel-2);
    color: var(--text-4);
  }
</style>
