<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { createSession } from '../lib/api'
  import { enablePush, pushState } from '../lib/push'
  import type { TaggedHistoryEntry, TaggedMeta } from '../lib/types'
  import { sessionStatus } from '../lib/sessionStatus'
  import StatusBadge from './StatusBadge.svelte'
  import Wordmark from './Wordmark.svelte'
  import Home from './Home.svelte'
  import SessionMenu from './SessionMenu.svelte'

  // A row prefers its live connection, which knows about a dropped socket and a
  // failed turn, and falls back to the polled metadata for sessions that are not
  // open as tabs. Both go through the same resolver so the two never disagree.
  function statusFor(m: TaggedMeta) {
    const c = app.connFor({ machineId: m.machineId, id: m.id })
    if (!c) return sessionStatus({ state: m.state })
    return sessionStatus({
      state: c.sessionState,
      online: c.status === 'online',
      errored: c.errorLine !== '',
    })
  }

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
  {@const st = statusFor(m)}
  <div class="row" class:current={app.activeId === m.id && app.activeMachineId === m.machineId}>
    <button class="hit" onclick={() => app.open(m.machineId, m.id)}>
      <span class="ic">{@render bubble()}</span>
      <span class="name">{shortName(m)}</span>
      <StatusBadge status={st} />
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

  <!-- Search and machine scope share one hairline bar: both narrow the session
       list, so they read as one control rather than two stacked pills. The
       scope shows the current machine as mono data with its status dot, and
       only appears when there's more than one machine to choose between. -->
  <div class="filterbar">
    <div class="fbar">
      <svg class="mag" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="7" /><path d="M21 21l-4.3-4.3" /></svg>
      <input type="search" placeholder="Search sessions" bind:value={q} autocomplete="off" />
      {#if multi}
        <button class="scope" onclick={() => (filterOpen = !filterOpen)} aria-label="Filter by machine">
          <span class="fdot" class:live={currentFilter?.online}></span>
          <span class="mlabel mono">{currentFilter ? currentFilter.label : 'All'}</span>
          <svg class="chev" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6" /></svg>
        </button>
      {/if}
    </div>
    {#if multi && filterOpen}
      <button class="fscrim" onclick={() => (filterOpen = false)} aria-label="Close"></button>
      <div class="fpop">
        <button class="fopt" class:on={app.machineFilter === 'all'} onclick={() => pickFilter('all')}>
          <span class="fdot"></span>
          <span class="mlabel mono">All machines</span>
          <span class="fcount">{app.sessions.length}</span>
        </button>
        {#each app.machines as m (m.id)}
          <button class="fopt" class:on={app.machineFilter === m.id} onclick={() => pickFilter(m.id)} title={m.url}>
            <span class="fdot" class:live={m.online}></span>
            <span class="mlabel mono">{m.label}</span>
            {#if activeCount(m.id)}<span class="fcount">{activeCount(m.id)}</span>{/if}
          </button>
        {/each}
      </div>
    {/if}
  </div>

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
    <button class="navitem" onclick={() => app.openChannels()}>
      <span class="ic">
        <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.4 8.4 0 01-9 8.4 8.5 8.5 0 01-3.9-.9L3 20.5l1.5-4.4a8.4 8.4 0 01-.9-3.9 8.5 8.5 0 018.4-8.7h.5a8.5 8.5 0 018.5 8.5z" /></svg>
      </span>
      Channels
    </button>
    <button class="navitem" onclick={() => app.openAccounts()}>
      <span class="ic">
        <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M16 21v-2a4 4 0 00-4-4H6a4 4 0 00-4 4v2" /><circle cx="9" cy="7" r="4" /><path d="M22 21v-2a4 4 0 00-3-3.87" /><path d="M16 3.13a4 4 0 010 7.75" /></svg>
      </span>
      Accounts
    </button>
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
  /* Search and machine scope in one hairline bar — boxy like the app's cards,
     not a candy pill, and one row instead of two so the list gets the space. */
  .filterbar {
    position: relative;
    padding: 4px 14px 6px;
  }
  .fbar {
    display: flex;
    align-items: center;
    gap: 9px;
    height: 38px;
    padding: 0 5px 0 12px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r);
    color: var(--text-4);
  }
  .fbar:focus-within {
    border-color: var(--border-2);
  }
  .mag {
    flex: none;
    color: var(--text-4);
  }
  .fbar:focus-within .mag {
    color: var(--text-3);
  }
  .fbar input {
    flex: 1;
    min-width: 0;
    background: none;
    border: none;
    outline: none;
    padding: 0;
    font-size: 13.5px;
    color: var(--text);
  }
  .fbar input::placeholder {
    color: var(--text-4);
  }
  .fbar input::-webkit-search-cancel-button {
    -webkit-appearance: none;
  }
  /* The scope chip: the current machine as mono data behind a hairline rule. */
  .scope {
    flex: none;
    display: flex;
    align-items: center;
    gap: 6px;
    height: 24px;
    padding: 0 7px 0 10px;
    margin-left: 1px;
    border-left: 1px solid var(--border);
    color: var(--text-3);
    font-size: 12px;
  }
  .scope:hover {
    color: var(--text);
  }
  .mlabel {
    max-width: 82px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .chev {
    flex: none;
    color: var(--text-4);
  }
  .fdot {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .fdot.live {
    background: var(--live);
  }
  .fscrim {
    position: fixed;
    inset: 0;
    z-index: 30;
  }
  .fpop {
    position: absolute;
    z-index: 31;
    top: calc(100% - 4px);
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
  .fopt:hover,
  .fopt.on {
    background: var(--panel-3);
    color: var(--text);
  }
  .fopt .mlabel {
    flex: 1;
    max-width: none;
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
    /* Without min-height:0 a flex scroll-child can refuse to shrink and overflow
       under the footer (seen on iOS): the session rows then bleed through the
       Settings row. This lets the list scroll within its track instead. */
    min-height: 0;
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
  .name {
    flex: 1;
    min-width: 0;
    font-size: 14.5px;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    -webkit-mask-image: linear-gradient(to right, #000 calc(100% - 30px), transparent);
    mask-image: linear-gradient(to right, #000 calc(100% - 30px), transparent);
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
  /* Opaque and never-shrinking, sitting above the list: even if the scroll area
     ever runs long on a stubborn browser, the footer covers it cleanly instead
     of letting a session row show through Settings. */
  .foot {
    flex: none;
    position: relative;
    z-index: 2;
    background: var(--bg);
    padding: 8px 16px calc(var(--safe-bottom) + 12px);
  }
  @media (min-width: 861px) {
    .foot {
      background: var(--bg-raised);
    }
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
