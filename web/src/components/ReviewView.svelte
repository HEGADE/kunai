<script lang="ts">
  import { app } from '../lib/app.svelte'
  import {
    reviewDraft,
    postReview,
    severityRank,
    PHASE_LABEL,
    type ReviewDraft,
    type ReviewEdit,
    type Severity,
  } from '../lib/api'
  import { workedFor } from '../lib/sidebar'
  import { severityLabel, verdictLine, tally, SEVERITIES } from '../lib/severity'
  import FindingCard from './FindingCard.svelte'

  // A review, as a review rather than as a conversation.
  //
  // This replaced showing a review inside the chat, and the tell that the chat
  // was wrong was that every improvement to it was an attempt to HIDE the chat:
  // the brief sent silently, the findings pinned above the transcript, the tool
  // calls collapsed, the prompt wrapped so a reopened session would not replay
  // it. When every change suppresses the surface, the surface is wrong. A chat is
  // for open-ended conversation; a review is a fixed set of judgements, each with
  // evidence, that you accept, rewrite or drop and then send.
  //
  // One column of self-contained cards rather than a list beside a detail pane,
  // because kunai is used from a phone and a split does not survive a narrow
  // screen. Same layout at both sizes, so there is one design to get right.
  //
  // The conversation is still there, one click away: a review you cannot argue
  // with is the thing CI already does.
  let { sessionId, machineId }: { sessionId: string; machineId: string } = $props()

  let draft = $state<ReviewDraft | null>(null)
  let dropped = $state<Set<number>>(new Set())
  // The user's rewrites, by finding index. Held here rather than in the cards so
  // they survive the draft being re-read while the review is still running.
  let edits = $state<Record<number, { title: string; body: string; severity: Severity }>>({})
  let summaryEdit = $state<string | null>(null)
  let filter = $state<Severity | 'all'>('all')
  let showDropped = $state(false)
  let cursor = $state(0)
  let posting = $state(false)
  let err = $state('')
  let loaded = $state(false)

  const base = $derived(app.baseForMachine(machineId))
  const meta = $derived(app.sessions.find((s) => s.machineId === machineId && s.id === sessionId))
  // Named sessionState, not state: a variable called `state` shadows the $state
  // rune for the whole component.
  const sessionState = $derived(meta ? app.liveState(meta) : '')
  // Blocked on a permission answer. Its own state, not folded into "running",
  // because it is the one the empty case got wrong: awaiting_permission is
  // neither running nor finished, so a review parked on a question fell through
  // to "nothing worth reporting" and claimed a clean bill of health for a review
  // that had not started looking. Reviews no longer ask (the toolset makes that
  // impossible), but a UI that lies when it happens is worth fixing anyway.
  const blocked = $derived(sessionState === 'awaiting_permission')
  const running = $derived(sessionState === 'running' || sessionState === 'starting' || blocked)
  // Whether the REVIEW is still going, which is not the same question as whether
  // the session is busy, and the review's own phase is the better answer. A
  // finished review reopened later has a session that reports `starting` while it
  // resumes, and reading that literally put a spinner and "Done" over a review
  // that had been finished for a week, hiding the verdict behind a progress line
  // for a job nobody was doing. The phase is recorded precisely because it
  // outlives the process that produced it.
  const reviewing = $derived(running && draft?.phase !== 'done')

  // The clock behind "Reviewing 2m".
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
  }

  $effect(() => {
    void sessionId
    void load()
  })
  // Re-read when the turn ends, which is when a phase finishes and the next one
  // starts. A phased review reports several times, not once.
  $effect(() => {
    if (!running && loaded) void load()
  })

  const findings = $derived(draft?.findings ?? [])
  const kept = $derived(findings.filter((f) => !dropped.has(f.index)))
  const keptInline = $derived(kept.filter((f) => f.inline).length)
  const posted = $derived(!!draft?.posted_url)
  const refuted = $derived(draft?.dropped ?? [])

  // The headline. Counted over what will actually be POSTED rather than over
  // everything found, because the number a person is deciding about is the one
  // they are about to send.
  //
  // Counted at the EDITED severity, which is not a detail: overruling a blocker
  // down to a minor and still being told "1 blocker" is the headline lying about
  // the thing it exists to summarise. The same applies to the filter chips, or
  // a chip would offer a count that its own filter then shows nothing for.
  const effective = $derived(findings.map((f) => ({ ...f, severity: sevOf(f.index, f.severity) })))
  const counts = $derived(tally(effective.filter((f) => !dropped.has(f.index))))
  const headline = $derived(verdictLine(counts))
  const allCounts = $derived(tally(effective))

  // Sorted most serious first and then filtered. Sorting client-side as well as
  // server-side is not redundant: an edited severity has to move the card, and
  // the server will not hear about that until Post.
  const shown = $derived(
    [...findings]
      .sort((a, b) => severityRank(sevOf(a.index, a.severity)) - severityRank(sevOf(b.index, b.severity)))
      .filter((f) => filter === 'all' || sevOf(f.index, f.severity) === filter),
  )

  function sevOf(index: number, fallback: Severity): Severity {
    return edits[index]?.severity ?? fallback
  }

  function toggle(i: number) {
    const next = new Set(dropped)
    if (next.has(i)) next.delete(i)
    else next.add(i)
    dropped = next
  }

  // Bulk, because judging a dozen findings one at a time is the friction. Scoped
  // to what is currently SHOWN, so "drop all" under a filter drops that severity
  // and not the blockers you filtered away.
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

  function setEdit(index: number, next: { title: string; body: string; severity: Severity } | undefined) {
    if (!next) {
      const { [index]: _drop, ...rest } = edits
      edits = rest
      return
    }
    edits = { ...edits, [index]: next }
  }

  function ask(i: number) {
    // The finding becomes the subject of a message, and the chat opens with it.
    // This is the one thing kunai has that a CI reviewer does not, so it is one
    // click rather than a mode you have to find.
    const f = findings.find((x) => x.index === i)
    if (!f) return
    app.reviewAsk = `About your finding on ${f.file}:${f.line} ("${f.title}") — `
    app.reviewChat = true
  }

  async function post() {
    if (posting || posted) return
    posting = true
    err = ''
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
        kept.map((f) => f.index),
        payload,
        summaryEdit ?? '',
      )
      draft = { ...(draft as ReviewDraft), posted_url: res.url }
    } catch (e) {
      err = (e as Error).message
    } finally {
      posting = false
    }
  }

  // Keyboard, because a review is a rhythm: move, judge, move. Ignored while
  // typing, and absent on a phone where scrolling and tapping is the whole
  // interaction anyway.
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
        cursor = Math.min(cursor + 1, shown.length - 1)
        break
      case 'k':
        cursor = Math.max(cursor - 1, 0)
        break
      case 'd':
      case 'x':
        if (shown[cursor]) toggle(shown[cursor].index)
        break
      default:
        return
    }
    e.preventDefault()
    document.getElementById(`f-${cursor}`)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="rv">
  <header class="top">
    <div class="ident">
      {#if draft}
        <span class="pr mono">{draft.owner}/{draft.repo}#{draft.number}</span>
        <span class="title">{draft.title}</span>
      {:else}
        <span class="pr mono">Review</span>
      {/if}
    </div>
    <div class="acts">
      <button class="chat" onclick={() => (app.reviewChat = true)}>Conversation</button>
      {#if posted}
        <a class="done" href={draft?.posted_url} target="_blank" rel="noreferrer">Posted &rarr;</a>
      {:else if findings.length || draft?.summary}
        <!-- Dropping every finding is a decision, not a dead end: the summary is
             still a review, and "I looked, nothing worth flagging" is worth
             sending. So the button changes what it promises rather than going
             grey. -->
        <button class="post" onclick={post} disabled={posting}>
          {posting ? 'Posting…' : kept.length ? `Post ${kept.length}` : 'Post summary'}
        </button>
      {/if}
    </div>
  </header>

  <!-- Honest progress: what the session is actually doing, not a phase invented
       to fill a bar. A phased review takes longer than the single-shot one did,
       so a bare elapsed clock reads as a hang; the phase is what makes the wait
       legible. -->
  {#if blocked}
    <!-- Stopped on a question. Said plainly, with the way to answer it, because
         a review that is waiting and a review that found nothing look identical
         from here otherwise. -->
    <div class="prog needs">
      This review is waiting for an answer before it can carry on.
      <button class="inline" onclick={() => (app.reviewChat = true)}>Answer it &rarr;</button>
    </div>
  {:else if reviewing}
    <div class="prog">
      <span class="spin" aria-hidden="true"></span>
      {draft?.phase ? PHASE_LABEL[draft.phase] : 'Reviewing'}
      <span class="mono">{workedFor(app.liveTurnStart(meta ?? ({} as never)), now)}</span>
      {#if findings.length}<span class="sofar">· {findings.length} so far</span>{/if}
    </div>
  {:else if draft && findings.length}
    <!-- The verdict, which is the one line worth reading before any of the
         findings: two blockers and five minor is a different pull request from
         seven minor, and that has to be readable without reading all twelve. -->
    <div class="verdict">
      <span class="vhead" class:bad={counts.blocker > 0}>{headline}</span>
      <span class="vsub">
        {keptInline} inline · {kept.length - keptInline} in the summary
        {#if refuted.length}· {refuted.length} refuted{/if}
      </span>
    </div>
  {/if}

  <div class="body">
    {#if draft?.summary}
      <!-- Editable, because the summary is the first thing the author reads and
           the reviewer's phrasing is not always the one you want to sign. -->
      {#if summaryEdit === null}
        <p class="sum">
          {draft.summary}
          {#if !posted}<button class="sedit" onclick={() => (summaryEdit = draft?.summary ?? '')}>Edit</button>{/if}
        </p>
      {:else}
        <div class="sumed">
          <textarea bind:value={summaryEdit} rows="4"></textarea>
          <button class="sdone" onclick={() => (summaryEdit = summaryEdit?.trim() ? summaryEdit : null)}>Done</button>
          <button class="scancel" onclick={() => (summaryEdit = null)}>Restore original</button>
        </div>
      {/if}
    {/if}

    {#if draft?.parse_error}
      <p class="empty">
        This review finished but did not produce findings kunai could read. Open the
        conversation and ask it to answer again in the required format.
      </p>
    {:else if !loaded}
      <p class="empty">Loading…</p>
    {:else if !draft}
      <p class="empty">This session is not a pull-request review.</p>
    {:else if !findings.length && !running}
      <p class="empty">Nothing worth reporting. Posting sends that as the review.</p>
    {/if}

    {#if findings.length > 1 && !posted}
      <!-- Filtering and bulk judgement. The friction this removes is real: a
           twelve-finding review judged one card at a time is why people stop
           reading at the fourth. -->
      <div class="tools">
        <div class="filters">
          <button class="chip" class:on={filter === 'all'} onclick={() => (filter = 'all')}>
            All {findings.length}
          </button>
          {#each SEVERITIES as s (s)}
            {#if allCounts[s] > 0}
              <button class="chip sev-{s}" class:on={filter === s} onclick={() => (filter = s)}>
                {severityLabel(s)} {allCounts[s]}
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

    {#each shown as f, i (f.index)}
      <div id="f-{i}">
        <FindingCard
          {f}
          dropped={dropped.has(f.index)}
          selected={i === cursor}
          edit={edits[f.index]}
          onToggle={() => toggle(f.index)}
          onAsk={() => ask(f.index)}
          onEdit={(next) => setEdit(f.index, next)}
        />
      </div>
    {/each}

    {#if refuted.length}
      <!-- What the review considered and threw away, with the reason. A reviewer
           you can audit is one you will trust: three findings from a reviewer
           that refuted four is a different thing from three findings from one
           that only found three, and nothing else can tell them apart. -->
      <div class="refuted">
        <button class="rhead" onclick={() => (showDropped = !showDropped)}>
          {showDropped ? '−' : '+'} {refuted.length} considered and dropped
        </button>
        {#if showDropped}
          {#each refuted as d, i (i)}
            <div class="rrow">
              <span class="rloc mono">{d.file}{d.line ? ':' + d.line : ''}</span>
              <span class="rtitle">{d.title}</span>
              <span class="rwhy">{d.why}</span>
            </div>
          {/each}
        {/if}
      </div>
    {/if}

    {#if err}<p class="err">{err}</p>{/if}

    {#if shown.length > 1}
      <p class="keys mono">j / k move · d drop · ⌘↵ post</p>
    {/if}
  </div>
</div>

<style>
  .rv {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
    background: var(--bg);
  }
  /* Sticky, because Post has to be reachable from anywhere in a long list. */
  .top {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: calc(var(--safe-top) + 14px) 18px 12px;
    border-bottom: 1px solid var(--border);
  }
  .ident {
    display: flex;
    align-items: baseline;
    gap: 10px;
    min-width: 0;
  }
  .pr {
    flex: none;
    font-size: 12px;
    color: var(--text-3);
  }
  .title {
    font-size: 13.5px;
    color: var(--text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .acts {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: none;
  }
  .chat {
    padding: 5px 11px;
    border-radius: var(--r-sm);
    color: var(--text-3);
    font-size: 12.5px;
  }
  .chat:hover {
    color: var(--text);
    background: var(--panel);
  }
  /* The one filled control, because it is the one irreversible action here. */
  .post {
    padding: 5px 14px;
    border-radius: var(--r-sm);
    background: var(--white);
    color: #0b0b0c;
    font-size: 12.5px;
    font-weight: 550;
  }
  .post:disabled {
    opacity: 0.5;
  }
  .done {
    font-size: 12.5px;
    color: var(--live);
    text-decoration: none;
  }

  .prog {
    flex: none;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 9px 18px;
    border-bottom: 1px solid var(--border);
    font-size: 12px;
    color: var(--live);
    font-variant-numeric: tabular-nums;
  }
  /* Amber, the colour this app already reserves for "blocked on you". */
  .prog.needs {
    color: var(--busy);
  }
  .inline {
    color: var(--busy);
    font-size: 12px;
    font-weight: 550;
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  .sofar {
    color: var(--text-4);
  }
  /* The same duty-cycled dashed ring the sidebar uses for a working session. */
  .spin {
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
    .spin {
      animation: none;
    }
  }

  /* The verdict line: the review's headline, in the place the progress bar was. */
  .verdict {
    flex: none;
    display: flex;
    align-items: baseline;
    gap: 10px;
    flex-wrap: wrap;
    padding: 9px 18px;
    border-bottom: 1px solid var(--border);
  }
  .vhead {
    font-size: 12.5px;
    font-weight: 550;
    color: var(--text-2);
  }
  .vhead.bad {
    color: var(--alert);
  }
  .vsub {
    font-size: 11.5px;
    color: var(--text-4);
    font-variant-numeric: tabular-nums;
  }

  .body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 16px 18px calc(var(--safe-bottom) + 28px);
    display: flex;
    flex-direction: column;
    gap: 12px;
    max-width: 860px;
    width: 100%;
    margin: 0 auto;
  }
  .sum {
    margin: 0;
    font-size: 13.5px;
    line-height: 1.65;
    color: var(--text-2);
  }
  .sedit {
    margin-left: 8px;
    font-size: 11px;
    color: var(--text-4);
  }
  .sedit:hover {
    color: var(--text-2);
  }
  .sumed {
    display: flex;
    flex-direction: column;
    gap: 8px;
    align-items: flex-start;
  }
  .sumed textarea {
    width: 100%;
    padding: 9px 11px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    color: var(--text);
    font-family: inherit;
    font-size: 13px;
    line-height: 1.6;
    outline: none;
    resize: vertical;
  }
  .sumed textarea:focus {
    border-color: var(--border-2);
  }
  .sdone {
    padding: 4px 12px;
    border-radius: var(--r-sm);
    background: var(--panel-2);
    color: var(--text-2);
    font-size: 12px;
  }
  .scancel {
    font-size: 11.5px;
    color: var(--text-4);
  }

  /* Filters and bulk actions. */
  .tools {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    flex-wrap: wrap;
  }
  .filters,
  .bulk {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }
  .chip {
    padding: 3px 10px;
    border-radius: 999px;
    border: 1px solid var(--border);
    background: none;
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
    background: var(--panel);
  }
  /* Severity reuses the tokens that already mean these things; see
     lib/severity.ts. The chip carries its name too, never colour alone. */
  .chip.sev-blocker.on {
    color: var(--alert);
    border-color: var(--alert);
  }
  .chip.sev-major.on {
    color: var(--busy);
    border-color: var(--busy);
  }
  .bact {
    padding: 3px 10px;
    border-radius: var(--r-sm);
    color: var(--text-4);
    font-size: 11.5px;
  }
  .bact:hover {
    color: var(--text-2);
    background: var(--panel);
  }

  /* The audit trail. */
  .refuted {
    margin-top: 4px;
    border-top: 1px solid var(--border);
    padding-top: 10px;
  }
  .rhead {
    font-size: 11.5px;
    color: var(--text-4);
    font-variant-numeric: tabular-nums;
  }
  .rhead:hover {
    color: var(--text-2);
  }
  .rrow {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 8px 0 0;
  }
  .rloc {
    font-size: 11px;
    color: var(--text-4);
    unicode-bidi: plaintext;
  }
  .rtitle {
    font-size: 12.5px;
    color: var(--text-3);
    text-decoration: line-through;
    text-decoration-color: var(--text-4);
  }
  .rwhy {
    font-size: 11.5px;
    line-height: 1.55;
    color: var(--text-4);
  }

  .empty {
    margin: 12px 0;
    font-size: 13px;
    line-height: 1.6;
    color: var(--text-3);
  }
  .err {
    margin: 0;
    font-size: 12.5px;
    color: var(--alert);
  }
  .keys {
    margin: 6px 0 0;
    font-size: 11px;
    color: var(--text-4);
  }
  /* No hover-to-discover on touch, and no keyboard either. */
  @media (pointer: coarse) {
    .keys {
      display: none;
    }
  }

  /* Phone. The header wraps rather than squeezing the title to nothing, and the
     controls take a real tap target. */
  @media (max-width: 560px) {
    .top {
      flex-wrap: wrap;
      gap: 8px;
    }
    .body {
      padding: 14px 12px calc(var(--safe-bottom) + 28px);
    }
    .post,
    .chat {
      padding: 8px 14px;
    }
    .bact,
    .chip {
      padding: 6px 12px;
    }
  }
</style>
