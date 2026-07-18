<script lang="ts">
  import { app } from '../lib/app.svelte'

  // Per-row actions for a session, live or past. Keyed by the shared id, so a pin
  // or rename set here follows the session across the live -> resumable boundary.
  // 'live' offers Close (ends the process, keeps the transcript); 'recent' offers
  // Delete (removes the transcript for good).
  let {
    machineId,
    id,
    title,
    pinned = false,
    kind,
  }: {
    machineId: string
    id: string
    title: string
    pinned?: boolean
    kind: 'live' | 'recent'
  } = $props()

  let open = $state(false)
  let mode = $state<'menu' | 'rename' | 'confirm'>('menu')
  let name = $state('')
  let err = $state('')
  let busy = $state(false)
  let input = $state<HTMLInputElement>()

  function show() {
    mode = 'menu'
    err = ''
    open = true
  }
  function close() {
    open = false
  }

  async function pin() {
    close()
    try {
      await app.setPinned(machineId, id, !pinned)
    } catch (e) {
      // The list refresh will correct an optimistic mismatch; nothing to show.
      void e
    }
  }

  function startRename() {
    name = title
    mode = 'rename'
    // Focus and select once the input is in the DOM.
    queueMicrotask(() => {
      input?.focus()
      input?.select()
    })
  }
  async function saveRename() {
    if (busy) return
    busy = true
    try {
      await app.renameSession(machineId, id, name)
      close()
    } catch (e) {
      err = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function doClose() {
    close()
    await app.endSession(machineId, id)
  }

  async function confirmDelete() {
    if (busy) return
    busy = true
    err = ''
    try {
      await app.deleteSession(machineId, id)
      close()
    } catch (e) {
      err = (e as Error).message
    } finally {
      busy = false
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault()
      saveRename()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      close()
    }
  }
</script>

<div class="wrap" class:open>
  <button
    class="trigger"
    aria-label="Session actions"
    onclick={(e) => {
      e.stopPropagation()
      open ? close() : show()
    }}
  >
    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="5" r="1.6" /><circle cx="12" cy="12" r="1.6" /><circle cx="12" cy="19" r="1.6" /></svg>
  </button>

  {#if open}
    <button class="scrim" aria-label="Close menu" onclick={(e) => { e.stopPropagation(); close() }}></button>
    <div class="pop" role="menu">
      {#if mode === 'menu'}
        <button class="item" role="menuitem" onclick={(e) => { e.stopPropagation(); pin() }}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 17v5" /><path d="M9 3h6l-1 7 3 3H7l3-3-1-7z" /></svg>
          {pinned ? 'Unpin' : 'Pin'}
        </button>
        <button class="item" role="menuitem" onclick={(e) => { e.stopPropagation(); startRename() }}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9" /><path d="M16.5 3.5a2.1 2.1 0 013 3L7 19l-4 1 1-4z" /></svg>
          Rename
        </button>
        {#if kind === 'live'}
          <button class="item" role="menuitem" onclick={(e) => { e.stopPropagation(); doClose() }}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6L6 18M6 6l12 12" /></svg>
            Close session
          </button>
        {:else}
          <button class="item danger" role="menuitem" onclick={(e) => { e.stopPropagation(); mode = 'confirm' }}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18M8 6V4a1 1 0 011-1h6a1 1 0 011 1v2M19 6l-1 14a2 2 0 01-2 2H8a2 2 0 01-2-2L5 6" /></svg>
            Delete
          </button>
        {/if}
      {:else if mode === 'rename'}
        <div class="rename">
          <input bind:this={input} bind:value={name} onkeydown={onKey} onclick={(e) => e.stopPropagation()} placeholder="Session name" spellcheck="false" />
          <div class="ren-row">
            <button class="mini" onclick={(e) => { e.stopPropagation(); close() }}>Cancel</button>
            <button class="mini save" disabled={busy} onclick={(e) => { e.stopPropagation(); saveRename() }}>Save</button>
          </div>
          {#if err}<p class="err">{err}</p>{/if}
        </div>
      {:else}
        <div class="confirm">
          <p class="ctext">Delete permanently? The transcript is removed and can't be resumed.</p>
          {#if err}<p class="err">{err}</p>{/if}
          <div class="ren-row">
            <button class="mini" onclick={(e) => { e.stopPropagation(); mode = 'menu' }}>Cancel</button>
            <button class="mini del" disabled={busy} onclick={(e) => { e.stopPropagation(); confirmDelete() }}>Delete</button>
          </div>
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .wrap {
    position: absolute;
    right: 6px;
    top: 50%;
    transform: translateY(-50%);
  }
  .trigger {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    border-radius: 50%;
    color: var(--text-4);
    background: var(--panel-2);
    opacity: 0;
  }
  /* Revealed by the parent row's hover (see :global below), and kept visible
     while its menu is open. Touch devices have no hover, so show it there. */
  .wrap.open .trigger,
  .trigger:focus-visible {
    opacity: 1;
  }
  @media (hover: none) {
    .trigger {
      opacity: 1;
      background: none;
    }
  }
  .trigger:hover {
    color: var(--text-2);
    background: var(--panel-3);
  }
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 40;
    background: none;
    cursor: default;
  }
  .pop {
    position: absolute;
    z-index: 41;
    top: calc(100% + 4px);
    right: 0;
    width: 210px;
    padding: 5px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
  }
  .item {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 9px;
    border-radius: var(--r-sm);
    color: var(--text-2);
    font-size: 13px;
    text-align: left;
  }
  .item svg {
    flex: none;
    color: var(--text-4);
  }
  .item:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .item:hover svg {
    color: var(--text-3);
  }
  .item.danger:hover {
    color: var(--alert);
  }
  .item.danger:hover svg {
    color: var(--alert);
  }
  .rename,
  .confirm {
    padding: 4px;
    display: flex;
    flex-direction: column;
    gap: 9px;
  }
  .rename input {
    width: 100%;
    padding: 8px 10px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    color: var(--text);
    font-size: 13px;
    outline: none;
  }
  .rename input:focus {
    border-color: var(--border-2);
  }
  .ctext {
    margin: 0;
    font-size: 12px;
    line-height: 1.45;
    color: var(--text-3);
  }
  .ren-row {
    display: flex;
    gap: 7px;
    justify-content: flex-end;
  }
  .mini {
    padding: 6px 12px;
    border-radius: var(--r-sm);
    background: var(--panel-3);
    color: var(--text-2);
    font-size: 12.5px;
    font-weight: 500;
  }
  .mini:hover {
    color: var(--text);
  }
  .mini.save {
    background: var(--white);
    color: #0b0b0c;
  }
  .mini.del {
    background: var(--alert);
    color: #0b0b0c;
  }
  .mini:disabled {
    opacity: 0.5;
  }
  .err {
    margin: 0;
    font-size: 11.5px;
    color: var(--alert);
  }
</style>
