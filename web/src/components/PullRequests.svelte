<script lang="ts">
  import { untrack } from 'svelte'
  import { app } from '../lib/app.svelte'
  import { githubApp, listPullRequests, startReview, type PullRequest } from '../lib/api'
  import { shortAgo } from '../lib/reltime'
  import { workedFor } from '../lib/sidebar'
  import { mention } from '../lib/reviewer'
  import { fetchQuery, keys, SLOW_TTL } from '../lib/query.svelte'

  // How long a pull-request listing is reused. Generous, because each one costs
  // two GitHub calls per pull request and the list barely moves; the poll below
  // forces a fresh read while a review is actually running.
  const PULLS_TTL = 30_000

  // The clock behind "Reviewing 2m".
  let now = $state(Date.now())

  // The open pull requests on the repositories this machine has checked out.
  //
  // Scoped to repos kunai already knows, which is not a limitation to work
  // around: a review is worth running here rather than in CI precisely because
  // the agent reads the real tree at the pull request's head, and that needs a
  // clone to make a worktree from. A repo you have not opened has nothing to
  // review against.
  let { repos }: { repos: { machineId: string; cwd: string; label: string }[] } = $props()

  type Row = PullRequest & { machineId: string; cwd: string; repoLabel: string }

  let rows = $state<Row[]>([])
  let loading = $state(false)
  let err = $state('')
  // Which pull request is being opened, so one click cannot start two reviews.
  let starting = $state('')

  const key = (r: { machineId: string; cwd: string; number: number }) =>
    `${r.machineId} ${r.cwd} ${r.number}`

  // Whether this machine can act as the App at all. Asked BEFORE anything else,
  // and the card renders nothing at all until the answer is yes.
  //
  // Not a nicety: without it, a machine with no App configured fetched every
  // repository, failed every fetch, and put "kunai has no GitHub App on this
  // machine yet" on the dashboard of everybody who does not use this feature.
  // A capability nobody has asked for should be invisible, not an error.
  let configured = $state(false)

  const repoKey = $derived(repos.map((r) => `${r.machineId} ${r.cwd}`).join('|'))

  // Every request goes through the shared cache, because Home is mounted TWICE
  // (the dashboard and the sidebar's compact copy) and every fetch here was
  // therefore firing twice, milliseconds apart, against somebody's GitHub rate
  // limit. Each pull request already costs two GitHub calls to enrich, so a
  // repository with ten open ones was making forty round trips per load instead
  // of twenty. The usage meters hit this exact problem and this exact fix.
  //
  // `force` on the polling path, so following a running review still gets fresh
  // answers while the two mounts continue to share the one request.
  async function load(force = false) {
    if (!repos.length) return
    const base0 = app.baseForMachine(repos[0].machineId)
    try {
      configured = (await fetchQuery(keys.githubApp(base0), () => githubApp(base0), { ttl: SLOW_TTL })).configured
    } catch {
      configured = false
    }
    if (!configured) {
      rows = []
      return
    }
    loading = true
    err = ''
    // Note what is NOT done here: rows are not cleared before the fetch. They
    // are replaced when an answer arrives, so a reload cannot make the card
    // blink out and back.
    // Every repo asked at once, and a repo that fails is simply absent rather
    // than taking the card down with it: one repository without the App
    // installed must not hide the pull requests on the others.
    const results = await Promise.allSettled(
      repos.map(async (r) => {
        const base = app.baseForMachine(r.machineId)
        const prs = await fetchQuery(keys.pulls(base, r.cwd), () => listPullRequests(base, r.cwd), {
          ttl: PULLS_TTL,
          force,
        })
        return prs.map((pr) => ({ ...pr, machineId: r.machineId, cwd: r.cwd, repoLabel: r.label }))
      }),
    )
    const next: Row[] = []
    let failures = 0
    for (const res of results) {
      if (res.status === 'fulfilled') next.push(...res.value)
      else failures++
    }
    rows = next
    // Retire the optimistic entries the server now knows about, or a click whose
    // review never registered would leave the card polling for ever.
    if (Object.keys(optimistic).length) {
      const known = new Set(next.filter((r) => r.review).map((r) => key(r)))
      const rest = Object.fromEntries(Object.entries(optimistic).filter(([k]) => !known.has(k)))
      if (Object.keys(rest).length !== Object.keys(optimistic).length) optimistic = rest
    }
    if (!next.length && failures === repos.length) {
      err = (results[0] as PromiseRejectedResult | undefined)?.reason?.message ?? 'Could not read pull requests'
    }
    loading = false
  }

  $effect(() => {
    // Keyed on the repo list so adding a project loads its pull requests too,
    // and untracked so nothing load() reads can pull the effect round again.
    //
    // repoKey is a STRING, derived above rather than computed here, and that is
    // load-bearing. Reading `repos` inside the effect makes the ARRAY the
    // dependency, and the app store hands out a fresh array on every poll even
    // when the fleet has not moved. So this re-ran every few seconds: GitHub
    // calls on a beat nobody asked for, and a heading that appeared with
    // "reading…" beside it and vanished again, shoving the dashboard around each
    // time. A string of the same information changes only when the information
    // does. The Usage page had this exact bug and this exact fix.
    void repoKey
    untrack(() => void load())
  })

  // Drafts are hidden by default, and that is not a preference: a draft is
  // explicitly unfinished, so reviewing one spends a real quota on code its
  // author has not asked anybody to look at yet. They are one click away rather
  // than gone, because "let me check this before I mark it ready" is a real use
  // and the only person it costs is the one who chose it.
  let showDrafts = $state(false)
  let query = $state('')

  const draftCount = $derived(rows.filter((r) => r.draft).length)

  // Searching by number as well as by words, because "the 4300 one" is how
  // people refer to a pull request they are looking for.
  const matches = $derived.by(() => {
    const q = query.trim().toLowerCase()
    return rows.filter((r) => {
      if (r.draft && !showDrafts) return false
      if (!q) return true
      return (
        r.title.toLowerCase().includes(q) ||
        r.author.toLowerCase().includes(q) ||
        String(r.number).includes(q) ||
        r.repoLabel.toLowerCase().includes(q)
      )
    })
  })

  // The search box earns its place only once the list is long enough to need
  // one. On three rows it is furniture in front of the thing you came for.
  const searchable = $derived(rows.length > 6)

  // Grouped by repository, in the order the repos were given, so the card reads
  // the way the sidebar does rather than as one flat list of numbers.
  const groups = $derived(
    repos
      .map((r) => ({
        label: r.label,
        items: matches.filter((row) => row.machineId === r.machineId && row.cwd === r.cwd),
      }))
      .filter((g) => g.items.length > 0),
  )

  // What a row says about its review.
  //
  // Read from the SERVER (pr.review), not from state this component holds.
  // Reviews started here used to be remembered in a local map, so the row knew
  // about one only while this tab stayed open: a refresh, or going to a session
  // and coming back, put "Review" back on a pull request that already had a
  // review, and clicking it started another whole reading. That is minutes of
  // work and real quota spent because the button forgot.
  //
  // Clicking Review does NOT navigate into the session. A review takes minutes
  // and you almost never want to watch it; being thrown into a chat that is
  // reading files is the single thing that made this feel wrong. The row reports
  // it and offers the way in.
  // Optimistic, for the seconds between the click and the next read: the server
  // is authoritative but it answers on its own beat, and a button that appears
  // to do nothing for two seconds is a button people press twice.
  let optimistic = $state<Record<string, string>>({})

  // Whether anything on this card is still working, which is what decides
  // whether the card is worth watching at all.
  //
  // A boolean derived rather than a read inside the effect: load() replaces
  // `rows`, so an effect that read them would re-arm itself on its own result,
  // while a boolean only invalidates when it actually flips.
  const anyRunning = $derived(
    rows.some((r) => r.review?.running) || Object.keys(optimistic).length > 0,
  )

  // Follow a review while one is going, and stop the moment none is.
  //
  // The row said "Reviewing" and then never changed: the list was only re-read
  // when the repositories did, so a review that finished four minutes ago still
  // showed a spinner until you navigated away and back. The two timers are
  // separate because they answer different questions at different costs -- the
  // clock is local and ticks every second, the list is a GitHub round trip and
  // is worth asking for far less often.
  $effect(() => {
    if (!anyRunning) return
    const tick = setInterval(() => (now = Date.now()), 1000)
    const poll = setInterval(() => void load(true), 5000)
    return () => {
      clearInterval(tick)
      clearInterval(poll)
    }
  })

  type Shown =
    | { kind: 'none' }
    | { kind: 'running'; sessionId: string; startedAt: number }
    | { kind: 'ready'; sessionId: string; findings: number }
    | { kind: 'failed'; sessionId: string }
    | { kind: 'stopped'; sessionId: string }
    | { kind: 'posted'; sessionId: string }

  function shownFor(row: Row): Shown {
    const rev = row.review
    const optimisticId = optimistic[key(row)]
    if (!rev && optimisticId) return { kind: 'running', sessionId: optimisticId, startedAt: 0 }
    if (!rev) return { kind: 'none' }
    if (rev.posted) return { kind: 'posted', sessionId: rev.session_id }
    // A review of an OLDER commit is not this pull request's review any more, so
    // the row offers a fresh reading rather than pointing at a stale draft. The
    // draft is still openable from the sidebar; it is just not what this row is
    // about.
    if (rev.stale) return { kind: 'none' }
    if (rev.running) {
      const meta = app.sessions.find((s) => s.id === rev.session_id && s.machineId === row.machineId)
      return { kind: 'running', sessionId: rev.session_id, startedAt: meta ? app.liveTurnStart(meta) : 0 }
    }
    // Stopped, or caught by a restart mid-phase. Not running and not an answer:
    // the row offers a fresh reading and says why one is needed.
    if (rev.stopped) return { kind: 'stopped', sessionId: rev.session_id }
    if (rev.failed) return { kind: 'failed', sessionId: rev.session_id }
    return { kind: 'ready', sessionId: rev.session_id, findings: rev.findings }
  }

  async function review(row: Row) {
    const k = key(row)
    if (starting) return
    starting = k
    try {
      const meta = await startReview(
        app.baseForMachine(row.machineId),
        row.cwd,
        row.number,
        mention(),
      )
      optimistic = { ...optimistic, [k]: meta.id }
      app.refresh()
      // And re-read the rows, so the server's own account of the review replaces
      // the optimistic one rather than the two disagreeing until the next poll.
      void load()
    } catch (e) {
      err = (e as Error).message
    } finally {
      starting = ''
    }
  }
</script>

<!-- Nothing at all unless there is something to show. `loading` is deliberately
     NOT a reason to render: a heading that appears saying "PULL REQUESTS
     reading…" and then disappears again is a hole opening and closing in the
     dashboard, and on a machine with no open pull requests that was the only
     thing this card ever did. The fetch happens quietly and the card exists
     only once it has rows to put in it. -->
{#if configured && (groups.length || err)}
  <section class="prs">
    <header>
      <h2>Pull requests</h2>
      {#if searchable}
        <input class="find" placeholder="Filter" bind:value={query} spellcheck="false" />
      {/if}
      {#if draftCount}
        <button class="drafts" class:on={showDrafts} onclick={() => (showDrafts = !showDrafts)}>
          {showDrafts ? 'Hide' : 'Show'} {draftCount} draft{draftCount > 1 ? 's' : ''}
        </button>
      {/if}
    </header>

    {#if err}
      <p class="err mono">{err}</p>
    {/if}

    {#if !groups.length && !err && rows.length}
      <!-- Distinguishes "nothing matches what you typed" from "there is nothing
           here", which look identical when the card simply goes blank. -->
      <p class="none">
        {query.trim() ? 'No pull request matches that.' : 'Only drafts are open here.'}
      </p>
    {/if}

    {#each groups as g (g.label)}
      <!-- The repository heading is mono, like every other name that is data
           rather than prose in this app. -->
      <div class="repo mono">{g.label}</div>
      {#each g.items as pr (pr.machineId + ':' + pr.cwd + ':' + pr.number)}
        {@const shown = shownFor(pr)}
        <div class="row" class:busy={starting === key(pr)}>
          <span class="num mono">#{pr.number}</span>
          <span class="title">{pr.title}</span>
          <!-- A flex line break, so a narrow row puts the title on a line of its
               own and everything about it on the next. Without it the title, the
               one thing you are choosing between, was the element that gave way:
               at 358px it clipped to "Nightly: p…" while the author, the diff
               stat and the fork badge all kept their full width. -->
          <span class="brk" aria-hidden="true"></span>
          <span class="by">{pr.author}</span>
          <!-- The diff stat in the two colours this app already reserves, and
               nothing else: a pull request's size is the one thing worth
               knowing before you spend a review on it. -->
          <span class="stat mono"
            ><span class="add">+{pr.additions}</span> <span class="del">-{pr.deletions}</span></span>
          {#if pr.draft}
            <!-- Named, because a draft in the list is only there because it was
                 asked for, and reviewing one is a deliberate choice. -->
            <span class="fork draft">draft</span>
          {/if}
          {#if pr.from_fork}
            <!-- Said plainly, because it changes what the review can do: no
                 commands run on a stranger's code, so no tests either. -->
            <span class="fork" title="From a fork, so the review reads only and runs nothing">fork</span>
          {/if}
          {#if shown.kind === 'running'}
            <!-- Running. The row reports it in place rather than navigating you
                 into a chat you did not ask to read; the way in is right here. -->
            <button class="open" onclick={() => app.open(pr.machineId, shown.sessionId)}>
              <span class="rspin" aria-hidden="true"></span>
              Reviewing
              {#if shown.startedAt}<span class="mono">{workedFor(shown.startedAt, now)}</span>{/if}
            </button>
          {:else if shown.kind === 'ready'}
            <!-- Finished and waiting to be read. It says WHAT it found, because
                 "ready" tells you nothing about whether it is worth opening and
                 the count is the one number that does. -->
            <button class="open ready" onclick={() => app.open(pr.machineId, shown.sessionId)}>
              {shown.findings ? `${shown.findings} finding${shown.findings === 1 ? '' : 's'}` : 'Nothing found'}
              &rarr;
            </button>
          {:else if shown.kind === 'stopped'}
            <!-- It never reached a verdict, so this is not a review of anything.
                 Reviewing again is the useful action; reading how far it got is
                 behind the label. -->
            <button class="again" onclick={() => app.open(pr.machineId, shown.sessionId)}>
              stopped &middot; pick it up &rarr;
            </button>
          {:else if shown.kind === 'failed'}
            <button class="open failed" onclick={() => app.open(pr.machineId, shown.sessionId)}>
              Review did not finish &rarr;
            </button>
          {:else if shown.kind === 'posted'}
            <button class="again" onclick={() => app.open(pr.machineId, shown.sessionId)}>posted</button>
          {:else if pr.reviewed_at}
            <!-- Already reviewed at THIS commit, possibly on a colleague's
                 machine: GitHub is the only state two installs share. The
                 action stays available behind it. -->
            <button class="again" onclick={() => review(pr)} disabled={!!starting}>
              reviewed {shortAgo(pr.reviewed_at)}
            </button>
          {:else}
            <button class="go" onclick={() => review(pr)} disabled={!!starting}>
              {starting === key(pr) ? 'Starting…' : 'Review'}
            </button>
          {/if}
        </div>
      {/each}
    {/each}
  </section>
{/if}

<style>
  .prs {
    margin-top: 18px;
  }
  header {
    display: flex;
    align-items: baseline;
    gap: 10px;
    margin-bottom: 8px;
  }
  h2 {
    margin: 0;
    font-size: 12px;
    font-weight: 550;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .err {
    margin: 0 0 8px;
    font-size: 11.5px;
    color: var(--alert);
  }
  .none {
    margin: 0 0 8px;
    font-size: 12px;
    color: var(--text-4);
  }
  /* Pushed to the right so the heading keeps the left edge the rest of the
     dashboard is aligned to. */
  .find {
    margin-left: auto;
    width: 140px;
    padding: 3px 9px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    color: var(--text);
    font-size: 11.5px;
    outline: none;
  }
  .find:focus {
    border-color: var(--border-2);
  }
  .drafts {
    font-size: 11px;
    color: var(--text-4);
  }
  .drafts:hover,
  .drafts.on {
    color: var(--text-2);
  }
  .draft {
    color: var(--text-4);
  }
  .repo {
    font-size: 12px;
    color: var(--text-3);
    padding: 10px 2px 4px;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 7px 8px 7px 10px;
    border-radius: var(--r-sm);
    min-width: 0;
  }
  .row:hover {
    background: var(--panel);
  }
  .row.busy {
    opacity: 0.6;
  }
  .num {
    flex: none;
    font-size: 12px;
    color: var(--text-4);
    font-variant-numeric: tabular-nums;
  }
  /* The one bright thing in the row: what you are choosing between. */
  .title {
    flex: 1;
    min-width: 0;
    font-size: 13.5px;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .row:hover .title {
    color: var(--text);
  }
  .by,
  .stat,
  .fork {
    flex: none;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .add {
    color: var(--live);
  }
  .del {
    color: var(--alert);
  }
  .fork {
    padding: 1px 6px;
    border-radius: 4px;
    background: var(--panel-2);
    font-size: 10.5px;
  }
  /* Review is a real action, so it rests visible and quiet rather than waiting
     for a hover: an action you cannot see is one nobody uses, and there is no
     hover at all on a phone. This is the lesson the worktree button already
     taught this codebase once. */
  .go,
  .again {
    flex: none;
    padding: 4px 12px;
    border-radius: var(--r-sm);
    background: var(--panel-2);
    color: var(--text-2);
    font-size: 12px;
    font-weight: 500;
  }
  .go:hover,
  .again:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .again {
    background: none;
    color: var(--text-4);
    font-size: 11.5px;
  }
  /* A running review: the row's own report, and the way in if you want it. */
  .open {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 4px 12px;
    border-radius: var(--r-sm);
    background: var(--panel-2);
    color: var(--live);
    font-size: 12px;
    font-weight: 500;
    font-variant-numeric: tabular-nums;
  }
  .open:hover {
    background: var(--panel-3);
  }
  /* A finished review is something to go and READ, so it reads as an invitation
     rather than as a status: white, like the one thing on the row worth doing. */
  .open.ready {
    color: var(--text);
  }
  .open.failed {
    color: var(--busy);
  }
  /* The same dashed ring the sidebar uses for a working session, on the same
     duty-cycled rotation. */
  .rspin {
    width: 9px;
    height: 9px;
    border: 1.5px dashed currentColor;
    border-radius: 50%;
    animation: rspin 2.4s steps(12) infinite;
  }
  @keyframes rspin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .rspin {
      animation: none;
    }
  }
  .go:disabled,
  .again:disabled {
    opacity: 0.5;
  }

  /* Narrow: the title takes a line, its details take the next.
     Scoped by container width rather than viewport, because this card renders in
     BOTH the dashboard and the sidebar's compact copy, and the sidebar is narrow
     on a laptop too. A viewport media query would have fixed the phone and left
     the sidebar exactly as squeezed. */
  .prs {
    container-type: inline-size;
  }
  .brk {
    display: none;
  }
  @container (max-width: 460px) {
    .row {
      flex-wrap: wrap;
      row-gap: 3px;
      padding-top: 8px;
      padding-bottom: 8px;
    }
    .brk {
      display: block;
      flex-basis: 100%;
      height: 0;
    }
    .title {
      /* Two lines rather than an ellipsis: on a phone there is no hover to
         reveal the rest, so a clipped title is simply gone. */
      white-space: normal;
      overflow: visible;
      line-height: 1.35;
    }
    /* The action ends the second line, where a thumb is. */
    .go,
    .again,
    .open {
      margin-left: auto;
    }
  }
</style>
