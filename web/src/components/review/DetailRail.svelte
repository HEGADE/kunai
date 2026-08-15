<script lang="ts">
  import type { ReviewFinding } from '../../lib/api'
  import type { Verdict } from '../../lib/reviewDeck'
  import { pad } from '../../lib/reviewDeck'
  import { copyText } from '../../lib/clipboard'
  import { effectiveSeverity, type Edits } from '../../lib/reviewDeck'

  // The active finding, answered in the order a reader asks.
  //
  // Once you believe a claim there are four questions and they come in a fixed
  // order: what would I change, what checked this, who can reach it, and what do
  // I do about it. As prose that is a paragraph nobody reads. As four named
  // panels it is three seconds of scanning, and two findings can be compared
  // because both answer in the same shape.
  //
  // The rail is where the DECISION lives, at the bottom, because it is what you
  // do after reading everything above it.
  let {
    f,
    position,
    verdict,
    edits,
    sent,
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
    onaccept: () => void
    ondismiss: () => void
    onundo: () => void
    onask: () => void
  } = $props()

  let copied = $state(false)
  let copyTimer: ReturnType<typeof setTimeout> | undefined

  async function copyPatch() {
    if (!f.patch) return
    const text = f.patch.lines.map((l) => `${l.sign} ${l.text}`).join('\n')
    await copyText(text)
    copied = true
    clearTimeout(copyTimer)
    copyTimer = setTimeout(() => (copied = false), 1400)
  }

  const impact = $derived(f.impact ?? null)
  // Structured rows only.
  //
  // A review from before this shape existed carries one long paragraph, and
  // wrapping that in a labelled row was the wrong fix: at 344px with a 70px
  // label column, a 400-word value became a forty-line ribbon and the panel it
  // sat in stopped being scannable, which is the one thing this panel is for.
  // The paragraph belongs in the pane, and it is there, behind the disclosure.
  const grounds = $derived(f.grounds ?? [])
</script>

<aside class="rail">
  <div class="head">
    <span class="x-cap">Finding {pad(position)}</span>
    <span class="x-cap sev" data-sev={effectiveSeverity(f, edits)}>
      {effectiveSeverity(f, edits).toUpperCase()}
    </span>
  </div>

  <div class="body">
    {#if f.patch}
      <div class="x-cap">Suggested patch</div>
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
      <div class="pacts">
        <!-- Copy, not apply.
             The design offers "Apply as a commit", and a review deliberately
             runs with Write, Edit and Bash withheld: that is the property that
             lets it run unattended on somebody else's branch. Handing this
             screen a button that writes to the tree would undo it, so the patch
             goes to the clipboard and the decision to apply it stays a thing a
             person does somewhere they can see it. -->
        <button class="apply" onclick={copyPatch}>{copied ? 'Copied ✓' : 'Copy the patch'}</button>
        <button class="ask" onclick={onask}>Ask</button>
      </div>
    {/if}

    {#if grounds.length}
      <div class="x-cap sp">What checked it</div>
      <div class="grounds">
        {#each grounds as g (g.key)}
          <div class="grow">
            <span class="gk x-mono">{g.key}</span>
            <span class="gv">{g.value}</span>
          </div>
        {/each}
      </div>
    {/if}

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

    {#if !f.patch && !grounds.length && !impact}
      <p class="bare">
        This finding came with the claim alone: no suggested change, nothing recorded about what
        checked it, and no reach. The argument in the pane is all there is.
      </p>
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
    flex: 1;
    overflow-y: auto;
    padding: 16px;
  }
  .sp {
    margin-top: 20px;
  }

  /* The patch. Blue, because blue means "something you are going to do" here and
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
    line-height: 1.8;
    padding: 8px 0;
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
     Go code at four levels of indentation in a 344px rail wraps three or four
     times, and with every continuation starting at the left edge the block
     stops looking like code and starts looking like prose with symbols in it.
     A hanging indent puts the continuations under the line they belong to. */
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
  .pacts {
    display: flex;
    gap: 6px;
  }
  .apply {
    flex: 1;
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
  .ask {
    height: 30px;
    padding: 0 12px;
    border: 1px solid var(--x-edge);
    border-radius: 6px;
    background: none;
    color: var(--x-body);
    font-size: 12px;
  }
  .ask:hover {
    color: var(--x-ink-2);
    border-color: var(--x-edge-lit);
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

  .bare {
    margin: 0;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--x-dim);
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
