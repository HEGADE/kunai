<script lang="ts">
  import { onMount } from 'svelte'
  import { app } from '../lib/app.svelte'
  import { createSession } from '../lib/api'
  import type { TaggedHistoryEntry } from '../lib/types'

  const PAGE = 40

  let entries = $state<TaggedHistoryEntry[]>([])
  let loading = $state(true)
  let q = $state('')
  let page = $state(0)
  let machineFilter = $state('all')
  let resuming = $state('')

  const multi = $derived(app.machines.length > 1)
  function machineLabel(id: string): string {
    return app.machines.find((m) => m.id === id)?.label || id
  }

  const query = $derived(q.trim().toLowerCase())
  const filtered = $derived(
    entries.filter(
      (h) =>
        (machineFilter === 'all' || h.machineId === machineFilter) &&
        (!query ||
          h.title.toLowerCase().includes(query) ||
          h.cwd.toLowerCase().includes(query) ||
          machineLabel(h.machineId).toLowerCase().includes(query)),
    ),
  )
  const pageCount = $derived(Math.max(1, Math.ceil(filtered.length / PAGE)))
  const clampedPage = $derived(Math.min(page, pageCount - 1))
  const pageItems = $derived(filtered.slice(clampedPage * PAGE, clampedPage * PAGE + PAGE))

  // Reset to the first page whenever the search or machine filter changes.
  $effect(() => {
    query
    machineFilter
    page = 0
  })

  onMount(() => {
    app.loadAllHistory().then((e) => {
      entries = e
      loading = false
    })
  })

  function baseName(cwd: string): string {
    return cwd.replace(/\/+$/, '').split('/').slice(-1)[0] || cwd
  }
  function ago(iso: string): string {
    const t = new Date(iso).getTime()
    if (!t) return ''
    const s = Math.floor((Date.now() - t) / 1000)
    if (s < 60) return 'just now'
    const m = Math.floor(s / 60)
    if (m < 60) return `${m}m ago`
    const h = Math.floor(m / 60)
    if (h < 24) return `${h}h ago`
    const d = Math.floor(h / 24)
    if (d < 30) return `${d}d ago`
    const mo = Math.floor(d / 30)
    if (mo < 12) return `${mo}mo ago`
    return `${Math.floor(mo / 12)}y ago`
  }

  async function open(h: TaggedHistoryEntry) {
    if (resuming) return
    resuming = h.id
    try {
      const meta = await createSession(app.baseForMachine(h.machineId), {
        cwd: h.cwd,
        resume: h.id,
        title: h.title,
        cli: h.cli, // reopen on the account it belongs to
      })
      app.open(h.machineId, meta.id)
      app.closeAllSessions()
      app.refresh()
    } catch {
      resuming = ''
    }
  }
</script>

<div class="page">
  <header>
    <div class="htop">
      <h2>All sessions</h2>
      <button class="close" onclick={() => app.closeAllSessions()} aria-label="Close">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
      </button>
    </div>
    <div class="tools">
      <div class="search">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="7" /><path d="M21 21l-4.3-4.3" /></svg>
        <input type="search" placeholder="Search all sessions" bind:value={q} autocomplete="off" />
      </div>
      {#if multi}
        <select class="mfilter" bind:value={machineFilter}>
          <option value="all">All machines</option>
          {#each app.machines as m (m.id)}
            <option value={m.id}>{m.label}</option>
          {/each}
        </select>
      {/if}
    </div>
  </header>

  <div class="body">
    {#if loading}
      <p class="note mono">loading…</p>
    {:else if filtered.length === 0}
      <p class="note">{query ? 'No matches.' : 'No past sessions.'}</p>
    {:else}
      {#each pageItems as h (h.machineId + ':' + h.id)}
        <button class="row" onclick={() => open(h)} disabled={!!resuming}>
          <span class="ic">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 01-.9 3.8 8.5 8.5 0 01-7.6 4.7 8.38 8.38 0 01-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 01-.9-3.8 8.5 8.5 0 014.7-7.6 8.38 8.38 0 013.8-.9h.5a8.48 8.48 0 018 8v.5z" /></svg>
          </span>
          <span class="txt">
            <span class="title">{resuming === h.id ? 'Resuming…' : h.title}</span>
            <span class="sub mono">
              {baseName(h.cwd)}
              {#if multi}· {machineLabel(h.machineId)}{/if}
              {#if h.cli}· {h.cli}{/if}
              · {ago(h.mtime)}
            </span>
          </span>
        </button>
      {/each}
    {/if}
  </div>

  {#if !loading && filtered.length > 0}
    <footer>
      <span class="count">{filtered.length} session{filtered.length === 1 ? '' : 's'}</span>
      <span class="sp"></span>
      {#if pageCount > 1}
        <button class="pbtn" onclick={() => (page = clampedPage - 1)} disabled={clampedPage === 0}>Prev</button>
        <span class="pnum">{clampedPage + 1} / {pageCount}</span>
        <button class="pbtn" onclick={() => (page = clampedPage + 1)} disabled={clampedPage >= pageCount - 1}>Next</button>
      {/if}
    </footer>
  {/if}
</div>

<style>
  .page {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: flex;
    flex-direction: column;
    background: var(--bg);
    animation: fade 0.14s ease-out;
  }
  @keyframes fade {
    from {
      opacity: 0;
    }
  }
  header {
    flex: none;
    padding: calc(var(--safe-top) + 16px) 20px 10px;
    max-width: 760px;
    width: 100%;
    margin: 0 auto;
  }
  .htop {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 12px;
  }
  h2 {
    font-size: 17px;
    font-weight: 600;
    letter-spacing: -0.01em;
    margin: 0;
  }
  .close {
    width: 32px;
    height: 32px;
    border-radius: 50%;
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-3);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .close:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .tools {
    display: flex;
    gap: 9px;
  }
  .search {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 0 13px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 100px;
    color: var(--text-4);
  }
  .search:focus-within {
    border-color: var(--border-2);
    color: var(--text-3);
  }
  .search input {
    flex: 1;
    min-width: 0;
    background: none;
    border: none;
    outline: none;
    padding: 11px 0;
    font-size: 14px;
    color: var(--text);
  }
  .search input::placeholder {
    color: var(--text-4);
  }
  .search input::-webkit-search-cancel-button {
    -webkit-appearance: none;
  }
  .mfilter {
    flex: none;
    padding: 0 12px;
    border-radius: 100px;
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-2);
    font-size: 13px;
    outline: none;
  }
  .body {
    flex: 1;
    overflow-y: auto;
    padding: 4px 14px 14px;
    max-width: 760px;
    width: 100%;
    margin: 0 auto;
  }
  .note {
    color: var(--text-3);
    font-size: 13px;
    padding: 16px 8px;
  }
  .row {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 12px;
    text-align: left;
    padding: 10px 10px;
    border-radius: var(--r);
  }
  .row:hover {
    background: var(--panel);
  }
  .row:disabled {
    opacity: 0.55;
  }
  .ic {
    flex: none;
    display: inline-flex;
    color: var(--text-4);
  }
  .row:hover .ic {
    color: var(--text-3);
  }
  .txt {
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .title {
    font-size: 14.5px;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sub {
    font-size: 11.5px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  footer {
    flex: none;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 20px calc(var(--safe-bottom) + 14px);
    border-top: 1px solid var(--border);
    max-width: 760px;
    width: 100%;
    margin: 0 auto;
  }
  .count {
    font-size: 12.5px;
    color: var(--text-3);
  }
  .sp {
    flex: 1;
  }
  .pnum {
    font-size: 12.5px;
    color: var(--text-3);
    min-width: 44px;
    text-align: center;
  }
  .pbtn {
    padding: 7px 14px;
    border-radius: var(--r-sm);
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-2);
    font-size: 13px;
    font-weight: 500;
  }
  .pbtn:hover:not(:disabled) {
    color: var(--text);
    border-color: var(--border-2);
  }
  .pbtn:disabled {
    opacity: 0.4;
  }
</style>
