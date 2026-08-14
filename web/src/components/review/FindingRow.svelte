<script lang="ts">
  import type { ReviewFinding, Severity } from '../../lib/api'
  import type { FindingEdit } from '../../lib/review'
  import { langFor } from '../../lib/outputShape'
  import { severityLabel } from '../../lib/severity'
  import Hunk from './Hunk.svelte'
  import FindingEditor from './FindingEditor.svelte'

  // One finding, as a row you triage rather than a document you read.
  //
  // The card this replaced gave every part of a finding the same weight and
  // showed all of it at once: a six-line wall of body prose, a four-line block
  // of evidence, and a thirteen-line hunk that was mostly comment, so ONE
  // finding filled a laptop screen and the Drop button sat below the fold. You
  // could not see how many findings there were, which was the worst one, or how
  // far through you had got.
  //
  // The parts of a finding are needed in a strict order, so they are ranked that
  // way. The claim decides most judgements on its own and is the only thing
  // always at full size. Where it is comes next, at a glance. The argument, the
  // code and what checked it are the reader's recourse when the claim is not
  // enough, and they are one click away rather than in the way.
  //
  // Drop stays on the header at every state, because deciding is the job and it
  // must never require opening anything.
  let {
    f,
    open,
    dropped,
    cursor,
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
    // The reader's rewrite, or undefined when they have not touched it. Owned by
    // the view so it survives the draft being re-read while the review runs.
    edit?: FindingEdit
    ontoggle: () => void
    ondrop: () => void
    onedit: (next: FindingEdit | undefined) => void
    onask: () => void
  } = $props()

  let editing = $state(false)

  const title = $derived(edit?.title ?? f.title)
  const body = $derived(edit?.body ?? f.body)
  const severity = $derived<Severity>(edit?.severity ?? f.severity)
  const rewritten = $derived(!!edit)

  const lang = $derived(langFor(f.file))
  const location = $derived(
    !f.file ? '' : !f.line ? f.file : f.end_line ? `${f.file}:${f.line}-${f.end_line}` : `${f.file}:${f.line}`,
  )

  // How sure the reviewer is, said only when it is worth saying. Verification
  // now runs on everything postable, so "checked" is the ordinary case and its
  // absence is the exception worth marking.
  const doubt = $derived(
    f.confidence === 'low'
      ? 'a suspicion, not a demonstrated bug'
      : f.confidence === 'medium'
        ? 'rests on an assumption that was not confirmed'
        : '',
  )
</script>

<article class="row sev-{severity}" class:open class:dropped class:cursor>
  <div class="head">
    <button class="disc" onclick={ontoggle} aria-expanded={open}>
      <h3 class="claim">{title}</h3>
      <div class="meta">
        <span class="sev">{severityLabel(severity)}</span>
        {#if location}<span class="loc mono">{location}</span>{/if}
        <span class="tag" class:sum={!f.inline}>{f.inline ? 'inline' : 'summary'}</span>
        {#if !f.verified}<span class="tag warn">unchecked</span>{/if}
        {#if rewritten}<span class="tag">yours</span>{/if}
      </div>
    </button>
    <button class="drop" class:on={dropped} onclick={ondrop}>
      {dropped ? 'Keep' : 'Drop'}
    </button>
  </div>

  {#if open}
    <div class="detail">
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
        {#if body}<p class="why">{body}</p>{/if}

        {#if !f.inline && f.why}
          <p class="aside">Goes in the summary rather than on the line: {f.why}</p>
        {/if}

        {#if f.hunk?.length}
          <Hunk lines={f.hunk} {lang} side={f.side} />
        {/if}

        {#if f.suggestion}
          <div class="sug">
            <span class="slbl">Suggested change</span>
            <pre class="mono">{f.suggestion}</pre>
          </div>
        {/if}

        <!-- What the claim rests on and whether anything independently tried to
             refute it. Last, and quiet: this is the recourse when a reader
             doubts a finding, not the finding. -->
        {#if f.evidence || !f.verified || doubt}
          <div class="ground">
            {#if f.evidence}<p><span class="gk">{f.verified ? 'Checked' : 'Rests on'}:</span> {f.evidence}</p>{/if}
            {#if !f.verified}
              <p class="warn">Nothing independently checked this claim.</p>
            {:else if doubt}
              <p>Held after an independent check, but {doubt}.</p>
            {/if}
          </div>
        {/if}

        <div class="acts">
          <button class="quiet" onclick={() => (editing = true)}>Edit the wording</button>
          <button class="quiet ask" onclick={onask}>Ask about this &rarr;</button>
        </div>
      {/if}
    </div>
  {/if}
</article>

<style>
  /* Severity is carried by a rule down the left edge rather than by a tinted
     box. The finding's own words have to stay the brightest thing in it. */
  .row {
    position: relative;
    padding: 13px 0 13px 15px;
    border-bottom: 1px solid var(--border);
    transition: background 0.14s, opacity 0.14s;
  }
  .row::before {
    content: '';
    position: absolute;
    left: 0;
    top: 12px;
    bottom: 12px;
    width: 2px;
    border-radius: 2px;
    background: var(--sev-ink);
  }
  .sev-blocker {
    --sev-ink: var(--alert);
  }
  .sev-major {
    --sev-ink: var(--busy);
  }
  .sev-minor {
    --sev-ink: var(--text-4);
  }

  /* Open lifts off the page. Closed rows are bare lines, not stacked boxes, so
     a twelve-finding review reads as a list of twelve claims. */
  .open {
    background: var(--panel);
    border-radius: var(--r);
    border-bottom-color: transparent;
    padding-right: 15px;
    margin: 4px 0;
  }
  .open::before {
    top: 14px;
    bottom: 14px;
  }
  .row:not(.open):hover {
    background: color-mix(in srgb, var(--panel) 55%, transparent);
    border-radius: var(--r-sm);
  }
  /* The keyboard cursor, which is not the same thing as being open: you can move
     past a row you have already read. */
  .cursor::before {
    box-shadow: 0 0 0 1px var(--sev-ink);
  }
  /* Dropped recedes rather than vanishing, so undoing is one click and the
     counts stay honest about what changed. */
  .dropped {
    opacity: 0.4;
  }

  .head {
    display: flex;
    align-items: flex-start;
    gap: 12px;
  }
  .disc {
    flex: 1;
    min-width: 0;
    display: block;
    text-align: left;
    padding: 0;
  }
  .claim {
    margin: 0;
    font-size: 15px;
    font-weight: 550;
    line-height: 1.42;
    color: var(--text);
    letter-spacing: -0.005em;
  }
  .dropped .claim {
    text-decoration: line-through;
    text-decoration-color: var(--text-4);
  }
  .meta {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 9px;
    margin-top: 5px;
  }
  .sev {
    flex: none;
    font-size: 10px;
    font-weight: 650;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--sev-ink);
  }
  .loc {
    min-width: 0;
    font-size: 11.5px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    /* A clipped path keeps its leading slash where it belongs. */
    unicode-bidi: plaintext;
  }
  .tag {
    flex: none;
    font-size: 10.5px;
    letter-spacing: 0.02em;
    color: var(--text-4);
  }
  .tag.sum {
    color: var(--text-3);
  }
  .tag.warn {
    color: var(--busy);
  }

  .drop {
    flex: none;
    padding: 4px 12px;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--text-3);
    font-size: 11.5px;
    font-weight: 550;
  }
  .drop:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .drop.on {
    color: var(--text-2);
    border-color: var(--border-2);
    background: var(--panel-2);
  }

  .detail {
    padding-top: 2px;
  }
  /* A measure, because the claim's justification is prose and prose set to 800px
     is not read. Paragraph breaks the model wrote are preserved. */
  .why {
    margin: 11px 0 0;
    max-width: 68ch;
    font-size: 13.5px;
    line-height: 1.7;
    color: var(--text-2);
    white-space: pre-wrap;
  }
  .aside {
    margin: 9px 0 0;
    max-width: 68ch;
    font-size: 12px;
    line-height: 1.6;
    color: var(--text-4);
  }

  .sug {
    margin-top: 12px;
  }
  .slbl {
    display: block;
    margin-bottom: 5px;
    font-size: 10.5px;
    letter-spacing: 0.04em;
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
    white-space: pre;
  }

  .ground {
    margin-top: 13px;
    padding-top: 10px;
    border-top: 1px solid var(--border);
  }
  .ground p {
    margin: 0 0 4px;
    max-width: 72ch;
    font-size: 11.5px;
    line-height: 1.6;
    color: var(--text-4);
  }
  .ground p:last-child {
    margin-bottom: 0;
  }
  .gk {
    color: var(--text-3);
  }
  .ground .warn {
    color: var(--busy);
  }

  .acts {
    display: flex;
    align-items: center;
    gap: 14px;
    margin-top: 14px;
  }
  .quiet {
    font-size: 11.5px;
    color: var(--text-4);
  }
  .quiet:hover {
    color: var(--text-2);
  }
  .ask {
    margin-left: auto;
  }

  @media (max-width: 560px) {
    .drop {
      padding: 7px 14px;
    }
    .claim {
      font-size: 14.5px;
    }
  }
</style>
