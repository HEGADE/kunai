<script lang="ts">
  import { app, tabKey, type Tab } from '../lib/app.svelte'

  // A tab is not an inert shell like a terminal's: it is an agent that keeps
  // working while you look elsewhere. So each tab carries its session's live
  // state, and the strip doubles as a board of what is running and what wants you.
  function label(t: Tab): string {
    const c = app.connFor(t)
    const name = c?.title || c?.cwd.replace(/\/+$/, '').split('/').slice(-1)[0]
    if (name) return name
    const m = app.sessions.find((s) => s.machineId === t.machineId && s.id === t.id)
    return m?.title || m?.cwd.split('/').slice(-1)[0] || 'session'
  }

  // Same status vocabulary as the chat header: green idle, amber working, red
  // offline — plus "needs you", the one state worth interrupting for.
  function state(t: Tab): string {
    const c = app.connFor(t)
    if (!c) return 'idle'
    if (c.status !== 'online') return 'offline'
    if (c.sessionState === 'awaiting_permission') return 'needs'
    if (c.sessionState === 'running' || c.sessionState === 'starting') return 'busy'
    return 'live'
  }
</script>

<div class="strip">
  {#each app.tabs as t (tabKey(t.machineId, t.id))}
    {@const on = app.activeKey === tabKey(t.machineId, t.id)}
    <div class="tab" class:on>
      <button class="hit" onclick={() => app.open(t.machineId, t.id)} title={label(t)}>
        <span class="dot" data-k={state(t)}></span>
        <span class="name">{label(t)}</span>
      </button>
      <button class="x" onclick={() => app.closeTab(t)} aria-label="Close tab" title="Close tab">
        <svg width="9" height="9" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M1 1l8 8M9 1l-8 8" /></svg>
      </button>
    </div>
  {/each}
  <button class="new" onclick={() => app.newSession()} aria-label="New session" title="New session">
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
  </button>
</div>

<style>
  .strip {
    display: flex;
    align-items: center;
    gap: 4px;
    /* The strip is the top of the session view, so it owns the safe area: on a
       phone the tabs sat under the status bar and collided with the clock. */
    padding: calc(var(--safe-top) + 6px) 10px 0;
    overflow-x: auto;
    scrollbar-width: none;
  }
  .strip::-webkit-scrollbar {
    display: none;
  }
  .tab {
    flex: 0 0 auto;
    display: flex;
    align-items: center;
    max-width: 190px;
    height: 26px;
    padding-right: 3px;
    border-radius: var(--r-sm);
    border: 1px solid transparent;
    color: var(--text-3);
  }
  .tab:hover {
    background: var(--panel);
    color: var(--text-2);
  }
  /* The active tab is the only raised surface; everything else stays flat. */
  .tab.on {
    background: var(--panel-2);
    border-color: var(--border-2);
    color: var(--text);
  }
  .hit {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    height: 100%;
    padding: 0 4px 0 10px;
    color: inherit;
    font-size: 12.5px;
    font-weight: 500;
  }
  .name {
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .dot {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .dot[data-k='live'] {
    background: var(--live);
  }
  .dot[data-k='busy'] {
    background: var(--busy);
  }
  .dot[data-k='offline'] {
    background: var(--alert);
  }
  /* "Needs you" is the one state worth pulling your eye across the strip. */
  .dot[data-k='needs'] {
    background: var(--busy);
    animation: wants 1.5s ease-in-out infinite;
  }
  @keyframes wants {
    50% {
      opacity: 0.25;
      transform: scale(0.8);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .dot[data-k='needs'] {
      animation: none;
      box-shadow: 0 0 0 3px color-mix(in srgb, var(--busy) 25%, transparent);
    }
  }
  .x {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    border-radius: 4px;
    color: var(--text-4);
    opacity: 0;
  }
  .tab.on .x,
  .tab:hover .x {
    opacity: 1;
  }
  .x:hover {
    color: var(--text);
    background: var(--panel-3);
  }
  /* Touch has no hover, so the close affordance is always there. */
  @media (pointer: coarse) {
    .x {
      opacity: 1;
    }
  }
  .new {
    flex: 0 0 auto;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    height: 30px;
    margin-left: 2px;
    border-radius: var(--r-sm);
    color: var(--text-3);
  }
  .new:hover {
    color: var(--text);
    background: var(--panel-2);
  }
</style>
