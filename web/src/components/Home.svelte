<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { usage } from '../lib/api'
  import { fetchQuery, keys, peek, SLOW_TTL, USAGE_TTL } from '../lib/query.svelte'
  import type { Usage, UsageWindow } from '../lib/types'
  import { noWorktree, worktreeBranches, type BranchList, type WorktreeChoice } from '../lib/worktrees'
  import Schedules from './Schedules.svelte'
  import PullRequests from './PullRequests.svelte'
  import Spinner from './Spinner.svelte'
  import WorktreeChoicePicker from './WorktreeChoice.svelte'

  let { compact = false }: { compact?: boolean } = $props()

  // Which machine's stats to show. '' = the hub/self. Lets you inspect any
  // machine in the fleet by clicking its tab.
  let picked = $state('')
  const multi = $derived(app.machines.length > 1)
  const sel = $derived(
    app.machines.find((m) => m.id === picked) ??
      app.machines.find((m) => m.self) ??
      app.machines[0] ??
      null,
  )
  const st = $derived(sel?.stats ?? null)
  // The repositories this machine has open, which is exactly the set a review
  // can run against: reviewing needs a real checkout to build a worktree from.
  // Derived from the sessions and history kunai already has rather than from a
  // list somebody maintains, so a repo you work in is reviewable with no setup.
  // A worktree session points at its repository, not at its own directory.
  const reviewRepos = $derived.by(() => {
    if (!sel) return []
    const seen = new Map<string, { machineId: string; cwd: string; label: string }>()
    const add = (machineId: string, repo?: string, cwd?: string) => {
      const dir = (repo || cwd || '').replace(/\/+$/, '')
      if (!dir || machineId !== sel.id || seen.has(dir)) return
      // A review's own throwaway checkout is not a repository. The server tags
      // those sessions with the repo they are reviewing, but a record it cannot
      // resolve would otherwise put the worktree directory here and list the same
      // pull requests twice, once under a heading named after the PR number.
      if (/\/worktrees\/[^/]+\/review\/[^/]+$/.test(dir)) return
      seen.set(dir, { machineId, cwd: dir, label: dir.split('/').pop() || dir })
    }
    for (const s of app.sessions) add(s.machineId, s.repo, s.cwd)
    for (const h of app.history) add(h.machineId, h.repo, h.cwd)
    return [...seen.values()]
  })
  const selSessions = $derived(sel ? app.sessions.filter((s) => s.machineId === sel.id).length : 0)
  const selResumable = $derived(sel ? app.history.filter((h) => h.machineId === sel.id).length : 0)

  const greeting = $derived.by(() => {
    const h = new Date().getHours()
    if (h < 5) return 'Good night'
    if (h < 12) return 'Good morning'
    if (h < 17) return 'Good afternoon'
    return 'Good evening'
  })

  // Memory in binary GiB, the convention for RAM (a "16 GB" stick is 16 GiB).
  function gb(n: number): string {
    if (!n) return '—'
    const g = n / 1024 ** 3
    return g >= 100 ? `${Math.round(g)} GB` : `${g.toFixed(1)} GB`
  }
  // Disk in decimal GB (÷10^9), which is what macOS and disk makers show, so the
  // total matches the OS instead of reading ~7% smaller as binary GiB would.
  function gbDisk(n: number): string {
    if (!n) return '—'
    const g = n / 1e9
    return g >= 100 ? `${Math.round(g)} GB` : `${g.toFixed(1)} GB`
  }
  function dur(sec: number): string {
    if (!sec) return '—'
    const d = Math.floor(sec / 86400)
    const h = Math.floor((sec % 86400) / 3600)
    const m = Math.floor((sec % 3600) / 60)
    if (d > 0) return `${d}d ${h}h`
    if (h > 0) return `${h}h ${m}m`
    return `${m}m`
  }
  const memUsedPct = $derived(
    st && st.mem_total ? Math.round(((st.mem_total - st.mem_available) / st.mem_total) * 100) : 0,
  )
  // Apple Silicon reports a pressure level, not degrees; these two levels mean
  // "backing off".
  const pressureHot = $derived(st?.thermal_pressure === 'serious' || st?.thermal_pressure === 'critical')
  const capitalize = (s: string) => (s ? s[0].toUpperCase() + s.slice(1) : s)

  // A vital is only worth reading when it is a problem, so these are the only
  // reason any of them raises its voice. Everything else stays a quiet line.
  const tempHot = $derived(!!st && (st.cpu_temp_c >= 80 || pressureHot))
  const memHigh = $derived(memUsedPct >= 90)
  const diskLow = $derived(!!st && !!st.disk_total && st.disk_free / st.disk_total < 0.1)
  // What the page is actually asked from a phone at 2am: is anything working?
  const running = $derived(
    selSessions === 0
      ? 'Nothing running'
      : `${selSessions} session${selSessions === 1 ? '' : 's'} running`,
  )

  // The selected machine's Claude quota. Depends on the primitives, not on the
  // machine object, so a stats refresh that changes nothing here doesn't refetch.
  const selId = $derived(sel?.id ?? '')
  const selUrl = $derived(sel?.url ?? '')
  const selOnline = $derived(sel?.online ?? false)
  // Quota is per account: a machine can run more than one Claude login and each
  // has its own windows, so one meter could only ever describe one of them.
  // `clis` is sent only when there is a real choice, so a single-account machine
  // keeps exactly one unlabelled meter as before. '' means the default account.
  const accounts = $derived<string[]>(st?.clis?.length ? st.clis : [''])
  let uses = $state<Record<string, Usage | null>>({})
  // Why an account has no numbers, when it has none. Without this a failed read
  // just deleted the rows, which reads as "still loading" forever.
  let usageErrs = $state<Record<string, string>>({})
  // Whether this machine's quota has come back yet, either way. It gates the
  // skeleton, so the skeleton shows once per machine and never again: a refresh
  // updates the numbers in place rather than blinking the rows away and back.
  let usageLoaded = $state(false)
  // Only a real machine switch clears the meters. A transient poll blip (a
  // dropped fetch that flips a machine offline for one tick) must NOT blank
  // numbers that were right a second ago: that blanking collapsed the quota
  // rows to zero height and shoved the whole sidebar list ("dancing").
  let usageFor = ''
  // Sessions on the machine being shown, split by what they need from you. A
  // session sitting at a permission gate is doing nothing until you answer, which
  // is the one state here that can quietly waste hours.
  const machineSessions = $derived(sel ? app.sessions.filter((s) => s.machineId === sel.id) : [])
  const waiting = $derived(machineSessions.find((s) => s.state === 'awaiting_permission') ?? null)
  const liveSessions = $derived(machineSessions.filter((s) => s.state === 'running'))

  // The best account to work on right now: the most REMAINING headroom on its
  // binding window (the one that walls you first) -- the same rule server-side
  // account failover uses. An account with nothing left is not a candidate.
  const bestAccount = $derived.by(() => {
    let best: { cli: string; left: number; when: string } | null = null
    for (const cli of accounts) {
      const b = binding(uses[cli] ?? null)
      if (!b) continue
      const left = Math.max(0, 100 - b.pct)
      if (left < 2) continue
      if (!best || left > best.left) best = { cli: cli || 'Claude', left, when: b.when }
    }
    return best
  })

  // The hero answers whichever question is live, in priority order.
  const hero = $derived.by(() => {
    if (waiting) return { head: 'Needs you', sub: '' }
    if (liveSessions.length) {
      const n = liveSessions.length
      return { head: n === 1 ? '1 session working' : n + ' sessions working', sub: 'Nothing needs you right now.' }
    }
    if (!usageLoaded) return { head: greeting + '.', sub: 'Checking what your accounts have left\u2026' }
    if (bestAccount) {
      return {
        head: 'Ready to work',
        sub: bestAccount.cli + ' has ' + Math.round(bestAccount.left) + '% left' + (bestAccount.when ? ' \u00b7 ' + bestAccount.when : ''),
      }
    }
    return { head: 'Every account is spent', sub: 'Work resumes when a window resets, or schedule a run for then.' }
  })

  $effect(() => {
    const url = selUrl,
      online = selOnline,
      id = selId,
      names = accounts
    if (id !== usageFor) {
      usageFor = id
      uses = {}
      usageErrs = {}
      usageLoaded = false
    }
    if (!online) {
      usageLoaded = true // an offline machine has no quota to wait for; keep the last-good numbers
      return
    }
    let done = false
    // Each account is asked separately, and one being logged out must not blank
    // the others: settle them independently rather than failing the batch.
    const load = () =>
      Promise.all(
        names.map((cli) =>
          // Through the cache: Home is mounted twice (the dashboard and the
          // sidebar's compact copy), so this fired every usage request twice,
          // 1-8ms apart, measured. Each one shells the claude CLI for about two
          // seconds and the server holds a mutex across it, so the duplicate did
          // not double the work, it blocked a handler waiting for it. Sharing the
          // key means the two mounts share the request and the answer.
          fetchQuery(keys.usage(url, cli), () => usage(url, cli), { ttl: USAGE_TTL })
            .then((u) => {
              if (done) return
              // Keep the last good numbers if a later poll comes back empty: a
              // blip should not blank a meter that was right a minute ago.
              if (u?.session || u?.weekly) {
                uses[cli] = u
                usageErrs[cli] = ''
              } else {
                usageErrs[cli] = u?.unavailable || 'unavailable'
              }
            })
            .catch((e) => {
              if (done) return
              usageErrs[cli] = String(e?.message || e)
            }),
        ),
      ).then(() => {
        if (!done) usageLoaded = true
      })
    load()
    // The server caches for a minute; match it rather than poll faster than the
    // number can move.
    const t = setInterval(load, 60_000)
    return () => {
      done = true
      clearInterval(t)
    }
  })
  // The rolling window only carries a reset time once it has usage in it. An idle
  // 5-hour window (0% used) reports no reset, which is not "unknown" — nothing has
  // started the clock — so say that plainly instead of raising a false alarm.
  function resetsIn(w: UsageWindow | null): string {
    if (!w) return ''
    if (!w.resets_at) return w.percent > 0 ? 'reset time unknown' : 'no usage this window'
    const s = w.resets_at - Math.floor(Date.now() / 1000)
    return s <= 0 ? 'resetting' : `resets in ${dur(s)}`
  }

  // With several accounts, two meters each is a wall: six bars that mostly say
  // "fine". The limit that stops you first is the only honest single reading, so
  // an account collapses to one row showing its fuller window, named, with when
  // it frees up. Same argument the loop meter makes for its two caps. Tapping a
  // row opens both windows, so nothing is lost, it is just not all shouted at
  // once.
  type Binding = { pct: number; window: string; when: string }
  function binding(u: Usage | null): Binding | null {
    const s = u?.session ?? null
    const w = u?.weekly ?? null
    if (!s && !w) return null
    const pick = !s ? w! : !w ? s! : w.percent >= s.percent ? w : s
    return { pct: pick.percent, window: pick === w ? 'weekly' : 'session', when: shortReset(pick) }
  }
  // Terse enough for a phone: "2h 39m", not "resets in 2h 39m".
  function shortReset(w: UsageWindow): string {
    if (!w.resets_at) return w.percent > 0 ? '' : 'idle'
    const s = w.resets_at - Math.floor(Date.now() / 1000)
    return s <= 0 ? 'resetting' : dur(s)
  }
  // Which account's detail is open. One at a time: this is a glance, not a table.
  // null rather than '' for "none", because the default account's own key IS the
  // empty string, so an empty sentinel meant that account could never open.
  let openAcct = $state<string | null>(null)

  // Every window an account reports, in the order they bind: the short one first,
  // because it is the one that stops you today. Named here rather than in the
  // wire type, since what "session" and "weekly" mean is a presentation question:
  // a provider's plan may have one long window and no short one at all.
  function windowsOf(u: Usage | null): { name: string; w: UsageWindow }[] {
    const out: { name: string; w: UsageWindow }[] = []
    if (u?.session) out.push({ name: 'session', w: u.session })
    if (u?.weekly) out.push({ name: 'weekly', w: u.weekly })
    return out
  }

  // The reset as a sentence, for the expanded rows where there is room for one.
  function fullReset(w: UsageWindow): string {
    if (!w.resets_at) return w.percent > 0 ? 'reset time unknown' : 'unused'
    const s = w.resets_at - Math.floor(Date.now() / 1000)
    if (s <= 0) return 'resetting now'
    return `back in ${dur(s)}`
  }

  // Quick-start dirs for the selected machine only — so chips don't each repeat
  // the machine name (that's stated once in the section header).
  const selProjects = $derived.by(() => {
    if (!sel) return []
    const seen = new Set<string>()
    const out: { cwd: string; name: string }[] = []
    const add = (cwd: string) => {
      if (seen.has(cwd) || out.length >= 8) return
      seen.add(cwd)
      out.push({ cwd, name: cwd.replace(/\/+$/, '').split('/').slice(-1)[0] || cwd })
    }
    // Folders with a session already open are listed rather than hidden. They
    // used to be filtered out to avoid offering what was already on screen, but
    // a second agent on a repository you are already working in is the case
    // worktrees exist for, and it was unreachable from here: the folder
    // disappeared from the launcher exactly when you had it open.
    for (const s of app.sessions) if (s.machineId === sel.id) add(s.cwd)
    for (const h of app.history) if (h.machineId === sel.id) add(h.cwd)
    return out
  })

  // The launcher. Starting work is the reason this screen exists, so the screen is
  // mostly an input: type the task, pick where it runs, go. The old flow (choose a
  // folder, land in a session, then type) was three steps for one thought.
  let brief = $state('')
  let dir = $state('')
  let dirOpen = $state(false)
  let dirQuery = $state('')
  // Matching on the whole path, not just the leaf: two checkouts can share a name
  // ("work"), and the thing that tells them apart is the path.
  const dirMatches = $derived.by(() => {
    const q = dirQuery.trim().toLowerCase()
    if (!q) return selProjects
    return selProjects.filter((pr) => (pr.name + ' ' + pr.cwd).toLowerCase().includes(q))
  })
  let launching = $state(false)
  // Where the work runs: whatever you last picked, else the most recent project.
  const targetDir = $derived(dir || selProjects[0]?.cwd || '')
  const targetName = $derived(targetDir.replace(/\/+$/, '').split('/').slice(-1)[0] || targetDir)

  // Whether this work gets a checkout of its own. Off by default, and reset when
  // the folder changes: a base branch and a name chosen for one repository mean
  // nothing in the next.
  let wt = $state<WorktreeChoice>(noWorktree())
  let wtOpen = $state(false)
  let lastDir = ''
  $effect(() => {
    if (targetDir !== lastDir) {
      lastDir = targetDir
      wt = noWorktree()
    }
  })
  // The folder's current branch, so the launcher bar states it the way it states
  // the folder and the account. It used to be reachable only by opening the
  // worktree picker, which meant the one fact you check before starting work was
  // the one thing the bar did not say.
  let onBranch = $state('')
  let branchFor = ''
  $effect(() => {
    const dir = targetDir
    if (!dir || branchFor === dir) return
    branchFor = dir
    // Cached and keyed, for two reasons. Home is mounted twice, so this fired the
    // same branch request twice 1ms apart, measured; and the worktree dialog asks
    // the identical question the moment you open it, so the answer is already here.
    // Seeded from the cache first, so the pill names the branch on the first paint
    // instead of appearing blank and filling in.
    const wtBase = app.baseForMachine(sel?.id ?? '')
    const known = peek<BranchList>(keys.branches(wtBase, dir))?.data
    onBranch = known ? (known.refs.find((r) => r.current)?.name ?? known.default) : ''
    fetchQuery(keys.branches(wtBase, dir), () => worktreeBranches(wtBase, dir), { ttl: SLOW_TTL })
      .then((b) => {
        if (branchFor === dir) onBranch = b.refs.find((r) => r.current)?.name ?? b.default
      })
      .catch(() => {
        // Not a repository, or a machine that cannot answer. The pill then says
        // where the work happens without naming a branch there is not.
      })
  })

  // What the pill says. Armed, it names the branch the new worktree comes off,
  // because that is the choice that was made; otherwise it names the branch the
  // work will actually run on, which is what you would check before starting.
  const wtLabel = $derived(
    wt.on
      ? `worktree of ${wt.base || onBranch || 'the default branch'}`
      : onBranch || 'this checkout',
  )

  async function launch() {
    const text = brief.trim()
    if (!text || !sel || !targetDir || launching) return
    launching = true
    try {
      await app.startWork(sel.id, targetDir, text, { cli: acct, wt })
      brief = ''
      wt = noWorktree()
    } finally {
      launching = false
    }
  }
  // Enter sends, Shift+Enter is a newline: the same contract as the composer, so
  // the muscle memory carries over.
  function onBriefKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      launch()
    }
  }
  // Machines are chosen from a list, not cycled through. Cycling was fine for two
  // and useless for ten: you cannot see what you are choosing between, and reaching
  // the last one takes nine taps.
  let machOpen = $state(false)
  // Which account (or provider) the work runs on. Empty means the machine's default.
  // Worth choosing at the start rather than switching after: accounts differ in what
  // they have left, and a session started on a spent one stops on its first turn.
  let acct = $state('')
  let acctOpen = $state(false)
  const acctNames = $derived<string[]>(st?.clis ?? [])
  const acctLabel = $derived(acct || acctNames[0] || '')
  // Remaining headroom per account, so the choice is made with the number that
  // decides it rather than by remembering which one you burned this morning.
  function acctLeft(name: string): string {
    const b = binding(uses[name] ?? null)
    if (!b) return ''
    const left = Math.max(0, 100 - b.pct)
    return left < 2 ? 'spent' : Math.round(left) + '% left'
  }
  // The dot beside the chosen account is its headroom, not decoration: the whole
  // reason to pick one account over another is which of them can still finish the
  // work, and that is worth knowing before you type rather than after it stops.
  // Unknown quota stays on the gray ramp instead of guessing green.
  function acctDot(name: string): string {
    const b = binding(uses[name] ?? null)
    if (!b) return 'var(--text-4)'
    const left = Math.max(0, 100 - b.pct)
    if (left < 2) return 'var(--alert)'
    return left < 20 ? 'var(--busy)' : 'var(--live)'
  }
</script>

<!-- Every scope control opens something, and without a mark saying so they read as
     labels: the folder and branch above the field state a fact, and nothing about
     them suggests the fact can be changed. -->
{#snippet chev()}
  <svg class="pchev" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6" /></svg>
{/snippet}

<!-- An ambient wash behind the home screen: two very low-contrast radial pools that
     drift on a slow cycle. It is the one gradient in the app, deliberately kept
     under the content and near-invisible (a few percent of white on the near-black
     canvas), so the page still reads as flat monochrome and nothing competes with
     the data. Fixed and pointer-transparent, so it never intercepts a tap. -->
<div class="ambient" aria-hidden="true"></div>
<div class="home" class:compact>
  <!-- An unreachable machine and an available update are the two things worth
       interrupting for, so they sit above the status line rather than in the quiet
       reference block. Both were lost when this screen was restructured; the update
       banner is the only way to update a machine from the app, so its absence was a
       dead end, not a cosmetic one. -->
  {#if !st && sel}
    <div class="offline">
      <span class="odot"></span>
      {sel.label} is offline — no stats to show.
    </div>
  {/if}

  <!-- The update banner used to sit here. It now lives in the sidebar, above the
       configuration nav (UpdateNudge.svelte): the sidebar is the only chrome that
       is always on screen, and this is the one way to update a machine from the
       app, so making it something you had to come to the home screen to find was
       the wrong shape for it. -->

  <!-- Status is a sentence, not a panel: it answers "can I work" in one line. A
       session stuck on a permission gate takes the line over, because an agent
       waiting on a click you never saw is the worst state this product has. -->
  <p class="pulse" class:needs={!!waiting} class:working={!waiting && liveSessions.length > 0}>
    <span class="pdot2" aria-hidden="true"></span>
    {#if waiting}
      <button class="plink" onclick={() => app.open(waiting.machineId, waiting.id)}>
        {waiting.title || waiting.cwd.split('/').slice(-1)[0]} is waiting on you →
      </button>
    {:else}
      <span>{hero.head}{hero.sub ? ` · ${hero.sub}` : ''}</span>
    {/if}
  </p>

  <!-- The launcher IS the page: one field, plus the things a task needs to run.
       It reads top to bottom as the sentence you are composing. WHERE it runs sits
       above the field, because it is what you check before you start typing and it
       is true of the run rather than of the words: folder, branch, machine. The
       field is then only the field. WHO runs it and the Start action sit inside the
       bottom edge, against the text they act on.
       Everything used to be crammed into one row inside the box, which read as a
       toolbar of unrelated controls and left the scope you were about to commit to
       as the least prominent thing on the page. -->
  <!-- Scope and field are one control, so they are one flex child: left as two,
       the column's 20px gap pushed the folder and branch away from the box they
       describe and they read as a stray row belonging to nothing. -->
  <div class="starter">
  <div class="scope">
    <div class="dirwrap">
      <button class="pick" class:on={dirOpen} onclick={() => (dirOpen = !dirOpen)} title={targetDir}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" /></svg>
        <span class="pname mono">{targetName || 'Choose a folder'}</span>
        {@render chev()}
      </button>
        {#if dirOpen}
          <button class="scrim2" onclick={() => (dirOpen = false)} aria-label="Close"></button>
          <div class="dirpop">
            <!-- Typing beats scrolling once there are more folders than fit, and the
                 filter matches the path as well as the name because two checkouts
                 can share a leaf ("work") and only the path distinguishes them. -->
            {#if selProjects.length > 5}
              <input
                class="dirq"
                bind:value={dirQuery}
                placeholder="Filter folders"
                aria-label="Filter folders"
                autocomplete="off"
              />
            {/if}
            <div class="dirlist">
              {#each dirMatches as pr (pr.cwd)}
                <button class:active={pr.cwd === targetDir} onclick={() => { dir = pr.cwd; dirOpen = false; dirQuery = '' }}>
                  <span class="dn">{pr.name}</span>
                  <span class="dp mono">{pr.cwd}</span>
                </button>
              {/each}
              {#if dirMatches.length === 0}
                <p class="dirempty">Nothing matches “{dirQuery}”. Browse for it below.</p>
              {/if}
            </div>
            <!-- Pinned, never scrolled away: it is the escape hatch for every folder
                 the list does not know about, and it used to sit under all of them. -->
            <button class="browse2" onclick={() => { dirOpen = false; dirQuery = ''; app.newSession() }}>
              Browse for a folder…
            </button>
          </div>
        {/if}
      </div>
      {#if multi}
        <div class="dirwrap">
          <button class="pick" class:on={machOpen} onclick={() => (machOpen = !machOpen)} title={sel?.url}>
            <span class="mdot2" class:live={sel?.online}></span>
            <span class="pname">{sel?.label}</span>
            {@render chev()}
          </button>
          {#if machOpen}
            <button class="scrim2" onclick={() => (machOpen = false)} aria-label="Close"></button>
            <div class="dirpop">
              <div class="dirlist">
                {#each app.machines as m (m.id)}
                  <button class:active={m.id === sel?.id} onclick={() => { picked = m.id; machOpen = false }}>
                    <span class="dn">
                      <span class="mdot2" class:live={m.online}></span>
                      {m.label}{m.online ? '' : ' · offline'}
                    </span>
                    <span class="dp mono">{m.url}</span>
                  </button>
                {/each}
              </div>
            </div>
          {/if}
        </div>
      {/if}
      <div class="dirwrap">
        <button
          class="pick wtpick"
          class:on={wtOpen}
          class:armed={wt.on}
          onclick={() => (wtOpen = !wtOpen)}
          title={wt.on ? 'A checkout of its own' : 'Runs on this branch, in this checkout'}
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M6 3v12M6 21a2 2 0 100-4 2 2 0 000 4zM6 7a2 2 0 100-4 2 2 0 000 4zM18 11a2 2 0 100-4 2 2 0 000 4zM18 9v2a4 4 0 01-4 4H6" /></svg>
          <span class="pname" class:mono={!wt.on && !!onBranch}>{wtLabel}</span>
          {@render chev()}
        </button>
      </div>
      <!-- The worktree popover hangs off the scope row's right edge rather than off
           the button: anchored to the button it overflowed whichever edge it was
           pointed at, right on a narrow window and left on a wide one, because the
           button sits in the middle of a row whose width changes. -->
      {#if wtOpen}
        <button class="scrim2" onclick={() => (wtOpen = false)} aria-label="Close"></button>
        <div class="dirpop wtpop">
          <WorktreeChoicePicker
            base={app.baseForMachine(sel?.id ?? '')}
            repo={targetDir}
            bind:value={wt}
            ondone={() => (wtOpen = false)}
          />
        </div>
      {/if}
  </div>

  <div class="launch" class:busy={launching}>
    <textarea
      class="brief"
      bind:value={brief}
      onkeydown={onBriefKey}
      rows="2"
      placeholder="What should Claude work on?"
      aria-label="Describe the task to start"
    ></textarea>
    <div class="lbar">
      {#if acctNames.length > 1}
        <div class="dirwrap up">
          <button class="pick" class:on={acctOpen} onclick={() => (acctOpen = !acctOpen)} title="Account or provider">
            <span class="adot" style:background={acctDot(acctLabel)}></span>
            <span class="pname">{acctLabel}</span>
            {@render chev()}
          </button>
          {#if acctOpen}
            <button class="scrim2" onclick={() => (acctOpen = false)} aria-label="Close"></button>
            <div class="dirpop">
              <div class="dirlist">
                {#each acctNames as name (name)}
                  <button class:active={name === acctLabel} onclick={() => { acct = name; acctOpen = false }}>
                    <span class="dn">{name}</span>
                    <span class="dp mono">{acctLeft(name) || 'no quota reported'}</span>
                  </button>
                {/each}
              </div>
            </div>
          {/if}
        </div>
      {:else}
        <!-- With one account there is no choice to offer, so the space says the one
             thing a first-timer does not know: Enter starts it. -->
        <span class="lkey">Enter to start, Shift Enter for a new line</span>
      {/if}
      <span class="lspacer"></span>
      <button class="go" disabled={!brief.trim() || !targetDir || launching} onclick={launch}>
        {#if launching}<Spinner />{/if}
        {launching ? 'Starting…' : 'Start'}
      </button>
    </div>
  </div>
  </div>

  <!-- Reference below: dense and quiet, one line each. These answer questions you
       ask occasionally, so they take the space that deserves. -->
  <div class="ref">
    {#if st}
      {#if st.thermal_trip}
        <p class="alarm">Ran too hot — the guard stopped every session here.</p>
      {/if}
      <div class="caps">
        {#each accounts as cli (cli)}
          {@const b = binding(uses[cli] ?? null)}
          {@const err = usageErrs[cli] ?? ''}
          {@const left = b ? Math.max(0, 100 - b.pct) : null}
          {@const spent = left !== null && left < 2}
          {@const rows = windowsOf(uses[cli] ?? null)}
          <!-- Collapsed, an account is one line about the limit that stops you
               first, because several accounts times two windows is a wall of bars
               that mostly say "fine". Both windows are a tap away rather than
               hidden: the reset that matters is often the other one. -->
          <button
            class="cap"
            class:spent
            class:on={openAcct === cli}
            disabled={rows.length === 0}
            aria-expanded={openAcct === cli}
            onclick={() => (openAcct = openAcct === cli ? null : cli)}
          >
            <span class="cname">{cli || 'Claude'}</span>
            {#if left !== null}
              <span class="cbar" aria-hidden="true"><i style="width:{spent ? 0 : Math.max(4, left)}%"></i></span>
              <span class="cnum mono">{spent ? 'spent' : Math.round(left) + '%'}</span>
              <!-- When it frees up, on the line rather than in a tooltip: a phone
                   has no hover, and "39%" without a time is half an answer. -->
              {#if b?.when}<span class="cwhen mono">{b.when}</span>{/if}
            {:else}
              <span class="cnum mono">{err ? 'no quota' : '—'}</span>
            {/if}
          </button>
        {/each}
      </div>
      <!-- Why, for the accounts that could not answer.
           "no quota" alone is indistinguishable from "this provider has no quota
           to show", and the server knows better than that: a stale login comes
           back as "the Codex login has expired; sign in to Codex again". The
           sentence is printed rather than classified into a shorter word, because
           a title attribute is nothing on a phone and guessing the category from
           the prose is a rule that rots. Only shown when a reason exists, so a
           healthy machine gains no line. -->
      {#each Object.entries(usageErrs).filter(([, m]) => m) as [cli, msg] (cli)}
        <p class="capwhy">
          <span class="cwho">{cli || 'Claude'}</span>{msg}
        </p>
      {/each}
      {#if openAcct !== null && windowsOf(uses[openAcct] ?? null).length > 0}
        <!-- Every window this account has, named, with what is left and when it
             comes back. One account at a time: this is a glance, not a table. -->
        <dl class="capdetail mono">
          {#each windowsOf(uses[openAcct] ?? null) as row (row.name)}
            {@const wleft = Math.max(0, 100 - row.w.percent)}
            <div class:spent={wleft < 2}>
              <dt>{row.name}</dt>
              <dd class="dbar" aria-hidden="true"><i style="width:{wleft < 2 ? 0 : Math.max(4, wleft)}%"></i></dd>
              <dd class="dnum">{Math.round(wleft)}% left</dd>
              <dd class="dwhen">{fullReset(row.w)}</dd>
            </div>
          {/each}
        </dl>
      {/if}
      <p class="vline mono">
        {#if st.hostname}{st.hostname}{/if}
        {#if st.cpu_temp_c > 0}<span class:warn={tempHot}> · {Math.round(st.cpu_temp_c)}°C</span>
        {:else if st.thermal_pressure}<span class:warn={tempHot}> · {capitalize(st.thermal_pressure)}</span>{/if}
        {#if st.mem_total}<span class:warn={memHigh}> · {memUsedPct}% memory</span>{/if}
        {#if st.disk_total}<span class:warn={diskLow}> · {gbDisk(st.disk_free)} free</span>{/if}
        {#if st.uptime_sec}<span> · up {dur(st.uptime_sec)}</span>{/if}
        {#if st.claude_version}<span> · claude {st.claude_version}</span>{/if}
      </p>
    {/if}
    <!-- The pull requests on the repositories this machine has open, each one a
         click away from a review. Below the machine's own numbers, because it is
         work you might pick up rather than the state of the box. -->
    <PullRequests repos={reviewRepos} />
    <Schedules />
  </div>
</div>

<style>
  /* The ambient wash. Two pools, offset and drifting on different periods so the
     motion never reads as a loop you can count. Amplitude is tiny on purpose: this
     is atmosphere, not decoration, and the theme stays near-monochrome. */
  .ambient {
    position: fixed;
    inset: 0;
    z-index: 0;
    pointer-events: none;
    background:
      /* The pool the launcher sits in. Centred and widest, so the hero is lifted off
         the canvas by light rather than by a heavier border. */
      radial-gradient(46rem 30rem at 50% 42%, rgba(255, 255, 255, 0.055), transparent 68%),
      radial-gradient(58rem 38rem at 14% 6%, rgba(255, 255, 255, 0.03), transparent 70%),
      radial-gradient(48rem 34rem at 90% 88%, rgba(255, 255, 255, 0.022), transparent 72%);
    animation: drift 42s ease-in-out infinite alternate;
  }
  @keyframes drift {
    from {
      transform: translate3d(0, 0, 0) scale(1);
    }
    to {
      transform: translate3d(-2.5%, 1.5%, 0) scale(1.06);
    }
  }
  /* Motion is atmosphere, so it is the first thing to go when it is unwelcome.
     The wash itself stays: it costs nothing and carries no information. */
  @media (prefers-reduced-motion: reduce) {
    .ambient {
      animation: none;
    }
  }
  .home {
    display: flex;
    flex-direction: column;
    gap: 18px;
    /* Sit above the wash. */
    position: relative;
    z-index: 1;
  }
  /* Full (desktop pane) variant centers a wider column */
  .home:not(.compact) {
    max-width: 660px;
    margin: 0 auto;
    width: 100%;
    padding: 32px;
    gap: 20px;
    /* Center the column in the pane. The previous layout pinned everything to the
       top and left a screenful of void underneath, which reads as unfinished;
       space distributed evenly reads as composed. */
    min-height: 100%;
    justify-content: center;
  }
  .hello h1 {
    font-size: 26px;
    font-weight: 600;
    letter-spacing: -0.02em;
    margin: 0 0 6px;
  }
  .compact .hello h1 {
    font-size: 21px;
  }
  .sub {
    margin: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    font-size: 11.5px;
  }
  .host {
    color: var(--text-2);
  }
  .dim {
    color: var(--text-4);
  }
  .mpick {
    display: flex;
    gap: 7px;
    flex-wrap: wrap;
  }
  .mp {
    max-width: 220px;
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 7px 13px;
    border-radius: 100px;
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-3);
    font-size: 13px;
    font-weight: 500;
  }
  .mp .pdot {
    flex: none;
  }
  .plabel {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .mp:hover {
    color: var(--text-2);
    border-color: var(--border-2);
  }
  .mp.on {
    color: var(--text);
    background: var(--panel-3);
    border-color: var(--border-2);
  }
  .mp.off {
    opacity: 0.55;
  }
  .pdot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .pdot.live {
    background: var(--live);
  }
  .offline {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 14px 16px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    color: var(--text-3);
    font-size: 13px;
  }
  .odot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--alert);
  }
  /* Quota: the page's one piece of weight. Reuses the track/fill and mono
     numerals the context meter already uses, so a budget reads the same
     everywhere in kunai. */
  /* The multi-account roster: one line per account, and the meter IS the row's
     fill, so a bar costs no vertical space at all. Three accounts read as three
     lines rather than six bars. */
  .qroster {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-bottom: 20px;
    max-width: 34rem;
  }
  .qrow {
    position: relative;
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    text-align: left;
    padding: 9px 12px;
    border-radius: 8px;
    overflow: hidden;
    background: var(--panel);
  }
  .qrow:hover,
  .qrow.open {
    background: var(--panel-2);
  }
  /* The fill sits behind the text: positioned so it paints over the row's own
     background, with the labels positioned after it so they stay on top. */
  .qfill {
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    background: var(--panel-3);
    pointer-events: none;
  }
  .qrow.hot .qfill {
    background: color-mix(in oklab, var(--busy) 20%, transparent);
  }
  .qname,
  .qwhen,
  .qpct {
    position: relative;
  }
  .qname {
    flex: 1;
    min-width: 0;
    font-size: 13.5px;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .qwhen {
    flex: none;
    font-size: 11px;
    color: var(--text-4);
  }
  .qpct {
    flex: none;
    font-size: 14px;
    color: var(--text);
    font-variant-numeric: tabular-nums;
    min-width: 2.6rem;
    text-align: right;
  }
  .qrow.hot .qpct {
    color: var(--busy);
  }
  .qpct small {
    font-size: 0.72em;
    opacity: 0.65;
  }
  /* Both windows, revealed under the row that asked for them. */
  .qdetail {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 10px 12px 6px;
  }
  .quota {
    display: flex;
    flex-direction: column;
    gap: 11px;
    margin-bottom: 20px;
    /* A row has to read as one unit. Left to fill the canvas, the eye travels
       from bar to number to reset and the three stop belonging together. */
    max-width: 34rem;
  }
  .q {
    display: grid;
    grid-template-columns: 3.4rem 1fr auto auto;
    align-items: center;
    gap: 12px;
  }
  .q-k {
    font-size: 13px;
    color: var(--text-2);
  }
  .q-track {
    height: 6px;
    border-radius: 100px;
    background: var(--panel-3);
    overflow: hidden;
  }
  .q-track i {
    display: block;
    height: 100%;
    border-radius: 100px;
    background: var(--text-2);
  }
  .q-track i.hot {
    background: var(--busy);
  }
  /* The skeleton is the same row with nothing in it: an empty track and a dash.
     No shimmer — a pulse here would be one more thing moving on a page whose
     whole point is that a quiet machine looks quiet. */
  .q.skel .q-pct {
    color: var(--text-4);
  }
  .q-pct {
    font-size: 15px;
    color: var(--text);
    font-variant-numeric: tabular-nums;
    min-width: 2.8rem;
    text-align: right;
  }
  .q-pct small {
    font-size: 0.72em;
    color: var(--text-3);
    margin-left: 1px;
  }
  .q-pct.hot {
    color: var(--busy);
  }
  .q-when {
    font-size: 12px;
    color: var(--text-3);
    text-align: right;
    white-space: nowrap;
  }
  /* On a phone the reset earns its own row rather than squeezing the track. */
  .home.compact .q {
    grid-template-columns: 1fr auto;
    row-gap: 7px;
  }
  .home.compact .q-k {
    order: 1;
  }
  .home.compact .q-pct {
    order: 2;
  }
  .home.compact .q-track {
    order: 3;
    grid-column: 1 / -1;
  }
  .home.compact .q-when {
    order: 4;
    grid-column: 1 / -1;
    text-align: left;
    min-width: 0;
  }
  .alarm {
    margin: 0 0 8px;
    font-size: 13px;
    color: var(--alert);
  }
  .status {
    display: flex;
    flex-direction: column;
    gap: 2px;
    margin-bottom: 26px;
  }
  .state {
    margin: 0;
    font-size: 13.5px;
    color: var(--text-2);
  }
  .sresume {
    color: var(--text-4);
  }
  .vitals {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0 8px;
    font-size: 12px;
    color: var(--text-4);
  }
  /* On the phone dashboard the vitals sit at the top of the session list, so a
     poll that changes a number's width (uptime ticking, "Nominal"→"45°C") must
     not wrap the line and shove every row below it. Pin it to one line; the
     least-important tail (uptime) is what clips if space runs out. */
  .home.compact .vitals {
    flex-wrap: nowrap;
    overflow: hidden;
  }
  /* The dots come from the layout, not the markup, so a vital that is absent
     never leaves a separator stranded. */
  .vitals span + span::before {
    content: '·';
    margin-right: 8px;
    color: var(--text-4);
    opacity: 0.5;
  }
  .state.live {
    color: var(--text);
  }
  .sdot {
    display: inline-block;
    width: 6px;
    height: 6px;
    margin-right: 7px;
    border-radius: 100px;
    background: var(--text-4);
    vertical-align: 1px;
  }
  .sdot.live {
    background: var(--live);
  }
  .vitals .warn {
    color: var(--busy);
  }

  .start {
    display: flex;
    flex-direction: column;
    gap: 9px;
  }
  .s-label {
    font-size: 11.5px;
    font-weight: 550;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-4);
    padding: 0 2px;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 8px 13px;
    border-radius: 100px;
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-2);
    font-size: 13px;
    font-weight: 500;
    max-width: 100%;
  }
  .chip:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .chip.browse {
    color: var(--text-3);
    border-style: dashed;
  }

  /* --- the adaptive hero -------------------------------------------------- */
  /* The dot carries the app's whole status language in one mark: amber while a
     session is stuck on you, green while work is happening, grey when idle. */
  .lede {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .ltop {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .ldot {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .lede.working .ldot {
    background: var(--live);
  }
  .lede.needs .ldot {
    background: var(--busy);
    animation: pulse 1.6s ease-in-out infinite;
  }
  @keyframes pulse {
    50% {
      opacity: 0.3;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .lede.needs .ldot {
      animation: none;
    }
  }
  .lede h1 {
    margin: 0;
    font-size: 26px;
    font-weight: 600;
    letter-spacing: -0.02em;
  }
  .compact .lede h1 {
    font-size: 21px;
  }
  .lsub {
    margin: 0;
    font-size: 14px;
    color: var(--text-2);
  }
  /* The one call to action on the page, so the only thing allowed to read as a
     link rather than a label. */
  .lact {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 0;
    font-size: 14px;
    font-weight: 500;
    color: var(--text);
  }
  .lact:hover {
    text-decoration: underline;
    text-underline-offset: 3px;
  }
  .larr {
    color: var(--text-3);
  }
  .lmeta {
    margin: 2px 0 0;
    font-size: 12px;
    color: var(--text-3);
  }
  .lmeta .dim {
    color: var(--text-4);
  }

  /* --- two columns -------------------------------------------------------- */
  .cols {
    display: grid;
    gap: 24px 40px;
    grid-template-columns: 1fr;
  }
  /* Split only when there is genuinely room for two readable columns. A phone
     stacks act-then-know, the same priority the hero follows -- two columns at
     390px is two cramped ones, not a layout. */
  @media (min-width: 900px) {
    .home:not(.compact) .cols {
      grid-template-columns: minmax(0, 1.1fr) minmax(0, 1fr);
      align-items: start;
    }
  }
  .col {
    display: flex;
    flex-direction: column;
    gap: 22px;
    min-width: 0;
  }
  .blk {
    display: flex;
    flex-direction: column;
    gap: 9px;
  }
  .blabel {
    margin: 0;
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-4);
  }

  /* --- running session rows ----------------------------------------------- */
  .rows {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .srow {
    display: flex;
    align-items: center;
    gap: 9px;
    width: 100%;
    padding: 7px 9px;
    border-radius: var(--r-sm);
    text-align: left;
    font-size: 13.5px;
    color: var(--text-2);
  }
  .srow:hover {
    background: var(--panel);
    color: var(--text);
  }
  .sd {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .sd.live {
    background: var(--live);
  }
  .stitle {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .srsp {
    flex: 1;
    min-width: 8px;
  }
  .scwd {
    flex: none;
    font-size: 11.5px;
    color: var(--text-4);
  }

  /* Capacity: the fill is remaining headroom, so a long bar is good news. It stays
     a low-contrast wash rather than a colour, because "how much is left" is a
     quantity to read, not an alarm. */
  .qhead {
    margin-bottom: 1px;
  }
  .qrow .qfill {
    background: rgba(255, 255, 255, 0.05);
  }
  /* A spent account is not an error, it is simply unavailable: no bar, dimmed, and
     the only thing it still owes you is when it comes back. */
  .qrow.spent {
    background: transparent;
  }
  .qrow.spent .qname,
  .qrow.spent .qpct {
    color: var(--text-4);
  }
  .qrow.spent .qwhen {
    color: var(--text-3);
  }

  /* --- status: one line ---------------------------------------------------- */
  .pulse {
    display: flex;
    align-items: center;
    gap: 9px;
    margin: 0;
    font-size: 14px;
    color: var(--text);
  }
  .pdot2 {
    flex: none;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--text-3);
  }
  .pulse.working .pdot2 {
    background: var(--live);
  }
  .pulse.needs .pdot2 {
    background: var(--busy);
    animation: hpulse 1.6s ease-in-out infinite;
  }
  @keyframes hpulse {
    50% {
      opacity: 0.3;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .pulse.needs .pdot2 {
      animation: none;
    }
  }
  .plink {
    padding: 0;
    font-size: 13.5px;
    font-weight: 500;
    color: var(--text);
  }
  .plink:hover {
    text-decoration: underline;
    text-underline-offset: 3px;
  }

  /* --- the launcher: the one element with real presence -------------------- */
  .launch {
    position: relative; /* the account popover anchors to the card, not the pill */
    display: flex;
    flex-direction: column;
    background: var(--panel-2);
    border: 1px solid #64666c; /* ~3:1 against the canvas: WCAG 1.4.11 floor for a UI boundary, and this is the hero */
    border-radius: 18px;
    padding: 15px 16px 10px;
    transition: border-color 0.14s;
  }
  .launch:focus-within {
    border-color: var(--text-3);
    background: var(--panel-3);
  }
  .launch.busy {
    opacity: 0.65;
  }
  .brief {
    width: 100%;
    resize: none;
    background: none;
    border: none;
    outline: none;
    padding: 0;
    color: var(--text);
    font-size: 17px;
    line-height: 1.5;
    min-height: 58px;
    max-height: 220px;
  }
  .brief::placeholder {
    color: var(--text-3);
  }
  /* Where the run lands, above the field. No pill chrome and no separators: these
     are a line of facts about the run you are about to start, so they should read
     as a sentence rather than as a toolbar, and the chevron is what says each one
     can be changed. The row is the popover anchor for everything in it. */
  .starter {
    display: flex;
    flex-direction: column;
  }
  .scope {
    position: relative;
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 2px;
    margin: 0 0 9px;
    padding: 0 4px;
  }
  .lbar {
    position: relative;
    display: flex;
    align-items: center;
    gap: 3px;
    padding-top: 8px;
  }
  /* The keyboard hint takes the space the account picker would, so the bottom edge
     is never a lone button floating against nothing. */
  .lkey {
    padding-left: 3px;
    color: var(--text-4);
    font-size: 11.5px;
  }
  /* A keyboard shortcut is not advice you can act on without a keyboard, and on a
     phone the line runs right up against Start. */
  @media (pointer: coarse) {
    .lkey {
      display: none;
    }
  }
  .adot {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
  }
  .pchev {
    flex: none;
    margin-left: -2px;
    color: var(--text-4);
  }
  .pick:hover .pchev,
  .pick.on .pchev {
    color: var(--text-3);
  }
  .lspacer {
    flex: 1;
    min-width: 6px;
  }
  .pick {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
    height: 30px;
    padding: 0 9px;
    border-radius: 8px;
    color: var(--text-2);
    font-size: 12.5px;
    font-weight: 500;
  }
  .pick:hover,
  .pick.on {
    color: var(--text);
    background: var(--panel-2);
  }
  .pname {
    max-width: 150px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  /* A branch name is one word you need whole: "feat-rule-builder…" and
     "feat-rule-builder-v2…" are the same string until they are not. Mono is
     narrower per character, so this costs less width than it reads. */
  .pname.mono {
    max-width: 220px;
    font-size: 12px;
  }
  .mdot2 {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .mdot2.live {
    background: var(--live);
  }
  /* Start is the only filled control on the page, because it is the only thing the
     page is for. */
  .go {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 30px;
    padding: 0 16px;
    border-radius: 8px;
    background: var(--text);
    color: #0b0b0c;
    font-size: 12.5px;
    font-weight: 600;
  }
  .go:disabled {
    background: transparent;
    border: 1px solid var(--border-2);
    color: var(--text-3);
  }
  .dirwrap {
    position: relative;
    display: inline-flex;
    min-width: 0;
  }
  .scrim2 {
    position: fixed;
    inset: 0;
    z-index: 30;
  }
  /* Three bands: an optional filter, the scrolling list, and a pinned action. Only
     the middle one scrolls, so Browse is always one tap away. */
  /* Popovers drop DOWN by default, because the controls that own them now sit
     above the field: opening upward from there would put the list off the top of
     the page. The one that still opens upward is the account picker, which lives
     on the field's bottom edge; it carries .up. */
  .dirpop {
    position: absolute;
    z-index: 31;
    top: calc(100% + 8px);
    left: 0;
    display: flex;
    flex-direction: column;
    min-width: 280px;
    max-width: calc(100vw - 32px);
    max-height: min(60vh, 380px);
    overflow: hidden;
    padding: 5px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
  }
  .dirpop button {
    width: 100%;
    display: flex;
    flex-direction: column;
    gap: 1px;
    padding: 7px 10px;
    border-radius: var(--r-sm);
    text-align: left;
  }
  .dirpop button:hover,
  .dirpop button.active {
    background: var(--panel-3);
  }
  .dn {
    font-size: 13px;
    color: var(--text);
  }
  .dp {
    font-size: 11px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    unicode-bidi: plaintext;
  }
  /* Anchored to the pill's right edge, because this pill sits at the right of the
     bar and a left-anchored popover hung out past the card. Same width and
     padding as the folder and account popovers it sits beside: it is a menu, not
     a dialog. */
  .up .dirpop {
    top: auto;
    bottom: calc(100% + 8px);
  }
  .wtpop {
    left: auto;
    right: 0;
    top: calc(100% + 6px);
    min-width: 260px;
    max-width: min(320px, calc(100% - 20px));
    padding: 5px;
    overflow: visible;
  }
  /* An armed pill states the choice rather than merely looking selected: this is
     the one pill whose value changes where the work lands. */
  .pick.armed {
    color: var(--text);
    border-color: var(--border-2);
  }

  .browse2 {
    flex: none;
    margin-top: 4px;
    padding-top: 8px;
    border-top: 1px solid var(--border-2);
    color: var(--text-2);
    font-size: 12.5px;
    font-weight: 500;
  }
  .browse2:hover {
    color: var(--text);
  }

  /* --- reference: dense, quiet ---------------------------------------------- */
  .ref {
    display: flex;
    flex-direction: column;
    gap: 11px;
  }
  /* Capacity as inline chips, each bar showing what is LEFT, so a longer bar is
     good news and a spent account simply says when it is back. Every account in
     one glance, instead of a stack of full-width slabs that read like alarms. */
  .caps {
    display: flex;
    flex-wrap: wrap;
    gap: 7px 16px;
  }
  .cap {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 3px 6px;
    margin: -3px -6px;
    border: 0;
    border-radius: var(--r-sm);
    background: transparent;
    font: inherit;
    font-size: 12.5px;
    color: var(--text);
    cursor: pointer;
    text-align: left;
  }
  .cap:hover:not(:disabled),
  .cap.on {
    background: var(--panel);
  }
  .cap:disabled {
    cursor: default;
  }
  .cap.spent {
    color: var(--text-3);
  }
  /* When it frees up, quieter than the number: the number is the state, the time
     is the consequence. */
  .cwhen {
    font-size: 11px;
    color: var(--text-4);
  }

  /* Both windows, only for the account you asked about. A table for three
     accounts at once is the wall the collapsed line exists to avoid. */
  /* One quiet line per account that could not report, under the pills rather
     than inside them: the pill is a glance and this is a sentence. */
  .capwhy {
    margin: 7px 2px 0;
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-4);
  }
  .cwho {
    color: var(--text-3);
  }
  .cwho::after {
    content: ' — ';
  }

  .capdetail {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin: 8px 0 0;
    padding: 8px 10px;
    border-left: 1px solid var(--border);
    font-size: 11.5px;
  }
  .capdetail div {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .capdetail dt {
    width: 54px;
    flex: none;
    color: var(--text-3);
  }
  .capdetail dd {
    margin: 0;
    color: var(--text-2);
  }
  .capdetail .dbar {
    flex: none;
    width: 60px;
    height: 4px;
    border-radius: 2px;
    background: #34363b;
    overflow: hidden;
  }
  .capdetail .dbar i {
    display: block;
    height: 100%;
    background: var(--text-2);
  }
  .capdetail .dnum {
    width: 62px;
    flex: none;
  }
  .capdetail .dwhen {
    color: var(--text-4);
  }
  .capdetail div.spent .dnum,
  .capdetail div.spent dt {
    color: var(--text-4);
  }
  .cname {
    max-width: 130px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .cbar {
    flex: none;
    width: 46px;
    height: 4px;
    border-radius: 2px;
    background: #34363b;
    overflow: hidden;
  }
  .cbar i {
    display: block;
    height: 100%;
    background: var(--text-2);
  }
  .cnum {
    font-size: 12px;
    color: var(--text-2);
  }
  .cap.spent .cnum {
    color: var(--text-3);
  }
  .vline {
    margin: 0;
    font-size: 12px;
    color: var(--text-3);
  }
  .vline .warn {
    color: var(--alert);
  }

  .dirlist {
    flex: 1 1 auto;
    min-height: 0;
    overflow-y: auto;
    overscroll-behavior: contain;
  }
  .dirq {
    flex: none;
    width: 100%;
    margin-bottom: 4px;
    padding: 7px 10px;
    border: 1px solid var(--border-2);
    border-radius: var(--r-sm);
    background: var(--panel);
    color: var(--text);
    font-size: 13px;
    outline: none;
  }
  .dirq:focus {
    border-color: var(--text-4);
  }
  .dirq::placeholder {
    color: var(--text-4);
  }
  .dirempty {
    margin: 0;
    padding: 10px;
    font-size: 12.5px;
    color: var(--text-3);
  }

  /* A machine row carries its status dot inline with the name, so "which machine"
     and "is it reachable" are one glance rather than two. */
  .dirpop .dn {
    display: flex;
    align-items: center;
    gap: 7px;
  }
</style>
