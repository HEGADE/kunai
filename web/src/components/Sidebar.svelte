<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { closeSession } from '../lib/api'
  import { enablePush, pushState } from '../lib/push'
  import type { Meta } from '../lib/types'
  import Wordmark from './Wordmark.svelte'

  let notif = $state(pushState())
  let notifHint = $state('')

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
  async function remove(e: MouseEvent, m: Meta) {
    e.stopPropagation()
    await closeSession(m.id)
    if (app.activeId === m.id) app.back()
    else app.refresh()
  }
</script>

<div class="sb">
  <header><Wordmark size={15} /></header>

  <button class="new" onclick={() => app.newSession()}>
    <span>New session</span>
    <span class="plus">+</span>
  </button>

  <div class="list">
    {#if app.listError}
      <p class="note mono">{app.listError}</p>
    {:else if app.sessions.length === 0}
      <p class="note">No sessions yet.</p>
    {:else}
      {#each app.sessions as m (m.id)}
        <div class="rowwrap" class:active={app.activeId === m.id}>
          <button class="row" onclick={() => app.open(m.id)}>
            <span class="dot" data-state={m.state}></span>
            <span class="meta">
              <span class="name">{shortName(m)}</span>
              <span class="path mono">{m.cwd}</span>
            </span>
          </button>
          <button class="x" onclick={(e) => remove(e, m)} aria-label="Close">✕</button>
        </div>
      {/each}
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
    padding: calc(var(--safe-top) + 20px) 18px 16px;
  }
  .new {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin: 0 12px 14px;
    padding: 9px 13px;
    border-radius: var(--r);
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text);
    font-size: 13.5px;
    font-weight: 500;
    transition: border-color 0.12s;
  }
  .new:hover {
    border-color: var(--border-2);
  }
  .plus {
    color: var(--text-3);
    font-size: 15px;
  }
  .list {
    flex: 1;
    overflow-y: auto;
    padding: 0 8px;
  }
  .note {
    color: var(--text-3);
    font-size: 13px;
    padding: 8px 10px;
  }
  .rowwrap {
    position: relative;
    border-radius: var(--r);
    margin-bottom: 1px;
  }
  .rowwrap.active {
    background: var(--panel);
  }
  .rowwrap.active::before {
    content: '';
    position: absolute;
    left: 0;
    top: 9px;
    bottom: 9px;
    width: 2px;
    border-radius: 2px;
    background: var(--text-2);
  }
  .row {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 10px;
    text-align: left;
    padding: 9px 34px 9px 12px;
    border-radius: var(--r);
  }
  .rowwrap:not(.active) .row:hover {
    background: var(--panel);
  }
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    flex: none;
    background: var(--text-4);
  }
  .dot[data-state='idle'] {
    background: var(--live);
  }
  .dot[data-state='running'] {
    background: var(--busy);
    animation: pulse 1.5s infinite;
  }
  .dot[data-state='awaiting_permission'] {
    background: var(--busy);
  }
  @keyframes pulse {
    50% {
      opacity: 0.35;
    }
  }
  .meta {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 1px;
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
  .x {
    position: absolute;
    top: 50%;
    right: 6px;
    transform: translateY(-50%);
    color: var(--text-4);
    font-size: 11px;
    padding: 7px;
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
