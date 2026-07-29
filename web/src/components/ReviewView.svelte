<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { reviewDraft, postReview, type ReviewDraft } from '../lib/api'
  import { workedFor } from '../lib/sidebar'
  import FindingCard from './FindingCard.svelte'

  // A review, as a review rather than as a conversation.
  //
  // This replaced showing a review inside the chat, and the tell that the chat
  // was wrong was that every improvement to it was an attempt to HIDE the chat:
  // the brief sent silently, the findings pinned above the transcript, the tool
  // calls collapsed, the prompt wrapped so a reopened session would not replay
  // it. When every change suppresses the surface, the surface is wrong. A chat is
  // for open-ended conversation; a review is a fixed set of judgements, each with
  // evidence, that you accept or drop and then send.
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
  let cursor = $state(0)
  let posting = $state(false)
  let err = $state('')
  let loaded = $state(false)

  const base = $derived(app.baseForMachine(machineId))
  const meta = $derived(app.sessions.find((s) => s.machineId === machineId && s.id === sessionId))
  // Named sessionState, not state: a variable called `state` shadows the $state
  // rune for the whole component.
  const sessionState = $derived(meta ? app.liveState(meta) : '')
  const running = $derived(sessionState === 'running' || sessionState === 'starting')

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
  // Re-read when the turn ends, which is when the findings arrive.
  $effect(() => {
    if (!running && loaded) void load()
  })

  const findings = $derived(draft?.findings ?? [])
  const kept = $derived(findings.filter((f) => !dropped.has(f.index)))
  const keptInline = $derived(kept.filter((f) => f.inline).length)
  const posted = $derived(!!draft?.posted_url)

  function toggle(i: number) {
    const next = new Set(dropped)
    if (next.has(i)) next.delete(i)
    else next.add(i)
    dropped = next
  }

  function ask(i: number) {
    // The finding becomes the subject of a message, and the chat opens with it.
    // This is the one thing kunai has that a CI reviewer does not, so it is one
    // click rather than a mode you have to find.
    const f = findings[i]
    app.reviewAsk = `About your finding on ${f.file}:${f.line} ("${f.title}") — `
    app.reviewChat = true
  }

  async function post() {
    if (posting || posted) return
    posting = true
    err = ''
    try {
      const res = await postReview(base, sessionId, kept.map((f) => f.index))
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
        cursor = Math.min(cursor + 1, findings.length - 1)
        break
      case 'k':
        cursor = Math.max(cursor - 1, 0)
        break
      case 'd':
      case 'x':
        if (findings[cursor]) toggle(findings[cursor].index)
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
        <a class="done" href={draft?.posted_url} target="_blank" rel="noreferrer">Posted →</a>
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
       to fill a bar. -->
  {#if running}
    <div class="prog">
      <span class="spin" aria-hidden="true"></span>
      Reviewing <span class="mono">{workedFor(app.liveTurnStart(meta ?? ({} as never)), now)}</span>
      {#if findings.length}<span class="sofar">· {findings.length} so far</span>{/if}
    </div>
  {:else if draft && findings.length}
    <div class="prog quiet">
      {kept.length} of {findings.length} kept · {keptInline} inline · {kept.length - keptInline} in the summary
    </div>
  {/if}

  <div class="body">
    {#if draft?.summary}
      <p class="sum">{draft.summary}</p>
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

    {#each findings as f, i (f.index)}
      <div id="f-{i}">
        <FindingCard
          {f}
          dropped={dropped.has(f.index)}
          selected={i === cursor}
          onToggle={() => toggle(f.index)}
          onAsk={() => ask(i)}
        />
      </div>
    {/each}

    {#if err}<p class="err">{err}</p>{/if}

    {#if findings.length > 1}
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
  .prog.quiet {
    color: var(--text-4);
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
</style>
