<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { browse, createSession } from '../lib/api'
  import type { Listing } from '../lib/types'

  let listing = $state<Listing | null>(null)
  let loading = $state(true)
  let creating = $state(false)
  let error = $state('')

  async function go(path: string) {
    loading = true
    error = ''
    try {
      listing = await browse(path)
    } catch (e) {
      error = (e as Error).message
    } finally {
      loading = false
    }
  }
  async function start() {
    if (!listing) return
    creating = true
    error = ''
    try {
      const meta = await createSession({ cwd: listing.path })
      app.open(meta.id)
    } catch (e) {
      error = (e as Error).message
      creating = false
    }
  }
  go('')
</script>

<div class="backdrop" onclick={() => app.closeNew()} role="presentation">
  <div class="modal" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
    <header>
      <h2>New session</h2>
      <button class="close" onclick={() => app.closeNew()} aria-label="Close">✕</button>
    </header>
    <div class="path mono">{listing?.path ?? '…'}</div>

    <div class="list">
      {#if error}<p class="note mono">{error}</p>{/if}
      {#if listing}
        {#if listing.parent}
          <button class="row up mono" onclick={() => go(listing!.parent)}>
            <span class="ic">↑</span> ..
          </button>
        {/if}
        {#each listing.entries.filter((e) => e.dir) as entry (entry.path)}
          <button class="row mono" onclick={() => go(entry.path)}>
            <span class="ic">▸</span>{entry.name}
          </button>
        {/each}
        {#each listing.entries.filter((e) => !e.dir).slice(0, 30) as entry (entry.path)}
          <span class="row file mono"><span class="ic"></span>{entry.name}</span>
        {/each}
      {:else if loading}
        <p class="note mono">scanning…</p>
      {/if}
    </div>

    <footer>
      <button class="ghost" onclick={() => app.closeNew()}>Cancel</button>
      <button class="start" onclick={start} disabled={!listing || creating}>
        {creating ? 'Starting…' : 'Start here'}
      </button>
    </footer>
  </div>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 50;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 16px;
    animation: fade 0.14s ease-out;
  }
  @keyframes fade {
    from {
      opacity: 0;
    }
  }
  .modal {
    width: 100%;
    max-width: 460px;
    max-height: min(76dvh, 620px);
    background: var(--panel);
    border: 1px solid var(--border-2);
    border-radius: var(--r-lg);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    box-shadow: 0 24px 70px -24px rgba(0, 0, 0, 0.75);
    animation: pop 0.15s ease-out;
  }
  @keyframes pop {
    from {
      transform: translateY(8px);
      opacity: 0;
    }
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 16px 16px 10px;
  }
  h2 {
    font-size: 15px;
    font-weight: 550;
    margin: 0;
  }
  .close {
    color: var(--text-3);
    font-size: 12px;
    padding: 6px;
  }
  .close:hover {
    color: var(--text);
  }
  .path {
    margin: 0 16px 8px;
    padding: 8px 11px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    font-size: 11.5px;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    text-align: left;
  }
  .list {
    flex: 1;
    overflow-y: auto;
    padding: 2px 8px;
  }
  .row {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 10px;
    text-align: left;
    padding: 10px 11px;
    border-radius: var(--r-sm);
    color: var(--text);
    font-size: 13px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  button.row:hover {
    background: var(--panel-2);
  }
  .file {
    color: var(--text-4);
  }
  .up {
    color: var(--text-2);
  }
  .ic {
    color: var(--text-3);
    width: 10px;
    text-align: center;
    flex: none;
  }
  .note {
    color: var(--text-3);
    font-size: 12.5px;
    padding: 12px;
  }
  footer {
    display: flex;
    gap: 9px;
    padding: 11px 16px calc(var(--safe-bottom) + 14px);
    border-top: 1px solid var(--border);
  }
  .ghost {
    padding: 11px 16px;
    border-radius: var(--r);
    background: var(--panel-2);
    border: 1px solid var(--border);
    color: var(--text-2);
    font-size: 13.5px;
  }
  .ghost:hover {
    color: var(--text);
  }
  .start {
    flex: 1;
    padding: 11px;
    border-radius: var(--r);
    background: var(--white);
    color: #0b0b0c;
    font-weight: 550;
    font-size: 14px;
  }
  .start:hover {
    opacity: 0.9;
  }
  .start:disabled {
    opacity: 0.45;
  }
</style>
