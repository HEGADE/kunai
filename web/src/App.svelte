<script lang="ts">
  import { onMount } from 'svelte'
  import { app } from './lib/app.svelte'
  import { reviewDraft } from './lib/api'
  import Sidebar from './components/Sidebar.svelte'
  import Chat from './components/Chat.svelte'
  import ReviewView from './components/ReviewView.svelte'
  import Home from './components/Home.svelte'
  import NewSession from './components/NewSession.svelte'
  import Settings from './components/Settings.svelte'
  import Accounts from './components/Accounts.svelte'
  import Providers from './components/Providers.svelte'
  import Usage from './components/Usage.svelte'
  import Channels from './components/Channels.svelte'
  import AllSessions from './components/AllSessions.svelte'
  import { applyThemeColor, themeColorFor } from './lib/themeColor'
  import PinGate from './components/PinGate.svelte'
  import { lanPin } from './lib/lanpin.svelte'

  // Keep the browser's own chrome (the status bar behind the notch, the address
  // bar below) matching whatever is actually at the top of the screen. On the
  // nightly channel the sidebar header is a night-sky purple, and a black status
  // bar above it read as a rendering fault rather than a design.
  $effect(() => {
    applyThemeColor(
      themeColorFor({ nightly: app.isNightly, fullView: !!app.chat || app.showUsage }),
    )
  })

  onMount(() => {
    app.startPolling()
    app.initRouting()
    const onVis = () => {
      if (document.visibilityState === 'visible') app.refresh()
    }
    document.addEventListener('visibilitychange', onVis)
    return () => document.removeEventListener('visibilitychange', onVis)
  })

  // Whether the open session is a pull-request review. Asked of the server once
  // per session rather than guessed from the title, which a rename would break.
  let reviewOf = $state<Record<string, boolean>>({})
  const key = $derived(app.chat ? `${app.activeMachineId}:${app.chat.sessionId}` : '')
  const isReview = $derived(!!key && reviewOf[key] === true)
  $effect(() => {
    const k = key
    const chat = app.chat
    if (!k || !chat || k in reviewOf) return
    reviewDraft(app.baseForMachine(app.activeMachineId ?? ''), chat.sessionId)
      .then(() => (reviewOf = { ...reviewOf, [k]: true }))
      .catch(() => (reviewOf = { ...reviewOf, [k]: false }))
  })
  // Opening a different session starts on its findings again, not wherever the
  // last one was left.
  $effect(() => {
    void key
    app.reviewChat = false
  })
</script>

<!-- Over everything, and only once the server has actually refused something. On
     loopback and over the tailnet nothing 401s, so this never appears there. -->
{#if lanPin.required}
  <PinGate />
{/if}

<!-- data-full marks "the main pane is showing a whole view, not the dashboard",
     which on a phone is what decides whether the sidebar or the pane is on
     screen. A session used to be the only thing that could claim it; Usage is a
     route now, so it claims it too. -->
<div
  class="shell"
  data-full={app.chat || app.showUsage ? 'true' : undefined}
  class:collapsed={!app.sidebarOpen}
>
  <aside class="sidebar"><Sidebar /></aside>
  <main class="main">
    {#if app.showUsage}
      <div class="pane"><Usage /></div>
    {:else if app.chat}
      <!-- A review session opens on its findings rather than on the transcript.
           The conversation is one click away (the view's Conversation button),
           because arguing with the reviewer is the thing kunai has that a CI
           reviewer does not; it is just not where you start. -->
      {#if isReview && !app.reviewChat}
        <div class="pane">
          <ReviewView sessionId={app.chat.sessionId} machineId={app.activeMachineId ?? ''} />
        </div>
      {:else}
        <div class="pane"><Chat chat={app.chat} /></div>
      {/if}
    {:else}
      {#if !app.sidebarOpen}
        <!-- Reopen affordance, shown only while collapsed: when the sidebar is
             open its own header holds the collapse button, so showing this too
             was two toggles at once. -->
        <button class="rail-toggle" onclick={() => app.toggleSidebar()} aria-label="Show sidebar" title="Show sidebar">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2.5" /><path d="M9.5 4v16" /></svg>
        </button>
      {/if}
      <div class="dash"><Home /></div>
    {/if}
  </main>
</div>

{#if app.showNew}
  <NewSession />
{/if}
{#if app.showSettings}
  <Settings />
{/if}
{#if app.showChannels}
  <Channels />
{/if}
{#if app.showAccounts}
  <Accounts />
{/if}
{#if app.showProviders}
  <Providers />
{/if}
{#if app.showAllSessions}
  <AllSessions />
{/if}

<style>
  .shell {
    height: 100dvh;
    display: grid;
    grid-template-columns: var(--sidebar-w) 1fr;
  }
  .sidebar {
    border-right: 1px solid var(--border);
    min-width: 0;
    overflow: hidden;
  }
  .shell.collapsed {
    grid-template-columns: 1fr;
  }
  .shell.collapsed .sidebar {
    display: none;
  }
  .main {
    position: relative;
    min-width: 0;
    overflow: hidden;
    background: var(--bg);
    display: flex;
    flex-direction: column;
  }
  /* The tab strip sits above the session; the chat takes whatever is left. */
  .pane {
    flex: 1;
    min-height: 0;
  }
  .rail-toggle {
    display: none;
  }
  @media (min-width: 861px) {
    .rail-toggle {
      display: flex;
      align-items: center;
      justify-content: center;
      position: absolute;
      top: 13px;
      left: 14px;
      z-index: 20;
      width: 34px;
      height: 34px;
      border-radius: 50%;
      background: var(--panel);
      border: 1px solid var(--border);
      color: var(--text-3);
    }
    .rail-toggle:hover {
      color: var(--text);
      border-color: var(--border-2);
    }
  }
  .dash {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }

  /* Phone: one column; show the sidebar until a session is open, then the chat.
     Desktop's collapsed state must not hide the phone home screen. */
  @media (max-width: 860px) {
    .shell,
    .shell.collapsed {
      grid-template-columns: 1fr;
    }
    .shell.collapsed .sidebar {
      display: block;
    }
    .sidebar {
      border-right: none;
    }
    .shell[data-full] .sidebar {
      display: none;
    }
    .shell:not([data-full]) .main {
      display: none;
    }
  }
</style>
