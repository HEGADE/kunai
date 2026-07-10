<script lang="ts">
  import { onMount } from 'svelte'
  import { app } from './lib/app.svelte'
  import Sidebar from './components/Sidebar.svelte'
  import Chat from './components/Chat.svelte'
  import EmptyState from './components/EmptyState.svelte'
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

<div class="shell" data-has-chat={app.chat ? 'true' : undefined}>
  <aside class="sidebar"><Sidebar /></aside>
  <main class="main">
    {#if app.chat}
      <Chat chat={app.chat} />
    {:else}
      <EmptyState />
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
    border-right: 1px solid var(--line);
    min-width: 0;
    overflow: hidden;
  }
  .main {
    min-width: 0;
    overflow: hidden;
    background: var(--bg);
  }

  /* Phone: one column; show the sidebar until a session is open, then the chat. */
  @media (max-width: 860px) {
    .shell {
      grid-template-columns: 1fr;
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
