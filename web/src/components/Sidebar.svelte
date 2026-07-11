<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { closeSession, createSession } from '../lib/api'
  import { enablePush, pushState } from '../lib/push'
  import type { HistoryEntry, Meta } from '../lib/types'
  import Wordmark from './Wordmark.svelte'
  import Home from './Home.svelte'

  let notif = $state(pushState())
  let notifHint = $state('')
  let resuming = $state('')
  let q = $state('')

  const query = $derived(q.trim().toLowerCase())
  const activeList = $derived(
    app.sessions.filter(
      (m) => !query || shortName(m).toLowerCase().includes(query) || m.cwd.toLowerCase().includes(query),
    ),
  )
  const recentList = $derived(
    app.history.filter(
      (h) => !query || h.title.toLowerCase().includes(query) || h.cwd.toLowerCase().includes(query),
    ),
  )

  async function toggleNotif() {
    if (notif === 'granted') return
    const err = await enablePush()
    notifHint = err
    notif = pushState()
    setTimeout(() => (notifHint = ''), err ? 5000 : 100)
  }

  function shortName(m: Meta): string {
    return m.title || m.cwd.replace(/\/+$/, '').split('/').slice(-1)[0] || 'session'
  }
  function ago(iso: string): string {
    const s = (Date.now() - new Date(iso).getTime()) / 1000
    if (s < 90) return 'now'
    if (s < 3600) return `${Math.round(s / 60)}m ago`
    if (s < 86400) return `${Math.round(s / 3600)}h ago`
    return `${Math.round(s / 86400)}d ago`
  }
  async function remove(e: MouseEvent, m: Meta) {
    e.stopPropagation()
    await closeSession(m.id)
    if (app.activeId === m.id) app.back()
    else app.refresh()
  }
  async function resume(h: HistoryEntry) {
    if (resuming) return
    resuming = h.id
    try {
      const meta = await createSession({ cwd: h.cwd, resume: h.id, title: h.title })
      app.open(meta.id)
      app.refresh()
    } catch (e) {
      notifHint = (e as Error).message
      setTimeout(() => (notifHint = ''), 5000)
    } finally {
      resuming = ''
    }
  }
</script>

<div class="sb">
  <header>
    <Wordmark size={17} />
    <div class="actions">
      <button class="icon" onclick={() => app.openSettings()} aria-label="Settings" title="Settings">
        <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3.2" /><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 11-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 11-2.83-2.83l.06-.06A1.65 1.65 0 004.6 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 112.83-2.83l.06.06A1.65 1.65 0 009 4.6a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 112.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z" /></svg>
      </button>
      <button class="add" onclick={() => app.newSession()} aria-label="New session">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
      </button>
    </div>
  </header>

  <div class="searchwrap">
    <div class="search">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="7" /><path d="M21 21l-4.3-4.3" /></svg>
      <input type="search" placeholder="Search sessions" bind:value={q} autocomplete="off" />
    </div>
  </div>

  <div class="list">
    <div class="homewrap"><Home compact /></div>

    {#if app.listError}
      <p class="note mono">{app.listError}</p>
    {/if}

    {#if activeList.length > 0}
      <div class="sec">Active</div>
      {#each activeList as m (m.id)}
        <div class="card" class:current={app.activeId === m.id}>
          <button class="hit" onclick={() => app.open(m.id)}>
            <span class="badge">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" /></svg>
              <span class="bdot" data-state={m.state}></span>
            </span>
            <span class="meta">
              <span class="name">{shortName(m)}</span>
              <span class="sub mono">{m.cwd}</span>
            </span>
          </button>
          <button class="x" onclick={(e) => remove(e, m)} aria-label="Close session">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
          </button>
        </div>
      {/each}
    {/if}

    {#if recentList.length > 0}
      <div class="sec">Recent</div>
      {#each recentList as h (h.id)}
        <div class="card">
          <button class="hit" onclick={() => resume(h)} disabled={!!resuming}>
            <span class="badge dim">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 109-9 9.75 9.75 0 00-6.74 2.74L3 8" /><path d="M3 3v5h5" /><path d="M12 7v5l4 2" /></svg>
            </span>
            <span class="meta">
              <span class="name">{resuming === h.id ? 'Resuming…' : h.title}</span>
              <span class="sub mono">{h.cwd} · {ago(h.mtime)}</span>
            </span>
            <span class="chev">›</span>
          </button>
        </div>
      {/each}
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
  {#if notif !== 'unsupported' && notif !== 'granted'}
    <div class="foot">
      {#if notifHint}<p class="hint">{notifHint}</p>{/if}
      <button class="notif" onclick={toggleNotif}>
        <span class="ndot"></span>
        Enable notifications
      </button>
    </div>
  {/if}
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
  .card {
    position: relative;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    margin-bottom: 8px;
    transition: border-color 0.12s;
  }
  .card:hover {
    border-color: var(--border-2);
  }
  .card.current {
    border-color: var(--border-2);
    background: var(--panel-2);
  }
  .hit {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 12px;
    text-align: left;
    padding: 12px 40px 12px 12px;
  }
  .hit:disabled {
    opacity: 0.55;
  }
  .badge {
    position: relative;
    flex: none;
    width: 36px;
    height: 36px;
    border-radius: 50%;
    background: var(--panel-3);
    color: var(--text-2);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .badge.dim {
    color: var(--text-3);
  }
  .bdot {
    position: absolute;
    right: -1px;
    bottom: -1px;
    width: 9px;
    height: 9px;
    border-radius: 50%;
    border: 2px solid var(--panel);
    background: var(--text-4);
  }
  .bdot[data-state='idle'] {
    background: var(--live);
  }
  .bdot[data-state='starting'],
  .bdot[data-state='running'] {
    background: var(--busy);
    animation: soften 1.6s ease-in-out infinite;
  }
  .bdot[data-state='awaiting_permission'] {
    background: var(--busy);
  }
  @keyframes soften {
    50% {
      opacity: 0.4;
    }
  }
  .meta {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .name {
    font-size: 14.5px;
    font-weight: 500;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sub {
    font-size: 10.5px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    unicode-bidi: plaintext;
    text-align: left;
  }
  .chev {
    position: absolute;
    right: 16px;
    top: 50%;
    transform: translateY(-50%);
    color: var(--text-4);
    font-size: 17px;
  }
  .x {
    position: absolute;
    right: 8px;
    top: 50%;
    transform: translateY(-50%);
    width: 30px;
    height: 30px;
    border-radius: 50%;
    color: var(--text-4);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .x:hover,
  .x:active {
    color: var(--text-2);
    background: var(--panel-3);
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
