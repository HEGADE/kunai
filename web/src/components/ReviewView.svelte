<script lang="ts">
  import { app } from '../lib/app.svelte'
  import {
    postReview,
    applyReviewFix,
    stopReview,
    closeSession,
    type ReviewEdit,
    type ReviewFinding,
  } from '../lib/api'
  import { DraftResource } from '../lib/reviewQuery.svelte'
  import {
    ordered,
    tally,
    sendLabel,
    headline as headlineOf,
    step,
    type Edits,
    type Verdicts,
  } from '../lib/reviewDeck'
  import { toasts } from '../lib/toast.svelte'
  import QueueRail from './review/QueueRail.svelte'
  import FindingPane from './review/FindingPane.svelte'
  import DetailRail from './review/DetailRail.svelte'
  import RunningReview from './review/RunningReview.svelte'

  // The review workspace.
  //
  // Three columns, each answering a different question: the queue says where you
  // are in a fixed list, the pane is the reading, and the rail is what to do
  // about the one you are reading. Both rails collapse, and the queue collapses
  // to numbered stubs rather than to nothing, so narrowing the screen never
  // costs you your place in the list.
  //
  // This file owns arrangement and the session. Everything it DECIDES comes from
  // lib/reviewDeck.ts, which is pure and unit-tested, because the numbers here
  // choose what lands publicly on somebody's pull request under a shared bot
  // identity, and that is not a thing to leave in a template where the only way
  // to check it is to click.
  let { sessionId, machineId }: { sessionId: string; machineId: string } = $props()

  const base = $derived(app.baseForMachine(machineId))

  // Through the shared cache: deduped in flight, kept across a visit, and able
  // to tell "nothing yet" from "refreshing behind what you can see". See
  // lib/reviewQuery.svelte.ts for why a bare fetch in an effect is not enough.
  const res = new DraftResource()
  const draft = $derived(res.data ?? null)

  let verdicts = $state<Verdicts>({})
  let edits = $state<Edits>({})
  let active = $state(0)
  let leftOpen = $state(true)
  let rightOpen = $state(true)
  let posting = $state(false)
  let finishing = $state(false)
  let waking = $state(false)
  let stopping = $state(false)
  // Which findings have been written, by index, so moving away and back does not
  // offer to apply the same edit a second time.
  let appliedAt = $state<Record<number, boolean>>({})
  let applying = $state(-1)
  let pane = $state<HTMLElement | undefined>()

  const meta = $derived(app.sessions.find((s) => s.machineId === machineId && s.id === sessionId))
  // Named sessionState: a variable called `state` shadows the $state rune.
  const sessionState = $derived(meta ? app.liveState(meta) : '')
  // Waiting on somebody, in this session or in one a phase borrowed. The
  // borrowed case is the one that hurts: the ask lands somewhere this screen is
  // not attached, so the review looks like it is working while it is stuck.
  const blockedIn = $derived(draft?.blocked_session ?? '')
  const blocked = $derived(sessionState === 'awaiting_permission' || !!blockedIn)
  const running = $derived(sessionState === 'running' || sessionState === 'starting' || blocked)
  // Whether the REVIEW is going, which is not whether this SESSION is busy, in
  // either direction. A finished review reopened later reports `starting` while
  // it resumes; and the verification phase runs in a session of its own, so the
  // session this screen is attached to is idle for the whole of it. Reading the
  // session made a review three minutes into verifying render the empty state:
  // "Nothing worth reporting", with a button offering to post that to GitHub as
  // the review. The server answers both questions now (see handleReviewDraft);
  // the session is only the fallback for a machine on an older build.
  const reviewing = $derived(draft?.running ?? (running && draft?.phase !== 'done'))
  // Started, never finished, and nothing is driving it: kunai was restarted in
  // the middle of it. Not a review of anything, and above all not a clean bill
  // of health.
  const stopped = $derived(draft?.stopped === true)

  const raw = $derived<ReviewFinding[]>(draft?.findings ?? [])
  const findings = $derived(ordered(raw, edits))
  const posted = $derived(!!draft?.posted_url)
  const t = $derived(tally(findings, verdicts, edits))
  const headline = $derived(headlineOf(t))
  const current = $derived(findings[active] ?? null)
  // The code on GitHub, at the commit that was READ rather than at the head.
  // That is where the code a finding describes actually is: a push since then
  // may have moved or removed the very line the claim is about, and pointing at
  // the branch would quietly show the reader something else.
  const permalink = $derived(
    draft && current && draft.head_sha
      ? `https://github.com/${draft.owner}/${draft.repo}/blob/${draft.head_sha}/${current.file}#L${current.line}`
      : '',
  )

  // Read on open. Re-read whenever the session stops working: a phase ending is
  // what produces new findings, and it is the only moment the draft changes
  // without this screen asking for it.
  $effect(() => {
    void sessionId
    void base
    void res.read(base, sessionId)
  })
  $effect(() => {
    if (!running && res.data) void res.read(base, sessionId, { force: true })
  })
  // The cursor cannot fall off the end when the list changes under it.
  $effect(() => {
    if (active > findings.length - 1) active = Math.max(0, findings.length - 1)
  })

  // The clock behind the running screen.
  let now = $state(Date.now())
  $effect(() => {
    if (!running) return
    const timer = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(timer)
  })

  // Stop it. Not the same as interrupting the turn, which is what pressing Stop
  // in the conversation does and why that looked broken: the engine asks for the
  // next phase at the end of every turn, so a stopped turn is followed by
  // another one. This cancels the run.
  async function stop() {
    if (stopping) return
    stopping = true
    try {
      await stopReview(base, sessionId)
      await res.read(base, sessionId, { force: true })
      toasts.done('Review stopped. Nothing was posted, and the conversation is still here to read.')
    } catch (e) {
      toasts.error((e as Error).message)
    } finally {
      stopping = false
    }
  }

  // Take me to the question. Usually this review's own conversation; when a
  // phase borrowed a session, the ask is over there and that is where to go.
  function answerIt() {
    if (blockedIn && blockedIn !== sessionId) {
      app.open(machineId, blockedIn)
      return
    }
    app.reviewChat = true
  }

  // What the review was doing when it stopped, in the same words the running
  // screen uses for that step.
  function phaseWord(phase: string): string {
    return phase === 'survey' ? 'reading the change' : phase === 'verify' ? 'checking what it found' : 'looking'
  }

  function go(i: number) {
    active = step(i, 0, findings.length)
    const f = findings[active]
    if (!f || !pane) return
    const node = document.getElementById(`x-f-${f.index}`)
    if (!node) return
    pane.scrollTo({
      top: pane.scrollTop + node.getBoundingClientRect().top - pane.getBoundingClientRect().top - 1,
      behavior: 'smooth',
    })
  }

  function decide(v: 'accept' | 'dismiss' | undefined) {
    const f = findings[active]
    if (!f || posted) return
    verdicts = { ...verdicts, [f.index]: v }
  }

  // Asking the reviewer about the finding you are reading.
  //
  // The wake goes first and always, because a review's session ends -- you press
  // Done, kunai restarts -- and the draft outlives it by design. Ask then opened
  // a transcript with a dead composer and a Reopen that cannot work, since a
  // review's checkout is swept when it finishes and the transcript's own cwd no
  // longer exists. The server can put both back from the record; see
  // prreviewreopen.go. It is idempotent, so a live review pays one round trip
  // and this code never has to work out which case it is in.
  async function ask() {
    const f = findings[active]
    if (!f || waking) return
    waking = true
    try {
      await app.wakeReview(machineId, sessionId)
    } catch (e) {
      // A wake that fails is only fatal when there was nothing there anyway. A
      // session still running does not need one, and an older server on another
      // machine does not have the endpoint at all, so refusing to open the
      // conversation over it would break Ask everywhere it used to work.
      if (app.chat?.status === 'gone') {
        toasts.error(`Could not reopen the reviewer: ${(e as Error).message}`)
        return
      }
    } finally {
      waking = false
    }
    app.reviewAsk = `About your finding on ${f.file}:${f.line} ("${f.title}") - `
    app.reviewChat = true
  }

  // Writing one finding's suggestion into the checkout the review read.
  //
  // The server does the matching: the change lands where the code the finding
  // quoted still is, or nowhere at all, so a file that has moved on since the
  // review is refused rather than written at a stale line number. It does not
  // commit, so `git diff` is the whole record of what this did.
  async function apply() {
    const f = findings[active]
    if (!f || applying >= 0 || appliedAt[f.index]) return
    applying = f.index
    try {
      const r = await applyReviewFix(base, sessionId, f.index, f.file)
      appliedAt = { ...appliedAt, [f.index]: true }
      const n = r.added === r.removed ? `${r.added} line${r.added === 1 ? '' : 's'}` : `-${r.removed} +${r.added}`
      toasts.done(`Applied to ${r.file}:${r.line} (${n}). Not committed.`)
    } catch (e) {
      toasts.error((e as Error).message)
    } finally {
      applying = -1
    }
  }

  async function post() {
    if (posting || posted || !draft) return
    posting = true
    try {
      // Dismissed findings are the only ones held back. An UNDECIDED one is
      // SENT: silence is not a dismissal, and a reviewer that quietly dropped
      // everything you had not got to would be worse than one that posted too
      // much, because you would never learn what it had found.
      const keep = findings.filter((f) => verdicts[f.index] !== 'dismiss').map((f) => f.index)
      const payload: ReviewEdit[] = Object.entries(edits).map(([index, e]) => ({
        index: Number(index),
        title: e.title,
        body: e.body,
        severity: e.severity,
      }))
      await postReview(base, sessionId, keep, payload, '')
      // Re-read rather than patching the local copy: the server decides what
      // actually went out, including anything it re-anchored on the way.
      await res.read(base, sessionId, { force: true })
    } catch (e) {
      toasts.error((e as Error).message)
    } finally {
      posting = false
    }
  }

  // Ending the review: the agent and its throwaway checkout go, the draft stays
  // on disk and reopens from Recent.
  async function finish() {
    if (finishing) return
    finishing = true
    try {
      await closeSession(base, sessionId)
      app.closeTabFor(machineId, sessionId, { ended: true })
    } catch (e) {
      toasts.error((e as Error).message)
    } finally {
      finishing = false
    }
    app.back()
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
    switch (e.key.toLowerCase()) {
      case 'j':
        go(step(active, 1, findings.length))
        break
      case 'k':
        go(step(active, -1, findings.length))
        break
      case 'a':
        decide('accept')
        break
      case 'x':
        decide('dismiss')
        break
      case '[':
        leftOpen = !leftOpen
        break
      case ']':
        rightOpen = !rightOpen
        break
      default:
        return
    }
    e.preventDefault()
  }

  const showRails = $derived(!!draft && !reviewing && findings.length > 0)
  const cols = $derived(
    showRails
      ? `${leftOpen ? 'minmax(200px,272px)' : '44px'} minmax(0,1fr) ${rightOpen ? 'minmax(268px,344px)' : '0px'}`
      : '0px minmax(0,1fr) 0px',
  )
</script>

<svelte:window onkeydown={onKey} />

<div class="rvx shell">
  <header class="bar">
    <!-- Drawn, not typed.
         These were the APL quad characters, which JetBrains Mono does not carry:
         the browser fell back and rendered an empty box, so the two controls in
         the top corners of the workspace were literally tofu. Everything else in
         kunai is a 24-box inline SVG at stroke 1.7, and the shape is the one the
         sidebar's own collapse button already uses, so the gesture is learned in
         one place and recognised in the other. -->
    <!-- Only while there are panels to toggle. A review with nothing to triage
         (still running, or nothing found) has no queue and no detail rail, so
         these collapsed nothing: a control that does nothing when pressed reads
         as broken, and this one read as a stray "close the sidebar" button on a
         screen that has no sidebar. -->
    {#if showRails}
      <button class="ic" class:on={leftOpen} title="Queue  [" aria-label="Queue" onclick={() => (leftOpen = !leftOpen)}>
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2.5" /><path d="M9.5 4v16" /></svg>
      </button>
    {/if}
    <span class="brand x-mono"><span class="dot"></span>KUNAI</span>
    <span class="div"></span>
    {#if draft}
      <span class="ident x-mono">
        {draft.owner}/{draft.repo}<span class="sep">·</span><span class="no">#{draft.number}</span>
        {#if draft.base_ref}<span class="into">→{draft.base_ref}</span>{/if}
      </span>
      {#if draft.files?.length}
        <span class="pill x-mono">{draft.files.length} FILES</span>
      {/if}
    {/if}
    <div class="sp"></div>

    {#if showRails}
      <span class="prog x-mono">{t.resolved}/{t.total} resolved</span>
      <div class="pips">
        {#each findings as f (f.index)}
          <span class="pip" data-v={verdicts[f.index] ?? 'todo'}></span>
        {/each}
      </div>
    {/if}

    <button class="btn" onclick={() => (app.reviewChat = true)}>Conversation</button>
    {#if posted && draft?.posted_url}
      <a class="btn" href={draft.posted_url} target="_blank" rel="noreferrer">Read on GitHub ↗</a>
    {/if}
    <!-- A way out, which there was not one of at all. A review is minutes of
         work on a large pull request and the only thing that looked like a stop
         (the conversation's own Stop button) interrupts a TURN, which the engine
         follows with the next phase. -->
    {#if reviewing || blocked}
      <button class="cta halt" onclick={stop} disabled={stopping}>
        {stopping ? 'Stopping' : 'Stop the review'}
      </button>
    {/if}
    <!-- Never offered on a review that stopped part-way: its emptiness is a
         review that never happened, and posting it would tell the author their
         change was read and found clean. -->
    {#if draft && !reviewing && !stopped && !posted}
      <button class="cta" class:ready={t.total > 0 && t.resolved === t.total} onclick={post} disabled={posting}>
        {sendLabel(t, posting)}
      </button>
    {:else if posted}
      <button class="cta done" onclick={finish} disabled={finishing}>Done</button>
    {/if}
    {#if showRails}
      <button class="ic" class:on={rightOpen} title="Detail  ]" aria-label="Detail" onclick={() => (rightOpen = !rightOpen)}>
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2.5" /><path d="M14.5 4v16" /></svg>
      </button>
    {/if}
  </header>

  <div class="cols" style="grid-template-columns: {cols}">
    {#if showRails && draft}
      <QueueRail {findings} {active} {verdicts} {edits} clean={draft.clean ?? []} open={leftOpen} onpick={go} />
    {:else}
      <div class="pad"></div>
    {/if}

    <main class="mid">
      {#if res.pending}
        <!-- pending, not "loading": a refresh behind data you can already see
             must never blank it, which is exactly what makes a poll flicker. -->
        <div class="skel">
          <div class="sk a"></div>
          <div class="sk b"></div>
          <div class="sk c"></div>
          <div class="sk c"></div>
        </div>
      {:else if !draft}
        <p class="msg">{res.error || 'This session is not a pull-request review.'}</p>
      {:else if blocked}
        <div class="empty">
          <h1 class="head">This review is waiting for an answer</h1>
          <p class="msg flush">
            It stopped to ask permission for something, and it cannot carry on until somebody says
            yes or no. {blockedIn ? 'The question is in the session it borrowed to check its findings, not this one.' : ''}
            <button class="link" onclick={answerIt}>Answer it →</button>
          </p>
        </div>
      {:else if reviewing}
        <div class="runwrap"><RunningReview {draft} chat={app.chat} {now} /></div>
      {:else if stopped}
        <!-- Said plainly, because the alternative is the worst thing this screen
             can do: a review that stopped part-way has found nothing YET, and
             rendering that as "nothing worth reporting" is a clean bill of
             health nobody gave. -->
        <div class="empty">
          <h1 class="head">This review stopped before it finished</h1>
          <p class="msg flush">
            It was {draft.phase ? `still ${phaseWord(draft.phase)}` : 'still working'} when it ended, so it
            never reached a verdict. Nothing here is a finding, and nothing here says the change is
            fine. Read the conversation to see how far it got, or review the pull request again from
            the dashboard.
          </p>
        </div>
      {:else if draft.parse_error}
        <p class="msg">
          This review finished but did not produce findings kunai could read, twice over. Open the
          conversation and ask it to answer again in the required format.
        </p>
      {:else if !findings.length}
        <div class="empty">
          <h1 class="head">Nothing worth reporting</h1>
          <p class="msg flush">Posting sends that as the review, which is worth saying out loud.</p>
        </div>
      {:else}
        <FindingPane
          {draft}
          {headline}
          {findings}
          {verdicts}
          {edits}
          {active}
          onactive={(i) => (active = i)}
          bind:scroller={pane}
        />
      {/if}
    </main>

    {#if showRails && current && rightOpen}
      <DetailRail
        f={current}
        position={active + 1}
        verdict={verdicts[current.index]}
        {edits}
        sent={posted}
        href={permalink}
        {waking}
        applying={applying === current.index}
        applied={!!appliedAt[current.index]}
        onapply={apply}
        onaccept={() => decide('accept')}
        ondismiss={() => decide('dismiss')}
        onundo={() => decide(undefined)}
        onask={ask}
      />
    {:else}
      <div class="pad right"></div>
    {/if}
  </div>

  <footer class="status x-mono">
    {#if posted}
      <span class="lit">{t.sending} finding{t.sending === 1 ? '' : 's'} posted as kunai[bot]</span>
    {:else if showRails}
      <span>{t.resolved} of {t.total} resolved</span>
      {#if t.dismissed}<span class="pipe">|</span><span>{t.dismissed} dismissed</span>{/if}
    {:else if reviewing}
      <span>reviewing</span>
    {:else if stopped}
      <span>stopped before it finished</span>
    {/if}
    <div class="sp"></div>
    <span class="keys">J next</span><span class="keys">K prev</span><span class="keys">A accept</span>
    <span class="keys">X dismiss</span><span class="keys">[ ] panels</span>
  </footer>
</div>

<style>
  .shell {
    display: grid;
    grid-template-rows: 44px minmax(0, 1fr) 30px;
    height: 100%;
    min-height: 0;
  }

  .bar {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 0 12px;
    border-bottom: 1px solid var(--x-line);
    background: var(--x-chrome);
    min-width: 0;
  }
  .ic {
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    flex: none;
    border: 1px solid var(--x-edge);
    border-radius: 6px;
    background: none;
    color: var(--x-dim);
    transition: all 140ms;
  }
  .ic.on,
  .ic:hover {
    border-color: var(--x-edge-lit);
    color: var(--x-ink-2);
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 7px;
    flex: none;
    font-size: 11px;
    color: var(--x-dim);
  }
  .dot {
    width: 6px;
    height: 6px;
    background: var(--x-accent);
  }
  .div {
    width: 1px;
    height: 16px;
    flex: none;
    background: #1e2025;
  }
  .ident {
    flex: none;
    font-size: 11.5px;
    color: var(--x-body);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .sep {
    color: var(--x-faint);
  }
  .no {
    color: var(--x-ink-3);
  }
  .into {
    color: var(--x-faint);
  }
  .pill {
    flex: none;
    padding: 2px 7px;
    border: 1px solid var(--x-edge);
    border-radius: 4px;
    font-size: 10px;
    letter-spacing: 0.1em;
    color: var(--x-dim);
  }
  .sp {
    flex: 1;
    min-width: 0;
  }
  .prog {
    flex: none;
    font-size: 11px;
    color: var(--x-dim);
  }
  /* One pip per finding, coloured by its verdict: the whole review's state in
     the width of a word, and the thing the top bar is actually for. */
  .pips {
    display: flex;
    gap: 3px;
    flex: none;
  }
  .pip {
    width: 22px;
    height: 3px;
    background: var(--x-accent);
  }
  .pip[data-v='accept'] {
    background: var(--x-go);
  }
  .pip[data-v='dismiss'] {
    background: var(--x-edge-lit);
  }
  .btn {
    flex: none;
    height: 26px;
    padding: 0 10px;
    display: inline-flex;
    align-items: center;
    border: 1px solid var(--x-edge);
    border-radius: 5px;
    background: none;
    color: var(--x-mute);
    font-size: 12px;
    text-decoration: none;
    white-space: nowrap;
  }
  .btn:hover {
    border-color: var(--x-edge-lit);
    color: var(--x-ink-2);
  }
  .cta {
    flex: none;
    height: 26px;
    padding: 0 12px;
    border: 1px solid var(--x-edge);
    border-radius: 6px;
    background: none;
    color: var(--x-mute);
    font-size: 12px;
    font-weight: 500;
    white-space: nowrap;
    transition: all 140ms;
  }
  /* Everything decided: sending becomes the obvious next thing rather than one
     more grey button among four. */
  .cta.ready,
  .cta.done {
    border-color: var(--x-go-edge);
    background: rgba(110, 155, 255, 0.14);
    color: var(--x-go-ink);
  }
  .cta:disabled {
    opacity: 0.5;
  }
  /* Stopping is not the accent action and must not look like one: the accent
     here means "the thing this screen is for". It is a way out, so it reads as
     one, and takes its colour only on hover. */
  .cta.halt:hover:not(:disabled) {
    border-color: var(--x-accent-edge);
    color: var(--x-accent-lit);
  }

  .cols {
    display: grid;
    min-height: 0;
    transition: grid-template-columns 260ms cubic-bezier(0.3, 0.8, 0.3, 1);
  }
  @media (prefers-reduced-motion: reduce) {
    .cols {
      transition: none;
    }
  }
  .pad {
    min-width: 0;
    overflow: hidden;
  }
  .mid {
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
    background: var(--x-bg);
  }
  .runwrap {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 26px 34px;
  }

  .head {
    margin: 0 0 10px;
    font-size: 22px;
    font-weight: 600;
    letter-spacing: -0.02em;
    color: var(--x-ink);
  }
  .empty {
    padding: 40px 34px;
  }
  .msg {
    margin: 0;
    padding: 26px 34px;
    max-width: 74ch;
    font-size: 14px;
    line-height: 1.7;
    color: var(--x-body);
  }
  .msg.flush {
    padding: 0;
  }
  .link {
    border: 0;
    background: none;
    padding: 0;
    color: var(--x-go-lit);
    font-size: 14px;
  }

  .skel {
    padding: 26px 34px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .sk {
    border-radius: 6px;
    background: linear-gradient(90deg, var(--x-panel) 25%, var(--x-panel-2) 50%, var(--x-panel) 75%);
    background-size: 260% 100%;
    animation: sh 1.5s linear infinite;
  }
  .sk.a {
    height: 26px;
    width: 38%;
  }
  .sk.b {
    height: 14px;
    width: 78%;
  }
  .sk.c {
    height: 88px;
  }
  @keyframes sh {
    to {
      background-position: -260% 0;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .sk {
      animation: none;
    }
  }

  .status {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 0 12px;
    border-top: 1px solid var(--x-line);
    background: var(--x-chrome);
    font-size: 10.5px;
    color: var(--x-dim);
    min-width: 0;
    overflow: hidden;
  }
  .status .lit {
    color: var(--x-go);
  }
  .pipe {
    color: var(--x-edge);
  }

  /* Narrow. The rails go: a three-column workspace on 390px is three unusable
     columns, and the reading is the part that survives. */
  @media (max-width: 900px) {
    .cols {
      grid-template-columns: 1fr !important;
    }
    .pad {
      display: none;
    }
    .bar {
      gap: 8px;
    }
    .brand,
    .div,
    .pill,
    .prog,
    .pips,
    .ic {
      display: none;
    }
    .keys {
      display: none;
    }
  }
</style>
