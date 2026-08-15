<script lang="ts">
  import type { ReviewDraft, ReviewPhase } from '../../lib/api'
  import type { ChatConnection } from '../../lib/chat.svelte'
  import { acts, opened, latest, coverage, changeSize, baseName, phaseSpans } from '../../lib/reviewlive'
  import { proseHtml } from '../../lib/prose'

  // A review while it is running.
  //
  // This replaced a phase name, a clock and eight hundred pixels of nothing. A
  // review takes minutes, and for those minutes the screen said less than a
  // progress bar would: not what is being reviewed, not what the reviewer
  // decided to look at, not what it is reading right now, and not whether any of
  // it was going anywhere.
  //
  // Every number here already existed. The review is an ordinary session whose
  // socket is already open, so every file it opens arrives as a tool call and
  // nothing had to be sent to show it. The survey was being computed and thrown
  // away. The change's size was known at creation. What was missing was the
  // decision to look.
  let {
    draft,
    chat,
    now,
  }: {
    draft: ReviewDraft
    // The review's own session. Its tool calls are the live half of this screen.
    chat: ChatConnection | null
    now: number
  } = $props()

  const phase = $derived<ReviewPhase>(draft.phase ?? 'find')
  const files = $derived(draft.files ?? [])
  const size = $derived(changeSize(files))

  const list = $derived(acts(chat?.items ?? []))
  const read = $derived(opened(list))
  const doing = $derived(latest(list))
  const cover = $derived(coverage(files.map((f) => f.path), read))
  const searches = $derived(list.filter((a) => a.kind === 'search').length)

  const spans = $derived(phaseSpans(draft.timeline ?? [], now))
  const STEPS: { key: ReviewPhase; label: string; doing: string }[] = [
    { key: 'survey', label: 'Read', doing: 'Reading the change' },
    { key: 'find', label: 'Find', doing: 'Looking for problems' },
    { key: 'verify', label: 'Check', doing: 'Trying to refute what it found' },
  ]
  const steps = $derived(draft.surveyed ? STEPS : STEPS.slice(1))
  const at = $derived(steps.findIndex((s) => s.key === phase))
  const nowDoing = $derived(steps[at]?.doing ?? 'Reviewing')

  const spanFor = (key: string) => spans.find((s) => s.phase === key)
  const total = $derived(spans.reduce((n, s) => n + s.ms, 0))

  function clock(ms: number): string {
    if (!ms || ms < 0) return ''
    const s = Math.floor(ms / 1000)
    if (s < 60) return `${s}s`
    const m = Math.floor(s / 60)
    return m < 60 ? `${m}m ${s % 60}s` : `${Math.floor(m / 60)}h ${m % 60}m`
  }

  // The biggest files in the change, which is where a reader's own suspicion
  // goes first and therefore what makes this list worth showing at all.
  const biggest = $derived(
    [...files]
      .sort((a, b) => (b.additions ?? 0) + (b.deletions ?? 0) - ((a.additions ?? 0) + (a.deletions ?? 0)))
      .slice(0, 14),
  )
  const wasRead = (p: string) => read.some((r) => r === p || r.endsWith('/' + p))
  // Files it opened that are not in the pull request: the callers it went to
  // check, which is exactly the work that makes a review here better than one
  // done against the diff alone.
  const elsewhere = $derived(
    [...read].reverse().filter((p) => !files.some((f) => p === f.path || p.endsWith('/' + f.path))),
  )
</script>

<div class="run">
  <header class="head">
    <div>
      <p class="doing">{nowDoing}</p>
      <p class="sub mono">
        {clock(total)} in
        {#if size.files}· {size.files} files, <span class="add">+{size.additions.toLocaleString()}</span>
          <span class="del">-{size.deletions.toLocaleString()}</span>{/if}
      </p>
    </div>
    <div class="counts">
      <div class="stat">
        <span class="n">{cover.seen}<span class="of">/{cover.total}</span></span>
        <span class="k">files opened</span>
      </div>
      <div class="stat">
        <span class="n">{searches}</span>
        <span class="k">searches</span>
      </div>
      <div class="stat">
        <span class="n" class:found={(draft.findings?.length ?? 0) > 0}>{draft.findings?.length ?? 0}</span>
        <span class="k">found so far</span>
      </div>
    </div>
  </header>

  <!-- The phases, each carrying how long it took. "How long has it been
       reading" is a different question from "how long has it been going", and
       the running turn's clock restarts at every phase so it could answer
       neither. -->
  <ol class="trail">
    {#each steps as s, i (s.key)}
      {@const span = spanFor(s.key)}
      <li class:done={at > i} class:on={at === i}>
        <span class="dot" aria-hidden="true"></span>
        <span class="lbl">{s.label}</span>
        <span class="took mono">{span ? clock(span.ms) : ''}</span>
      </li>
    {/each}
  </ol>

  <div class="cols">
    <section class="col">
      <h3 class="ttl">Doing now</h3>
      {#if doing}
        <div class="live">
          <span class="pulse" aria-hidden="true"></span>
          <span class="what mono">
            {#if doing.kind === 'read'}Reading {baseName(doing.what)}
            {:else if doing.kind === 'search'}Searching for {doing.what}
            {:else}{doing.tool}{/if}
          </span>
        </div>
        {#if doing.kind === 'read' && doing.what.includes('/')}
          <p class="path mono">{doing.what}</p>
        {/if}
      {:else if draft.phase === 'verify'}
        <!-- Nothing is streaming here and that is by design: the check runs in a
             session of its own, so it cannot be seen from this one. Said out
             loud, because "Starting up." on a review six minutes in reads as a
             hang, and the reason it is quiet is the reason the check is worth
             anything. -->
        <p class="quiet">Each claim is being handed to a separate reader to argue against, in its own session.</p>
      {:else}
        <p class="quiet">Starting up.</p>
      {/if}

      <!-- Progress against the change itself: which files it has been into and
           which are still untouched. This is the question somebody waiting
           actually has, and it is the one a spinner can never answer. -->
      {#if biggest.length}
        <h3 class="ttl sp">The change<span class="cnt mono">{cover.seen}/{cover.total} opened</span></h3>
        <ul class="files">
          {#each biggest as f (f.path)}
            <li class:seen={wasRead(f.path)}>
              <span class="tick" aria-hidden="true">{wasRead(f.path) ? '✓' : ''}</span>
              <span class="fp mono">{f.path}</span>
              <span class="fs mono"
                ><span class="add">+{f.additions ?? 0}</span> <span class="del">-{f.deletions ?? 0}</span></span
              >
            </li>
          {/each}
          {#if files.length > biggest.length}
            <li class="rest">and {files.length - biggest.length} smaller</li>
          {/if}
        </ul>
      {/if}

      <!-- Files it opened that are NOT in the pull request: the callers it went
           to check. Worth its own list, because following a caller is exactly
           the work that makes this better than reading the diff. -->
      {#if elsewhere.length}
        <h3 class="ttl sp">Also read, outside the change</h3>
        <ul class="opened">
          {#each elsewhere.slice(0, 8) as p (p)}
            <li><span class="mono">{baseName(p)}</span></li>
          {/each}
        </ul>
      {/if}
    </section>

    <section class="col wide">
      {#if draft.survey?.areas?.length}
        <!-- What it decided to look at, before it looked. The only account of
             where the reviewer thought the risk was, and the thing to argue
             with when a review comes back having looked in the wrong place. -->
        <h3 class="ttl">Where it decided to look</h3>
        {#if draft.survey.intent}
          <p class="intent">{draft.survey.intent}</p>
        {/if}
        <ul class="areas">
          {#each draft.survey.areas as a, i (i)}
            <li>
              <span class="num mono">{String(i + 1).padStart(2, '0')}</span>
              <div>
                <p class="what">{a.what}</p>
                {#if a.why}<p class="why">{@html proseHtml(a.why)}</p>{/if}
                {#if a.files?.length}
                  <p class="in mono">
                    {#each a.files as f, j (f)}<span class="fl" class:seen={wasRead(f)}
                        >{baseName(f)}</span
                      >{j < (a.files?.length ?? 0) - 1 ? ' ' : ''}{/each}
                  </p>
                {/if}
              </div>
            </li>
          {/each}
        </ul>
      {:else}
        <!-- No survey yet. Said rather than left blank: this phase produces the
             plan, so "there is nothing here yet" is the honest state and it has
             a reason worth giving. -->
        <h3 class="ttl">Where it decides to look</h3>
        <p class="waiting">
          {phase === 'survey'
            ? 'It is reading the change to work out what it is for and where the risk sits. That plan appears here, and the next phase is pointed at it.'
            : 'This change was small enough to read straight through, so there is no plan to show.'}
        </p>
      {/if}
    </section>
  </div>
</div>

<style>
  .run {
    display: flex;
    flex-direction: column;
    gap: 22px;
  }

  .head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 24px;
    flex-wrap: wrap;
  }
  .doing {
    margin: 0;
    font-family: var(--serif);
    font-size: 28px;
    font-weight: 500;
    line-height: 1.15;
    letter-spacing: -0.015em;
    color: var(--text);
    font-variant-ligatures: common-ligatures;
  }
  .sub {
    margin: 7px 0 0;
    font-size: 12px;
    color: var(--text-4);
    font-variant-numeric: tabular-nums;
  }
  .add {
    color: var(--live);
  }
  .del {
    color: var(--alert);
  }

  /* The numbers, at a size worth reading. A review that has opened 12 of 41
     files and run 9 searches is going somewhere; one that has opened none in
     four minutes is not, and both used to look identical. */
  .counts {
    display: flex;
    gap: 30px;
    flex: none;
  }
  .stat {
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .n {
    font-family: var(--mono);
    font-size: 26px;
    line-height: 1;
    color: var(--text);
    font-variant-numeric: tabular-nums;
  }
  .n.found {
    color: var(--busy);
  }
  .of {
    font-size: 15px;
    color: var(--text-4);
  }
  .k {
    font-size: 10px;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-4);
  }

  .trail {
    display: flex;
    align-items: stretch;
    gap: 0;
    margin: 0;
    padding: 0;
    list-style: none;
    border-top: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
  }
  .trail li {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 11px 20px 11px 0;
    margin-right: 20px;
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-transform: uppercase;
    color: var(--text-4);
    border-right: 1px solid var(--border);
  }
  .trail li:last-child {
    border-right: none;
  }
  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    border: 1px solid currentColor;
  }
  .trail li.done {
    color: var(--text-3);
  }
  .trail li.done .dot {
    background: currentColor;
  }
  .trail li.on {
    color: var(--live);
  }
  .trail li.on .dot {
    background: currentColor;
    animation: pulse 1.8s ease-in-out infinite;
  }
  .took {
    font-size: 11px;
    letter-spacing: 0;
    text-transform: none;
    color: var(--text-4);
    font-variant-numeric: tabular-nums;
  }

  .cols {
    display: grid;
    grid-template-columns: minmax(210px, 1fr) 2fr;
    gap: 34px;
    align-items: start;
  }
  .ttl {
    margin: 0 0 10px;
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .ttl.sp {
    margin-top: 24px;
  }

  .live {
    display: flex;
    align-items: center;
    gap: 9px;
  }
  .what {
    font-size: 13px;
    color: var(--text);
  }
  .pulse {
    flex: none;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--live);
    animation: pulse 1.4s ease-in-out infinite;
  }
  @keyframes pulse {
    50% {
      opacity: 0.25;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .pulse,
    .trail li.on .dot {
      animation: none;
    }
  }
  .path {
    margin: 5px 0 0 16px;
    font-size: 11px;
    color: var(--text-4);
    word-break: break-all;
    unicode-bidi: plaintext;
  }
  .quiet {
    margin: 0;
    font-size: 13px;
    color: var(--text-4);
  }

  .opened {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 5px;
  }
  .opened li {
    font-size: 12px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .intent {
    margin: 0 0 14px;
    max-width: 70ch;
    font-size: 13.5px;
    line-height: 1.6;
    color: var(--text-3);
  }
  .areas {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  .areas li {
    display: grid;
    grid-template-columns: 30px 1fr;
    gap: 12px;
  }
  .num {
    font-size: 11px;
    color: var(--text-4);
    padding-top: 3px;
  }
  .areas .what {
    margin: 0;
    font-family: var(--serif);
    font-size: 16px;
    font-weight: 500;
    line-height: 1.35;
    color: var(--text);
    font-variant-ligatures: common-ligatures;
  }
  .areas .why {
    margin: 4px 0 0;
    max-width: 72ch;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--text-4);
  }
  .areas .why :global(code) {
    font-family: var(--mono);
    font-size: 0.92em;
    color: var(--text-3);
  }
  .in {
    margin: 6px 0 0;
    font-size: 11px;
    color: var(--text-4);
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  /* A file the reviewer has actually opened. The area list stops being a plan
     and becomes progress against it. */
  .fl.seen {
    color: var(--live);
  }

  .files {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
  }
  .files li {
    display: flex;
    align-items: baseline;
    gap: 8px;
    padding: 5px 0;
    border-bottom: 1px solid color-mix(in srgb, var(--border) 55%, transparent);
  }
  .tick {
    flex: none;
    width: 10px;
    font-size: 10px;
    color: var(--live);
  }
  .files .fp {
    flex: 1;
  }
  .cnt {
    margin-left: 9px;
    letter-spacing: 0;
    text-transform: none;
    color: var(--text-4);
    font-variant-numeric: tabular-nums;
  }
  .waiting {
    margin: 0;
    max-width: 62ch;
    font-size: 13.5px;
    line-height: 1.65;
    color: var(--text-4);
  }
  .fp {
    font-size: 11.5px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    unicode-bidi: plaintext;
  }
  .files li.seen .fp {
    color: var(--text-2);
  }
  .fs {
    flex: none;
    font-size: 11px;
    font-variant-numeric: tabular-nums;
  }
  .rest {
    border-bottom: none;
    padding-top: 8px;
    font-size: 11.5px;
    color: var(--text-4);
  }

  @media (max-width: 820px) {
    .cols {
      grid-template-columns: 1fr;
      gap: 26px;
    }
    .counts {
      gap: 22px;
    }
    .n {
      font-size: 21px;
    }
    .doing {
      font-size: 23px;
    }
    .trail {
      overflow-x: auto;
    }
  }
</style>
