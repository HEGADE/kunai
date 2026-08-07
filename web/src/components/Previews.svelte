<script lang="ts">
  import { listPreviews, openPreview, closePreview, type PreviewServer } from '../lib/api'

  // What the agent is running, and a way to look at it.
  //
  // The card only exists when there is something to show. An agent that never
  // starts a server should not be paying rent for a row that says "no servers",
  // and most sessions never start one.
  //
  // "Open" is a deliberate act rather than something kunai does on discovery,
  // because opening puts a dev server on the tailnet. Finding it is free and
  // private; exposing it is a decision, so it is a tap.
  let { base = '', sessionId }: { base?: string; sessionId: string } = $props()

  let servers = $state<PreviewServer[]>([])
  let busy = $state(0)
  let err = $state('')

  async function load() {
    try {
      servers = await listPreviews(base, sessionId)
      err = ''
    } catch {
      // A machine without lsof, or a session that has gone. Silent: this is a
      // convenience, and an error bar for it would be louder than the feature.
      servers = []
    }
  }

  // Rescanned when the session changes and on a slow tick, because a dev server
  // appears partway through a turn rather than at a moment kunai is told about.
  $effect(() => {
    void sessionId
    void load()
    const t = setInterval(load, 15_000)
    return () => clearInterval(t)
  })

  async function toggle(s: PreviewServer) {
    busy = s.port
    err = ''
    try {
      if (s.forwarding) await closePreview(base, sessionId, s.port)
      else await openPreview(base, sessionId, s.port)
      await load()
    } catch (e) {
      err = (e as Error).message
    } finally {
      busy = 0
    }
  }
</script>

{#if servers.length}
  <div class="card">
    {#each servers as s (s.port)}
      <div class="row">
        <span class="k">
          <span class="name mono">:{s.port}</span>
          <span class="sub">{s.command}{s.local ? '' : ' · already on the network'}</span>
        </span>
        {#if s.url}
          <a class="open" href={s.url} target="_blank" rel="noreferrer">Open →</a>
          {#if s.forwarding}
            <button class="plain" onclick={() => toggle(s)} disabled={busy === s.port}>Stop</button>
          {/if}
        {:else}
          <button class="btn" onclick={() => toggle(s)} disabled={busy === s.port}>
            {busy === s.port ? 'Opening…' : 'Open'}
          </button>
        {/if}
      </div>
    {/each}
    {#if err}<p class="err">{err}</p>{/if}
  </div>
{/if}

<style>
  .card {
    display: flex;
    flex-direction: column;
    margin: 0 0 12px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    overflow: hidden;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 9px 13px;
  }
  .row + .row {
    border-top: 1px solid var(--border);
  }
  .k {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: baseline;
    gap: 9px;
  }
  .name {
    font-size: 13px;
    color: var(--text-2);
  }
  .sub {
    font-size: 11px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .btn {
    flex: none;
    height: 26px;
    padding: 0 12px;
    border-radius: 8px;
    background: var(--text);
    color: #0b0b0c;
    font-size: 12px;
    font-weight: 600;
  }
  .btn:disabled {
    opacity: 0.4;
  }
  .open {
    flex: none;
    font-size: 12.5px;
    color: var(--live);
    text-decoration: none;
  }
  .plain {
    flex: none;
    padding: 4px 9px;
    border-radius: 7px;
    font-size: 12px;
    color: var(--text-4);
    background: none;
  }
  .plain:hover:not(:disabled) {
    color: var(--text);
    background: var(--panel-3);
  }
  .err {
    margin: 0;
    padding: 0 13px 10px;
    font-size: 11.5px;
    color: var(--alert);
  }
</style>
