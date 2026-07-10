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

  function shortCwd(p: string): string {
    const parts = p.replace(/\/+$/, '').split('/')
    return parts.slice(-1)[0] || p
  }
  const stateLabel: Record<string, string> = {
    idle: 'live',
    running: 'working',
    awaiting_permission: 'needs you',
  }

  async function remove(e: MouseEvent, m: Meta) {
    e.stopPropagation()
    await closeSession(m.id)
    app.refresh()
  }
</script>

<div class="screen">
  <header>
    <Wordmark size={23} />
    <div class="head-actions">
      <span class="label tag">relay-free</span>
      {#if notif !== 'unsupported'}
        <button class="bell" class:on={notif === 'granted'} onclick={toggleNotif} aria-label="Notifications">
          {notif === 'granted' ? '🔔' : '🔕'}
        </button>
      {/if}
    </div>
  </header>
  {#if notifHint}<p class="hint">{notifHint}</p>{/if}

  <div class="scroll">
    {#if app.listError}
      <p class="err mono">! can't reach the server — {app.listError}</p>
    {/if}

    {#if app.sessions.length === 0}
      <div class="empty">
        <span class="label">no active sessions</span>
        <p>Open one in any project directory on your machine. It runs directly over your tailnet — nothing in between.</p>
      </div>
    {:else}
      <span class="label sec">Sessions · {app.sessions.length}</span>
      <ul>
        {#each app.sessions as m (m.id)}
          <li>
            <button class="card" onclick={() => app.open(m.id)}>
              <span class="mark" data-state={m.state}></span>
              <span class="body">
                <span class="title mono">{m.title || shortCwd(m.cwd)}</span>
                <span class="path mono">{m.cwd}</span>
              </span>
              <span class="state label" data-state={m.state}>{stateLabel[m.state] ?? m.state}</span>
            </button>
            <button class="x" onclick={(e) => remove(e, m)} aria-label="Close session">✕</button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <div class="footer">
    <button class="new" onclick={() => app.newSession()}>
      <span class="mono chevron">❯</span> new session
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
    justify-content: space-between;
    padding: calc(var(--safe-top) + 18px) 18px 15px;
    border-bottom: 1px solid var(--line);
  }
  .head-actions {
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .tag {
    color: var(--wire);
    letter-spacing: 0.16em;
  }
  .bell {
    font-size: 15px;
    line-height: 1;
    padding: 4px;
    opacity: 0.6;
    filter: grayscale(0.5);
  }
  .bell.on {
    opacity: 1;
    filter: none;
  }
  .hint {
    margin: 0;
    padding: 10px 18px;
    font-size: 12.5px;
    color: var(--amber);
    background: var(--amber-dim);
  }
  .scroll {
    flex: 1;
    overflow-y: auto;
    padding: 14px;
  }
  .sec {
    display: block;
    margin: 2px 4px 10px;
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 7px;
  }
  li {
    position: relative;
  }
  .card {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 12px;
    text-align: left;
    background: var(--bg-2);
    border: 1px solid var(--line);
    border-radius: var(--r);
    padding: 13px 42px 13px 13px;
    transition: border-color 0.15s;
  }
  .card:active {
    border-color: var(--wire);
  }
  .mark {
    width: 3px;
    align-self: stretch;
    border-radius: 2px;
    flex: none;
    background: var(--ink-faint);
  }
  .mark[data-state='running'] {
    background: var(--wire);
    animation: dim 1.3s infinite;
  }
  .mark[data-state='awaiting_permission'] {
    background: var(--amber);
  }
  .mark[data-state='idle'] {
    background: var(--go);
  }
  @keyframes dim {
    50% {
      opacity: 0.4;
    }
  }
  .body {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .title {
    font-weight: 600;
    font-size: 14px;
    color: var(--ink);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .path {
    font-size: 11px;
    color: var(--ink-faint);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    text-align: left;
  }
  .state {
    flex: none;
    color: var(--ink-dim);
  }
  .state[data-state='awaiting_permission'] {
    color: var(--amber);
  }
  .state[data-state='running'] {
    color: var(--wire);
  }
  .x {
    position: absolute;
    top: 50%;
    right: 8px;
    transform: translateY(-50%);
    color: var(--ink-faint);
    font-size: 12px;
    padding: 8px;
  }
  .x:active {
    color: var(--stop);
  }
  .empty {
    text-align: center;
    color: var(--ink-dim);
    margin-top: 20vh;
    padding: 0 34px;
  }
  .empty .label {
    display: block;
    margin-bottom: 10px;
    color: var(--ink-faint);
  }
  .empty p {
    font-size: 14px;
    line-height: 1.6;
  }
  .err {
    color: var(--stop);
    font-size: 12.5px;
    padding: 4px;
  }
  .footer {
    padding: 12px 16px calc(var(--safe-bottom) + 14px);
    border-top: 1px solid var(--line);
  }
  .new {
    width: 100%;
    background: var(--wire);
    color: #06222a;
    font-weight: 650;
    font-size: 15px;
    border-radius: var(--r);
    padding: 15px;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 9px;
  }
  .new:active {
    background: var(--wire-bright);
  }
  .chevron {
    font-weight: 700;
  }
</style>
