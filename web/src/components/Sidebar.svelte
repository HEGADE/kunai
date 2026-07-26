<script lang="ts">
  import { app, type StartSpec } from '../lib/app.svelte'
  import { createSession } from '../lib/api'
  import { enablePush, pushState } from '../lib/push'
  import type { TaggedHistoryEntry, TaggedMeta } from '../lib/types'
  import { groupSessions, groupStartTarget } from '../lib/grouping'
  import { isGitRepo } from '../lib/worktrees'
  import Wordmark from './Wordmark.svelte'
  import Home from './Home.svelte'
  import SessionMenu from './SessionMenu.svelte'
  import Hint from './Hint.svelte'
  import WorktreeStart from './WorktreeStart.svelte'

  // The nightly channel gets a night-sky header, so you can tell a nightly build
  // from a stable one at a glance. This is the one place the "no gradients" rule
  // is broken, and only when the build serving the app is nightly.
  const nightly = $derived(app.isNightly)

  let notif = $state(pushState())
  let notifHint = $state('')
  let resuming = $state('')
  let q = $state('')

  const query = $derived(q.trim().toLowerCase())
  const multi = $derived(app.machines.length > 1)
  function machineLabel(id: string): string {
    return app.machines.find((m) => m.id === id)?.label || id
  }
  const inFilter = (mid: string) => app.machineFilter === 'all' || app.machineFilter === mid
  const activeList = $derived(
    app.sessions.filter(
      (m) =>
        inFilter(m.machineId) &&
        (!query ||
          shortName(m).toLowerCase().includes(query) ||
          m.cwd.toLowerCase().includes(query) ||
          machineLabel(m.machineId).toLowerCase().includes(query)),
    ),
  )
  const recentList = $derived(
    app.history.filter(
      (h) =>
        inFilter(h.machineId) &&
        (!query ||
          h.title.toLowerCase().includes(query) ||
          h.cwd.toLowerCase().includes(query) ||
          machineLabel(h.machineId).toLowerCase().includes(query)),
    ),
  )
  // Pinned sessions rise to the top in their own section, drawn from both the
  // live list and Recent (an id is in exactly one). They keep their own kind, so
  // a pinned live session still opens and a pinned past one still resumes.
  const pinnedActive = $derived(activeList.filter((m) => m.pinned))
  const pinnedRecent = $derived(recentList.filter((h) => h.pinned))
  const hasPinned = $derived(pinnedActive.length > 0 || pinnedRecent.length > 0)
  const activeUnpinned = $derived(activeList.filter((m) => !m.pinned))
  const recentUnpinned = $derived(recentList.filter((h) => !h.pinned))
  // Keep the sidebar tidy: show only the most recent few; the rest live behind
  // "View all sessions" (a full, searchable, paginated view).
  const RECENT_MAX = 8
  const recentDisplay = $derived(recentUnpinned.slice(0, RECENT_MAX))
  // Sessions sit under the codebase they belong to: the directory they started
  // in, or a workspace name once that directory stops describing them. Pinned
  // stays flat, because a pin is a priority list and grouping it would bury the
  // thing you pinned. A single group needs no heading, so a one-project machine
  // looks exactly as it did before.
  // Starting a session from a group heading. Keyed by the group so one heading's
  // spinner never disables the others, and guarded because a double tap would
  // otherwise start two sessions in the same folder.
  let starting = $state<Record<string, boolean>>({})
  // Which folders can hold a worktree. Asked once per folder rather than learned
  // from a failure: finding out by failing meant the button appeared beside a
  // folder that was not a repository, and tapping it put a raw error where the
  // session list should be.
  let isRepo = $state<Record<string, boolean>>({})
  const probed = new Set<string>()
  function probeRepo(machineId: string, cwd: string) {
    const key = `${machineId}\u0000${cwd}`
    if (probed.has(key)) return
    probed.add(key)
    isGitRepo(app.baseForMachine(machineId), cwd).then((ok) => {
      isRepo = { ...isRepo, [cwd]: ok }
    })
  }

  // Which heading's worktree dialog is open. A worktree exists to hold a piece
  // of work, so it asks what that work is, which branch to cut from and how to
  // run it; all of that was being decided for you before.
  let wtPanel = $state<{ key: string; machineId: string; cwd: string } | null>(null)

  function openWorktree(key: string, target: { machineId: string; cwd: string }) {
    wtPanel = { key, machineId: target.machineId, cwd: target.cwd }
  }

  // One entry point for both heading buttons. A prompt means the dialog was
  // used, so the session opens with the work already sent; without one this is
  // the plus button's one tap into an empty session, which is deliberately still
  // a single tap.
  async function startInGroup(
    key: string,
    machineId: string,
    cwd: string,
    spec: StartSpec = {},
    prompt = '',
  ) {
    if (starting[key]) return
    starting = { ...starting, [key]: true }
    try {
      if (prompt) await app.startWork(machineId, cwd, prompt, spec)
      else await app.quickStart(machineId, cwd, spec)
    } catch {
      // Both have already reported it; nothing to add here.
    } finally {
      const next = { ...starting }
      delete next[key]
      starting = next
    }
  }

  // What to call a worktree session: its branch, minus the kunai/ prefix. The
  // branch rather than the directory, because a worktree that names itself from
  // its first prompt renames the branch and leaves the directory alone.
  const wtName = (m: { branch?: string; cwd: string }) =>
    (m.branch ?? '').replace(/^kunai\//, '') ||
    m.cwd.replace(/\/+$/, '').split('/').slice(-1)[0]

  // One list, grouped by folder, live and past together.
  //
  // There used to be an Active section above Recent, which meant a session moved
  // out of its project the moment it started running and back again when it
  // stopped. That is exactly backwards for worktrees: you start three agents on
  // one repository precisely so you can watch them side by side, and the sidebar
  // was scattering them the moment they had anything to show. A folder is where
  // its work lives whatever state that work is in, and the presence dot on each
  // row already says which ones are running.
  //
  // Live rows are listed before past ones, and since groupSessions preserves the
  // order it is given, that also floats the folders with something running to the
  // top for free.
  type Row =
    | { kind: 'live'; m: TaggedMeta }
    | { kind: 'recent'; h: TaggedHistoryEntry }
  // The grouping fields are lifted onto the row because groupSessions groups by
  // what an item says about itself, and it must not have to know which of the two
  // shapes it is holding.
  type GroupedRow = Row & {
    machineId: string
    cwd: string
    workspace?: string
    projects?: number
    repo?: string
  }
  const rowId = (r: Row) => (r.kind === 'live' ? r.m.id : r.h.id)
  const sessionGroups = $derived(
    groupSessions<GroupedRow>([
      ...activeUnpinned.map((m) => ({
        kind: 'live' as const,
        m,
        machineId: m.machineId,
        cwd: m.cwd,
        workspace: m.workspace,
        projects: m.projects,
        repo: m.repo,
      })),
      ...recentDisplay.map((h) => ({
        kind: 'recent' as const,
        h,
        machineId: h.machineId,
        cwd: h.cwd,
        workspace: h.workspace,
        repo: h.repo,
      })),
    ]),
  )
  // Ask about each heading's folder as the headings appear, so the worktree
  // button is only ever offered where it can work. Once per folder, guarded by
  // `probed`, so re-rendering the list costs nothing.
  $effect(() => {
    for (const g of sessionGroups) {
      const target = groupStartTarget(g)
      if (target) probeRepo(target.machineId, target.cwd)
    }
  })
  function activeCount(mid: string): number {
    return app.sessions.filter((m) => m.machineId === mid).length
  }

  let filterOpen = $state(false)
  const currentFilter = $derived(
    app.machineFilter === 'all' ? null : app.machines.find((m) => m.id === app.machineFilter),
  )
  function pickFilter(id: string) {
    app.machineFilter = id
    filterOpen = false
  }

  async function toggleNotif() {
    if (notif === 'granted') return
    const err = await enablePush()
    notifHint = err
    notif = pushState()
    setTimeout(() => (notifHint = ''), err ? 5000 : 100)
  }

  function shortName(m: TaggedMeta): string {
    return m.title || m.cwd.replace(/\/+$/, '').split('/').slice(-1)[0] || 'session'
  }
  async function resume(h: TaggedHistoryEntry) {
    if (resuming) return
    resuming = h.id
    try {
      const meta = await createSession(app.baseForMachine(h.machineId), {
        cwd: h.cwd,
        resume: h.id,
        title: h.title,
        cli: h.cli, // reopen on the account it belongs to
      })
      app.open(h.machineId, meta.id)
      app.refresh()
    } catch (e) {
      notifHint = (e as Error).message
      setTimeout(() => (notifHint = ''), 5000)
    } finally {
      resuming = ''
    }
  }
</script>

{#snippet gear()}
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3.2" /><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 11-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 11-2.83-2.83l.06-.06A1.65 1.65 0 004.6 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 112.83-2.83l.06.06A1.65 1.65 0 009 4.6a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 112.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z" /></svg>
{/snippet}

{#snippet railIcon()}
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2.5" /><path d="M9.5 4v16" /></svg>
{/snippet}

{#snippet newChat()}
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 11.5a8.5 8.5 0 01-8.5 8.5 8.38 8.38 0 01-3.8-.9L3 21l1.9-4.7a8.38 8.38 0 01-.9-3.8A8.5 8.5 0 0112.5 4" /><path d="M18.5 3v5M21 5.5h-5" /></svg>
{/snippet}

{#snippet bubble()}
  <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 01-.9 3.8 8.5 8.5 0 01-7.6 4.7 8.38 8.38 0 01-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 01-.9-3.8 8.5 8.5 0 014.7-7.6 8.38 8.38 0 013.8-.9h.5a8.48 8.48 0 018 8v.5z" /></svg>
{/snippet}

{#snippet activeRow(m: TaggedMeta)}
  <div class="row" class:current={app.activeId === m.id && app.activeMachineId === m.machineId}>
    <button class="hit" onclick={() => app.open(m.machineId, m.id)}>
      <span class="ic">
        {@render bubble()}
        <span class="live" data-state={m.state}></span>
      </span>
      <span class="name">{shortName(m)}</span>
      <!-- A worktree session sits under its repository's heading like any other,
           so the branch is what tells it apart from a session in the main
           checkout. Skipped when the session has no title of its own yet, since
           the name is then already the worktree's directory and the chip would
           just say it twice. -->
      {#if m.repo && wtName(m) && wtName(m) !== shortName(m)}
        <span class="wtchip mono" title="On {m.branch} in {m.cwd}">{wtName(m)}</span>
      {/if}
    </button>
    <SessionMenu
      machineId={m.machineId}
      id={m.id}
      title={shortName(m)}
      pinned={m.pinned}
      workspace={m.workspace ?? ''}
      projects={m.projects ?? 0}
      kind="live"
    />
  </div>
{/snippet}

{#if wtPanel}
  <WorktreeStart
    base={app.baseForMachine(wtPanel.machineId)}
    machineId={wtPanel.machineId}
    repo={wtPanel.cwd}
    busy={!!starting[wtPanel.key]}
    onclose={() => (wtPanel = null)}
    onstart={(prompt, spec) => {
      // Held open until the start resolves rather than closed on the click: a
      // worktree runs its setup command before the agent may touch the tree, and
      // that can take a while. A dialog that vanished into a quiet sidebar for a
      // minute would read as a click that did nothing.
      const p = wtPanel!
      startInGroup(p.key, p.machineId, p.cwd, spec, prompt).finally(() => {
        if (wtPanel === p) wtPanel = null
      })
    }}
  />
{/if}

{#snippet groupHead(group: { key: string; label: string; named: boolean; items: { machineId: string; cwd: string }[] })}
  {@const target = groupStartTarget(group)}
  <!-- Mono, because a project or workspace name is data rather than prose. A
       named workspace gets a leading mark so you can tell at a glance which
       headings you chose and which were derived from a directory. -->
  <div class="grp mono" class:named={group.named} title={group.named ? 'Workspace' : 'Project directory'}>
    {#if group.named}<span class="wsmark" aria-hidden="true"></span>{/if}
    <span class="glabel">{group.label}</span>
    <!-- One tap starts a session in this folder with the usual defaults: the
         heading already names the directory, so asking again would be asking a
         question you just answered. Absent when the group spans directories, since
         there is then no single folder to mean. -->
    {#if target}
      <!-- Two buttons rather than one that asks. The plus keeps its promise of
           starting work in this folder with no questions; the branch beside it is
           the same single tap into a checkout of its own, taking every default
           (the repository's own base and its own setup command). Choosing a base
           or a name is what the launcher's picker is for. -->
      {#if isRepo[target.cwd]}
        <Hint
          title="Another agent, no collisions"
          body="Say what the work is and which branch to cut from, and it happens in a separate checkout of that branch. Another agent can work there while this checkout stays exactly as you left it. It is git's own worktree feature, so the repository itself is untouched."
        >
        <button
          class="gadd wt"
          disabled={starting[group.key]}
          onclick={() => openWorktree(group.key, target)}
          aria-label="New worktree session in {group.label}"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 3v12M6 21a2 2 0 100-4 2 2 0 000 4zM6 7a2 2 0 100-4 2 2 0 000 4zM18 11a2 2 0 100-4 2 2 0 000 4zM18 9v2a4 4 0 01-4 4H6" /></svg>
          <!-- A tooltip is a cursor affordance, so on a touch screen this button
               would be an unexplained icon. The heading row has the space going
               spare, so the word goes there instead. -->
          <span class="wtlbl mono">worktree</span>
        </button>
        </Hint>
      {/if}
      <button
        class="gadd"
        disabled={starting[group.key]}
        onclick={() => startInGroup(group.key, target.machineId, target.cwd)}
        title="New session in {target.cwd}"
        aria-label="New session in {group.label}"
      >
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
      </button>
    {/if}
  </div>
{/snippet}

{#snippet recentRow(h: TaggedHistoryEntry)}
  <div class="row">
    <button class="hit" onclick={() => resume(h)} disabled={!!resuming}>
      <span class="ic">{@render bubble()}</span>
      <span class="name">{resuming === h.id ? 'Resuming…' : h.title}</span>
    </button>
    <SessionMenu
      machineId={h.machineId}
      id={h.id}
      title={h.title}
      pinned={h.pinned}
      workspace={h.workspace ?? ''}
      kind="recent"
    />
  </div>
{/snippet}

<div class="sb">
  <header class:nightly>
    <Wordmark size={17} />
    <div class="actions">
      <button
        class="icon deskonly"
        onclick={() => app.toggleSidebar()}
        aria-label="Collapse sidebar"
        title="Collapse sidebar"
      >
        {@render railIcon()}
      </button>
      <button class="add" onclick={() => app.newSession()} aria-label="New chat" title="New chat">
        {@render newChat()}
      </button>
    </div>
  </header>

  <!-- Search and machine scope share one hairline bar: both narrow the session
       list, so they read as one control rather than two stacked pills. The
       scope shows the current machine as mono data with its status dot, and
       only appears when there's more than one machine to choose between. -->
  <div class="filterbar">
    <div class="fbar">
      <svg class="mag" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="7" /><path d="M21 21l-4.3-4.3" /></svg>
      <input type="search" placeholder="Search sessions" bind:value={q} autocomplete="off" />
      {#if multi}
        <button class="scope" onclick={() => (filterOpen = !filterOpen)} aria-label="Filter by machine">
          <span class="fdot" class:live={currentFilter?.online}></span>
          <span class="mlabel mono">{currentFilter ? currentFilter.label : 'All'}</span>
          <svg class="chev" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6" /></svg>
        </button>
      {/if}
    </div>
    {#if multi && filterOpen}
      <button class="fscrim" onclick={() => (filterOpen = false)} aria-label="Close"></button>
      <div class="fpop">
        <button class="fopt" class:on={app.machineFilter === 'all'} onclick={() => pickFilter('all')}>
          <span class="fdot"></span>
          <span class="mlabel mono">All machines</span>
          <span class="fcount">{app.sessions.length}</span>
        </button>
        {#each app.machines as m (m.id)}
          <button class="fopt" class:on={app.machineFilter === m.id} onclick={() => pickFilter(m.id)} title={m.url}>
            <span class="fdot" class:live={m.online}></span>
            <span class="mlabel mono">{m.label}</span>
            {#if activeCount(m.id)}<span class="fcount">{activeCount(m.id)}</span>{/if}
          </button>
        {/each}
      </div>
    {/if}
  </div>

  <div class="list">
    <div class="homewrap"><Home compact /></div>

    {#if app.listError}
      <p class="note mono">{app.listError}</p>
    {/if}

    {#if hasPinned}
      <div class="sec">Pinned</div>
      {#each pinnedActive as m (m.machineId + ':' + m.id)}
        {@render activeRow(m)}
      {/each}
      {#each pinnedRecent as h (h.machineId + ':' + h.id)}
        {@render recentRow(h)}
      {/each}
    {/if}

    {#if sessionGroups.length > 0}
      <div class="sec">Sessions</div>
      {#each sessionGroups as g (g.key)}
        {#if sessionGroups.length > 1}
          {@render groupHead(g)}
        {/if}
        {#each g.items as it (it.kind + ':' + it.machineId + ':' + rowId(it))}
          {#if it.kind === 'live'}
            {@render activeRow(it.m)}
          {:else}
            {@render recentRow(it.h)}
          {/if}
        {/each}
      {/each}
    {/if}

    {#if app.history.length > 0}
      <button class="viewall" onclick={() => app.openAllSessions()}>
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7" rx="1.5" /><rect x="14" y="3" width="7" height="7" rx="1.5" /><rect x="3" y="14" width="7" height="7" rx="1.5" /><rect x="14" y="14" width="7" height="7" rx="1.5" /></svg>
        View all sessions
      </button>
    {/if}

    {#if activeList.length === 0 && recentList.length === 0 && !app.listError}
      <div class="empty">
        <p class="e1">{query ? 'No matches' : 'No sessions yet'}</p>
        <p class="e2">
          {query ? 'Try a different search.' : 'Start one in any project directory on your machine.'}
        </p>
      </div>
    {/if}
  </div>

  <!-- Only surface the notification control while it still needs action. Once
       granted it's the desired steady state, so the persistent bar just eats
       session-list space and is dropped. -->
  <div class="foot">
    {#if notif !== 'unsupported' && notif !== 'granted'}
      {#if notifHint}<p class="hint">{notifHint}</p>{/if}
      <button class="notif" onclick={toggleNotif}>
        <span class="ndot"></span>
        Enable notifications
      </button>
    {/if}
    <!-- Four destinations, all of them configuration you visit rarely. As a
         stacked list they cost 188px, which is nearly a third of a phone screen
         permanently spent on things you are not looking at. On a phone they
         become one row instead; see .nav below. -->
    <nav class="nav">
    <button class="navitem" onclick={() => app.openChannels()}>
      <span class="ic">
        <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.4 8.4 0 01-9 8.4 8.5 8.5 0 01-3.9-.9L3 20.5l1.5-4.4a8.4 8.4 0 01-.9-3.9 8.5 8.5 0 018.4-8.7h.5a8.5 8.5 0 018.5 8.5z" /></svg>
      </span>
      <span class="nlbl">Channels</span>
    </button>
    <button class="navitem" onclick={() => app.openAccounts()}>
      <span class="ic">
        <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M16 21v-2a4 4 0 00-4-4H6a4 4 0 00-4 4v2" /><circle cx="9" cy="7" r="4" /><path d="M22 21v-2a4 4 0 00-3-3.87" /><path d="M16 3.13a4 4 0 010 7.75" /></svg>
      </span>
      <span class="nlbl">Accounts</span>
    </button>
    <button class="navitem" onclick={() => app.openProviders()}>
      <span class="ic">
        <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7" rx="1.5" /><rect x="14" y="3" width="7" height="7" rx="1.5" /><rect x="14" y="14" width="7" height="7" rx="1.5" /><rect x="3" y="14" width="7" height="7" rx="1.5" /></svg>
      </span>
      <span class="nlbl">Providers</span>
    </button>
    <button class="navitem" onclick={() => app.openSettings()}>
      <span class="ic">{@render gear()}</span>
      Settings
    </button>
    </nav>
  </div>
</div>

<style>
  .sb {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }
  @media (min-width: 861px) {
    .sb {
      background: var(--bg-raised);
    }
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: calc(var(--safe-top) + 18px) 20px 14px;
  }
  /* Nightly channel only: a night-sky header so a nightly build is obvious at a
     glance. Stars and the purple gradient are stacked background layers (first is
     topmost), so they sit behind the wordmark and buttons with no z-index work.
     This is the single deliberate exception to the no-gradients rule. */
  header.nightly {
    background-image:
      radial-gradient(1.2px 1.2px at 18% 34%, rgba(255, 255, 255, 0.85), transparent 60%),
      radial-gradient(1px 1px at 40% 20%, rgba(255, 255, 255, 0.6), transparent 60%),
      radial-gradient(1.3px 1.3px at 63% 44%, rgba(255, 255, 255, 0.8), transparent 60%),
      radial-gradient(1px 1px at 82% 26%, rgba(255, 255, 255, 0.55), transparent 60%),
      radial-gradient(1px 1px at 52% 66%, rgba(255, 255, 255, 0.5), transparent 60%),
      radial-gradient(1px 1px at 30% 72%, rgba(255, 255, 255, 0.5), transparent 60%),
      radial-gradient(1.4px 1.4px at 90% 58%, rgba(255, 255, 255, 0.7), transparent 60%),
      radial-gradient(1px 1px at 9% 60%, rgba(255, 255, 255, 0.45), transparent 60%),
      radial-gradient(130% 100% at 25% -35%, #3a2f6e 0%, #241d49 45%, transparent 80%),
      linear-gradient(180deg, #1d1739 0%, transparent 100%);
  }
  .actions {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .add,
  .icon {
    width: 34px;
    height: 34px;
    border-radius: 50%;
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-2);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .icon {
    background: none;
    border-color: transparent;
    color: var(--text-3);
  }
  .add:hover,
  .icon:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .icon:hover {
    background: var(--panel);
  }
  /* Search and machine scope in one hairline bar — boxy like the app's cards,
     not a candy pill, and one row instead of two so the list gets the space. */
  .filterbar {
    position: relative;
    padding: 4px 14px 6px;
  }
  .fbar {
    display: flex;
    align-items: center;
    gap: 9px;
    height: 38px;
    padding: 0 5px 0 12px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r);
    color: var(--text-4);
  }
  .fbar:focus-within {
    border-color: var(--border-2);
  }
  .mag {
    flex: none;
    color: var(--text-4);
  }
  .fbar:focus-within .mag {
    color: var(--text-3);
  }
  .fbar input {
    flex: 1;
    min-width: 0;
    background: none;
    border: none;
    outline: none;
    padding: 0;
    font-size: 13.5px;
    color: var(--text);
  }
  .fbar input::placeholder {
    color: var(--text-4);
  }
  .fbar input::-webkit-search-cancel-button {
    -webkit-appearance: none;
  }
  /* The scope chip: the current machine as mono data behind a hairline rule. */
  .scope {
    flex: none;
    display: flex;
    align-items: center;
    gap: 6px;
    height: 24px;
    padding: 0 7px 0 10px;
    margin-left: 1px;
    border-left: 1px solid var(--border);
    color: var(--text-3);
    font-size: 12px;
  }
  .scope:hover {
    color: var(--text);
  }
  .mlabel {
    max-width: 82px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .chev {
    flex: none;
    color: var(--text-4);
  }
  .fdot {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .fdot.live {
    background: var(--live);
  }
  .fscrim {
    position: fixed;
    inset: 0;
    z-index: 30;
  }
  .fpop {
    position: absolute;
    z-index: 31;
    top: calc(100% - 4px);
    left: 14px;
    right: 14px;
    padding: 5px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
    max-height: 60vh;
    overflow-y: auto;
  }
  .fopt {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 9px 10px;
    border-radius: var(--r-sm);
    color: var(--text-2);
    font-size: 13px;
  }
  .fopt:hover,
  .fopt.on {
    background: var(--panel-3);
    color: var(--text);
  }
  .fopt .mlabel {
    flex: 1;
    max-width: none;
  }
  .fcount {
    flex: none;
    padding: 1px 7px;
    border-radius: 100px;
    background: var(--bg);
    color: var(--text-3);
    font-size: 11px;
  }
  .list {
    flex: 1;
    /* Without min-height:0 a flex scroll-child can refuse to shrink and overflow
       under the footer (seen on iOS): the session rows then bleed through the
       Settings row. This lets the list scroll within its track instead. */
    min-height: 0;
    overflow-y: auto;
    padding: 4px 14px 14px;
  }
  /* The dashboard lives in the main pane on desktop; on phones the sidebar IS
     the home screen, so it renders here. */
  .homewrap {
    display: none;
  }
  @media (max-width: 860px) {
    .homewrap {
      display: block;
      padding: 8px 2px 16px;
    }
  }
  .sec {
    font-size: 11.5px;
    font-weight: 550;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-4);
    padding: 12px 6px 8px;
  }
  /* The branch a worktree session is on: data, so mono, and quiet, since the
     name beside it is what you are reading. */
  .wtchip {
    flex: none;
    margin-left: 6px;
    padding: 1px 5px;
    border-radius: 4px;
    background: var(--panel-2);
    color: var(--text-4);
    font-size: 10px;
    max-width: 92px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* A project or workspace heading sits under a section heading, so it is
     quieter than one: sentence case, not uppercase, and indented to the row
     text so the sessions below read as belonging to it. */
  .grp {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    color: var(--text-3);
    padding: 7px 8px 3px 10px;
    min-width: 0;
  }
  /* The label takes the slack, so the buttons sit together at the end whatever
     wraps them. They used to push themselves there with margin-left:auto on the
     first of them, which broke the moment one was wrapped for a hover hint:
     display:contents keeps the layout but not the sibling relationship, so the
     adjacency rule stopped matching and both buttons claimed the space again. */
  .glabel {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    unicode-bidi: plaintext;
  }
  /* Named workspaces read a shade stronger than a derived directory: you chose
     them, so they are the heading you are actually looking for. */
  .grp.named {
    color: var(--text-3);
  }
  .wsmark {
    flex: none;
    width: 4px;
    height: 4px;
    border-radius: 1px;
    background: currentColor;
    opacity: 0.7;
  }
  .note {
    color: var(--text-3);
    font-size: 12.5px;
    padding: 10px;
  }
  /* Sessions as single-line rows: a chat-bubble icon + the title, nothing else.
     Long titles fade at the right edge; the open one highlights. */
  .row {
    position: relative;
    border-radius: var(--r);
  }
  .row:hover {
    background: var(--panel);
  }
  .row.current {
    background: var(--panel-2);
  }
  .hit {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 11px;
    text-align: left;
    padding: 8px 10px;
  }
  .hit:disabled {
    opacity: 0.55;
  }
  .ic {
    position: relative;
    flex: none;
    display: inline-flex;
    color: var(--text-4);
  }
  .row:hover .ic,
  .row.current .ic {
    color: var(--text-3);
  }
  /* Small presence dot on the icon for live sessions. */
  .live {
    position: absolute;
    right: -3px;
    top: -3px;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    border: 2px solid var(--bg);
    background: var(--text-4);
  }
  .row:hover .live {
    border-color: var(--panel);
  }
  .row.current .live {
    border-color: var(--panel-2);
  }
  .live[data-state='idle'] {
    background: var(--live);
  }
  .live[data-state='starting'],
  .live[data-state='running'] {
    background: var(--busy);
    animation: soften 1.6s ease-in-out infinite;
  }
  .live[data-state='awaiting_permission'] {
    background: var(--busy);
  }
  @keyframes soften {
    50% {
      opacity: 0.4;
    }
  }
  .name {
    flex: 1;
    min-width: 0;
    font-size: 14.5px;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    -webkit-mask-image: linear-gradient(to right, #000 calc(100% - 22px), transparent);
    mask-image: linear-gradient(to right, #000 calc(100% - 22px), transparent);
  }
  .row:hover .name,
  .row.current .name {
    color: var(--text);
  }
  .row.current .name {
    font-weight: 500;
  }
  /* The per-row menu (SessionMenu) lives where the close button used to; reveal
     its trigger on row hover, matching the old close affordance. */
  .row:hover :global(.trigger) {
    opacity: 1;
  }
  /* A row whose menu is open stays highlighted, and is lifted into its own
     stacking layer so its dropdown paints above the rows below it (they are
     positioned too, so without this they'd cover the menu). */
  .row:has(:global(.wrap.open)) {
    background: var(--panel);
    z-index: 20;
  }
  .viewall {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 11px;
    padding: 9px 10px;
    margin-top: 2px;
    border-radius: var(--r);
    color: var(--text-3);
    font-size: 13.5px;
    font-weight: 500;
  }
  .viewall svg {
    flex: none;
    color: var(--text-4);
  }
  .viewall:hover {
    background: var(--panel);
    color: var(--text);
  }
  .viewall:hover svg {
    color: var(--text-3);
  }
  .empty {
    text-align: center;
    margin-top: 24vh;
    padding: 0 30px;
  }
  .e1 {
    font-size: 15px;
    font-weight: 550;
    color: var(--text);
    margin: 0 0 5px;
  }
  .e2 {
    font-size: 13.5px;
    color: var(--text-3);
    margin: 0;
    line-height: 1.55;
  }
  .navitem {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 8px 10px;
    border-radius: var(--r-sm);
    font-size: 13.5px;
    color: var(--text-2);
  }
  /* A phone lays them side by side: icon over label, the shape of a tab bar.
     The labels stay, because an unexplained icon is the problem this is not
     meant to trade for. Desktop keeps the stacked list, where the room exists. */
  @media (max-width: 860px) {
    .nav {
      display: flex;
      gap: 2px;
    }
    .navitem {
      flex: 1;
      min-width: 0;
      flex-direction: column;
      gap: 4px;
      padding: 8px 2px 6px;
      font-size: 10.5px;
      color: var(--text-3);
    }
    .navitem .nlbl {
      max-width: 100%;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }
  .navitem:hover {
    color: var(--text);
    background: var(--panel);
  }
  .navitem .ic {
    display: flex;
    color: var(--text-3);
  }
  .navitem:hover .ic {
    color: var(--text-2);
  }
  /* Opaque and never-shrinking, sitting above the list: even if the scroll area
     ever runs long on a stubborn browser, the footer covers it cleanly instead
     of letting a session row show through Settings. */
  .foot {
    flex: none;
    position: relative;
    z-index: 2;
    background: var(--bg);
    padding: 8px 16px calc(var(--safe-bottom) + 12px);
  }
  @media (min-width: 861px) {
    .foot {
      background: var(--bg-raised);
    }
  }
  .hint {
    margin: 0 2px 8px;
    font-size: 12px;
    color: var(--text-3);
    line-height: 1.5;
  }
  .notif {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 8px;
    color: var(--text-4);
    font-size: 12px;
    border-radius: var(--r-sm);
  }
  .notif:hover {
    color: var(--text-2);
  }
  .ndot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .ndot.on {
    background: var(--live);
  }

  /* The start action on a group heading. Hiding it until hover was a mistake: an
     action you cannot see is an action nobody uses, and on touch there is no hover
     to reveal it with. So it rests visible and quiet -- a real 26px hit target with
     its own surface, one text tier up from the heading it sits beside -- and lights
     up fully on hover. Quiet is achieved with contrast, not with invisibility. */
  .gadd {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    border-radius: var(--r-sm);
    background: var(--panel);
    color: var(--text-2);
    transition: color 0.12s, background 0.12s;
  }
  /* A finger is not a cursor. These are the smallest targets in the sidebar and
     there are now two of them side by side, so a touch screen gets room to hit
     the one it meant. */
  /* The label is hidden wherever a tooltip works, so the desktop heading stays
     as spare as it was. */
  .wtlbl {
    display: none;
  }
  @media (pointer: coarse) {
    .gadd {
      width: 34px;
      height: 34px;
    }
    .gadd.wt {
      width: auto;
      gap: 6px;
      padding: 0 10px;
    }
    .wtlbl {
      display: inline;
      font-size: 10.5px;
      color: var(--text-4);
    }
  }
  .gadd:hover,
  .gadd:focus-visible {
    color: var(--text);
    background: var(--panel-3);
  }
  .gadd:disabled {
    color: var(--text-4);
    background: transparent;
  }
</style>
