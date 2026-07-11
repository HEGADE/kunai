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
          <button class="row up" onclick={() => go(listing!.parent)}>
            <span class="ic">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5M5 12l7-7 7 7" /></svg>
            </span>
            <span class="nm">Up one level</span>
          </button>
        {/if}
        {#each listing.entries.filter((e) => e.dir) as entry (entry.path)}
          <button class="row" onclick={() => go(entry.path)}>
            <span class="ic">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" /></svg>
            </span>
            <span class="nm">{entry.name}</span>
            <span class="chev">›</span>
          </button>
        {/each}
        {#each listing.entries.filter((e) => !e.dir).slice(0, 8) as entry (entry.path)}
          <span class="row file"><span class="ic"></span><span class="nm">{entry.name}</span></span>
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
    padding: 9px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    unicode-bidi: plaintext;
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
    gap: 11px;
    text-align: left;
    padding: 11px 11px;
    border-radius: var(--r-sm);
    color: var(--text);
  }
  .nm {
    flex: 1;
    min-width: 0;
    font-size: 14px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .chev {
    flex: none;
    color: var(--text-4);
    font-size: 15px;
  }
  button.row:hover,
  button.row:active {
    background: var(--panel-2);
  }
  .file {
    color: var(--text-4);
  }
  .file .nm {
    font-size: 13px;
  }
  .up .nm {
    color: var(--text-2);
    font-size: 13.5px;
  }
  .ic {
    color: var(--text-3);
    width: 14px;
    display: inline-flex;
    justify-content: center;
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
