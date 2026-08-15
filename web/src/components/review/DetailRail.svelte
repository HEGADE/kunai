<script lang="ts">
  import type { ReviewFinding } from '../../lib/api'
  import type { Verdict } from '../../lib/reviewDeck'
  import { pad, fixOf, checkRows } from '../../lib/reviewDeck'
  import { copyText } from '../../lib/clipboard'
  import { effectiveSeverity, type Edits } from '../../lib/reviewDeck'

  // The active finding, answered in the order a reader asks.
  //
  // Once you believe a claim there are four questions and they come in a fixed
  // order: what would I change, what checked this, who can reach it, and where
  // is it. As prose that is a paragraph nobody reads. As four named panels it is
  // three seconds of scanning, and two findings can be compared because both
  // answer in the same shape.
  //
  // Every panel answers from whatever the record holds, and NONE of them is
  // allowed to report the absence of a field as the absence of an answer. The
  // rail used to give up whenever a finding carried no patch, no grounds and no
  // impact, and print one apologetic paragraph into an otherwise empty column --
  // on a finding that had been independently verified, carried a thousand words
  // of evidence, and had a confidence the rail had never shown anywhere. Three
  // of the four questions had answers; the panel had simply looked for them
  // under the wrong names.
  //
  // The rail is where the DECISION lives, at the bottom, because it is what you
  // do after reading everything above it.
  let {
    f,
    position,
    verdict,
    edits,
    sent,
    href,
    waking,
    onaccept,
    ondismiss,
    onundo,
    onask,
  }: {
    f: ReviewFinding
    position: number
    verdict: Verdict | undefined
    edits: Edits
    // Posted already: every decision above is spent.
    sent: boolean
    // The code on GitHub at the commit that was READ, which is where the code a
    // finding describes actually is. Empty when the review has no head to point at.
    href: string
    // Whether the reviewer's session is being brought back right now. A finished
    // review has to be resumed before it can answer, which takes a moment, and a
    // button that looks untouched for three seconds reads as broken.
    waking: boolean
    onaccept: () => void
    ondismiss: () => void
    onundo: () => void
    onask: () => void
  } = $props()

  const fix = $derived(fixOf(f))
  const rows = $derived(checkRows(f))
  const impact = $derived(f.impact ?? null)
  const span = $derived(f.end_line && f.end_line > f.line ? `${f.line}-${f.end_line}` : `${f.line}`)

  let copied = $state(false)
  let copyTimer: ReturnType<typeof setTimeout> | undefined

  async function copyFix() {
    const text =
      fix.kind === 'patch'
        ? (f.patch?.lines ?? []).map((l) => `${l.sign} ${l.text}`).join('\n')
        : fix.kind === 'text'
          ? fix.text
          : ''
    if (!text) return
    await copyText(text)
    copied = true
    clearTimeout(copyTimer)
    copyTimer = setTimeout(() => (copied = false), 1400)
  }
</script>

<aside class="rail">
  <div class="head">
    <span class="x-cap">Finding {pad(position)}</span>
    <span class="x-cap sev" data-sev={effectiveSeverity(f, edits)}>
      {effectiveSeverity(f, edits).toUpperCase()}
    </span>
  </div>

  <div class="body">
    <div class="x-cap">Suggested change</div>
    {#if fix.kind === 'patch' && f.patch}
      <div class="patch">
        <div class="ptitle">{f.patch.title}</div>
        <div class="plines x-mono">
          {#each f.patch.lines as l, i (i)}
            <div class="pl" class:add={l.sign === '+'} class:del={l.sign === '-'}>
              <span class="psign">{l.sign}</span>
              <span class="ptext">{l.text}</span>
            </div>
          {/each}
        </div>
      </div>
    {:else if fix.kind === 'text'}
      <!-- The suggestion as written.
           A finding can carry one the server could not turn into a diff, and
           showing nothing then loses the answer entirely: the fix is here, it
           just does not line up line for line with the code it points at. -->
      <div class="patch">
        <div class="ptitle">Replacement for {f.file.split('/').pop()}:{span}</div>
        <pre class="sugg x-mono">{fix.text}</pre>
      </div>
    {:else}
      <p class="none">
        None offered. The reviewer suggests a change only when the fix is small, local and
        unambiguous, and explains instead when it is not.
      </p>
    {/if}
    {#if fix.kind !== 'none'}
      <!-- Copy, not apply.
           The design offers "Apply as a commit", and a review deliberately runs
           with Write, Edit and Bash withheld: that is the property that lets it
           run unattended on somebody else's branch. Handing this screen a button
           that writes to the tree would undo it, so the change goes to the
           clipboard and applying it stays a thing a person does where they can
           see it. -->
      <button class="apply" onclick={copyFix}>{copied ? 'Copied ✓' : 'Copy the change'}</button>
    {/if}

    <div class="x-cap sp">What checked it</div>
    <div class="grounds">
      {#each rows as g (g.key)}
        <div class="grow">
          <span class="gk x-mono">{g.key}</span>
          <span class="gv">{g.value}</span>
        </div>
      {/each}
    </div>

    {#if impact && (impact.who || impact.radius || impact.size)}
      <div class="x-cap sp">Impact</div>
      <div class="impact">
        {#if impact.who}
          <div class="irow"><span>Reachable by</span><span class="iv">{impact.who}</span></div>
        {/if}
        {#if impact.radius}
          <div class="irow"><span>Blast radius</span><span class="iv hot">{impact.radius}</span></div>
        {/if}
        {#if impact.size}
          <div class="irow"><span>Fix size</span><span class="iv">{impact.size}</span></div>
        {/if}
      </div>
    {/if}

    {#if href}
      <div class="foot">
        <a class="where x-mono" {href} target="_blank" rel="noreferrer">
          <span class="wpath">{f.file}</span><span class="wline">:{span}</span><span class="warr">↗</span>
        </a>
      </div>
    {/if}
  </div>

  <div class="decide">
    {#if sent}
      <p class="spent x-mono">Posted. Decisions are spent.</p>
    {:else if verdict}
      <div class="verdict">
        <span class="vlabel" data-v={verdict}>
          <span class="vdot" aria-hidden="true"></span>
          {verdict === 'accept' ? 'Accepted — will be posted' : 'Dismissed — not sent'}
        </span>
        <div class="sp"></div>
        <button class="undo" onclick={onundo}>Undo</button>
      </div>
    {:else}
      <div class="choose">
        <button class="accept" onclick={onaccept}>Accept <span class="k">A</span></button>
        <button class="dismiss" onclick={ondismiss}>Dismiss <span class="k">X</span></button>
      </div>
    {/if}
    <!-- Always, including after posting, and never behind a patch.
         Arguing with the reviewer is the thing kunai has that a CI reviewer does
         not, and it applies to every finding: the ones with no suggested fix are
         if anything the ones most worth asking about. It used to be rendered
         inside the patch panel, so exactly those findings had no way to ask. -->
    <button class="ask" onclick={onask} disabled={waking}>
      {waking ? 'Waking the reviewer…' : 'Ask the reviewer about this →'}
    </button>
  </div>
</aside>

<style>
  .rail {
    display: flex;
    flex-direction: column;
    min-height: 0;
    overflow: hidden;
    border-left: 1px solid var(--x-line);
    background: var(--x-rail);
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 16px;
    border-bottom: 1px solid var(--x-line-soft);
  }
  .sev[data-sev='blocker'] {
    color: var(--x-accent-lit);
  }
  .body {
    display: flex;
    flex-direction: column;
    flex: 1;
    overflow-y: auto;
    padding: 16px;
  }
  /* A column that scrolls must not squeeze its own children to avoid it. */
  .body > :global(*) {
    flex: none;
  }
  .sp {
    margin-top: 20px;
  }

  /* The change. Blue, because blue means "something you are going to do" here and
     that is the same idea as an accepted finding. */
  .patch {
    border: 1px solid #23303f;
    border-radius: 10px;
    overflow: hidden;
    background: #0b0d10;
    margin: 10px 0;
  }
  .ptitle {
    padding: 9px 12px;
    border-bottom: 1px solid #1c2531;
    font-size: 12.5px;
    line-height: 1.5;
    color: var(--x-go-lit);
  }
  .plines {
    font-size: 11.5px;
    line-height: 1.7;
    padding: 8px 0;
    /* Real code in a 344px column. A tab is eight characters wide by default,
       which at this width is a quarter of the line spent on nothing. */
    tab-size: 2;
  }
  .pl {
    display: flex;
    padding: 0 10px;
  }
  .pl.add {
    background: rgba(110, 155, 255, 0.09);
  }
  .pl.del {
    background: var(--x-accent-wash);
  }
  .psign {
    width: 14px;
    flex: none;
    text-align: center;
    color: var(--x-fainter);
  }
  .pl.add .psign {
    color: var(--x-go);
  }
  .pl.del .psign {
    color: #b0634a;
  }
  /* A wrapped line has to read as one line.
     Code four levels deep in a 344px rail wraps three or four times, and with
     every continuation starting at the left edge the block stops looking like
     code and starts looking like prose with symbols in it. A hanging indent puts
     the continuations under the line they belong to. (The shared margin itself
     is stripped server-side; see stripCommonIndent.) */
  .ptext {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    padding-left: 1.4em;
    text-indent: -1.4em;
    color: var(--x-ink-4);
  }
  .pl.del .ptext {
    color: #8e8a92;
  }
  .sugg {
    margin: 0;
    padding: 8px 12px;
    max-height: 220px;
    overflow: auto;
    font-size: 11.5px;
    line-height: 1.7;
    tab-size: 2;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    color: var(--x-ink-4);
  }
  .none {
    margin: 10px 0 0;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--x-dim);
  }
  .apply {
    width: 100%;
    height: 30px;
    border: 1px solid var(--x-go-edge);
    border-radius: 6px;
    background: var(--x-go-wash);
    color: var(--x-go-ink);
    font-size: 12px;
  }
  .apply:hover {
    background: var(--x-go-wash-lit);
  }

  .grounds {
    display: flex;
    flex-direction: column;
    margin-top: 10px;
  }
  .grow {
    display: grid;
    grid-template-columns: 70px 1fr;
    gap: 10px;
    padding: 9px 0;
    border-bottom: 1px solid var(--x-line-soft);
  }
  /* The last row's rule and the footer's would otherwise sit a few pixels apart
     and read as a line drawn by mistake. */
  .grow:last-child {
    border-bottom: 0;
  }
  .gk {
    font-size: 10px;
    letter-spacing: 0.08em;
    color: var(--x-dim);
    padding-top: 2px;
  }
  .gv {
    font-size: 12.5px;
    line-height: 1.55;
    color: var(--x-body);
    /* Belt and braces on top of the server's cut: a row is a phrase, and five
       lines is where one stops being one. */
    display: -webkit-box;
    -webkit-line-clamp: 5;
    line-clamp: 5;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .impact {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-top: 10px;
    padding: 12px;
    border: 1px solid var(--x-line-panel);
    border-radius: 8px;
    background: var(--x-panel);
  }
  .irow {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    font-size: 12.5px;
    color: var(--x-body);
  }
  .iv {
    color: var(--x-ink-3);
    text-align: right;
  }
  .iv.hot {
    color: var(--x-accent-lit);
  }

  /* Where the code is, at the commit that was read. The pane names the file
     too; what this adds is the one click to go and look at it.
     Pinned to the bottom of the panel rather than left hanging under whatever
     the last block happened to be: on a short finding it read as a stray line
     in the middle of a void, and it is a footer. */
  .foot {
    margin-top: auto;
    padding-top: 24px;
  }
  .where {
    display: flex;
    align-items: baseline;
    gap: 2px;
    padding-top: 12px;
    border-top: 1px solid var(--x-line-soft);
    font-size: 11px;
    color: var(--x-dim);
    text-decoration: none;
    unicode-bidi: plaintext;
  }
  .where:hover {
    color: var(--x-ink-3);
  }
  .wpath {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    direction: rtl;
    text-align: left;
  }
  .wline {
    flex: none;
    color: var(--x-accent);
  }
  .warr {
    flex: none;
    margin-left: 6px;
  }

  .decide {
    border-top: 1px solid var(--x-line-soft);
    padding: 14px 16px;
  }
  .choose {
    display: flex;
    gap: 6px;
  }
  .accept,
  .dismiss {
    flex: 1;
    height: 32px;
    border-radius: 6px;
    font-size: 12.5px;
  }
  .accept {
    border: 1px solid var(--x-go-edge);
    background: rgba(110, 155, 255, 0.12);
    color: var(--x-go-ink);
    font-weight: 500;
  }
  .accept:hover {
    background: var(--x-go-wash-lit);
  }
  .dismiss {
    border: 1px solid var(--x-edge);
    background: none;
    color: var(--x-body);
  }
  .dismiss:hover {
    color: var(--x-ink-2);
    border-color: var(--x-edge-lit);
  }
  .k {
    color: #8aa0c6;
  }
  .dismiss .k {
    color: var(--x-dim);
  }
  .ask {
    width: 100%;
    margin-top: 8px;
    padding: 6px 0;
    border: 0;
    background: none;
    color: var(--x-dim);
    font-size: 11.5px;
    text-align: left;
  }
  .ask:hover {
    color: var(--x-ink-3);
  }
  .ask:disabled {
    color: var(--x-faint);
  }

  .verdict {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .vlabel {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12.5px;
    color: var(--x-body);
  }
  .vlabel[data-v='accept'] {
    color: var(--x-go-lit);
  }
  .vdot {
    width: 6px;
    height: 6px;
    background: currentColor;
  }
  .verdict .sp {
    flex: 1;
    margin: 0;
  }
  .undo {
    height: 26px;
    padding: 0 10px;
    border: 1px solid var(--x-edge);
    border-radius: 5px;
    background: none;
    color: var(--x-dim);
    font-size: 11.5px;
  }
  .undo:hover {
    color: var(--x-ink-2);
  }
  .spent {
    margin: 0;
    font-size: 11px;
    color: var(--x-dim);
  }
</style>
