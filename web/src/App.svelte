<script lang="ts">
  import { onMount } from 'svelte'
  import { app } from './lib/app.svelte'
  import Sidebar from './components/Sidebar.svelte'
  import Chat from './components/Chat.svelte'
  import Home from './components/Home.svelte'
  import NewSession from './components/NewSession.svelte'

  onMount(() => {
    app.startPolling()
    const onVis = () => {
      if (document.visibilityState === 'visible') app.refresh()
    }
    document.addEventListener('visibilitychange', onVis)
    return () => document.removeEventListener('visibilitychange', onVis)
  })
</script>

<div class="shell" data-has-chat={app.chat ? 'true' : undefined} class:collapsed={!app.sidebarOpen}>
  <aside class="sidebar"><Sidebar /></aside>
  <main class="main">
    <button class="rail-toggle" onclick={() => app.toggleSidebar()} aria-label="Toggle sidebar" title="Toggle sidebar">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2.5" /><path d="M9.5 4v16" /></svg>
    </button>
    {#if app.chat}
      <Chat chat={app.chat} />
    {:else}
      <div class="dash"><Home /></div>
    {/if}
  </main>
</div>

{#if app.showNew}
  <NewSession />
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
    height: 100%;
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
    .shell[data-has-chat] .sidebar {
      display: none;
    }
    .shell:not([data-has-chat]) .main {
      display: none;
    }
  }
</style>
