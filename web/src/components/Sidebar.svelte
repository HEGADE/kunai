<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { createSession } from '../lib/api'
  import { enablePush, pushState } from '../lib/push'
  import type { TaggedHistoryEntry, TaggedMeta } from '../lib/types'
  import Wordmark from './Wordmark.svelte'
  import Home from './Home.svelte'
  import SessionMenu from './SessionMenu.svelte'

  let notif = $state(pushState())
  let notifHint = $state('')
  let resuming = $state('')
  let q = $state('')

  const query = $derived(q.trim().toLowerCase())
  const multi = $derived(app.machines.length > 1)
  function machineLabel(id: string): string {
    return app.machines.find((m) => m.id === id)?.label || id
  }
  const inFilter = (mid: string) => app.machineFilter === 'all' || app.machineFilter === mid
  const activeList = $derived(
    app.sessions.filter(
      (m) =>
        inFilter(m.machineId) &&
        (!query ||
          shortName(m).toLowerCase().includes(query) ||
          m.cwd.toLowerCase().includes(query) ||
          machineLabel(m.machineId).toLowerCase().includes(query)),
    ),
  )
  const recentList = $derived(
    app.history.filter(
      (h) =>
        inFilter(h.machineId) &&
        (!query ||
          h.title.toLowerCase().includes(query) ||
          h.cwd.toLowerCase().includes(query) ||
          machineLabel(h.machineId).toLowerCase().includes(query)),
    ),
  )
  // Pinned sessions rise to the top in their own section, drawn from both the
  // live list and Recent (an id is in exactly one). They keep their own kind, so
  // a pinned live session still opens and a pinned past one still resumes.
  const pinnedActive = $derived(activeList.filter((m) => m.pinned))
  const pinnedRecent = $derived(recentList.filter((h) => h.pinned))
  const hasPinned = $derived(pinnedActive.length > 0 || pinnedRecent.length > 0)
  const activeUnpinned = $derived(activeList.filter((m) => !m.pinned))
  const recentUnpinned = $derived(recentList.filter((h) => !h.pinned))
  // Keep the sidebar tidy: show only the most recent few; the rest live behind
  // "View all sessions" (a full, searchable, paginated view).
  const RECENT_MAX = 8
  const recentDisplay = $derived(recentUnpinned.slice(0, RECENT_MAX))
  function activeCount(mid: string): number {
    return app.sessions.filter((m) => m.machineId === mid).length
  }

  let filterOpen = $state(false)
  const currentFilter = $derived(
    app.machineFilter === 'all' ? null : app.machines.find((m) => m.id === app.machineFilter),
  )
  function pickFilter(id: string) {
    app.machineFilter = id
    filterOpen = false
  }

  async function toggleNotif() {
    if (notif === 'granted') return
    const err = await enablePush()
    notifHint = err
    notif = pushState()
    setTimeout(() => (notifHint = ''), err ? 5000 : 100)
  }

  function shortName(m: TaggedMeta): string {
    return m.title || m.cwd.replace(/\/+$/, '').split('/').slice(-1)[0] || 'session'
  }
  async function resume(h: TaggedHistoryEntry) {
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
      app.refresh()
    } catch (e) {
      notifHint = (e as Error).message
      setTimeout(() => (notifHint = ''), 5000)
    } finally {
      resuming = ''
    }
  }
</script>

{#snippet gear()}
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3.2" /><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 11-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 11-2.83-2.83l.06-.06A1.65 1.65 0 004.6 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 112.83-2.83l.06.06A1.65 1.65 0 009 4.6a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 112.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z" /></svg>
{/snippet}

{#snippet railIcon()}
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2.5" /><path d="M9.5 4v16" /></svg>
{/snippet}

{#snippet newChat()}
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 11.5a8.5 8.5 0 01-8.5 8.5 8.38 8.38 0 01-3.8-.9L3 21l1.9-4.7a8.38 8.38 0 01-.9-3.8A8.5 8.5 0 0112.5 4" /><path d="M18.5 3v5M21 5.5h-5" /></svg>
{/snippet}

{#snippet bubble()}
  <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 01-.9 3.8 8.5 8.5 0 01-7.6 4.7 8.38 8.38 0 01-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 01-.9-3.8 8.5 8.5 0 014.7-7.6 8.38 8.38 0 013.8-.9h.5a8.48 8.48 0 018 8v.5z" /></svg>
{/snippet}

{#snippet activeRow(m: TaggedMeta)}
  <div class="row" class:current={app.activeId === m.id && app.activeMachineId === m.machineId}>
    <button class="hit" onclick={() => app.open(m.machineId, m.id)}>
      <span class="ic">
        {@render bubble()}
        <span class="live" data-state={m.state}></span>
      </span>
      <span class="name">{shortName(m)}</span>
    </button>
    <SessionMenu machineId={m.machineId} id={m.id} title={shortName(m)} pinned={m.pinned} kind="live" />
  </div>
{/snippet}

{#snippet recentRow(h: TaggedHistoryEntry)}
  <div class="row">
    <button class="hit" onclick={() => resume(h)} disabled={!!resuming}>
      <span class="ic">{@render bubble()}</span>
      <span class="name">{resuming === h.id ? 'Resuming…' : h.title}</span>
    </button>
    <SessionMenu machineId={h.machineId} id={h.id} title={h.title} pinned={h.pinned} kind="recent" />
  </div>
{/snippet}

<div class="sb">
  <header>
    <Wordmark size={17} />
    <div class="actions">
      <button
        class="icon deskonly"
        onclick={() => app.toggleSidebar()}
        aria-label="Collapse sidebar"
        title="Collapse sidebar"
      >
        {@render railIcon()}
      </button>
      <button class="add" onclick={() => app.newSession()} aria-label="New chat" title="New chat">
        {@render newChat()}
      </button>
    </div>
  </header>

  <div class="searchwrap">
    <div class="search">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="7" /><path d="M21 21l-4.3-4.3" /></svg>
      <input type="search" placeholder="Search sessions" bind:value={q} autocomplete="off" />
    </div>
  </div>

  {#if multi}
    <div class="mfilterwrap">
      <button class="mfilter" onclick={() => (filterOpen = !filterOpen)}>
        {#if currentFilter}
          <span class="fdot" class:live={currentFilter.online}></span>
          <span class="flabel">{currentFilter.label}</span>
        {:else}
          <svg class="fico" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"><rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 20h8M12 16v4" /></svg>
          <span class="flabel">All machines</span>
        {/if}
        <svg class="fchev" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6" /></svg>
      </button>
      {#if filterOpen}
        <button class="fscrim" onclick={() => (filterOpen = false)} aria-label="Close"></button>
        <div class="fpop">
          <button class="fopt" class:on={app.machineFilter === 'all'} onclick={() => pickFilter('all')}>
            <svg class="fico" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"><rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 20h8M12 16v4" /></svg>
            <span class="flabel">All machines</span>
            <span class="fcount">{app.sessions.length}</span>
          </button>
          {#each app.machines as m (m.id)}
            <button class="fopt" class:on={app.machineFilter === m.id} onclick={() => pickFilter(m.id)} title={m.url}>
              <span class="fdot" class:live={m.online}></span>
              <span class="flabel">{m.label}</span>
              {#if activeCount(m.id)}<span class="fcount">{activeCount(m.id)}</span>{/if}
            </button>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <div class="list">
    <div class="homewrap"><Home compact /></div>

    {#if app.listError}
      <p class="note mono">{app.listError}</p>
    {/if}

    {#if hasPinned}
      <div class="sec">Pinned</div>
      {#each pinnedActive as m (m.machineId + ':' + m.id)}
        {@render activeRow(m)}
      {/each}
      {#each pinnedRecent as h (h.machineId + ':' + h.id)}
        {@render recentRow(h)}
      {/each}
    {/if}

    {#if activeUnpinned.length > 0}
      <div class="sec">Active</div>
      {#each activeUnpinned as m (m.machineId + ':' + m.id)}
        {@render activeRow(m)}
      {/each}
    {/if}

    {#if recentDisplay.length > 0}
      <div class="sec">Recent</div>
      {#each recentDisplay as h (h.machineId + ':' + h.id)}
        {@render recentRow(h)}
      {/each}
    {/if}

    {#if app.history.length > 0}
      <button class="viewall" onclick={() => app.openAllSessions()}>
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7" rx="1.5" /><rect x="14" y="3" width="7" height="7" rx="1.5" /><rect x="3" y="14" width="7" height="7" rx="1.5" /><rect x="14" y="14" width="7" height="7" rx="1.5" /></svg>
        View all sessions
      </button>
    {/if}

    {#if activeList.length === 0 && recentList.length === 0 && !app.listError}
      <div class="empty">
        <p class="e1">{query ? 'No matches' : 'No sessions yet'}</p>
        <p class="e2">
          {query ? 'Try a different search.' : 'Start one in any project directory on your machine.'}
        </p>
      </div>
    {/if}
  </div>

  <!-- Only surface the notification control while it still needs action. Once
       granted it's the desired steady state, so the persistent bar just eats
       session-list space and is dropped. -->
  <div class="foot">
    {#if notif !== 'unsupported' && notif !== 'granted'}
      {#if notifHint}<p class="hint">{notifHint}</p>{/if}
      <button class="notif" onclick={toggleNotif}>
        <span class="ndot"></span>
        Enable notifications
      </button>
    {/if}
    <button class="navitem" onclick={() => app.openSettings()}>
      <span class="ic">{@render gear()}</span>
      Settings
    </button>
  </div>
</div>

<style>
  .sb {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }
  @media (min-width: 861px) {
    .sb {
      background: var(--bg-raised);
    }
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: calc(var(--safe-top) + 18px) 20px 14px;
  }
  .actions {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .add,
  .icon {
    width: 34px;
    height: 34px;
    border-radius: 50%;
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-2);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .icon {
    background: none;
    border-color: transparent;
    color: var(--text-3);
  }
  .add:hover,
  .icon:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .icon:hover {
    background: var(--panel);
  }
  .searchwrap {
    padding: 0 14px 4px;
  }
  .search {
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
    padding: 10px 0;
    font-size: 14px;
    color: var(--text);
  }
  .search input::placeholder {
    color: var(--text-4);
  }
  .search input::-webkit-search-cancel-button {
    -webkit-appearance: none;
  }
  /* Machine filter: a dropdown so it stays one line no matter how many
     machines join the fleet. */
  .mfilterwrap {
    position: relative;
    padding: 8px 14px 4px;
  }
  .mfilter {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 9px 12px;
    border-radius: 100px;
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-2);
    font-size: 13px;
    font-weight: 500;
  }
  .mfilter:hover {
    border-color: var(--border-2);
    color: var(--text);
  }
  .fchev {
    flex: none;
    color: var(--text-4);
  }
  .flabel {
    flex: 1;
    min-width: 0;
    text-align: left;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .fico,
  .fdot {
    flex: none;
  }
  .fdot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .fdot.live {
    background: var(--live);
  }
  .fico {
    color: var(--text-4);
  }
  .fscrim {
    position: fixed;
    inset: 0;
    z-index: 30;
  }
  .fpop {
    position: absolute;
    z-index: 31;
    top: calc(100% - 2px);
    left: 14px;
    right: 14px;
    padding: 5px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
    max-height: 60vh;
    overflow-y: auto;
  }
  .fopt {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 9px 10px;
    border-radius: var(--r-sm);
    color: var(--text-2);
    font-size: 13px;
  }
  .fopt:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .fopt.on {
    color: var(--text);
    background: var(--panel-3);
  }
  .fcount {
    flex: none;
    padding: 1px 7px;
    border-radius: 100px;
    background: var(--bg);
    color: var(--text-3);
    font-size: 11px;
  }
  .list {
    flex: 1;
    overflow-y: auto;
    padding: 4px 14px 14px;
  }
  /* The dashboard lives in the main pane on desktop; on phones the sidebar IS
     the home screen, so it renders here. */
  .homewrap {
    display: none;
  }
  @media (max-width: 860px) {
    .homewrap {
      display: block;
      padding: 8px 2px 16px;
    }
  }
  .sec {
    font-size: 11.5px;
    font-weight: 550;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-4);
    padding: 12px 6px 8px;
  }
  .note {
    color: var(--text-3);
    font-size: 12.5px;
    padding: 10px;
  }
  /* Sessions as single-line rows: a chat-bubble icon + the title, nothing else.
     Long titles fade at the right edge; the open one highlights. */
  .row {
    position: relative;
    border-radius: var(--r);
  }
  .row:hover {
    background: var(--panel);
  }
  .row.current {
    background: var(--panel-2);
  }
  .hit {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 11px;
    text-align: left;
    padding: 8px 10px;
  }
  .hit:disabled {
    opacity: 0.55;
  }
  .ic {
    position: relative;
    flex: none;
    display: inline-flex;
    color: var(--text-4);
  }
  .row:hover .ic,
  .row.current .ic {
    color: var(--text-3);
  }
  /* Small presence dot on the icon for live sessions. */
  .live {
    position: absolute;
    right: -3px;
    top: -3px;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    border: 2px solid var(--bg);
    background: var(--text-4);
  }
  .row:hover .live {
    border-color: var(--panel);
  }
  .row.current .live {
    border-color: var(--panel-2);
  }
  .live[data-state='idle'] {
    background: var(--live);
  }
  .live[data-state='starting'],
  .live[data-state='running'] {
    background: var(--busy);
    animation: soften 1.6s ease-in-out infinite;
  }
  .live[data-state='awaiting_permission'] {
    background: var(--busy);
  }
  @keyframes soften {
    50% {
      opacity: 0.4;
    }
  }
  .name {
    flex: 1;
    min-width: 0;
    font-size: 14.5px;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    -webkit-mask-image: linear-gradient(to right, #000 calc(100% - 22px), transparent);
    mask-image: linear-gradient(to right, #000 calc(100% - 22px), transparent);
  }
  .row:hover .name,
  .row.current .name {
    color: var(--text);
  }
  .row.current .name {
    font-weight: 500;
  }
  /* The per-row menu (SessionMenu) lives where the close button used to; reveal
     its trigger on row hover, matching the old close affordance. */
  .row:hover :global(.trigger) {
    opacity: 1;
  }
  /* A row whose menu is open stays highlighted, and is lifted into its own
     stacking layer so its dropdown paints above the rows below it (they are
     positioned too, so without this they'd cover the menu). */
  .row:has(:global(.wrap.open)) {
    background: var(--panel);
    z-index: 20;
  }
  .viewall {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 11px;
    padding: 9px 10px;
    margin-top: 2px;
    border-radius: var(--r);
    color: var(--text-3);
    font-size: 13.5px;
    font-weight: 500;
  }
  .viewall svg {
    flex: none;
    color: var(--text-4);
  }
  .viewall:hover {
    background: var(--panel);
    color: var(--text);
  }
  .viewall:hover svg {
    color: var(--text-3);
  }
  .empty {
    text-align: center;
    margin-top: 24vh;
    padding: 0 30px;
  }
  .e1 {
    font-size: 15px;
    font-weight: 550;
    color: var(--text);
    margin: 0 0 5px;
  }
  .e2 {
    font-size: 13.5px;
    color: var(--text-3);
    margin: 0;
    line-height: 1.55;
  }
  .navitem {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 8px 10px;
    border-radius: var(--r-sm);
    font-size: 13.5px;
    color: var(--text-2);
  }
  .navitem:hover {
    color: var(--text);
    background: var(--panel);
  }
  .navitem .ic {
    display: flex;
    color: var(--text-3);
  }
  .navitem:hover .ic {
    color: var(--text-2);
  }
  .foot {
    padding: 8px 16px calc(var(--safe-bottom) + 12px);
  }
  .hint {
    margin: 0 2px 8px;
    font-size: 12px;
    color: var(--text-3);
    line-height: 1.5;
  }
  .notif {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 8px;
    color: var(--text-4);
    font-size: 12px;
    border-radius: var(--r-sm);
  }
  .notif:hover {
    color: var(--text-2);
  }
  .ndot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .ndot.on {
    background: var(--live);
  }
</style>
