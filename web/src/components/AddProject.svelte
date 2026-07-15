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

  <div class="crumb mono" title={listing?.path}>{listing?.path ?? '…'}</div>

  <div class="list">
    {#if error}
      <p class="err">{error}</p>
    {:else if loading && !listing}
      <p class="dim">Loading…</p>
    {:else if listing}
      {#if listing.parent}
        <button class="row up" onclick={() => go(listing!.parent)}>
          <span class="ic">↑</span><span class="nm">Up a level</span>
        </button>
      {/if}
      {#each dirs as d (d.path)}
        <button class="row" onclick={() => go(d.path)}>
          <span class="ic">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" /></svg>
          </span>
          <span class="nm">{d.name}</span>
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
    {:else}
      Add {listing ? listing.path.split('/').filter(Boolean).slice(-1)[0] || listing.path : ''}
    {/if}
  </button>
</div>

<style>
  .sheet {
    display: flex;
    flex-direction: column;
    gap: 9px;
    max-width: 720px;
    width: 100%;
    margin: 0 auto;
    padding: 14px 16px 12px;
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
    width: 26px;
    height: 26px;
    border-radius: 50%;
    color: var(--text-4);
    font-size: 12px;
  }
  .x:hover {
    color: var(--text);
    background: var(--panel-3);
  }
  .lede {
    margin: -4px 0 0;
    font-size: 12px;
    color: var(--text-4);
  }
  .crumb {
    font-size: 11.5px;
    color: var(--text-3);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    unicode-bidi: plaintext;
    text-align: left;
  }
  .list {
    max-height: 210px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 4px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
  }
  .row {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 8px 9px;
    border-radius: 6px;
    color: var(--text-2);
    font-size: 13px;
    text-align: left;
  }
  .row:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .ic {
    flex: none;
    display: flex;
    color: var(--text-4);
  }
  .up .ic {
    font-size: 12px;
  }
  .nm {
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .dim,
  .err {
    margin: 0;
    padding: 10px;
    font-size: 12px;
    color: var(--text-4);
  }
  .err {
    color: var(--alert);
  }
  .add {
    height: 40px;
    border-radius: var(--r);
    background: var(--white);
    color: #0b0b0c;
    font-weight: 600;
    font-size: 13.5px;
  }
  .add:disabled {
    background: var(--panel-3);
    color: var(--text-4);
  }
</style>
