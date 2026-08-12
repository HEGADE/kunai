<script lang="ts">
  import type { ReviewFinding, Severity } from '../lib/api'
  import { langFor } from '../lib/outputShape'
  import { highlightToHtml } from '../lib/highlight'
  import { SEVERITIES, severityLabel, severityHint } from '../lib/severity'

  // One finding, carrying its own evidence.
  //
  // Self-contained on purpose: the claim, the code it is about, and the
  // decisions you can make about it, all in one card. That is what lets the same
  // layout work on a laptop and on a phone, which a list-plus-detail split cannot
  // do, and kunai is used from a phone.
  //
  // The card leads with severity rather than with the file, and that is the fix
  // for the thing that made a review unreadable: a dozen identical cards in
  // whatever order the model emitted them, with nothing to say which one was the
  // data-loss bug. The severity stripe and the word are two channels for the
  // same fact, because colour alone excludes readers who cannot separate red
  // from amber.
  let {
    f,
    dropped,
    selected,
    edit,
    onToggle,
    onAsk,
    onEdit,
  }: {
    f: ReviewFinding
    dropped: boolean
    selected: boolean
    // The user's rewrite, or undefined when they have not touched it. Owned by
    // the view rather than the card so it survives the card being re-rendered
    // when the draft is re-read.
    edit?: { title: string; body: string; severity: Severity }
    onToggle: () => void
    onAsk: () => void
    onEdit: (next: { title: string; body: string; severity: Severity } | undefined) => void
  } = $props()

  let editing = $state(false)

  // What is shown: the user's words when they have rewritten it, the reviewer's
  // otherwise.
  const title = $derived(edit?.title ?? f.title)
  const body = $derived(edit?.body ?? f.body)
  const severity = $derived(edit?.severity ?? f.severity)
  const rewritten = $derived(!!edit)

  const lang = $derived(langFor(f.file))
  const location = $derived(
    !f.file ? '' : !f.line ? f.file : f.end_line ? `${f.file}:${f.line}-${f.end_line}` : `${f.file}:${f.line}`,
  )
  // The gutter number is the one the finding quotes: the new file normally, the
  // old file for a finding about a deleted line.
  const num = (l: { old?: number; new?: number }) => (f.side === 'LEFT' ? l.old : l.new) || ''

  // Draft state while the editor is open, committed on Save so an abandoned edit
  // changes nothing.
  let draftTitle = $state('')
  let draftBody = $state('')
  let draftSeverity = $state<Severity>('minor')

  function openEditor() {
    draftTitle = title
    draftBody = body
    draftSeverity = severity
    editing = true
  }

  function save() {
    editing = false
    // An edit identical to the original is not an edit: recording it would mark
    // the card as rewritten and send a pointless override to the server.
    if (draftTitle === f.title && draftBody === f.body && draftSeverity === f.severity) {
      onEdit(undefined)
      return
    }
    onEdit({ title: draftTitle, body: draftBody, severity: draftSeverity })
  }

  function revert() {
    editing = false
    onEdit(undefined)
  }
</script>

<article class="card sev-{severity}" class:dropped class:selected>
  <header>
    <span class="sev">{severityLabel(severity)}</span>
    <span class="loc mono">{location}</span>
    <span class="where" class:sum={!f.inline} title={f.why ?? 'Posted as a comment on this line'}>
      {f.inline ? 'inline' : 'summary'}
    </span>
  </header>

  {#if editing}
    <!-- Editing rather than dropping. A finding whose point is right and whose
         wording is wrong used to leave only one option, which threw away the
         finding to get rid of the sentence. -->
    <div class="editor">
      <input class="ed-title" bind:value={draftTitle} placeholder="What is wrong, in one line" />
      <textarea class="ed-body" bind:value={draftBody} rows="4" placeholder="Why it is wrong and what it breaks"></textarea>
      <div class="ed-sev">
        {#each SEVERITIES as s (s)}
          <button
            class="sevpick sev-{s}"
            class:on={draftSeverity === s}
            title={severityHint(s)}
            onclick={() => (draftSeverity = s)}
          >
            {severityLabel(s)}
          </button>
        {/each}
      </div>
      <div class="ed-acts">
        <button class="ed-save" onclick={save}>Save</button>
        <button class="ed-cancel" onclick={() => (editing = false)}>Cancel</button>
        {#if rewritten}
          <button class="ed-revert" onclick={revert}>Restore original</button>
        {/if}
      </div>
    </div>
  {:else}
    <h3 class="claim">{title}</h3>
    {#if body}<p class="why">{body}</p>{/if}
    {#if rewritten}<p class="note edited">Rewritten by you. This is what will be posted.</p>{/if}
    {#if f.why}<p class="note">{f.why}</p>{/if}

    <!-- Whether anything independently checked this claim, and what it rests on.
         Both are shown because a reviewer that hedges when unsure earns the
         right to be believed when it does not. -->
    {#if f.evidence}
      <p class="ev"><span class="evk">Checked:</span> {f.evidence}</p>
    {/if}
    {#if !f.verified || f.confidence !== 'high'}
      <p class="unver">
        {#if !f.verified}Not independently checked{:else}Confirmed{/if}{#if f.confidence !== 'high'}
          &middot; {f.confidence === 'low' ? 'a suspicion, not a demonstrated bug' : 'rests on an unconfirmed assumption'}
        {/if}
      </p>
    {/if}
  {/if}

  {#if f.hunk?.length}
    <!-- The code, in the diff's own vocabulary. Highlighted per line so a change
         reads as a change AND as code, which a plain red/green block does not. -->
    <div class="hunk mono">
      {#each f.hunk as l, i (i)}
        <div class="hl" class:add={l.kind === '+'} class:del={l.kind === '-'} class:focus={l.focus}>
          <span class="n">{num(l)}</span>
          <span class="k">{l.kind === ' ' ? '' : l.kind}</span>
          <span class="t">{@html highlightToHtml(l.text, lang)}</span>
        </div>
      {/each}
    </div>
  {/if}

  {#if f.suggestion}
    <div class="sug">
      <span class="slbl mono">suggested change</span>
      <pre class="scode mono">{f.suggestion}</pre>
    </div>
  {/if}

  {#if !editing}
    <footer>
      <button class="act" class:on={!dropped} onclick={onToggle}>
        {dropped ? 'Include' : 'Drop'}
      </button>
      <button class="act quiet" onclick={openEditor}>Edit</button>
      <button class="ask" onclick={onAsk}>Ask about this &rarr;</button>
    </footer>
  {/if}
</article>

<style>
  .card {
    position: relative;
    border: 1px solid var(--border);
    border-radius: var(--r);
    background: var(--panel);
    padding: 13px 15px 11px;
    transition: opacity 0.15s, border-color 0.15s;
  }
  /* The severity stripe. A 2px edge rather than a tinted card: the finding's
     own content has to stay the brightest thing in it. */
  .card::before {
    content: '';
    position: absolute;
    left: 0;
    top: 10px;
    bottom: 10px;
    width: 2px;
    border-radius: 2px;
    background: var(--sev-ink);
  }
  /* Severity reuses the tokens that already mean these things elsewhere in the
     app. No new hue is introduced; see lib/severity.ts. */
  .sev-blocker {
    --sev-ink: var(--alert);
  }
  .sev-major {
    --sev-ink: var(--busy);
  }
  .sev-minor {
    --sev-ink: var(--text-4);
  }
  /* Dropped recedes rather than disappearing, so undoing is one click and the
     count in the header stays honest about what changed. */
  .dropped {
    opacity: 0.38;
  }
  /* Keyboard focus is a border, not a glow: this theme has no glows. */
  .selected {
    border-color: var(--border-2);
  }
  header {
    display: flex;
    align-items: baseline;
    gap: 9px;
    margin-bottom: 7px;
  }
  /* The word, beside the colour. Never the colour alone. */
  .sev {
    flex: none;
    font-size: 10.5px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--sev-ink);
  }
  .loc {
    flex: 1;
    min-width: 0;
    font-size: 11.5px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    /* A clipped path keeps its leading slash where it belongs. */
    unicode-bidi: plaintext;
  }
  .where {
    flex: none;
    font-size: 10.5px;
    letter-spacing: 0.03em;
    color: var(--text-4);
  }
  .where.sum {
    color: var(--busy);
  }
  .claim {
    margin: 0;
    font-size: 14px;
    font-weight: 550;
    line-height: 1.4;
    color: var(--text);
  }
  .why {
    margin: 6px 0 0;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--text-2);
    white-space: pre-wrap;
  }
  .note {
    margin: 6px 0 0;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .note.edited {
    color: var(--text-3);
  }
  /* What the claim rests on, and whether anything checked it. Quiet: this is
     the reader's recourse when they doubt a finding, not the finding itself. */
  .ev {
    margin: 7px 0 0;
    font-size: 11.5px;
    line-height: 1.55;
    color: var(--text-4);
  }
  .evk {
    color: var(--text-3);
  }
  .unver {
    margin: 5px 0 0;
    font-size: 11px;
    color: var(--text-4);
  }

  /* The editor. */
  .editor {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .ed-title,
  .ed-body {
    width: 100%;
    padding: 8px 10px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    color: var(--text);
    font-size: 12.5px;
    font-family: inherit;
    line-height: 1.55;
    outline: none;
    resize: vertical;
  }
  .ed-title {
    font-size: 13.5px;
    font-weight: 550;
  }
  .ed-title:focus,
  .ed-body:focus {
    border-color: var(--border-2);
  }
  .ed-sev {
    display: flex;
    gap: 6px;
  }
  .sevpick {
    padding: 4px 11px;
    border-radius: var(--r-sm);
    border: 1px solid var(--border);
    background: none;
    color: var(--text-4);
    font-size: 11.5px;
    font-weight: 550;
  }
  .sevpick:hover {
    color: var(--text-2);
  }
  .sevpick.on {
    color: var(--sev-ink);
    border-color: var(--sev-ink);
  }
  .ed-acts {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .ed-save {
    padding: 5px 14px;
    border-radius: var(--r-sm);
    background: var(--white);
    color: #0b0b0c;
    font-size: 12px;
    font-weight: 550;
  }
  .ed-cancel,
  .ed-revert {
    font-size: 11.5px;
    color: var(--text-4);
  }
  .ed-cancel:hover,
  .ed-revert:hover {
    color: var(--text-2);
  }

  /* The evidence. A diff gutter, not a code block: the numbers are the point,
     because they are what the finding is quoting. */
  .hunk {
    margin-top: 10px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg);
    overflow-x: auto;
    font-size: 12px;
    line-height: 1.55;
  }
  .hl {
    display: flex;
    gap: 0;
    white-space: pre;
  }
  .n {
    flex: none;
    width: 44px;
    padding-right: 9px;
    text-align: right;
    color: var(--text-4);
    opacity: 0.6;
    user-select: none;
  }
  .k {
    flex: none;
    width: 12px;
    color: var(--text-4);
    user-select: none;
  }
  .t {
    flex: 1;
    padding-right: 10px;
  }
  /* The same muted green and red the changed-files card uses, at the same low
     opacity: a diff should read as a diff without shouting. */
  .add {
    background: color-mix(in srgb, var(--live) 11%, transparent);
  }
  .del {
    background: color-mix(in srgb, var(--alert) 10%, transparent);
  }
  .add .k {
    color: var(--live);
  }
  .del .k {
    color: var(--alert);
  }
  /* The lines the finding is actually about, marked so context can be generous
     without the point getting lost in it. */
  .focus {
    box-shadow: inset 2px 0 0 var(--text-3);
  }

  .sug {
    margin-top: 10px;
  }
  .slbl {
    display: block;
    margin-bottom: 4px;
    font-size: 10.5px;
    color: var(--text-4);
  }
  .scode {
    margin: 0;
    padding: 9px 11px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg);
    font-size: 12px;
    line-height: 1.5;
    color: var(--text-2);
    overflow-x: auto;
    white-space: pre;
  }

  footer {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 11px;
  }
  .act {
    padding: 4px 12px;
    border-radius: var(--r-sm);
    background: var(--panel-2);
    color: var(--text-2);
    font-size: 12px;
    font-weight: 500;
  }
  .act:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .act.quiet {
    background: none;
    color: var(--text-4);
  }
  .act.quiet:hover {
    background: var(--panel-2);
    color: var(--text-2);
  }
  .ask {
    margin-left: auto;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .ask:hover {
    color: var(--text-2);
  }
</style>
