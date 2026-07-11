<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { closeSession, createSession } from '../lib/api'
  import { enablePush, pushState } from '../lib/push'
  import type { HistoryEntry, Meta } from '../lib/types'
  import Wordmark from './Wordmark.svelte'

  let notif = $state(pushState())
  let notifHint = $state('')
  let resuming = $state('')

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
    if (s < 3600) return `${Math.round(s / 60)}m`
    if (s < 86400) return `${Math.round(s / 3600)}h`
    return `${Math.round(s / 86400)}d`
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
    <Wordmark size={15} />
    <button class="add" onclick={() => app.newSession()} aria-label="New session" title="New session">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
    </button>
  </header>

  <div class="list">
    {#if app.listError}
      <p class="note mono">{app.listError}</p>
    {/if}

    {#if app.sessions.length > 0}
      <div class="sec">Active</div>
      {#each app.sessions as m (m.id)}
        <div class="rowwrap" class:active={app.activeId === m.id}>
          <button class="row" onclick={() => app.open(m.id)}>
            <span class="meta">
              <span class="name">{shortName(m)}</span>
              <span class="path mono">{m.cwd}</span>
            </span>
            <span class="dot" data-state={m.state}></span>
          </button>
          <button class="x" onclick={(e) => remove(e, m)} aria-label="Close">✕</button>
        </div>
      {/each}
    {/if}

    {#if app.history.length > 0}
      <div class="sec">Recent</div>
      {#each app.history as h (h.id)}
        <div class="rowwrap">
          <button class="row" onclick={() => resume(h)} disabled={!!resuming}>
            <span class="meta">
              <span class="name">{resuming === h.id ? 'Resuming…' : h.title}</span>
              <span class="path mono">{h.cwd}</span>
            </span>
            <span class="when">{ago(h.mtime)}</span>
          </button>
        </div>
      {/each}
    {/if}

    {#if app.sessions.length === 0 && app.history.length === 0 && !app.listError}
      <p class="note">Nothing yet — start a session in any project directory.</p>
    {/if}
  </div>

  {#if notif !== 'unsupported'}
    <div class="foot">
      {#if notifHint}<p class="hint">{notifHint}</p>{/if}
      <button class="notif" onclick={toggleNotif}>
        <span class="ndot" class:on={notif === 'granted'}></span>
        {notif === 'granted' ? 'Notifications on' : 'Enable notifications'}
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
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: calc(var(--safe-top) + 18px) 18px 14px;
  }
  .add {
    width: 32px;
    height: 32px;
    border-radius: 50%;
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-2);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .add:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .list {
    flex: 1;
    overflow-y: auto;
    padding: 0 10px 10px;
  }
  .sec {
    font-size: 11px;
    font-weight: 500;
    letter-spacing: 0.03em;
    text-transform: uppercase;
    color: var(--text-4);
    padding: 14px 8px 7px;
  }
  .note {
    color: var(--text-3);
    font-size: 13px;
    padding: 10px;
    line-height: 1.5;
  }
  .rowwrap {
    position: relative;
    border-radius: var(--r);
  }
  .rowwrap.active {
    background: var(--panel);
  }
  .row {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 10px;
    text-align: left;
    padding: 10px 12px;
    border-radius: var(--r);
  }
  .row:disabled {
    opacity: 0.6;
  }
  .rowwrap:not(.active) .row:hover {
    background: var(--panel);
  }
  .meta {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .name {
    font-size: 13.5px;
    font-weight: 500;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .path {
    font-size: 10.5px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    text-align: left;
  }
  .dot {
    flex: none;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .dot[data-state='idle'] {
    background: var(--live);
  }
  .dot[data-state='running'] {
    background: var(--busy);
    animation: soften 1.6s ease-in-out infinite;
  }
  .dot[data-state='awaiting_permission'] {
    background: var(--busy);
  }
  @keyframes soften {
    50% {
      opacity: 0.4;
    }
  }
  .when {
    flex: none;
    font-size: 11px;
    color: var(--text-4);
  }
  .x {
    position: absolute;
    top: 8px;
    right: 8px;
    color: var(--text-4);
    font-size: 10px;
    padding: 5px;
    opacity: 0;
    transition: opacity 0.12s;
  }
  .rowwrap:hover .x,
  .rowwrap.active .x {
    opacity: 1;
  }
  .x:hover {
    color: var(--text-2);
  }
  .foot {
    padding: 10px 12px calc(var(--safe-bottom) + 12px);
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
    padding: 8px 10px;
    color: var(--text-3);
    font-size: 12.5px;
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

  @media (max-width: 860px) {
    .x {
      opacity: 1;
    }
  }
</style>
