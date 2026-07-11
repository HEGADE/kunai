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
    <Wordmark size={17} />
    <button class="add" onclick={() => app.newSession()} aria-label="New session">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
    </button>
  </header>

  <div class="list">
    {#if app.listError}
      <p class="note mono">{app.listError}</p>
    {/if}

    {#if app.sessions.length > 0}
      <div class="sec">Active</div>
      <div class="group">
        {#each app.sessions as m (m.id)}
          <div class="rowwrap" class:current={app.activeId === m.id}>
            <button class="row" onclick={() => app.open(m.id)}>
              <span class="dot" data-state={m.state}></span>
              <span class="meta">
                <span class="name">{shortName(m)}</span>
                <span class="path mono">{m.cwd}</span>
              </span>
            </button>
            <button class="x" onclick={(e) => remove(e, m)} aria-label="Close session">
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
            </button>
          </div>
        {/each}
      </div>
    {/if}

    {#if app.history.length > 0}
      <div class="sec">Recent</div>
      <div class="group">
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
      </div>
    {/if}

    {#if app.sessions.length === 0 && app.history.length === 0 && !app.listError}
      <div class="empty">
        <p class="e1">No sessions yet</p>
        <p class="e2">Start one in any project directory on your machine.</p>
      </div>
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
    padding: calc(var(--safe-top) + 18px) 20px 16px;
  }
  .add {
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
  .add:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .list {
    flex: 1;
    overflow-y: auto;
    padding: 0 14px 14px;
  }
  .sec {
    font-size: 11.5px;
    font-weight: 550;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-4);
    padding: 12px 6px 8px;
  }
  .group {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    overflow: hidden;
  }
  .note {
    color: var(--text-3);
    font-size: 12.5px;
    padding: 10px;
  }
  .rowwrap {
    position: relative;
  }
  .rowwrap + .rowwrap::before {
    content: '';
    position: absolute;
    left: 16px;
    right: 0;
    top: 0;
    height: 1px;
    background: var(--border);
  }
  .rowwrap.current {
    background: var(--panel-2);
  }
  .row {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 11px;
    text-align: left;
    padding: 13px 44px 13px 16px;
  }
  .row:disabled {
    opacity: 0.55;
  }
  .rowwrap:not(.current) .row:active,
  .rowwrap:not(.current) .row:hover {
    background: var(--panel-2);
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
  .path {
    font-size: 10.5px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    unicode-bidi: plaintext;
    text-align: left;
  }
  .when {
    position: absolute;
    right: 16px;
    top: 50%;
    transform: translateY(-50%);
    font-size: 11.5px;
    color: var(--text-4);
  }
  .x {
    position: absolute;
    right: 10px;
    top: 50%;
    transform: translateY(-50%);
    width: 28px;
    height: 28px;
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
    margin-top: 26vh;
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
