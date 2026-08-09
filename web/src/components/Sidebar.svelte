<script lang="ts">
  import { app, type StartSpec } from '../lib/app.svelte'
  import { createSession } from '../lib/api'
  import { enablePush, pushState } from '../lib/push'
  import type { TaggedHistoryEntry, TaggedMeta } from '../lib/types'
  import { groupSessions, groupStartTarget } from '../lib/grouping'
  import { shortAgo } from '../lib/reltime'
  import { hasWork, isAwaiting, isUnreadDone, isWorking, needsAttention, recedes, summarise, workedFor } from '../lib/sidebar'
  import { isSnoozed, isWoke, snoozeIn } from '../lib/snooze'
  import { visited } from '../lib/visited.svelte'
  import { markFor } from '../lib/providerMarks'
  import { isGitRepo } from '../lib/worktrees'
  import { updateAvailable } from '../lib/update'
  import { fetchQuery, keys, peek, SLOW_TTL } from '../lib/query.svelte'
  import Wordmark from './Wordmark.svelte'
  import Home from './Home.svelte'
  import SessionMenu from './SessionMenu.svelte'
  import Hint from './Hint.svelte'
  import UpdateNudge from './UpdateNudge.svelte'
  import WorktreeStart from './WorktreeStart.svelte'

  // The nightly channel gets a night-sky header, so you can tell a nightly build
  // from a stable one at a glance. This is the one place the "no gradients" rule
  // is broken, and only when the build serving the app is nightly.
  const nightly = $derived(app.isNightly)

  // Every machine running an older build than the release channel offers. Read
  // across the whole fleet rather than for one selected machine: the sidebar is
  // not scoped to a machine, and a peer being a release behind is exactly the
  // thing nobody would go looking for.
  const outdatedMachines = $derived(
    app.machines.filter((m) =>
      updateAvailable(m.stats?.kunai_version, app.latestVersion, m.stats?.channel),
    ),
  )

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
  // The lifecycle clock. Separate from the per-second turn clock below, which
  // only runs while something is working: snooze expiry has to be noticed on a
  // quiet sidebar too, and a half-minute beat is plenty for a shelf measured in
  // hours.
  let nowSlow = $state(Date.now())
  $effect(() => {
    const t = setInterval(() => (nowSlow = Date.now()), 30_000)
    return () => clearInterval(t)
  })

  // Snoozed rows leave the list for the shelf. The wake rule (lib/snooze.ts)
  // reads the session's state, so a live row is judged on its socket-preferred
  // state: a snoozed agent that stops to ask permission comes off the shelf on
  // the next beat, not the next poll.
  const liveSnoozeView = (m: TaggedMeta) => ({ ...m, state: app.liveState(m) })
  const isShelved = (x: TaggedMeta | TaggedHistoryEntry) =>
    'state' in x ? isSnoozed(liveSnoozeView(x as TaggedMeta), nowSlow) : isSnoozed(x, nowSlow)
  const snoozedActive = $derived(activeList.filter((m) => isShelved(m)))
  const snoozedRecent = $derived(recentList.filter((h) => isShelved(h)))
  const snoozedCount = $derived(snoozedActive.length + snoozedRecent.length)
  const activeAwake = $derived(activeList.filter((m) => !isShelved(m)))
  const recentAwake = $derived(recentList.filter((h) => !isShelved(h)))
  // Collapsed by default: the shelf's job is to be out of the way, and the
  // count in its heading already answers "is anything parked".
  let snoozeOpen = $state(false)

  // Pinned sessions rise to the top in their own section, drawn from both the
  // live list and Recent (an id is in exactly one). They keep their own kind, so
  // a pinned live session still opens and a pinned past one still resumes.
  const pinnedActive = $derived(activeAwake.filter((m) => m.pinned))
  const pinnedRecent = $derived(recentAwake.filter((h) => h.pinned))
  const hasPinned = $derived(pinnedActive.length > 0 || pinnedRecent.length > 0)
  const activeUnpinned = $derived(activeAwake.filter((m) => !m.pinned))
  const recentUnpinned = $derived(recentAwake.filter((h) => !h.pinned))
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
    // Through the shared cache: `probed` only stops this asking twice within one
    // mount, and the sidebar remounts. Whether a folder is a git repository is
    // also about the slowest-changing fact in the app, so it gets the long TTL,
    // and a cached answer applies without a request at all.
    const base = app.baseForMachine(machineId)
    const cached = peek<boolean>(keys.isRepo(base, cwd))?.data
    if (cached !== undefined) {
      isRepo = { ...isRepo, [cwd]: cached }
      return
    }
    fetchQuery(keys.isRepo(base, cwd), () => isGitRepo(base, cwd), { ttl: SLOW_TTL }).then((ok) => {
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

  // Every state read in this file goes through the app's liveState, which prefers
  // an open session's own socket over the polled list. Reading Meta.state
  // directly is how the folder summary came to announce "1 working" on a session
  // that had already stopped to ask a question: the list is refreshed on a slow
  // beat by design, because a live session is supposed to report itself.
  const stateful = (m: TaggedMeta) => ({ state: app.liveState(m) })

  // The clock behind every "working 17s". One timer for the whole list, not one
  // per row, and it only runs while something is actually working: a sidebar
  // full of finished sessions should not be waking the tab once a second to
  // recompute durations that are not being shown.
  let now = $state(Date.now())
  $effect(() => {
    if (!app.busy) return
    const t = setInterval(() => (now = Date.now()), 1000)
    now = Date.now()
    return () => clearInterval(t)
  })

  // Which folders are collapsed, remembered across reloads. A folder you closed
  // because you are not working in it today should stay closed tomorrow.
  let collapsed = $state<Record<string, boolean>>(readCollapsed())
  function readCollapsed(): Record<string, boolean> {
    try {
      const raw = localStorage.getItem('kunai-sb-collapsed')
      const keys = raw ? (JSON.parse(raw) as string[]) : []
      return Object.fromEntries(keys.map((k) => [k, true]))
    } catch {
      return {}
    }
  }
  function toggleGroup(key: string) {
    collapsed = { ...collapsed, [key]: !collapsed[key] }
    try {
      localStorage.setItem(
        'kunai-sb-collapsed',
        JSON.stringify(Object.keys(collapsed).filter((k) => collapsed[k])),
      )
    } catch {
      // A browser refusing storage costs the memory of the choice, not the choice.
    }
  }

  // Time is only shown for a session that is not running: a live one has
  // something better to say about itself. Live sessions carry no mtime anyway.
  const staleHours = 24
  const isStale = (iso: string) => {
    const t = new Date(iso).getTime()
    return !!t && Date.now() - t > staleHours * 3600_000
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

  // The codebase this session belongs to, for the row's top line -- and ONLY
  // when the group heading directly above does not already say it.
  //
  // The reference this row was built from has no headings: it is one flat list,
  // so every row has to name its own project. kunai groups by project, so
  // repeating it put the same word twice within about twenty pixels. The line
  // still earns its place when the two genuinely differ, which is a session
  // sitting under a workspace heading somebody named by hand.
  function projectOf(m: TaggedMeta, heading: string): string {
    const base = m.repo || m.cwd
    const name = base.replace(/\/+$/, '').split('/').slice(-1)[0] || ''
    return name && name !== heading ? name : ''
  }

  // Where the work lands, when that is somewhere other than the obvious place.
  //
  // A worktree gives its branch, which is the point of the line: it is what
  // tells two agents in one repository apart, and what you would type to go and
  // look at their work. An ordinary session has no branch kunai knows about, so
  // this used to fall back to the last two path segments -- which ends in the
  // project's own folder name and so said it a THIRD time. It now returns the
  // path BELOW the project, and nothing at all when the session sits at the
  // project root, because there the row has already said everything true.
  function whereOf(m: TaggedMeta): string {
    if (m.repo && wtName(m)) return wtName(m)
    if (m.branch) return m.branch
    const root = (m.repo || '').replace(/\/+$/, '')
    const cwd = m.cwd.replace(/\/+$/, '')
    if (root && cwd.startsWith(root + '/')) return cwd.slice(root.length + 1)
    return ''
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

{#snippet brand(mark: { color: string; d: string; label: string })}
  <svg
    class="mark"
    width="12"
    height="12"
    viewBox="0 0 24 24"
    style="color:{mark.color}"
    aria-label={mark.label}><path fill="currentColor" d={mark.d} /></svg>
{/snippet}

{#snippet activeRow(m: TaggedMeta, heading: string)}
  <!-- A node on the group's stem, the way a commit sits on a branch line. This is
       the whole state display: colour says what the session is doing, and the
       active session's node is the one white mark in the list. It replaced a
       filled pill plus a left bar plus bold text, which was three signals for one
       state, and the pill was the only rounded shape in a list of text. -->
  {@const mark = markFor(m.cli)}
  {@const st = stateful(m)}
  <!-- The row grows only as far as it has something to say. A plain session
       sitting idle in its project root is one line and a mark; a worktree
       mid-turn is the full three. Rendering every line unconditionally is what
       put the same folder name on the heading, the top line AND inside the
       bottom line of a single row. -->
  {@const proj = projectOf(m, heading)}
  {@const where = whereOf(m)}
  {@const working = isWorking(st)}
  {@const awaiting = isAwaiting(st)}
  {@const isCurrent = app.activeId === m.id && app.activeMachineId === m.machineId}
  <!-- The attention model, borrowed from t3code's sidebar: a working row RECEDES
       (there is nothing for you in it until it stops) and the prominence goes to
       the one that finished while you were away. Woke is a session back from a
       snooze; it outranks Done because you explicitly asked to be re-shown it. -->
  {@const seenAt = visited.at(m.machineId, m.id)}
  {@const attention = { state: st.state, turn_ended_at: m.turn_ended_at }}
  {@const doneUnread = isUnreadDone(attention, seenAt)}
  {@const woke = isWoke({ ...m, state: st.state }, nowSlow)}
  {@const receding = recedes(attention, seenAt, isCurrent)}
  <div
    class="row"
    class:current={isCurrent}
    class:waiting={isAwaiting(st)}
    class:receding
    class:unread={doneUnread || woke}
  >
    <span class="node" data-state={app.liveState(m)} aria-hidden="true"></span>
    <button class="hit card" onclick={() => app.open(m.machineId, m.id)}>
      <!-- Top line: where the work is, and what it is doing. Only appears when
           one of those is worth saying. -->
      {#if proj || working || awaiting || woke || doneUnread}
        <span class="l1">
          {#if proj}
            <svg class="fold" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v9a2 2 0 01-2 2H5a2 2 0 01-2-2z" /></svg>
            <span class="proj">{proj}</span>
          {:else}
            <span class="grow"></span>
          {/if}
          <!-- Named states, in priority: blocked on you, then in motion, then
               back from a snooze, then finished-unseen. Only states worth
               trusting are named: a resumed session reports `starting` until
               its first prompt, so anything built on that would sit there
               claiming work forever. -->
          {#if awaiting}
            <span class="status needs">Needs you</span>
          {:else if working}
            <span class="status working">
              <span class="spin" aria-hidden="true"></span>
              Working <span class="mono">{workedFor(app.liveTurnStart(m), now)}</span>
            </span>
          {:else if woke}
            <span class="status wokep">Woke</span>
          {:else if doneUnread}
            <span class="status done">
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6L9 17l-5-5" /></svg>
              Done
            </span>
          {/if}
        </span>
      {/if}

      <!-- The title: what the agent is actually doing, which is the row's reason
           to exist, so it is the one thing set bright and bold. It carries the
           account mark itself when there is no branch line to put it on. -->
      <span class="l2">
        <span class="tname">{shortName(m)}</span>
        {#if !where}{@render brand(mark)}{/if}
      </span>

      <!-- Where the work lands, when that is somewhere other than the obvious
           place: a worktree's branch, or a subdirectory of the project. Omitted
           for a session sitting at the project root, where the heading has
           already said it. -->
      {#if where}
        <span class="l3">
          <span class="where mono">{where}</span>
          {@render brand(mark)}
        </span>
      {/if}
    </button>
    <SessionMenu
      machineId={m.machineId}
      id={m.id}
      title={shortName(m)}
      pinned={m.pinned}
      workspace={m.workspace ?? ''}
      projects={m.projects ?? 0}
      snoozedUntil={m.snoozed_until ?? 0}
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

{#snippet groupHead(group: { key: string; label: string; named: boolean; items: (GroupedRow)[] })}
  {@const target = groupStartTarget(group)}
  {@const live = group.items.filter((it) => it.kind === 'live').map((it) => stateful(it.m))}
  {@const state = summarise(live)}
  {@const attention = needsAttention(live)}
  {@const shut = collapsed[group.key] && !attention}
  <!-- The heading carries a name and nothing decorative. It used to trail a
       hairline rule from the label to the chevron: six of those in a 288px column,
       each starting at a different x because label lengths differ, all brighter
       than the titles they were subordinate to. A line to nowhere. Containment is
       expressed by the stem down the group's rows instead, which is one line per
       group rather than six across it, runs with the reading rather than against
       it, and has a length that means something.
       The chevron sits in the gutter, so the label still starts exactly where the
       titles below it do. -->
  <div class="grp" class:named={group.named} class:shut>
    <button
      class="gtoggle"
      onclick={() => toggleGroup(group.key)}
      title={group.named ? 'Workspace' : 'Project directory'}
      aria-expanded={!shut}
    >
      <svg class="gchev" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 9l6 6 6-6" /></svg>
      {#if group.named}<span class="wsmark" aria-hidden="true"></span>{/if}
      <!-- Mono, because a project or workspace name is data rather than prose. A
           named workspace gets a leading mark so you can tell at a glance which
           headings you chose and which were derived from a directory. -->
      <span class="glabel mono">{group.label}</span>
      {#if state}
        <span class="gstate" class:alert={attention}>{state}</span>
      {/if}
      <span class="gfill" aria-hidden="true"></span>
      <!-- The count sits in the same right-hand column as the rows' times, so the
           numbers in this list line up with each other rather than trailing the
           label at whatever x it happens to end. Skipped when the folder has a
           state to report: "2 working" is what you needed the count for. -->
      {#if shut && !state}<span class="gcount mono">{group.items.length}</span>{/if}
    </button>
    <!-- The two start actions wait for the pointer, so the right edge of the list
         is not four buttons deep. A touch screen has no pointer to wait for, so
         there they stay (see .gacts). -->
    {#if target}
      <span class="gacts">
        {#if isRepo[target.cwd]}
          <Hint
            title="Another agent, no collisions"
            body="Say what the work is and which branch to cut from, and it happens in a separate checkout of that branch. Another agent can work there while this checkout stays exactly as you left it. It is git's own worktree feature, so the repository itself is untouched."
          >
            <button
              class="gadd"
              disabled={starting[group.key]}
              onclick={() => openWorktree(group.key, target)}
              aria-label="New worktree session in {group.label}"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 3v12M6 21a2 2 0 100-4 2 2 0 000 4zM6 7a2 2 0 100-4 2 2 0 000 4zM18 11a2 2 0 100-4 2 2 0 000 4zM18 9v2a4 4 0 01-4 4H6" /></svg>
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
      </span>
    {/if}
  </div>
{/snippet}

{#snippet recentRow(h: TaggedHistoryEntry)}
  <!-- Recency is the orientation the list had none of: with no time at all, a
       session from two minutes ago and one from three weeks ago read identically.
       It costs no extra row, and the title dims past a day so brightness carries
       the same information a second way. -->
  <!-- A past session gets no node: the stem simply passes it. Marks are for
       sessions with something to say, so a folder of finished work reads as a
       quiet line rather than a column of dots. -->
  {@const woke = isWoke(h, nowSlow)}
  <div class="row" class:stale={isStale(h.mtime) && !woke} class:unread={woke}>
    <span class="node" data-state="past" aria-hidden="true"></span>
    <button class="hit" onclick={() => resume(h)} disabled={!!resuming}>
      <span class="name">{resuming === h.id ? 'Resuming…' : h.title}</span>
      {#if h.repo && wtName(h) && wtName(h) !== h.title}
        <span class="wtchip mono" title="On {h.branch} in {h.cwd}">{wtName(h)}</span>
      {/if}
      {#if woke}
        <span class="status wokep">Woke</span>
      {:else}
        <span class="tail mono" title={h.mtime}>{shortAgo(h.mtime)}</span>
      {/if}
    </button>
    <SessionMenu
      machineId={h.machineId}
      id={h.id}
      title={h.title}
      pinned={h.pinned}
      workspace={h.workspace ?? ''}
      snoozedUntil={h.snoozed_until ?? 0}
      kind="recent"
    />
  </div>
{/snippet}

{#snippet snoozedRow(machineId: string, id: string, title: string, until: number, kind: 'live' | 'recent', activate: () => void)}
  <!-- Opening a snoozed session IS waking it: app.open clears the snooze, so
       there is no separate wake affordance on the row beyond the menu's. -->
  <div class="row snz">
    <span class="node" data-state="past" aria-hidden="true"></span>
    <button class="hit" onclick={activate} disabled={!!resuming}>
      <span class="name">{resuming === id ? 'Resuming…' : title}</span>
      <span class="tail mono snin">in {snoozeIn(until, nowSlow)}</span>
    </button>
    <SessionMenu {machineId} {id} {title} snoozedUntil={until} {kind} />
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
        {@render activeRow(m, '')}
      {/each}
      {#each pinnedRecent as h (h.machineId + ':' + h.id)}
        {@render recentRow(h)}
      {/each}
    {/if}

    <!-- No "Sessions" heading. It labelled everything, which is no information,
         and it was the third level of heading over two levels of structure. -->
    {#each sessionGroups as g (g.key)}
      {@const attention = needsAttention(g.items.filter((it) => it.kind === 'live').map((it) => stateful(it.m)))}
      {#if sessionGroups.length > 1}
        {@render groupHead(g)}
      {/if}
      {#if sessionGroups.length === 1 || !collapsed[g.key] || attention}
        <!-- The group's rows are wrapped so a single stem can run down them: the
             branch line, with each session a node on it. That is where this list's
             structure lives now, and it is the one place any line is spent. -->
        <div class="kids" class:stemmed={sessionGroups.length > 1}>
          {#each g.items as it (it.kind + ':' + it.machineId + ':' + rowId(it))}
            {#if it.kind === 'live'}
              {@render activeRow(it.m, g.label)}
            {:else}
              {@render recentRow(it.h)}
            {/if}
          {/each}
        </div>
      {/if}
    {/each}

    <!-- The snoozed shelf: sessions parked until a time, or until they need you,
         whichever comes first. Collapsed by default because its whole job is to
         be out of the way; the count answers "is anything parked" without
         opening it. A row here is slim -- the return ticket is its whole story. -->
    {#if snoozedCount > 0}
      <button class="snhead" onclick={() => (snoozeOpen = !snoozeOpen)} aria-expanded={snoozeOpen}>
        <svg class="snchev" class:openc={snoozeOpen} width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 9l6 6 6-6" /></svg>
        Snoozed
        <span class="sncount mono">{snoozedCount}</span>
      </button>
      {#if snoozeOpen}
        {#each snoozedActive as m (m.machineId + ':' + m.id)}
          {@render snoozedRow(m.machineId, m.id, shortName(m), m.snoozed_until ?? 0, 'live', () => app.open(m.machineId, m.id))}
        {/each}
        {#each snoozedRecent as h (h.machineId + ':' + h.id)}
          {@render snoozedRow(h.machineId, h.id, h.title, h.snoozed_until ?? 0, 'recent', () => resume(h))}
        {/each}
      {/if}
    {/if}

    {#if app.history.length > 0}
      <!-- No icon: the gutter is 14px wide and holds row state marks, so an icon
           here would have to sit outside the text column and make a fourth left
           edge out of a link. The words are the affordance. -->
      <button class="viewall" onclick={() => app.openAllSessions()}>View all sessions →</button>
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
    <!-- An available update sits here, above the destinations, because the
         sidebar is the only chrome that is always on screen. On the home screen
         it was something you had to go and look at, which for the only way to
         update a machine from the app is the wrong shape. It is absent entirely
         when everything is current, so it nudges rather than nags.

         ONE row, however many machines are behind: they all track the same
         channel, so they are all going to the same version and there is a single
         decision to make. -->
    {#if outdatedMachines.length}
      <UpdateNudge machines={outdatedMachines} />
    {/if}
    {#if !outdatedMachines.length && app.versionCheckFailed}
      <!-- A check that could not run has to say so. Showing nothing is exactly
           what "you are up to date" looks like, and a machine sitting a release
           behind a silent screen is how this went unnoticed before. -->
      <p class="ufail mono" title={app.versionCheckFailed}>{app.versionCheckFailed}</p>
    {/if}

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
    <!-- Four destinations you visit rarely, as one icon row rather than four
         labelled rows: as rows they cost about 180px of the session list
         permanently.

         The labels used to be hidden wherever a hover bubble could stand in for
         them, which meant every pointer user had to hover each icon and read a
         tooltip to find out what it opened. A tooltip is a reference, not a
         label: it answers a question you already had to know to ask. So the word
         is always on now. The row costs 4px more than it did and nothing has to
         be discovered.

         The icons then stopped trying to explain and started trying to identify,
         which is a job they can actually do. Three of the four were replaced. A
         speech bubble for Channels collided with New session a few inches above
         it (see the newChat snippet, a bubble with a plus), so within one sidebar
         the same glyph meant both "a conversation" and "how to reach one". Two
         people for Accounts read as collaborators, when these are Claude logins
         and nobody else is involved. And a 2x2 grid for Providers meant nothing
         at all: it is the "apps" icon in every other product on earth. -->
    <nav class="nav" aria-label="Configuration">
      <Hint
        title="Channels"
        body="Reach a session from somewhere that is not this app. Telegram is set up here: pair a chat, and you can drive an agent from your phone without kunai open."
      >
        <button class="navitem" onclick={() => app.openChannels()} aria-label="Channels">
          <!-- Signal, not a speech bubble: a channel is a way IN to a session
               from outside, and the bubble already belongs to the sessions
               themselves. Deliberately generic rather than Telegram's plane,
               because the server assumes more channels are coming. -->
          <span class="nic">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M5.2 5.2a9.6 9.6 0 000 13.6" /><path d="M18.8 5.2a9.6 9.6 0 010 13.6" /><path d="M8.5 8.5a5 5 0 000 7" /><path d="M15.5 8.5a5 5 0 010 7" /><circle cx="12" cy="12" r="1.5" fill="currentColor" stroke="none" /></svg>
          </span>
          <span class="nlbl">Channels</span>
        </button>
      </Hint>
      <Hint
        title="Accounts"
        body="The Claude logins this machine can run a session on. Add another and you can work on two subscriptions at once, or hand a session to whichever one still has quota."
      >
        <button class="navitem" onclick={() => app.openAccounts()} aria-label="Accounts">
          <!-- Claude's own mark, in Claude's own colour. These are Claude logins
               and nothing else, so the most direct thing the icon can say is the
               name of the thing. It is the only warm pixel in the sidebar, which
               is fitting: it is what the whole app is pointed at. -->
          <span class="nic claude">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.1" stroke-linecap="round"><path d="M12 12V3.4" /><path d="M12 12v8.6" /><path d="M12 12H3.4" /><path d="M12 12h8.6" /><path d="M12 12L5.95 5.95" /><path d="M12 12l6.05 6.05" /><path d="M12 12l6.05-6.05" /><path d="M12 12l-6.05 6.05" /></svg>
          </span>
          <span class="nlbl">Accounts</span>
        </button>
      </Hint>
      <Hint
        title="Providers"
        body="Run the agent on a model that is not Claude. A Codex or Grok subscription is authorised here, and every tool, edit and permission keeps working; only the model answering changes."
      >
        <button class="navitem" onclick={() => app.openProviders()} aria-label="Providers">
          <!-- A chip: which brain answers. The whole idea of a provider is that
               everything else about the session is unchanged and only the thing
               doing the thinking is swapped, so the icon is the part being
               swapped. -->
          <span class="nic">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="6.4" y="6.4" width="11.2" height="11.2" rx="2.2" /><path d="M9.9 2.9v3.5M14.1 2.9v3.5M9.9 17.6v3.5M14.1 17.6v3.5M2.9 9.9h3.5M2.9 14.1h3.5M17.6 9.9h3.5M17.6 14.1h3.5" /></svg>
          </span>
          <span class="nlbl">Providers</span>
        </button>
      </Hint>
      <Hint
        title="Usage"
        body="What the work cost. Every transcript on this machine, priced at API rates and split by model and by day, so you can see which brain ate the month rather than only how full the window is."
      >
        <!-- The one nav item that can be "where you are" rather than something
             you open over the app, now that Usage is a route. Marked as such, or
             the sidebar claims nothing is open while the page fills the screen. -->
        <button
          class="navitem"
          class:on={app.showUsage}
          aria-current={app.showUsage ? 'page' : undefined}
          onclick={() => app.openUsage()}
          aria-label="Usage"
        >
          <!-- Bars over time. The dashboard's quota meters already say how full
               the window is; this says where it went, and a chart is the one
               glyph that reads as "over time" without a label. Deliberately not
               a currency mark: the money here is a counterfactual, not a bill,
               and a dollar sign would assert otherwise before the page loads. -->
          <span class="nic">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M4 20h16" /><path d="M6.5 20v-5.5" /><path d="M11 20V8.5" /><path d="M15.5 20v-8.5" /><path d="M20 20V5" /></svg>
          </span>
          <span class="nlbl">Usage</span>
        </button>
      </Hint>
      <Hint
        title="Settings"
        body="Machines on your tailnet, the thermal guard that stops unattended work before a closed laptop cooks, notifications, and updates."
      >
        <button class="navitem" onclick={() => app.openSettings()} aria-label="Settings">
          <span class="nic">{@render gear()}</span>
          <span class="nlbl">Settings</span>
        </button>
      </Hint>
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
  /* Raised from --text-4 (3.07:1 on the sidebar, under AA for text) to --text-3
     (5.11:1). It was legible only if you already knew what it said. */
  .sec {
    font-size: 11.5px;
    font-weight: 550;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-3);
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
  /* One text column at 28px: list padding of 14 plus a 14px gutter. The gutter is
     where every mark in this list lives -- a heading's chevron, and the stem with
     its nodes -- so nothing structural ever pushes the text around. */
  .grp {
    position: relative;
    display: flex;
    align-items: center;
    gap: 4px;
    /* The space above a heading is a margin, not top padding. As padding it was
       asymmetric (14 top, 3 bottom), and anything positioned with top:50% then
       centres on the padding box while the text centres on the content box -- which
       sat the count 5.5px above the label it belongs to. Symmetric padding makes
       50% mean the same thing for both. */
    margin-top: 11px;
    padding: 3px 4px 3px 0;
  }
  .gtoggle {
    display: flex;
    align-items: center;
    gap: 7px;
    flex: 1;
    min-width: 0;
    padding: 4px 0 4px 14px;
    border: 0;
    background: transparent;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  /* In the gutter, not in the flow: leading with it in the flow pushed the label
     fourteen pixels right of the titles underneath, so the heading was outdented
     from its own children.
     Named gchev, not chev. The machine filter inside the search bar already used
     .chev, so making that class absolute sent ITS chevron out of its button to the
     nearest positioned ancestor -- a stray dropdown arrow floating at the sidebar's
     left edge beside the search field. Styles here are component-scoped, not
     globally unique, and two elements in one component can share a name. */
  .gchev {
    position: absolute;
    left: 2px;
    top: 50%;
    margin-top: -5px;
    color: var(--text-4);
    transform: rotate(0deg);
    transition: transform var(--t) var(--ease);
  }
  .grp.shut .gchev {
    transform: rotate(-90deg);
  }
  .grp:hover .gchev {
    color: var(--text-3);
  }
  .glabel {
    flex: none;
    max-width: 62%;
    font-size: 12.5px;
    color: var(--text-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .grp:hover .glabel {
    color: var(--text);
  }
  .grp.named .glabel {
    font-family: var(--sans);
    font-size: 13px;
  }
  .wsmark {
    flex: none;
    width: 4px;
    height: 4px;
    border-radius: 50%;
    background: var(--text-3);
  }
  /* Empty, and that is the point: it pushes the count to the same right-hand
     column the rows' times occupy. A hairline used to live here and it was the
     loudest thing in the sidebar. */
  .gfill {
    flex: 1;
    min-width: 8px;
  }
  /* What the folder reports. Only running and awaiting_permission are ever
     counted; see lib/sidebar.ts for why `starting` is not. */
  .gstate {
    flex: none;
    max-width: 50%;
    font-size: 11.5px;
    color: var(--text-3);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .gstate.alert {
    color: var(--busy);
  }
  /* Positioned from the list's right edge, not left at the end of a flex row.
     In the flow it landed wherever .gtoggle happened to end, and .gtoggle's width
     depends on .gacts -- which still occupies its ~54px while sitting at opacity 0
     waiting for a hover. So a git repository's count sat 54px left of a plain
     folder's, and the column was ragged for a reason nothing on screen explained.
     Absolute puts every count in the same column as the rows' times. */
  .gcount {
    position: absolute;
    right: 10px;
    top: 50%;
    /* translateY, not a negative margin: a margin has to guess the line box, and
       guessing 16px for an 11.5px line sat the number a few pixels above the
       label it belongs to. */
    transform: translateY(-50%);
    font-size: 11.5px;
    font-variant-numeric: tabular-nums;
    color: var(--text-4);
    text-align: right;
  }
  /* It yields to the actions rather than being overlapped by them, the same trade
     a row's time makes with its menu. */
  @media (hover: hover) and (pointer: fine) {
    .grp:hover .gcount {
      opacity: 0;
      transition: opacity var(--t-fast);
    }
  }
  /* Hidden until the pointer arrives, but only where there is a pointer: a touch
     screen never hovers, so on one the actions simply stay. */
  .gacts {
    display: inline-flex;
    align-items: center;
    gap: 1px;
    flex: none;
  }
  @media (hover: hover) and (pointer: fine) {
    .gacts {
      opacity: 0;
      transition: opacity 0.12s;
    }
    .grp:hover .gacts,
    .gacts:focus-within {
      opacity: 1;
    }
  }

  /* The stem: one hairline down a group's rows, with each session a node on it.
     A branch line, which is this product's own vernacular -- the worktree control
     is a branch glyph and the changed-files card reads as `git diff --stat` -- used
     structurally rather than as decoration. It replaced a hairline trailing every
     heading: this is one line per group instead of six across the list, it runs
     with the reading rather than against it, and its length says how much work is
     in the folder.
     It stops short at top and bottom so it reads as a segment belonging to this
     group rather than a rule dividing the whole column. */
  .kids {
    position: relative;
  }
  /* Only where there are two rows to connect. A one-session folder drew a stem a
     few pixels long, which is a line pretending to join one thing; its node says
     everything the stem would have. */
  .kids.stemmed:has(.row + .row)::before {
    content: '';
    position: absolute;
    /* .kids already begins at the list's 14px padding, so the 14px gutter is 0-14
       in ITS coordinates: the stem belongs at 4, not 18. Setting 18 here put the
       line at absolute 32, past where the text starts at 28, and drew every node
       on top of a title's first letter. */
    left: 4px;
    top: 2px;
    bottom: 6px;
    width: 1px;
    background: var(--border);
    /* Drawn from the top on expand: the one orchestrated moment in this list, and
       it belongs to the disclosure that caused it. */
    transform-origin: top;
    animation: stem var(--t-slow) var(--ease);
  }
  @keyframes stem {
    from {
      transform: scaleY(0);
    }
  }

  .row {
    position: relative;
    border-radius: var(--r);
  }
  .row:hover {
    background: var(--panel);
  }
  /* The active session gets a filled card. An earlier version expressed selection
     only through the node and the title's brightness, on the reasoning that a
     pill was the one rounded shape in a column of text. That held while the rows
     were single lines; with three lines each, the list needs a shape to say where
     one row ends and the next begins, and the fill does both jobs at once. */
  .row.current .card {
    background: var(--panel-2);
  }
  .row.current .l2,
  .row.current .tname {
    color: var(--text);
  }
  /* An awaiting row is the one thing in this list actually blocked on you, so it
     is the only row allowed a fill. */
  .row.waiting {
    background: color-mix(in srgb, var(--busy) 9%, transparent);
  }
  .hit {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 8px;
    text-align: left;
    padding: 8px 10px 8px 14px;
  }
  /* The three-line session row: where the work is and what it is doing, then what
     it is doing it to, then where it lands and who pays. Read top to bottom, so
     it is laid out that way rather than as one line with things hung off it. */
  .hit.card {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 3px;
    /* The right gutter is for the row menu, which is a 26px circle absolutely
       positioned against the row at right:6px and vertically centred. A
       single-line row shared that slot with its status and hid the status on
       hover; a three-line row has no single slot to share, so whichever line the
       circle happens to cross would sit underneath it -- which is how the
       account mark ended up behind the three dots. Reserved permanently rather
       than on hover, so revealing the menu never shifts the text. */
    padding: 9px 36px 9px 14px;
    border-radius: 11px;
  }
  .l1,
  .l3 {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
  }
  .fold {
    flex: none;
    color: var(--text-4);
  }
  .proj {
    flex: 1;
    min-width: 0;
    font-size: 12.5px;
    color: var(--text-3);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* The row's reason to exist, so it is the one thing set bright and heavy. */
  .l2 {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    font-size: 14px;
    font-weight: 550;
    line-height: 1.3;
    color: var(--text-2);
  }
  .tname {
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    /* A fade rather than an ellipsis, which is the list's existing habit: a
       clipped title still shows you it continues without spending characters. */
    -webkit-mask-image: linear-gradient(to right, #000 calc(100% - 24px), transparent);
    mask-image: linear-gradient(to right, #000 calc(100% - 24px), transparent);
  }
  .row:hover .l2 {
    color: var(--text);
  }
  .grow {
    flex: 1;
  }
  .where {
    flex: 1;
    min-width: 0;
    font-size: 11.5px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* The account or provider paying for this session, as its own mark. On a
     Claude-only machine every row carries the same one and it reads as texture;
     the moment a Codex or Grok session appears it is the fastest way to tell
     them apart. */
  .mark {
    flex: none;
    opacity: 0.9;
  }
  .status {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 5px;
    font-size: 12px;
    font-weight: 550;
    font-variant-numeric: tabular-nums;
  }
  .status.working {
    color: var(--live);
    /* Duty-cycled breathe (a t3code trick worth copying exactly): steps() with
       long holds means the compositor paints a handful of discrete frames per
       cycle instead of every vsync, so a sidebar of working agents costs
       almost nothing to keep alive. */
    animation: breathe 3.4s steps(8) infinite;
  }
  @keyframes breathe {
    0%,
    38% {
      opacity: 1;
    }
    52%,
    72% {
      opacity: 0.65;
    }
    86%,
    100% {
      opacity: 1;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .status.working {
      animation: none;
    }
  }
  .status.needs {
    color: var(--busy);
  }
  /* Finished while you were away. White, not a new colour: brightness is this
     design's attention mechanism, and the check plus a full-brightness title is
     what "worth opening" looks like in a monochrome list. */
  .status.done {
    color: var(--text);
  }
  /* Back from a snooze, unvisited: amber, because you asked to be re-shown it
     and it is now waiting on your look. */
  .status.wokep {
    color: var(--busy);
  }
  /* A working row recedes: nothing in it needs you until it stops, so it steps
     back and lets the finished rows carry the brightness. Hover restores it,
     and the colored status label keeps its full strength throughout. */
  .row.receding .hit {
    opacity: 0.68;
    transition: opacity 0.15s;
  }
  .row.receding:hover .hit,
  .row.receding.current .hit {
    opacity: 1;
  }
  /* Unread rows are the loud ones: the title takes full brightness. */
  .row.unread .tname,
  .row.unread .name {
    color: var(--text);
  }
  .hit:disabled {
    opacity: 0.55;
  }
  /* A node on the stem, the way a commit sits on a branch line. Centred on the
     stem's 1px at x=18, so the line appears to pass through it.
     A past session gets no node and the line simply passes: marks are for
     sessions with something to say, so a folder of finished work reads as a quiet
     line rather than a column of dots. */
  .node {
    position: absolute;
    /* Centred on the stem: the row shares .kids' origin, so 1px + half of 7 puts
       the centre at 4.5, which is the stem's own centre. */
    left: 1px;
    top: 50%;
    width: 7px;
    height: 7px;
    margin-top: -3.5px;
    border-radius: 50%;
    background: transparent;
  }
  /* The ring cuts the stem so a node reads as sitting ON the line rather than
     crossing it -- but only where there is a node to see. It was on every .node
     including a past session's, whose fill is transparent, so invisible rings
     chopped the stem into ticks: a dashed line with no dots on it, which is
     exactly how it looked. A past session lets the line run straight through. */
  .node:not([data-state='past']) {
    box-shadow: 0 0 0 2.5px var(--bg-raised);
  }
  @media (max-width: 860px) {
    .node:not([data-state='past']) {
      box-shadow: 0 0 0 2.5px var(--bg);
    }
  }
  .node[data-state='idle'],
  /* `starting` is the same quiet mark as idle rather than a busy one: a resumed
     session reads `starting` until its first prompt, so animating it would show
     work that is not happening. */
  .node[data-state='starting'] {
    background: var(--text-4);
  }
  .node[data-state='running'] {
    background: var(--live);
    /* steps(), like the working label: discrete frames, not a vsync-rate tween. */
    animation: soften 2s steps(6) infinite;
  }
  .node[data-state='awaiting_permission'] {
    background: var(--busy);
  }
  /* The one white mark in the list. */
  .row.current .node {
    background: var(--text);
  }
  @keyframes soften {
    50% {
      opacity: 0.45;
    }
  }
  .name {
    flex: 1;
    min-width: 0;
    font-size: 15px;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    -webkit-mask-image: linear-gradient(to right, #000 calc(100% - 26px), transparent);
    mask-image: linear-gradient(to right, #000 calc(100% - 26px), transparent);
  }
  .row:hover .name {
    color: var(--text);
  }
  /* Brightness carries recency. Everything used to sit at --text/16.5:1, the
     maximum, so the eye was pulled equally to a session from three weeks ago and
     to one running right now; past a day a row steps back. */
  .row.stale .name {
    color: var(--text-3);
  }
  .row.stale:hover .name {
    color: var(--text-2);
  }
  /* The right-hand column: a time for a past session, a word for a live one.
     Right-aligned with a floor width so the times line up as a column instead of
     ragged against each title, and padded off the name, whose fade otherwise ran
     straight into it and read as a collision rather than an ellipsis. */
  /* Tabular figures, so "3h" and "22h" occupy the same width and the column is a
     column rather than ragged text that happens to be right-aligned.
     It yields its slot to the row's menu rather than sitting beside it behind a
     permanent 34px reserve, which cost every title that width forever. The
     trigger is 26px at 6px from the edge, so the two overlap by design and only
     one of them is ever visible. */
  .tail {
    flex: none;
    min-width: 30px;
    padding-left: 6px;
    font-size: 11.5px;
    font-variant-numeric: tabular-nums;
    color: var(--text-4);
    text-align: right;
    transition: opacity 0.12s;
  }
  @media (hover: hover) and (pointer: fine) {
    .row:hover .tail,
    .row:has(:global(.wrap.open)) .tail {
      opacity: 0;
    }
  }
  /* A touch screen shows the trigger permanently, because there is no hover to
     reveal it with, so there the two cannot share a slot and the row reserves the
     space instead. */
  @media (pointer: coarse) {
    .hit {
      padding-right: 34px;
    }
  }
  /* A dashed ring, matching the reference: it reads as motion even at 8px, where
     a solid arc just looks like a comma. Drawn from a border with one side
     transparent, so it costs no markup and no asset. */
  .spin {
    flex: none;
    width: 10px;
    height: 10px;
    border: 1.5px dashed currentColor;
    border-radius: 50%;
    opacity: 0.95;
    animation: spin 2.4s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  /* Motion in the corner of the eye is exactly what somebody asks to be rid of,
     and the ring is decoration: the duration beside it carries the meaning, so
     stopping it loses nothing. */
  @media (prefers-reduced-motion: reduce) {
    .spin {
      animation: none;
    }
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
  /* The snoozed shelf's heading: a section heading with a disclosure, quieter
     than the group headings above it because its contents are deliberately out
     of the way. */
  .snhead {
    position: relative; /* anchors the chevron in the gutter, like .gchev */
    width: 100%;
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 8px 10px 6px 14px;
    margin-top: 10px;
    font-size: 11.5px;
    font-weight: 550;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-3);
    text-align: left;
  }
  .snhead:hover {
    color: var(--text-2);
  }
  .snchev {
    position: absolute;
    left: 2px;
    transform: rotate(-90deg);
    transition: transform var(--t) var(--ease);
  }
  .snchev.openc {
    transform: rotate(0deg);
  }
  .sncount {
    font-size: 11px;
    font-variant-numeric: tabular-nums;
    color: var(--text-4);
    text-transform: none;
    letter-spacing: 0;
  }
  /* A shelved row is quiet by construction: its countdown is the one thing it
     says, and like every tail it yields its slot to the menu on hover. */
  .row.snz .name {
    color: var(--text-3);
  }
  .row.snz:hover .name {
    color: var(--text-2);
  }
  .snin {
    color: var(--text-4);
  }

  .viewall {
    width: 100%;
    display: block;
    padding: 10px 10px 10px 14px;
    margin-top: 6px;
    border-radius: var(--r);
    color: var(--text-3);
    font-size: 13px;
    font-weight: 500;
    text-align: left;
  }
  .viewall:hover {
    background: var(--panel);
    color: var(--text);
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
  /* One row of icons rather than four labelled rows, but sized like something you
     are meant to hit: 44px is the touch target these were missing at 38. */
  .nav {
    display: flex;
    gap: 2px;
  }
  .navitem {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 4px;
    flex: 1;
    min-width: 0;
    height: 50px;
    border: 0;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-3);
    font-size: 10px;
    cursor: pointer;
  }
  .navitem:hover {
    color: var(--text);
    background: var(--panel);
  }
  .navitem.on {
    color: var(--text);
    background: var(--panel-2);
  }
  .nic {
    display: flex;
  }
  /* Claude's mark keeps its own colour at every state. Dimming it to the gray
     ramp at rest would throw away the recognition that is the entire reason it
     is here; it only warms slightly on hover, like everything else in the row. */
  .nic.claude {
    color: var(--claude);
  }
  .navitem:hover .nic.claude {
    filter: brightness(1.12);
  }
  /* Always on. The icon identifies; the word is what tells you what it opens,
     and a hover tooltip cannot do that job for someone who has not already
     guessed. The Hint stays for the sentence of detail underneath. */
  .nlbl {
    display: block;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  @media (pointer: coarse) {
    .navitem {
      height: 54px;
      font-size: 10.5px;
    }
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
  /* Quiet: a version check that could not run is not an error, it is an answer
     that has not arrived. It only appears when nothing is known to be outdated,
     so it never competes with a real update. */
  .ufail {
    margin: 0 2px 8px;
    font-size: 10.5px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* The nudge sits above the nav with room to breathe, and stacks when more than
     one machine is behind. */
  .foot > :global(.nudge) {
    margin-bottom: 8px;
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
     there are two of them side by side, so a touch screen gets room to hit the
     one it meant. The worktree label is gone with them: on touch the actions no
     longer hide behind a hover, so the icon is reachable without a word to
     explain a control you can just press. */
  @media (pointer: coarse) {
    .gadd {
      width: 32px;
      height: 32px;
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
