<script lang="ts">
  import {
    listPreviews,
    openPreview,
    closePreview,
    hidePreview,
    type PreviewServer,
  } from '../lib/api'

  // What the agent is running, and a way to look at it.
  //
  // The card only exists when there is something to show. An agent that never
  // starts a server should not be paying rent for a row that says "no servers",
  // and most sessions never start one.
  //
  // "Open" is a deliberate act rather than something kunai does on discovery,
  // because opening puts a dev server on the tailnet. Finding it is free and
  // private; exposing it is a decision, so it is a tap.
  //
  // A row can also be DISMISSED, because attribution answers a different
  // question from the one a reader has: kunai proves a port belongs to this
  // session, and that says nothing about whether anybody wants to look at it. A
  // language server, a database, a dev server whose address you already know are
  // all correctly found and all noise sitting above the composer.
  let { base = '', sessionId }: { base?: string; sessionId: string } = $props()

  let servers = $state<PreviewServer[]>([])
  let busy = $state(0)
  let err = $state('')

  // Split rather than filtered at the source, because a dismissal must be
  // reversible and the count is the only thing that can say so. Hiding
  // everything and rendering nothing would leave no way back and no tell that a
  // later dev server on the same port is being swallowed -- the same reason the
  // sidebar names how many quiet folders it is holding.
  const shown = $derived(servers.filter((s) => !s.hidden))
  const hiddenCount = $derived(servers.length - shown.length)

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

  // Dismiss one, or bring every dismissed row back. The server stops forwarding
  // a shared port before it hides it, so this can never strand a listener.
  async function setHidden(port: number, hidden: boolean) {
    busy = port
    err = ''
    try {
      await hidePreview(base, sessionId, port, hidden)
      await load()
    } catch (e) {
      err = (e as Error).message
    } finally {
      busy = 0
    }
  }

  async function showAll() {
    for (const s of servers.filter((x) => x.hidden)) await setHidden(s.port, false)
  }
</script>

{#if servers.length}
  <!-- No chrome once every row is dismissed: what is left is a one-line receipt,
       not a card. The point of dismissing was to get the space back. -->
  <div class="card" class:bare={!shown.length}>
    {#each shown as s (s.port)}
      <div class="row">
        <span class="k">
          <span class="name mono">:{s.port}</span>
          <span class="sub">{s.command}</span>
        </span>
        <!-- What reaching it costs in privacy, said in the three states it can
             be in. The old line said "already on the network" for anything not
             loopback-only, which named a fact about socket binding rather than
             the thing anyone wants to know: can other people see this. -->
        {#if s.forwarding}
          <span class="tag shared">shared on your tailnet</span>
          <a class="open" href={s.url} target="_blank" rel="noreferrer">Open</a>
          <button class="plain" onclick={() => toggle(s)} disabled={busy === s.port}>Stop</button>
        {:else if s.url}
          <span class="tag">on your tailnet</span>
          <a class="open" href={s.url} target="_blank" rel="noreferrer">Open</a>
        {:else}
          <span class="tag">this machine only</span>
          <button
            class="btn"
            onclick={() => toggle(s)}
            disabled={busy === s.port}
            title="Puts this port on your tailnet so another device can open it"
          >
            {busy === s.port ? 'Sharing…' : 'Share'}
          </button>
        {/if}
        <button
          class="dismiss"
          onclick={() => setHidden(s.port, true)}
          disabled={busy === s.port}
          aria-label="Remove :{s.port} from this card"
          title="Remove this from the card. Sharing stops."
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
            <path d="M6 6l12 12M18 6L6 18" stroke-linecap="round" />
          </svg>
        </button>
      </div>
    {/each}
    <!-- Always said, never merely absent. It is the way back, and it is also the
         only thing that can tell you a dev server started later on a dismissed
         port is being held back rather than missing. -->
    {#if hiddenCount}
      <div class="held">
        <span>{hiddenCount} hidden</span>
        <button class="plain" onclick={showAll} disabled={busy > 0}>Show</button>
      </div>
    {/if}
    {#if err}<p class="err">{err}</p>{/if}
  </div>
{/if}

<style>
  /* Matched to the composer's 720px and centred with it. Full-bleed, it hung off
     both sides of the field it sits above -- the same mistake .actionbar in
     Chat.svelte already had and already fixed, which is the tell that this is a
     property of the dock rather than of any one strip in it. */
  .card {
    max-width: 720px;
    margin: 0 auto 8px;
    display: flex;
    flex-direction: column;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    overflow: hidden;
  }
  .card.bare {
    background: none;
    border-color: transparent;
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
  /* State before action, and quiet: which of the three it is matters less than
     the fact that a link exists, and it must not compete with Open. */
  .tag {
    flex: none;
    font-size: 10.5px;
    color: var(--text-4);
    white-space: nowrap;
  }
  .tag.shared {
    color: var(--live);
  }
  .open {
    flex: none;
    padding: 4px 10px;
    border-radius: 8px;
    font-size: 12.5px;
    color: var(--live);
    text-decoration: none;
  }
  .open:hover {
    background: var(--panel-3);
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
  /* Quiet enough to be furniture and always present rather than revealed on
     hover: a control that only exists under a pointer does not exist on a
     phone, which is most of where kunai is read. */
  .dismiss {
    flex: none;
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    margin-right: -4px;
    border-radius: 7px;
    background: none;
    color: var(--text-4);
  }
  .dismiss svg {
    width: 15px;
    height: 15px;
  }
  .dismiss:hover:not(:disabled) {
    color: var(--text-2);
    background: var(--panel-3);
  }
  .dismiss:disabled {
    opacity: 0.4;
  }
  .held {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 4px;
    padding: 4px 9px;
    font-size: 11px;
    color: var(--text-4);
  }
  .row + .held {
    border-top: 1px solid var(--border);
  }
  .err {
    margin: 0;
    padding: 0 13px 10px;
    font-size: 11.5px;
    color: var(--alert);
  }
</style>
