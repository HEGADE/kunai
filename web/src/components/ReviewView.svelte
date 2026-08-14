<script lang="ts">
  import { tick } from 'svelte'
  import { app } from '../lib/app.svelte'
  import { reviewDraft, postReview, PHASE_LABEL, type ReviewDraft, type ReviewEdit, type Severity } from '../lib/api'
  import { workedFor } from '../lib/sidebar'
  import { severityLabel, SEVERITIES } from '../lib/severity'
  import { ordered, decide, type Edits, type FindingEdit } from '../lib/review'
  import FindingRow from './review/FindingRow.svelte'
  import PhaseTrail from './review/PhaseTrail.svelte'
  import { toasts } from '../lib/toast.svelte'
  import RefutedList from './review/RefutedList.svelte'
  import ReviewBar from './review/ReviewBar.svelte'

  // A review, as a deck you work through.
  //
  // This is a rewrite of a surface that was correct and unusable. It showed
  // every part of every finding at full size at once, so one finding filled a
  // laptop screen: a wall of body prose, a block of evidence, a thirteen-line
  // hunk, and the decision buttons below the fold. Nothing said how many
  // findings there were, which was the worst, or how far through you were. The
  // fix is not more controls, it is rank: the claim is what you judge, and
  // everything supporting it is recourse for when the claim is not enough.
  //
  // So exactly one finding is open at a time and the rest are single lines. That
  // is a deliberate constraint rather than a limitation: a review is worked
  // through in order, and a list where everything can be open is a list that is
  // a screen and a half tall again by the third click.
  //
  // The conversation is still one click away. A review you cannot argue with is
  // the thing CI already does.
  let { sessionId, machineId }: { sessionId: string; machineId: string } = $props()

  let draft = $state<ReviewDraft | null>(null)
  let dropped = $state<Set<number>>(new Set())
  // Held here rather than in the rows so a rewrite survives the draft being
  // re-read while the review is still running.
  let edits = $state<Edits>({})
  let summaryEdit = $state<string | null>(null)
  let filter = $state<Severity | 'all'>('all')
  let cursor = $state(0)
  // Which finding is open, by its index. -1 is nothing open.
  let openIndex = $state(-1)
  let opened = false
  let posting = $state(false)
  let loaded = $state(false)

  const base = $derived(app.baseForMachine(machineId))
  const meta = $derived(app.sessions.find((s) => s.machineId === machineId && s.id === sessionId))
  // Named sessionState, not state: a variable called `state` shadows the $state
  // rune for the whole component.
  const sessionState = $derived(meta ? app.liveState(meta) : '')
  const blocked = $derived(sessionState === 'awaiting_permission')
  const running = $derived(sessionState === 'running' || sessionState === 'starting' || blocked)
  // Whether the REVIEW is going, which is not the same question as whether the
  // session is busy. A finished review reopened later has a session reporting
  // `starting` while it resumes, and reading that literally put a spinner over a
  // review that had been done for a week. The phase outlives the process.
  const reviewing = $derived(running && draft?.phase !== 'done')

  const findings = $derived(draft?.findings ?? [])
  const refuted = $derived(draft?.dropped ?? [])
  const posted = $derived(!!draft?.posted_url)
  const shown = $derived(ordered(findings, edits, filter))
  const d = $derived(decide(findings, dropped, edits))
  // Counted over everything, so a chip never offers a count its own filter then
  // shows nothing for.
  const all = $derived(decide(findings, new Set<number>(), edits))

  // The clock behind "Looking for problems 2m".
  let now = $state(Date.now())
  $effect(() => {
    if (!running) return
    const t = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(t)
  })

  async function load() {
    try {
      draft = await reviewDraft(base, sessionId)
    } catch {
      draft = null
    }
    loaded = true
    // Open the worst finding the first time there is one, so the review opens
    // already reading rather than as a list of closed lines.
    if (!opened && draft?.findings?.length) {
      opened = true
      const first = ordered(draft.findings, {}, 'all')[0]
      if (first) openIndex = first.index
    }
  }

  $effect(() => {
    void sessionId
    // Reset per review, or opening a second one leaves it as a list of closed
    // lines because the first already spent the one-shot.
    opened = false
    void load()
  })
  // Keep the cursor inside the list. Filtering, dropping and overruling a
  // severity all change what is shown, and a cursor left past the end silently
  // stops the keyboard working: j, k and d all resolve against shown[cursor],
  // so they would do nothing at all with no visible reason why.
  $effect(() => {
    if (cursor > shown.length - 1) cursor = Math.max(0, shown.length - 1)
  })
  // Re-read when the turn ends, which is when one phase finishes and the next
  // begins. A phased review reports several times, not once.
  $effect(() => {
    if (!running && loaded) void load()
  })

  function toggleOpen(index: number) {
    openIndex = openIndex === index ? -1 : index
    cursor = Math.max(0, shown.findIndex((f) => f.index === index))
  }

  function toggleDrop(index: number) {
    const next = new Set(dropped)
    if (next.has(index)) next.delete(index)
    else next.add(index)
    dropped = next
  }

  // Bulk, scoped to what is SHOWN, so "drop all" under a filter drops that
  // severity and not the blockers you filtered away.
  function keepAll() {
    const next = new Set(dropped)
    for (const f of shown) next.delete(f.index)
    dropped = next
  }
  function dropAll() {
    const next = new Set(dropped)
    for (const f of shown) next.add(f.index)
    dropped = next
  }

  function setEdit(index: number, next: FindingEdit | undefined) {
    if (!next) {
      const { [index]: _gone, ...rest } = edits
      edits = rest
      return
    }
    edits = { ...edits, [index]: next }
  }

  function ask(index: number) {
    // The finding becomes the subject of a message and the chat opens with it.
    // This is the one thing kunai has that a CI reviewer does not, so it is a
    // click rather than a mode you have to find.
    const f = findings.find((x) => x.index === index)
    if (!f) return
    app.reviewAsk = `About your finding on ${f.file}:${f.line} ("${f.title}") - `
    app.reviewChat = true
  }

  async function post() {
    if (posting || posted) return
    posting = true
    try {
      const payload: ReviewEdit[] = Object.entries(edits).map(([index, e]) => ({
        index: Number(index),
        title: e.title,
        body: e.body,
        severity: e.severity,
      }))
      const res = await postReview(
        base,
        sessionId,
        findings.filter((f) => !dropped.has(f.index)).map((f) => f.index),
        payload,
        summaryEdit ?? '',
      )
      draft = { ...(draft as ReviewDraft), posted_url: res.url }
      toasts.done('Review posted.', { label: 'Open on GitHub', run: () => window.open(res.url, '_blank') })
    } catch (e) {
      // A toast, not a line at the end of the list. Post is a button in the bar
      // at the BOTTOM of the screen and the findings above it scroll, so the
      // reason it failed was being rendered somewhere the reader was not looking
      // and styled like a footnote. It is the most important sentence on the
      // screen at that moment.
      toasts.error((e as Error).message)
    } finally {
      posting = false
    }
  }

  // A review is a rhythm: move, judge, move. Ignored while typing, and absent on
  // a phone where scrolling and tapping is the whole interaction.
  async function move(by: number) {
    if (!shown.length) return
    cursor = Math.min(Math.max(cursor + by, 0), shown.length - 1)
    const index = shown[cursor].index
    openIndex = index
    // After the DOM has grown the newly-opened row. Scrolling first measures the
    // collapsed height, so `nearest` decides it is already in view and leaves
    // most of the finding you just moved to below the fold.
    await tick()
    document.getElementById(`f-${index}`)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }

  function onKey(e: KeyboardEvent) {
    const el = e.target as HTMLElement | null
    if (el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable)) return
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault()
      void post()
      return
    }
    if (e.metaKey || e.ctrlKey || e.altKey) return
    switch (e.key) {
      case 'j':
        move(1)
        break
      case 'k':
        move(-1)
        break
      case 'o':
        if (shown[cursor]) toggleOpen(shown[cursor].index)
        break
      case 'd':
      case 'x':
        if (shown[cursor]) toggleDrop(shown[cursor].index)
        break
      default:
        return
    }
    e.preventDefault()
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="rv">
  <header class="top">
    <div class="ident">
      {#if draft}
        <span class="repo mono">{draft.owner}/{draft.repo}#{draft.number}</span>
        <h1 class="title">{draft.title}</h1>
      {:else}
        <span class="repo mono">Review</span>
      {/if}
    </div>
    <button class="conv" onclick={() => (app.reviewChat = true)}>Conversation</button>
  </header>

  <div class="body">
    <!-- Honest progress: what the session is actually doing. A phased review
         takes minutes, so a bare elapsed clock reads as a hang; naming the phase
         is what makes the wait legible. -->
    {#if blocked}
      <div class="prog needs">
        This review is waiting for an answer before it can carry on.
        <button class="inline" onclick={() => (app.reviewChat = true)}>Answer it &rarr;</button>
      </div>
    {:else if reviewing}
      {@const elapsed = workedFor(app.liveTurnStart(meta ?? ({} as never)), now)}
      <PhaseTrail phase={draft?.phase ?? 'find'} skippedSurvey={draft ? !draft.surveyed : false} />
      <p class="elapsed">
        {#if elapsed}<span class="mono">{elapsed}</span>{/if}
        {#if findings.length}
          {elapsed ? '· ' : ''}{findings.length} finding{findings.length === 1 ? '' : 's'} so far
        {:else}
          {elapsed ? '· ' : ''}Findings appear here as they are found, and are checked before you see the verdict.
        {/if}
      </p>
    {/if}

    {#if draft?.parse_error}
      <p class="empty">
        This review finished but did not produce findings kunai could read, twice over. Open
        the conversation and ask it to answer again in the required format.
      </p>
    {:else if !loaded}
      <p class="empty">Loading&hellip;</p>
    {:else if !draft}
      <p class="empty">This session is not a pull-request review.</p>
    {:else if !findings.length && !running && draft.phase && draft.phase !== 'done'}
      <!-- Stopped before it finished. This MUST NOT fall through to "nothing
           worth reporting", which is the same lie this view told once before:
           a review parked on a question claimed a clean bill of health for code
           it had not started reading. The recorded phase is what tells them
           apart. -->
      <p class="empty">
        This review stopped while {PHASE_LABEL[draft.phase].toLowerCase()} and never finished,
        so nothing has been looked at yet. Start it again from the dashboard.
      </p>
    {/if}

    {#if draft && (findings.length || draft.summary) && !reviewing}
      <section class="verdict">
        <div class="tally">
          {#each SEVERITIES as s (s)}
            {#if all.counts[s] > 0}
              <span class="pip sev-{s}">{all.counts[s]} {severityLabel(s).toLowerCase()}</span>
            {/if}
          {/each}
          {#if !findings.length}<span class="pip clean">Nothing worth reporting</span>{/if}
          {#if refuted.length}<span class="pip quiet">{refuted.length} refuted</span>{/if}
        </div>

        {#if draft.summary}
          {#if summaryEdit === null}
            <p class="sum">
              {draft.summary}
              {#if !posted}<button class="sedit" onclick={() => (summaryEdit = draft?.summary ?? '')}>Edit</button>{/if}
            </p>
          {:else}
            <div class="sumed">
              <textarea bind:value={summaryEdit} rows="5"></textarea>
              <div class="sumacts">
                <button class="sdone" onclick={() => (summaryEdit = summaryEdit?.trim() ? summaryEdit : null)}>
                  Save
                </button>
                <button class="quiet" onclick={() => (summaryEdit = null)}>Restore the original</button>
              </div>
            </div>
          {/if}
        {/if}
      </section>
    {/if}

    {#if findings.length > 1 && !posted}
      <div class="tools">
        <div class="chips">
          <button class="chip" class:on={filter === 'all'} onclick={() => (filter = 'all')}>All {findings.length}</button>
          {#each SEVERITIES as s (s)}
            {#if all.counts[s] > 0}
              <button class="chip sev-{s}" class:on={filter === s} onclick={() => (filter = s)}>
                {severityLabel(s)} {all.counts[s]}
              </button>
            {/if}
          {/each}
        </div>
        <div class="bulk">
          <button class="bact" onclick={keepAll}>Keep all</button>
          <button class="bact" onclick={dropAll}>Drop all</button>
        </div>
      </div>
    {/if}

    <div class="list">
      {#each shown as f, i (f.index)}
        <div id="f-{f.index}">
          <FindingRow
            {f}
            open={openIndex === f.index}
            dropped={dropped.has(f.index)}
            cursor={i === cursor}
            edit={edits[f.index]}
            ontoggle={() => toggleOpen(f.index)}
            ondrop={() => toggleDrop(f.index)}
            onedit={(next) => setEdit(f.index, next)}
            onask={() => ask(f.index)}
          />
        </div>
      {/each}
    </div>

    {#if refuted.length}
      <RefutedList items={refuted} />
    {/if}

  </div>

  {#if draft && !reviewing && !draft.parse_error}
    <ReviewBar {d} {posting} {posted} postedUrl={draft.posted_url} onpost={post} />
  {/if}
</div>

<style>
  .rv {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
    background: var(--bg);
  }

  .top {
    flex: none;
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 14px;
    padding: calc(var(--safe-top) + 14px) 18px 13px;
    border-bottom: 1px solid var(--border);
  }
  .ident {
    min-width: 0;
  }
  .repo {
    display: block;
    font-size: 11px;
    letter-spacing: 0.01em;
    color: var(--text-4);
  }
  .title {
    margin: 3px 0 0;
    font-size: 15px;
    font-weight: 550;
    letter-spacing: -0.01em;
    color: var(--text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .conv {
    flex: none;
    padding: 6px 13px;
    border-radius: var(--r-sm);
    color: var(--text-3);
    font-size: 12.5px;
  }
  .conv:hover {
    color: var(--text);
    background: var(--panel);
  }

  .body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 18px 18px 32px;
    max-width: 860px;
    width: 100%;
    margin: 0 auto;
  }

  .prog {
    display: flex;
    align-items: center;
    gap: 9px;
    margin-bottom: 18px;
    font-size: 12.5px;
    color: var(--live);
    font-variant-numeric: tabular-nums;
  }
  /* Amber, which this app already reserves for "blocked on you". */
  .prog.needs {
    color: var(--busy);
  }
  .inline {
    color: var(--busy);
    font-size: 12.5px;
    font-weight: 550;
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  /* How long it has been going, under the trail rather than beside it: the
     phase answers "is it stuck", the clock only answers "how long". */
  .elapsed {
    margin: -12px 0 22px;
    font-size: 11.5px;
    color: var(--text-4);
    font-variant-numeric: tabular-nums;
  }

  /* The verdict: the shape of the review before any of it is read. */
  .verdict {
    margin-bottom: 20px;
  }
  .tally {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 14px;
  }
  .pip {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .pip.sev-blocker {
    color: var(--alert);
  }
  .pip.sev-major {
    color: var(--busy);
  }
  .pip.sev-minor {
    color: var(--text-3);
  }
  .pip.clean {
    color: var(--live);
  }
  .pip.quiet {
    color: var(--text-4);
    font-weight: 500;
    letter-spacing: 0.02em;
    text-transform: none;
  }
  /* The review's own words, given the size they are worth: this is the first
     thing the author will read and it was a small grey paragraph. */
  .sum {
    margin: 12px 0 0;
    max-width: 70ch;
    font-size: 14.5px;
    line-height: 1.68;
    color: var(--text-2);
  }
  .sedit {
    margin-left: 9px;
    font-size: 11px;
    color: var(--text-4);
    vertical-align: baseline;
  }
  .sedit:hover {
    color: var(--text-2);
  }
  .sumed {
    margin-top: 12px;
    display: flex;
    flex-direction: column;
    gap: 9px;
  }
  .sumed textarea {
    width: 100%;
    padding: 10px 12px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    color: var(--text);
    font-family: inherit;
    font-size: 13.5px;
    line-height: 1.65;
    outline: none;
    resize: vertical;
  }
  .sumed textarea:focus {
    border-color: var(--border-2);
  }
  .sumacts {
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .sdone {
    padding: 5px 14px;
    border-radius: var(--r-sm);
    background: var(--panel-2);
    color: var(--text);
    font-size: 12px;
    font-weight: 550;
  }
  .quiet {
    font-size: 11.5px;
    color: var(--text-4);
  }
  .quiet:hover {
    color: var(--text-2);
  }

  .tools {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    flex-wrap: wrap;
    margin-bottom: 6px;
  }
  .chips,
  .bulk {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }
  .chip {
    padding: 4px 11px;
    border-radius: 999px;
    border: 1px solid transparent;
    color: var(--text-4);
    font-size: 11.5px;
    font-variant-numeric: tabular-nums;
  }
  .chip:hover {
    color: var(--text-2);
  }
  .chip.on {
    color: var(--text);
    border-color: var(--border-2);
  }
  .chip.sev-blocker.on {
    color: var(--alert);
    border-color: var(--alert);
  }
  .chip.sev-major.on {
    color: var(--busy);
    border-color: var(--busy);
  }
  .bact {
    padding: 4px 10px;
    border-radius: var(--r-sm);
    color: var(--text-4);
    font-size: 11.5px;
  }
  .bact:hover {
    color: var(--text-2);
    background: var(--panel);
  }

  .list {
    margin-top: 8px;
  }
  /* The last row's rule would otherwise draw a line under the list with nothing
     below it. */
  .list > div:last-child :global(.row:not(.open)) {
    border-bottom-color: transparent;
  }

  .empty {
    margin: 8px 0 20px;
    max-width: 66ch;
    font-size: 13.5px;
    line-height: 1.68;
    color: var(--text-3);
  }

  @media (max-width: 560px) {
    .body {
      padding: 14px 12px 24px;
    }
    .top {
      padding: calc(var(--safe-top) + 12px) 12px 11px;
    }
    .bact,
    .chip {
      padding: 6px 12px;
    }
  }
</style>
