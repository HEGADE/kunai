<script lang="ts">
  import type { ReviewDraft, ReviewPhase } from '../../lib/api'
  import type { ChatConnection } from '../../lib/chat.svelte'
  import { acts, opened, latest, coverage, changeSize, baseName, phaseSpans } from '../../lib/reviewlive'

  // A review while it is running, built from the design's "Review Running".
  //
  // One column, read top to bottom, and the shape is the argument: somebody
  // looking at this is WAITING, so the thing they came for is the largest thing
  // on the page (what it is doing, in words), the thing they might want next is
  // one line under it, and everything that is a record rather than a question is
  // behind a disclosure. The two-column dashboard this replaced put a stat strip
  // across the top and a file list down the side, which is a lot of furniture
  // around the one sentence anybody reads.
  //
  // Every number here already existed. The review is an ordinary session whose
  // socket is already open, so every file it opens arrives as a tool call and
  // nothing had to be sent to show it; the survey was being computed and thrown
  // away; the change's size was known at creation. What was missing was the
  // decision to look.
  //
  // Two things in the design are deliberately NOT here, and both for the same
  // reason: they are answers this screen does not have. The steps are not
  // buttons (there is nothing to switch to -- a phase that has not happened has
  // nothing to show, and one that has is not re-runnable), and a lead carries no
  // CHASING / HELD UP / DROPPED chip, because nothing records which area the
  // reviewer is on. Inventing either would make the screen more convincing and
  // less true, which is the trade this whole surface exists to refuse.
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

  // What each step is called and what it is for. The headline is the phase in
  // the reviewer's own terms; the line under it is what that actually means,
  // because "verify" is a word this screen should not make anybody guess at.
  const STEPS: { key: ReviewPhase; label: string; head: string; lede: string }[] = [
    {
      key: 'survey',
      label: 'Read',
      head: 'Reading the change',
      lede: 'Opening every diff to work out what this change is for, and where the risk is likely to sit.',
    },
    {
      key: 'find',
      label: 'Find',
      head: 'Looking for problems',
      lede: 'Following the questions it decided are worth asking, and reading past the diff wherever one needs it.',
    },
    {
      key: 'verify',
      label: 'Check',
      head: 'Checking what it found',
      lede: 'Every claim goes to a separate reader whose job is to refute it. Anything that cannot be demonstrated is dropped rather than reported.',
    },
  ]
  const steps = $derived(draft.surveyed ? STEPS : STEPS.slice(1))
  const at = $derived(steps.findIndex((s) => s.key === phase))
  const step = $derived(steps[at] ?? steps[0])

  const spans = $derived(phaseSpans(draft.timeline ?? [], now))
  const spanFor = (key: string) => spans.find((s) => s.phase === key)

  const areas = $derived(draft.survey?.areas ?? [])
  let open = $state<Record<number, boolean>>({ 0: true })
  let traceOpen = $state(false)
  let filesOpen = $state(false)

  // Newest first: this is a record of what has happened, and the interesting end
  // of it is the end nearest now.
  const trace = $derived([...list].reverse())
  const biggest = $derived(
    [...files].sort((a, b) => (b.additions ?? 0) + (b.deletions ?? 0) - ((a.additions ?? 0) + (a.deletions ?? 0))),
  )
  const shown = $derived(biggest.slice(0, 14))

  function clock(ms: number): string {
    if (!ms || ms < 0) return ''
    const s = Math.floor(ms / 1000)
    if (s < 60) return `${s}s`
    const m = Math.floor(s / 60)
    return m < 60 ? `${m}m ${s % 60}s` : `${Math.floor(m / 60)}h ${m % 60}m`
  }
  const n = (x: number) => x.toLocaleString()
</script>

<div class="run">
  <div class="eyebrow x-mono">
    <span class="pulse" aria-hidden="true"></span>
    <span class="lit">Step {at + 1} of {steps.length}</span>
    <span class="sep">·</span>
    <span>{step.label}</span>
  </div>

  <h1 class="head">{step.head}</h1>
  <p class="lede">{step.lede}</p>

  <!-- Where it is. Not buttons: there is nothing to switch to, and a control
       that does nothing when pressed is worse than no control. Each carries the
       time that step took, which the timeline already records and which is the
       one thing a waiting reader can do arithmetic with. -->
  <div class="steps">
    {#each steps as s, i (s.key)}
      {@const span = spanFor(s.key)}
      <div class="stp" data-state={i < at ? 'done' : i === at ? 'on' : 'todo'}>
        <span class="bar"></span>
        <span class="lbl x-mono">
          {s.label}
          <span class="num">{i + 1}</span>
          {#if span}<span class="took">{clock(span.ms)}</span>{/if}
        </span>
      </div>
    {/each}
  </div>

  <section class="rn">
    <div class="cap x-mono">Right now</div>
    {#if phase === 'verify'}
      <!-- Nothing streams here during the check and that is by design: it runs
           in a session of its own, which is the whole reason its verdict is
           worth anything. Said out loud, or a quiet screen reads as a hang. -->
      <div class="line">
        <span class="dot" aria-hidden="true"></span>
        <span class="what x-mono">Handing each claim to a separate reader</span>
      </div>
      <div class="count">In its own session, with none of the reasoning that produced the claim.</div>
    {:else if doing}
      <div class="line">
        <span class="dot" aria-hidden="true"></span>
        <span class="what x-mono">
          {doing.kind === 'read' ? 'Reading' : doing.kind === 'search' ? 'Searching for' : doing.tool}
          <span class="lit">{doing.kind === 'read' ? baseName(doing.what) : doing.what}</span>
        </span>
      </div>
      <div class="count">
        {cover.seen} of {cover.total} files opened{searches ? ` · ${searches} search${searches === 1 ? '' : 'es'}` : ''}
      </div>
    {:else}
      <div class="line">
        <span class="dot" aria-hidden="true"></span>
        <span class="what x-mono">Starting up</span>
      </div>
    {/if}
  </section>

  {#if areas.length}
    <div class="cap x-mono">What it decided to look at</div>
    <p class="note">
      The questions it thinks are worth asking of this change, written before it went looking. Open
      one to see why.
    </p>
    <div class="leads">
      {#each areas as a, i (i)}
        <div class="lead">
          <button class="lhead" onclick={() => (open = { ...open, [i]: !open[i] })}>
            <span class="ldot" aria-hidden="true"></span>
            <span class="ltitle">{a.what}</span>
            <span class="lcaret x-mono">{open[i] ? '−' : '+'}</span>
          </button>
          {#if open[i]}
            <div class="lbody">
              {#if a.why}<p class="lwhy">{a.why}</p>{/if}
              {#if a.files?.length}
                <div class="chips">
                  {#each a.files as f (f)}<span class="chip x-mono">{baseName(f)}</span>{/each}
                </div>
              {/if}
            </div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  <div class="rows">
    <button class="row" onclick={() => (traceOpen = !traceOpen)}>
      <span class="caret x-mono">{traceOpen ? '−' : '+'}</span>
      What it has done so far
      <span class="sp"></span>
      <span class="meta x-mono">{list.length} step{list.length === 1 ? '' : 's'}</span>
    </button>
    {#if traceOpen}
      <div class="drop">
        {#each trace as a, i (i)}
          <div class="tr">
            <span class="tk x-mono">{a.kind === 'read' ? 'read' : a.kind === 'search' ? 'grep' : a.tool.toLowerCase()}</span>
            <span class="tt">{a.kind === 'read' ? baseName(a.what) : a.what || a.tool}</span>
          </div>
        {/each}
        {#if !trace.length}<div class="tr empty">Nothing yet.</div>{/if}
      </div>
    {/if}

    <button class="row" onclick={() => (filesOpen = !filesOpen)}>
      <span class="caret x-mono">{filesOpen ? '−' : '+'}</span>
      The {size.files} changed file{size.files === 1 ? '' : 's'}
      <span class="sp"></span>
      <span class="meta x-mono"><span class="add">+{n(size.additions)}</span> <span class="del">−{n(size.deletions)}</span></span>
    </button>
    {#if filesOpen}
      <div class="drop">
        {#each shown as f (f.path)}
          <div class="fl" class:seen={read.some((p) => p === f.path || p.endsWith('/' + f.path))}>
            <span class="fn x-mono">{f.path}</span>
            <span class="add x-mono">+{f.additions ?? 0}</span>
            <span class="del x-mono">−{f.deletions ?? 0}</span>
          </div>
        {/each}
        {#if biggest.length > shown.length}
          <div class="more">and {biggest.length - shown.length} smaller</div>
        {/if}
      </div>
    {/if}
  </div>
</div>

<style>
  /* One column, 720px, centred. The design's measure: long enough for a
     sentence to be read and short enough that the eye does not have to travel
     back across the screen to find the next line. */
  .run {
    max-width: 720px;
    margin: 0 auto;
    padding: 8px 0 80px;
  }

  .eyebrow {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 11px;
    letter-spacing: 0.14em;
    text-transform: uppercase;
    color: var(--x-body);
    margin-bottom: 18px;
  }
  .eyebrow .lit {
    color: var(--x-accent-lit);
  }
  .eyebrow .sep {
    color: var(--x-faint);
  }
  .pulse {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--x-accent);
    animation: kpulse 1.4s ease-in-out infinite;
  }
  @keyframes kpulse {
    0%,
    100% {
      opacity: 1;
    }
    50% {
      opacity: 0.25;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .pulse,
    .dot {
      animation: none;
    }
  }

  .head {
    margin: 0 0 14px;
    font-size: 40px;
    font-weight: 600;
    letter-spacing: -0.03em;
    line-height: 1.1;
    color: var(--x-ink);
  }
  .lede {
    margin: 0 0 40px;
    max-width: 56ch;
    font-size: 16px;
    line-height: 1.7;
    color: var(--x-body);
  }

  .steps {
    display: flex;
    gap: 6px;
    margin-bottom: 52px;
  }
  .stp {
    display: flex;
    flex: 1;
    flex-direction: column;
    gap: 9px;
    padding-bottom: 8px;
  }
  .bar {
    display: block;
    height: 4px;
    border-radius: 2px;
    background: var(--x-line-panel);
  }
  .stp[data-state='done'] .bar {
    background: var(--x-accent-edge);
  }
  .stp[data-state='on'] .bar {
    background: var(--x-accent);
  }
  .lbl {
    display: flex;
    align-items: center;
    gap: 7px;
    font-size: 10.5px;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--x-dim);
  }
  .stp[data-state='done'] .lbl {
    color: var(--x-mute);
  }
  .stp[data-state='on'] .lbl {
    color: var(--x-accent-lit);
  }
  .num,
  .took {
    color: var(--x-dim);
    letter-spacing: 0.06em;
  }
  .took {
    margin-left: auto;
    text-transform: none;
  }

  /* The one line somebody is actually here for, bounded top and bottom so it
     reads as its own thing rather than as another paragraph. */
  .rn {
    padding: 20px 0 22px;
    border-top: 1px solid var(--x-line-panel);
    border-bottom: 1px solid var(--x-line-panel);
    margin-bottom: 44px;
  }
  .cap {
    font-size: 10px;
    letter-spacing: 0.18em;
    text-transform: uppercase;
    color: var(--x-body);
    margin-bottom: 12px;
  }
  .line {
    display: flex;
    align-items: baseline;
    gap: 10px;
  }
  .dot {
    width: 6px;
    height: 6px;
    flex: none;
    border-radius: 50%;
    background: var(--x-accent);
    animation: kpulse 1.2s ease-in-out infinite;
  }
  .what {
    font-size: 15px;
    line-height: 1.5;
    color: var(--x-ink);
    word-break: break-word;
  }
  .what .lit {
    color: var(--x-accent-lit);
  }
  .count {
    margin-top: 12px;
    padding-left: 16px;
    font-size: 13.5px;
    color: var(--x-body);
  }

  .note {
    margin: 0 0 12px;
    max-width: 56ch;
    font-size: 13.5px;
    line-height: 1.6;
    color: var(--x-body);
  }
  .leads {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin-bottom: 44px;
  }
  .lead {
    border: 1px solid var(--x-line-panel);
    border-radius: 12px;
    background: var(--x-panel);
    overflow: hidden;
  }
  .lhead {
    display: flex;
    align-items: center;
    gap: 14px;
    width: 100%;
    padding: 18px 20px;
    border: 0;
    background: none;
    text-align: left;
  }
  .ldot {
    width: 7px;
    height: 7px;
    flex: none;
    border-radius: 50%;
    background: var(--x-accent);
  }
  .ltitle {
    flex: 1;
    min-width: 0;
    font-size: 16px;
    line-height: 1.4;
    color: var(--x-ink);
  }
  .lcaret {
    flex: none;
    font-size: 12px;
    color: var(--x-dim);
  }
  .lbody {
    padding: 0 20px 20px 41px;
  }
  .lwhy {
    margin: 0 0 14px;
    font-size: 14.5px;
    line-height: 1.75;
    color: var(--x-body);
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .chip {
    padding: 4px 9px;
    border: 1px solid var(--x-line-panel);
    border-radius: 5px;
    font-size: 10.5px;
    color: var(--x-mute);
  }

  /* The record, folded away. Two rows, each saying how much is behind it, so
     opening one is a decision rather than a gamble. */
  .rows {
    display: flex;
    flex-direction: column;
    border-top: 1px solid var(--x-line-panel);
  }
  .row {
    display: flex;
    align-items: center;
    gap: 12px;
    width: 100%;
    padding: 16px 0;
    border: 0;
    border-bottom: 1px solid var(--x-line-panel);
    background: none;
    text-align: left;
    font-size: 13.5px;
    color: var(--x-body);
  }
  .row:hover {
    color: var(--x-ink-2);
  }
  .caret {
    font-size: 12px;
    color: var(--x-body);
  }
  .sp {
    flex: 1;
  }
  .meta {
    font-size: 11px;
    color: var(--x-body);
  }

  .drop {
    display: flex;
    flex-direction: column;
    padding: 12px 0 20px;
    max-height: 340px;
    overflow-y: auto;
  }
  .tr {
    display: grid;
    grid-template-columns: 52px minmax(0, 1fr);
    gap: 14px;
    padding: 7px 0;
  }
  .tk {
    font-size: 11px;
    color: var(--x-dim);
    text-align: right;
  }
  .tt {
    font-size: 13.5px;
    line-height: 1.5;
    color: var(--x-mute);
    overflow-wrap: anywhere;
  }
  .tr.empty {
    color: var(--x-dim);
    font-size: 13px;
  }

  .fl {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 7px 0;
  }
  .fn {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 11.5px;
    color: var(--x-dim);
    unicode-bidi: plaintext;
  }
  /* A file it has been into reads brighter: the list doubles as progress. */
  .fl.seen .fn {
    color: var(--x-mute);
  }
  .add {
    flex: none;
    font-size: 11px;
    color: var(--x-add);
  }
  .del {
    flex: none;
    font-size: 11px;
    color: var(--x-accent-dim);
  }
  .more {
    padding-top: 10px;
    font-size: 13px;
    color: var(--x-body);
  }

  @media (max-width: 700px) {
    .head {
      font-size: 30px;
    }
    .lede {
      font-size: 15px;
      margin-bottom: 30px;
    }
    .steps {
      margin-bottom: 36px;
    }
    .took {
      display: none;
    }
  }
</style>
