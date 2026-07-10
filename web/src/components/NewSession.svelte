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

<div class="screen">
  <header>
    <button class="back" onclick={() => (app.view = 'list')} aria-label="Back">‹</button>
    <div class="head">
      <span class="label">select project</span>
      <span class="here mono">{listing?.path ?? '…'}</span>
    </div>
  </header>
  <div class="wire"></div>

  <div class="scroll">
    {#if error}<p class="err mono">! {error}</p>{/if}
    {#if listing}
      <ul>
        {#if listing.parent}
          <li>
            <button class="row up mono" onclick={() => go(listing!.parent)}>
              <span class="ic">↑</span> ..
            </button>
          </li>
        {/if}
        {#each listing.entries.filter((e) => e.dir) as entry (entry.path)}
          <li>
            <button class="row mono" onclick={() => go(entry.path)}>
              <span class="ic">▸</span>{entry.name}
            </button>
          </li>
        {/each}
        {#each listing.entries.filter((e) => !e.dir).slice(0, 40) as entry (entry.path)}
          <li>
            <span class="row file mono"><span class="ic">·</span>{entry.name}</span>
          </li>
        {/each}
      </ul>
    {:else if loading}
      <p class="muted mono">scanning…</p>
    {/if}
  </div>

  <div class="footer">
    <button class="start" onclick={start} disabled={!listing || creating}>
      {creating ? 'starting claude…' : 'start here'}
    </button>
  </div>
</div>

<style>
  .screen {
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: calc(var(--safe-top) + 15px) 14px 12px;
  }
  .back {
    font-size: 28px;
    line-height: 1;
    color: var(--ink-dim);
    padding: 0 6px 4px;
  }
  .head {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .here {
    font-size: 12px;
    color: var(--wire);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    text-align: left;
  }
  .wire {
    height: 2px;
    background: linear-gradient(90deg, transparent, var(--wire-dim) 40%, var(--wire-dim) 60%, transparent);
    flex: none;
  }
  .scroll {
    flex: 1;
    overflow-y: auto;
    padding: 8px;
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .row {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 11px;
    text-align: left;
    padding: 12px 12px;
    border-radius: var(--r-sm);
    color: var(--ink);
    font-size: 13.5px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .row:active {
    background: var(--bg-2);
  }
  .file {
    color: var(--ink-faint);
  }
  .up {
    color: var(--wire);
  }
  .ic {
    color: var(--wire);
    width: 12px;
    text-align: center;
    flex: none;
  }
  .file .ic {
    color: var(--ink-faint);
  }
  .footer {
    padding: 12px 16px calc(var(--safe-bottom) + 14px);
    border-top: 1px solid var(--line);
  }
  .start {
    width: 100%;
    background: var(--wire);
    color: #06222a;
    font-weight: 650;
    font-size: 15px;
    border-radius: var(--r);
    padding: 15px;
  }
  .start:disabled {
    opacity: 0.5;
  }
  .err {
    color: var(--stop);
    font-size: 12.5px;
    padding: 8px 12px;
  }
  .muted {
    color: var(--ink-faint);
    padding: 12px;
  }
</style>
