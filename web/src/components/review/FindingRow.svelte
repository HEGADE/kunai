<script lang="ts">
  import type { ReviewFinding, Severity } from '../../lib/api'
  import type { FindingEdit } from '../../lib/review'
  import { langFor } from '../../lib/outputShape'
  import { proseHtml } from '../../lib/prose'
  import { severityLabel } from '../../lib/severity'
  import Hunk from './Hunk.svelte'
  import FindingEditor from './FindingEditor.svelte'

  // One finding, as a written judgement with exhibits under it.
  //
  // The design rests on one split, and everything else follows from it: the
  // CLAIM is prose a reviewer wrote about your code, and everything supporting
  // it is machinery. So the claim is set in a serif at a size you read rather
  // than scan, and the location, the counts and the code are mono. A reader can
  // tell in one glance which part of the card is somebody's opinion and which
  // part is a fact about the repository, which is the distinction the whole
  // screen is about and which a single sans ramp cannot make.
  //
  // The gutter is the second half. Severity, position in the deck and whether
  // anything checked the claim all live in a fixed column to the left of a rule,
  // the way a diff puts its line numbers or a document puts its margin notes.
  // They were inline before, as small grey words in a row of other small grey
  // words, which is why a review of a dozen findings had no shape at all.
  //
  // And the argument is CLAMPED. A verified finding now carries the verifier's
  // full working, which runs to a dozen lines of dense technical prose, and
  // printing all of it under every claim is the wall this card exists to avoid.
  // Four lines is enough to decide most findings; the rest is one click away.
  let {
    f,
    open,
    dropped,
    cursor,
    position,
    total,
    edit,
    ontoggle,
    ondrop,
    onedit,
    onask,
  }: {
    f: ReviewFinding
    open: boolean
    dropped: boolean
    cursor: boolean
    // Where this sits in the deck, 1-based. A reader working through a review
    // needs to know how much is left, and nothing else on the card says.
    position: number
    total: number
    edit?: FindingEdit
    ontoggle: () => void
    ondrop: () => void
    onedit: (next: FindingEdit | undefined) => void
    onask: () => void
  } = $props()

  let editing = $state(false)
  let full = $state(false)
  let showCode = $state(true)
  let showChecked = $state(false)

  const title = $derived(edit?.title ?? f.title)
  const body = $derived(edit?.body ?? f.body)
  const severity = $derived<Severity>(edit?.severity ?? f.severity)
  const rewritten = $derived(!!edit)

  const lang = $derived(langFor(f.file))
  const location = $derived(
    !f.file ? '' : !f.line ? f.file : f.end_line ? `${f.file}:${f.line}-${f.end_line}` : `${f.file}:${f.line}`,
  )
  // Long enough that clamping it is worth a control. Below this the "more"
  // button is more furniture than the two lines it hides.
  const longBody = $derived(body.length > 400)

  const doubt = $derived(
    f.confidence === 'low'
      ? 'a suspicion, not a demonstrated bug'
      : f.confidence === 'medium'
        ? 'it rests on an assumption that was not confirmed'
        : '',
  )
</script>

<article class="row sev-{severity}" class:open class:dropped class:cursor>
  <!-- The gutter: what this is, where it is in the deck, and whether anything
       checked it. Fixed width, so every row's claim starts on the same line and
       the column reads down the page. -->
  <div class="gutter">
    <span class="mark" aria-hidden="true"></span>
    <span class="sev">{severityLabel(severity)}</span>
    <span class="pos mono">{position}<span class="of">/{total}</span></span>
    {#if f.verified}
      <span class="checked" title="An independent pass tried to refute this and failed">checked</span>
    {:else}
      <span class="checked un" title="Nothing independently checked this claim">unchecked</span>
    {/if}
  </div>

  <div class="main" class:closed={!open}>
    <button class="disc" onclick={ontoggle} aria-expanded={open}>
      <h3 class="claim">{title}</h3>
      <div class="where">
        <span class="loc mono">{location}</span>
        <span class="tag" class:sum={!f.inline}>{f.inline ? 'on the line' : 'in the summary'}</span>
        {#if rewritten}<span class="tag yours">your wording</span>{/if}
      </div>
    </button>

    {#if open}
      {#if editing}
        <FindingEditor
          initial={{ title, body, severity }}
          original={{ title: f.title, body: f.body, severity: f.severity }}
          onsave={(next) => {
            onedit(next)
            editing = false
          }}
          oncancel={() => (editing = false)}
          onrevert={rewritten ? () => (onedit(undefined), (editing = false)) : undefined}
        />
      {:else}
        {#if body}
          <!-- The identifiers and file references in the argument are set as
               code, because they are what a reader is hunting for and flat text
               hides them. See lib/prose.ts. -->
          <div class="why" class:clamped={longBody && !full}>{@html proseHtml(body)}</div>
          {#if longBody}
            <button class="more" onclick={() => (full = !full)}>{full ? 'Less' : 'The rest of the argument'}</button>
          {/if}
        {/if}

        {#if !f.inline && f.why}
          <p class="aside">In the summary rather than on the line: {f.why}</p>
        {/if}

        <div class="exhibits">
          {#if f.hunk?.length}
            <button class="ex" class:on={showCode} onclick={() => (showCode = !showCode)}>
              <span class="chev" class:o={showCode} aria-hidden="true">›</span> The code
            </button>
          {/if}
          {#if f.evidence || !f.verified || doubt}
            <button class="ex" class:on={showChecked} onclick={() => (showChecked = !showChecked)}>
              <span class="chev" class:o={showChecked} aria-hidden="true">›</span>
              {f.verified ? 'What checked it' : 'What it rests on'}
            </button>
          {/if}
        </div>

        {#if showCode && f.hunk?.length}
          <Hunk lines={f.hunk} {lang} side={f.side} />
        {/if}

        {#if f.suggestion}
          <div class="sug">
            <span class="slbl mono">suggested change</span>
            <pre class="mono">{f.suggestion}</pre>
          </div>
        {/if}

        {#if showChecked}
          <div class="ground">
            {#if f.evidence}<p>{@html proseHtml(f.evidence)}</p>{/if}
            {#if !f.verified}
              <p class="warn">Nothing independently checked this claim.</p>
            {:else if doubt}
              <p>It held under an independent check, but {doubt}.</p>
            {/if}
          </div>
        {/if}

        <div class="acts">
          <button class="drop" class:on={dropped} onclick={ondrop}>{dropped ? 'Put it back' : 'Drop'}</button>
          <button class="quiet" onclick={() => (editing = true)}>Edit the wording</button>
          <button class="quiet ask" onclick={onask}>Ask about this &rarr;</button>
        </div>
      {/if}
    {:else}
      <!-- Closed, the decision still has to be reachable: triage must never
           require opening anything. -->
      <button class="drop tight" class:on={dropped} onclick={ondrop}>{dropped ? 'Put it back' : 'Drop'}</button>
    {/if}
  </div>
</article>

<style>
  .row {
    display: grid;
    grid-template-columns: 92px 1fr;
    gap: 0 18px;
    padding: 14px 0;
    border-bottom: 1px solid var(--border);
    transition: background 0.14s, opacity 0.14s;
  }
  .sev-blocker {
    --ink: var(--alert);
  }
  .sev-major {
    --ink: var(--busy);
  }
  .sev-minor {
    --ink: #7b8794;
  }

  /* Open lifts onto a warmer, slightly raised surface. The one place in this app
     where a panel is worth having: it marks the difference between the row you
     are reading and the rows you are scanning past, and reading is what this
     screen is for. */
  .open {
    background: linear-gradient(180deg, #17171b 0%, #141416 100%);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    padding: 18px 20px 16px;
    margin: 10px 0;
    box-shadow: 0 1px 0 rgba(255, 255, 255, 0.03) inset;
  }
  .row:not(.open):hover {
    background: color-mix(in srgb, var(--panel) 50%, transparent);
    border-radius: var(--r-sm);
  }
  .dropped {
    opacity: 0.36;
  }

  /* The gutter, read down the page like a margin. */
  .gutter {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 4px;
    padding-top: 3px;
    border-right: 1px solid transparent;
  }
  .open .gutter {
    border-right-color: var(--border);
    padding-right: 16px;
    margin-right: -2px;
  }
  .mark {
    width: 100%;
    height: 3px;
    border-radius: 2px;
    background: var(--ink);
    margin-bottom: 4px;
  }
  .sev {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--ink);
  }
  .pos {
    font-size: 11px;
    color: var(--text-3);
    font-variant-numeric: tabular-nums;
  }
  .of {
    color: var(--text-4);
  }
  .checked {
    font-size: 10px;
    letter-spacing: 0.03em;
    color: var(--text-4);
  }
  .checked.un {
    color: var(--busy);
  }
  .cursor .mark {
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--ink) 35%, transparent);
  }

  .main {
    min-width: 0;
  }
  .disc {
    display: block;
    width: 100%;
    text-align: left;
    padding: 0;
  }
  /* The claim, in a serif. It is the one thing on this card that is a sentence
     somebody wrote rather than a fact about the repository, and setting it apart
     that way is what makes a card readable at a glance instead of a stack of
     grey rows in the same voice. */
  .claim {
    margin: 0;
    font-family: var(--serif);
    font-size: 19px;
    font-weight: 500;
    line-height: 1.34;
    letter-spacing: -0.005em;
    color: var(--text);
    /* Ligatures are off globally for code; prose at this size wants them. */
    font-variant-ligatures: common-ligatures;
  }
  .open .claim {
    font-size: 21px;
  }
  .dropped .claim {
    text-decoration: line-through;
    text-decoration-color: var(--text-4);
  }
  .where {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: 7px;
  }
  .loc {
    font-size: 11.5px;
    color: var(--text-3);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    unicode-bidi: plaintext;
  }
  .tag {
    font-size: 10.5px;
    color: var(--text-4);
  }
  .tag.sum {
    color: var(--busy);
  }
  .tag.yours {
    color: var(--text-3);
  }

  .why {
    margin: 14px 0 0;
    max-width: 74ch;
    font-size: 14px;
    line-height: 1.66;
    color: var(--text-2);
    white-space: pre-wrap;
  }
  /* Four lines is enough to judge most findings. A verified one now carries the
     verifier's whole working underneath, and printing all of it under every
     claim is the wall this card exists to avoid. */
  .clamped {
    display: -webkit-box;
    -webkit-line-clamp: 4;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  /* The identifiers a reader is hunting for. Mono and a step brighter than the
     prose around them, so the eye lands on them without the paragraph turning
     into a ransom note. */
  .why :global(code),
  .ground :global(code) {
    font-family: var(--mono);
    font-size: 0.9em;
    color: var(--text);
    background: rgba(255, 255, 255, 0.045);
    padding: 0.05em 0.32em;
    border-radius: 4px;
  }
  .why :global(code.loc),
  .ground :global(code.loc) {
    color: #9fb2c4;
    background: rgba(120, 160, 200, 0.09);
  }
  .more {
    margin-top: 7px;
    font-size: 12px;
    color: var(--text-3);
    text-decoration: underline;
    text-underline-offset: 3px;
    text-decoration-color: var(--border-2);
  }
  .more:hover {
    color: var(--text);
  }
  .aside {
    margin: 10px 0 0;
    max-width: 74ch;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--text-4);
  }

  /* The exhibits, named rather than dumped. A reader who trusts the claim never
     opens either; a reader who doubts it knows exactly which one to open. */
  .exhibits {
    display: flex;
    gap: 8px;
    margin-top: 16px;
  }
  .ex {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 4px 11px 4px 8px;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--text-3);
    font-size: 11.5px;
  }
  .ex:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .ex.on {
    color: var(--text-2);
    background: rgba(255, 255, 255, 0.035);
  }
  .chev {
    display: inline-block;
    font-size: 13px;
    line-height: 1;
    transition: transform 0.15s;
  }
  .chev.o {
    transform: rotate(90deg);
  }
  @media (prefers-reduced-motion: reduce) {
    .chev {
      transition: none;
    }
  }

  .sug {
    margin-top: 14px;
  }
  .slbl {
    display: block;
    margin-bottom: 5px;
    font-size: 10px;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .sug pre {
    margin: 0;
    padding: 10px 12px;
    border-radius: var(--r-sm);
    background: var(--bg);
    font-size: 12px;
    line-height: 1.6;
    color: var(--text-2);
    overflow-x: auto;
  }

  .ground {
    margin-top: 12px;
    padding: 12px 14px;
    border-radius: var(--r-sm);
    background: rgba(255, 255, 255, 0.022);
  }
  .ground p {
    margin: 0 0 6px;
    max-width: 78ch;
    font-size: 12.5px;
    line-height: 1.62;
    color: var(--text-3);
  }
  .ground p:last-child {
    margin-bottom: 0;
  }
  .ground .warn {
    color: var(--busy);
  }

  .acts {
    display: flex;
    align-items: center;
    gap: 14px;
    margin-top: 18px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }
  .drop {
    flex: none;
    padding: 5px 14px;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--text-2);
    font-size: 12px;
    font-weight: 550;
  }
  .drop:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .drop.on {
    color: var(--text);
    border-color: var(--border-2);
    background: var(--panel-2);
  }
  /* Closed, the claim and the decision share a line: the row stays as short as
     its title and the control sits where the eye ends up. Laid out rather than
     floated, so a one-line title and a three-line one both put it in the right
     place instead of wherever a negative margin happened to suit. */
  .main.closed {
    display: flex;
    align-items: flex-start;
    gap: 14px;
  }
  .main.closed .disc {
    flex: 1;
    min-width: 0;
  }
  .drop.tight {
    flex: none;
    margin-top: 2px;
    padding: 3px 12px;
    font-size: 11.5px;
  }
  .quiet {
    font-size: 12px;
    color: var(--text-4);
  }
  .quiet:hover {
    color: var(--text-2);
  }
  .ask {
    margin-left: auto;
  }

  /* Narrow: the gutter turns into a row above the claim, because 92px of margin
     on a 360px screen is a third of the width spent on four small words. */
  @media (max-width: 620px) {
    .row {
      grid-template-columns: 1fr;
      gap: 8px;
    }
    .open {
      padding: 14px 14px 12px;
    }
    .gutter {
      flex-direction: row;
      align-items: center;
      gap: 10px;
    }
    .open .gutter {
      border-right: none;
      padding-right: 0;
      margin-right: 0;
      border-bottom: 1px solid var(--border);
      padding-bottom: 8px;
    }
    .mark {
      width: 22px;
      height: 3px;
      margin-bottom: 0;
    }
    .claim,
    .open .claim {
      font-size: 18px;
    }
    .drop.tight {
      padding: 6px 14px;
    }
    .acts {
      flex-wrap: wrap;
      row-gap: 10px;
    }
  }
</style>
