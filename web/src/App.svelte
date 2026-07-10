<script lang="ts">
  import { onMount } from 'svelte'
  import { app } from './lib/app.svelte'
  import SessionList from './components/SessionList.svelte'
  import NewSession from './components/NewSession.svelte'
  import Chat from './components/Chat.svelte'

  onMount(() => {
    app.startPolling()
    const onVis = () => {
      if (document.visibilityState === 'visible' && app.view !== 'chat') app.refresh()
    }
    document.addEventListener('visibilitychange', onVis)
    return () => document.removeEventListener('visibilitychange', onVis)
  })
</script>

{#if app.view === 'chat' && app.chat}
  <Chat chat={app.chat} />
{:else if app.view === 'new'}
  <NewSession />
{:else}
  <SessionList />
{/if}
