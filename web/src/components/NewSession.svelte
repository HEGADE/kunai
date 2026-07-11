<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { browse, createSession } from '../lib/api'
  import type { Listing } from '../lib/types'

  let listing = $state<Listing | null>(null)
  let loading = $state(true)
  let creating = $state(false)
  let error = $state('')
  let editing = $state(false)
  let typed = $state('')
  let crumbEl = $state<HTMLElement | null>(null)
  let pathInput = $state<HTMLInputElement | null>(null)

  // Breadcrumb segments with cumulative paths: / , home , ninja , …
  const crumbs = $derived.by(() => {
    const p = listing?.path ?? ''
    const parts = p.split('/').filter(Boolean)
    const out: { name: string; path: string }[] = [{ name: '/', path: '/' }]
    let acc = ''
    for (const part of parts) {
      acc += '/' + part
      out.push({ name: part, path: acc })
    }
    return out
  })
  const baseName = $derived(listing ? listing.path.split('/').filter(Boolean).slice(-1)[0] || '/' : '')

  // Keep the tail of the path visible as it grows.
  $effect(() => {
    listing?.path
    if (crumbEl) queueMicrotask(() => crumbEl && (crumbEl.scrollLeft = crumbEl.scrollWidth))
  })

  async function go(path: string) {
    loading = true
    error = ''
    try {
      listing = await browse(path)
      editing = false
    } catch (e) {
      error = (e as Error).message
    } finally {
      loading = false
    }
  }
  function startEdit() {
    typed = listing?.path ?? ''
    editing = true
    queueMicrotask(() => pathInput?.focus())
  }
  function onPathKey(e: KeyboardEvent) {
    if (e.key === 'Enter') go(typed.trim())
    if (e.key === 'Escape') editing = false
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
    <div class="grab" aria-hidden="true"></div>
    <header>
      <h2>New session</h2>
      <button class="close" onclick={() => app.closeNew()} aria-label="Close">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
      </button>
    </header>

    {#if app.projects.length > 0}
      <div class="quick">
        {#each app.projects as p (p.cwd)}
          <button class="qchip" title={p.cwd} onclick={() => app.quickStart(p.cwd)}>
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" /></svg>
            {p.name}
          </button>
        {/each}
      </div>
    {/if}

    <div class="pathbar">
      {#if editing}
        <input
          bind:this={pathInput}
          bind:value={typed}
          class="pathinput mono"
          spellcheck="false"
          autocomplete="off"
          autocapitalize="off"
          placeholder="/absolute/path"
          onkeydown={onPathKey}
        />
        <button class="pbtn go" onclick={() => go(typed.trim())}>Go</button>
      {:else}
        <div class="crumbs" bind:this={crumbEl}>
          {#each crumbs as c, i (c.path)}
            {#if i > 1}<span class="sep">/</span>{/if}
            <button class="crumb" class:last={i === crumbs.length - 1} onclick={() => go(c.path)}>
              {c.name}
            </button>
          {/each}
        </div>
        <button class="pbtn" onclick={startEdit} aria-label="Type a path" title="Type a path">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9" /><path d="M16.5 3.5a2.1 2.1 0 013 3L7 19l-4 1 1-4z" /></svg>
        </button>
      {/if}
    </div>

    <div class="list">
      {#if error}<p class="note err mono">{error}</p>{/if}
      {#if listing}
        {#if listing.parent}
          <button class="row up" onclick={() => go(listing!.parent)}>
            <span class="ic">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5M5 12l7-7 7 7" /></svg>
            </span>
            <span class="nm">Up one level</span>
          </button>
        {/if}
        {#each listing.entries.filter((e) => e.dir) as entry (entry.path)}
          <button class="row" onclick={() => go(entry.path)}>
            <span class="ic">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" /></svg>
            </span>
            <span class="nm">{entry.name}</span>
            <span class="chev">›</span>
          </button>
        {/each}
        {#if listing.entries.filter((e) => e.dir).length === 0}
          <p class="note">No subdirectories here.</p>
        {/if}
      {:else if loading}
        <p class="note mono">scanning…</p>
      {/if}
    </div>

    <footer>
      <button class="ghost" onclick={() => app.closeNew()}>Cancel</button>
      <button class="start" onclick={start} disabled={!listing || creating}>
        {creating ? 'Starting…' : `Start in ${baseName}`}
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
    padding: 20px;
    animation: fade 0.14s ease-out;
  }
  @keyframes fade {
    from {
      opacity: 0;
    }
  }
  .modal {
    width: 100%;
    max-width: 520px;
    max-height: min(78dvh, 660px);
    background: var(--panel);
    border: 1px solid var(--border-2);
    border-radius: var(--r-lg);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    box-shadow: 0 24px 70px -24px rgba(0, 0, 0, 0.75);
    animation: pop 0.16s ease-out;
  }
  @keyframes pop {
    from {
      transform: translateY(10px);
      opacity: 0;
    }
  }
  .grab {
    display: none;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 18px 20px 12px;
  }
  h2 {
    font-size: 16px;
    font-weight: 600;
    letter-spacing: -0.01em;
    margin: 0;
  }
  .close {
    width: 30px;
    height: 30px;
    border-radius: 50%;
    background: var(--panel-2);
    border: 1px solid var(--border);
    color: var(--text-3);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .close:hover {
    color: var(--text);
  }
  .quick {
    display: flex;
    gap: 7px;
    overflow-x: auto;
    padding: 0 20px 12px;
    scrollbar-width: none;
  }
  .quick::-webkit-scrollbar {
    display: none;
  }
  .qchip {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 8px 13px;
    border-radius: 100px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    color: var(--text-2);
    font-size: 13px;
    font-weight: 500;
  }
  .qchip:hover {
    color: var(--text);
    border-color: var(--border-2);
  }

  /* Path bar: readable breadcrumbs that scroll, never clip or ellipsize. */
  .pathbar {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 20px 10px;
    padding: 4px 6px 4px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r);
    min-height: 42px;
  }
  .crumbs {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 2px;
    overflow-x: auto;
    white-space: nowrap;
    scrollbar-width: none;
    -webkit-overflow-scrolling: touch;
  }
  .crumbs::-webkit-scrollbar {
    display: none;
  }
  .crumb {
    flex: none;
    padding: 5px 6px;
    border-radius: 6px;
    font-family: var(--mono);
    font-size: 13px;
    line-height: 1.4;
    color: var(--text-3);
  }
  .crumb:hover {
    color: var(--text);
    background: var(--panel-2);
  }
  .crumb.last {
    color: var(--text);
    font-weight: 550;
  }
  .sep {
    flex: none;
    color: var(--text-4);
    font-family: var(--mono);
    font-size: 12px;
  }
  .pathinput {
    flex: 1;
    min-width: 0;
    background: none;
    border: none;
    outline: none;
    font-size: 13.5px;
    line-height: 1.5;
    padding: 6px 2px;
    color: var(--text);
  }
  .pbtn {
    flex: none;
    height: 30px;
    min-width: 30px;
    padding: 0 8px;
    border-radius: var(--r-sm);
    color: var(--text-3);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 12.5px;
    font-weight: 550;
  }
  .pbtn:hover {
    color: var(--text);
    background: var(--panel-2);
  }
  .pbtn.go {
    background: var(--white);
    color: #0b0b0c;
  }

  .list {
    flex: 1;
    overflow-y: auto;
    padding: 2px 12px;
  }
  .row {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 11px;
    text-align: left;
    padding: 11px 10px;
    border-radius: var(--r-sm);
    color: var(--text);
  }
  .row:hover,
  .row:active {
    background: var(--panel-2);
  }
  .nm {
    flex: 1;
    min-width: 0;
    font-size: 14px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .up .nm {
    color: var(--text-2);
    font-size: 13.5px;
  }
  .chev {
    flex: none;
    color: var(--text-4);
    font-size: 15px;
  }
  .ic {
    color: var(--text-3);
    width: 16px;
    display: inline-flex;
    justify-content: center;
    flex: none;
  }
  .note {
    color: var(--text-3);
    font-size: 12.5px;
    padding: 12px;
    margin: 0;
  }
  .note.err {
    color: var(--alert);
  }
  footer {
    display: flex;
    gap: 9px;
    padding: 12px 16px 16px;
    border-top: 1px solid var(--border);
  }
  .ghost {
    padding: 12px 16px;
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
    min-width: 0;
    padding: 12px;
    border-radius: var(--r);
    background: var(--white);
    color: #0b0b0c;
    font-weight: 550;
    font-size: 14px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .start:hover {
    opacity: 0.9;
  }
  .start:disabled {
    opacity: 0.45;
  }

  /* Phone: bottom sheet. */
  @media (max-width: 640px) {
    .backdrop {
      align-items: flex-end;
      padding: 0;
    }
    .modal {
      max-width: none;
      max-height: 86dvh;
      border-radius: 20px 20px 0 0;
      border-left: none;
      border-right: none;
      border-bottom: none;
      animation: rise 0.2s ease-out;
    }
    @keyframes rise {
      from {
        transform: translateY(24px);
        opacity: 0;
      }
    }
    .grab {
      display: block;
      width: 38px;
      height: 4px;
      border-radius: 3px;
      background: var(--border-2);
      margin: 10px auto 0;
      flex: none;
    }
    header {
      padding-top: 10px;
    }
    footer {
      padding-bottom: calc(var(--safe-bottom) + 14px);
    }
  }
</style>
