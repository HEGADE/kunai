<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { snoozePresets, type SnoozePreset } from '../lib/snooze'

  // Per-row actions for a session, live or past. Keyed by the shared id, so a pin
  // or rename set here follows the session across the live -> resumable boundary.
  // 'live' offers Close (ends the process, keeps the transcript); 'recent' offers
  // Delete (removes the transcript for good).
  let {
    machineId,
    id,
    title,
    pinned = false,
    workspace = '',
    projects = 0,
    snoozedUntil = 0,
    kind,
  }: {
    machineId: string
    id: string
    title: string
    pinned?: boolean
    // The group this session sits under, if it has been named, and how many
    // codebases it holds. More than one is what turns "which folder is this"
    // into a question worth answering by hand.
    workspace?: string
    projects?: number
    // When the session is parked on the snoozed shelf (unix ms, 0 for not).
    // The menu offers Wake instead of Snooze while it holds.
    snoozedUntil?: number
    kind: 'live' | 'recent'
  } = $props()

  let open = $state(false)
  let mode = $state<'menu' | 'edit' | 'confirm' | 'snooze'>('menu')
  // Resolved when the submenu opens, not when the row mounted: "this evening"
  // computed at mount would drift stale in a long-lived tab.
  let presets = $state<SnoozePreset[]>([])
  // Renaming the session and naming its workspace are the same interaction over
  // a different field, so they share one editor rather than two near-identical
  // ones. `field` is which of them is being edited.
  let field = $state<'title' | 'workspace'>('title')
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

  // A workspace is worth offering once a session holds more than one codebase,
  // or once it already has a name to correct.
  const canName = $derived(projects > 1 || !!workspace)

  function startEdit(which: 'title' | 'workspace') {
    field = which
    name = which === 'title' ? title : workspace
    err = ''
    mode = 'edit'
    // Focus and select once the input is in the DOM.
    queueMicrotask(() => {
      input?.focus()
      input?.select()
    })
  }
  async function saveEdit() {
    if (busy) return
    busy = true
    err = ''
    try {
      if (field === 'title') await app.renameSession(machineId, id, name)
      else await app.setWorkspace(machineId, id, name)
      close()
    } catch (e) {
      err = (e as Error).message
    } finally {
      busy = false
    }
  }

  function openSnooze() {
    presets = snoozePresets(new Date())
    mode = 'snooze'
  }
  async function snooze(untilMs: number) {
    close()
    try {
      await app.setSnooze(machineId, id, untilMs)
    } catch (e) {
      void e // the next list refresh shows the truth
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
      saveEdit()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      close()
    }
  }
</script>

<!-- Escape closes the menu itself, not only the rename field inside it. A
     popover you can open with the keyboard and only dismiss with the mouse is
     half a control. -->
<svelte:window
  onkeydown={(e) => {
    if (open && e.key === 'Escape') close()
  }} />

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
        <button class="item" role="menuitem" onclick={(e) => { e.stopPropagation(); startEdit('title') }}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9" /><path d="M16.5 3.5a2.1 2.1 0 013 3L7 19l-4 1 1-4z" /></svg>
          Rename
        </button>
        {#if canName}
          <button class="item" role="menuitem" onclick={(e) => { e.stopPropagation(); startEdit('workspace') }}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" /></svg>
            {workspace ? 'Rename workspace' : 'Name workspace'}
          </button>
        {/if}
        {#if snoozedUntil > 0}
          <button class="item" role="menuitem" onclick={(e) => { e.stopPropagation(); snooze(0) }}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="13" r="8" /><path d="M12 9v4l2.5 2.5" /><path d="M5 3L2 6M19 3l3 3" /></svg>
            Wake now
          </button>
        {:else}
          <button class="item" role="menuitem" onclick={(e) => { e.stopPropagation(); openSnooze() }}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="13" r="8" /><path d="M12 9v4l2.5 2.5" /><path d="M5 3L2 6M19 3l3 3" /></svg>
            Snooze
            <svg class="sub" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6" /></svg>
          </button>
        {/if}
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
      {:else if mode === 'snooze'}
        <!-- The presets, not a time picker: four times cover "later today",
             "tonight", "tomorrow" and "not this week", and anything finer is
             precision nobody asked of a shelf. A snoozed session still comes
             back early the moment it needs you (see lib/snooze.ts). -->
        <p class="hint">Hides the session until then. It comes back early if it needs you.</p>
        {#each presets as p (p.label)}
          <button class="item" role="menuitem" onclick={(e) => { e.stopPropagation(); snooze(p.until) }}>
            {p.label}
            <span class="when mono">{new Date(p.until).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}</span>
          </button>
        {/each}
      {:else if mode === 'edit'}
        <div class="rename">
          {#if field === 'workspace'}
            <p class="hint">Groups this session in the sidebar. Sessions sharing a name group together; clear it to go back to the folder.</p>
          {/if}
          <input
            bind:this={input}
            bind:value={name}
            onkeydown={onKey}
            onclick={(e) => e.stopPropagation()}
            placeholder={field === 'workspace' ? 'Workspace name' : 'Session name'}
            spellcheck="false"
          />
          <div class="ren-row">
            <button class="mini" onclick={(e) => { e.stopPropagation(); close() }}>Cancel</button>
            <button class="mini save" disabled={busy} onclick={(e) => { e.stopPropagation(); saveEdit() }}>Save</button>
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
  /* Centred with a margin rather than a transform, and that is a fix rather than
     a preference.
     A transform makes the element the containing block for any position:fixed
     DESCENDANT. The close scrim below is position:fixed inset:0, so instead of
     covering the viewport it covered this 26px trigger -- a scrim exactly the
     size and place of the button that opens the menu. Clicking the dots closed
     it (the trigger toggles) and clicking anywhere else did nothing at all,
     because there was nothing there to click.
     -13px is half the trigger's 26px height; .wrap is sized by the trigger,
     since the popover and the scrim are both out of flow. */
  .wrap {
    position: absolute;
    right: 6px;
    top: 50%;
    margin-top: -13px;
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
  /* The submenu chevron and a preset's resolved time both sit at the right
     edge, quiet: they qualify the item, they are not the item. */
  .item .sub {
    margin-left: auto;
  }
  .item .when {
    margin-left: auto;
    font-size: 11px;
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
  .hint {
    color: var(--text-3);
    font-size: 11.5px;
    line-height: 1.45;
    padding: 0 2px 7px;
  }
  .err {
    margin: 0;
    font-size: 11.5px;
    color: var(--alert);
  }
</style>
