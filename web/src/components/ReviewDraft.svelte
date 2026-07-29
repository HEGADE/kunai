<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { reviewDraft, postReview, type ReviewDraft, type ReviewFinding } from '../lib/api'

  // The review, before it goes to GitHub.
  //
  // This card is the reason posting is a second click. You are about to write
  // publicly, under an identity your whole team shares, on somebody else's pull
  // request, so the draft does not summarise what was found: it shows exactly
  // what will land and where. Each finding says whether it becomes a comment on
  // the line itself or a note in the summary, and the ones GitHub will not allow
  // inline say so, because that constraint is real and finding out afterwards
  // would be a surprise.
  //
  // Every finding can be dropped. The header is the promise: seven findings,
  // five inline, two in the summary. Nothing else will appear.
  let { sessionId, machineId }: { sessionId: string; machineId: string } = $props()

  let draft = $state<ReviewDraft | null>(null)
  let dropped = $state<Set<number>>(new Set())
  let open = $state<Set<number>>(new Set())
  let posting = $state(false)
  let err = $state('')

  const base = $derived(app.baseForMachine(machineId))

  async function load() {
    try {
      draft = await reviewDraft(base, sessionId)
    } catch {
      // Not a review session, or no record: the card simply does not appear.
      draft = null
    }
  }

  $effect(() => {
    void sessionId
    void load()
  })

  // Re-read when the session stops working, which is when the findings arrive.
  // Cheap, and it is what turns "still reviewing" into the draft without a
  // refresh.
  $effect(() => {
    const state = app.chat?.sessionState
    if (state && state !== 'running' && state !== 'starting') void load()
  })

  const findings = $derived(draft?.findings ?? [])
  const kept = $derived(findings.filter((f) => !dropped.has(f.index)))
  const keptInline = $derived(kept.filter((f) => f.inline).length)
  const keptSummary = $derived(kept.length - keptInline)

  function toggleDrop(i: number) {
    const next = new Set(dropped)
    if (next.has(i)) next.delete(i)
    else next.add(i)
    dropped = next
  }
  function toggleOpen(i: number) {
    const next = new Set(open)
    if (next.has(i)) next.delete(i)
    else next.add(i)
    open = next
  }

  function location(f: ReviewFinding): string {
    if (!f.file) return ''
    if (!f.line) return f.file
    return f.end_line ? `${f.file}:${f.line}-${f.end_line}` : `${f.file}:${f.line}`
  }

  async function post() {
    if (posting || !kept.length) return
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
</script>

{#if draft}
  <section class="draft">
    <header>
      <div class="who">
        <span class="lbl">Review draft</span>
        <span class="pr mono">{draft.owner}/{draft.repo}#{draft.number}</span>
      </div>
      {#if draft.posted_url}
        <a class="posted" href={draft.posted_url} target="_blank" rel="noreferrer">Posted →</a>
      {:else if findings.length || draft.summary}
        <button class="post" onclick={post} disabled={posting}>
          {posting ? 'Posting…' : 'Post review'}
        </button>
      {/if}
    </header>

    {#if draft.parse_error}
      <!-- The review ran but produced nothing usable. Said plainly, because an
           empty card is indistinguishable from "found nothing". -->
      <p class="note">This review did not produce findings kunai could read. Ask it to try again in the composer below.</p>
    {:else if !draft.findings}
      <p class="note">Reviewing…</p>
    {:else}
      <!-- The promise: what will land, and where. -->
      <p class="counts mono">
        {kept.length} finding{kept.length === 1 ? '' : 's'} · {keptInline} inline · {keptSummary} in
        the summary{#if draft.requester} · requested by {draft.requester}{/if}
      </p>

      {#if draft.summary}
        <p class="summary">{draft.summary}</p>
      {/if}

      {#each findings as f (f.index)}
        <div class="f" class:dropped={dropped.has(f.index)}>
          <div class="fhead">
            <button class="keep" onclick={() => toggleDrop(f.index)} aria-label={dropped.has(f.index) ? 'Include this finding' : 'Drop this finding'}>
              {#if dropped.has(f.index)}
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M5 12h14" /></svg>
              {:else}
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5" /></svg>
              {/if}
            </button>
            <button class="ftitle" onclick={() => toggleOpen(f.index)}>
              <span class="loc mono">{location(f)}</span>
              <span class="claim">{f.title}</span>
            </button>
            <!-- The badge is the point of this card: where this finding lands.
                 A finding GitHub will not take inline says so here rather than
                 after you have posted. -->
            <span class="where" class:sum={!f.inline} title={f.why ?? 'Posted as a comment on this line'}>
              {f.inline ? 'inline' : 'summary'}
            </span>
          </div>
          {#if open.has(f.index)}
            <div class="fbody">
              {#if f.body}<p class="btext">{f.body}</p>{/if}
              {#if f.why}<p class="why">{f.why}</p>{/if}
              {#if f.suggestion}
                <p class="slbl mono">suggested change</p>
                <pre class="sug mono">{f.suggestion}</pre>
              {/if}
            </div>
          {/if}
        </div>
      {/each}

      {#if !findings.length}
        <p class="note">Nothing worth reporting. Posting sends that as the review.</p>
      {/if}
    {/if}

    {#if err}<p class="err">{err}</p>{/if}
  </section>
{/if}

<style>
  /* Context rather than conversation, so a card, following the same rule as the
     project card and the changed-files card. */
  .draft {
    margin: 14px 0 6px;
    border: 1px solid var(--border);
    border-radius: var(--r);
    background: var(--panel);
    overflow: hidden;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    padding: 10px 12px;
    border-bottom: 1px solid var(--border);
  }
  .who {
    display: flex;
    align-items: baseline;
    gap: 9px;
    min-width: 0;
  }
  .lbl {
    font-size: 12px;
    font-weight: 550;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .pr {
    font-size: 11.5px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  /* The one filled control in the card, because it is the one irreversible
     action in it. */
  .post {
    flex: none;
    padding: 5px 13px;
    border-radius: var(--r-sm);
    background: var(--white);
    color: #0b0b0c;
    font-size: 12.5px;
    font-weight: 550;
  }
  .post:disabled {
    opacity: 0.55;
  }
  .posted {
    flex: none;
    font-size: 12.5px;
    color: var(--live);
    text-decoration: none;
  }
  .counts {
    margin: 0;
    padding: 9px 12px 0;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .summary {
    margin: 6px 0 0;
    padding: 0 12px 4px;
    font-size: 13px;
    line-height: 1.55;
    color: var(--text-2);
  }
  .note {
    margin: 0;
    padding: 12px;
    font-size: 12.5px;
    color: var(--text-3);
  }
  .err {
    margin: 0;
    padding: 0 12px 12px;
    font-size: 12px;
    color: var(--alert);
  }

  .f {
    border-top: 1px solid var(--border);
  }
  .f:first-of-type {
    margin-top: 9px;
  }
  /* A dropped finding recedes rather than disappearing, so undoing is one click
     and the count above stays honest about what changed. */
  .f.dropped {
    opacity: 0.4;
  }
  .fhead {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 8px 12px;
    min-width: 0;
  }
  .keep {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    border-radius: 5px;
    background: var(--panel-2);
    color: var(--text-3);
  }
  .keep:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .ftitle {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: baseline;
    gap: 9px;
    text-align: left;
  }
  .loc {
    flex: none;
    max-width: 45%;
    font-size: 11px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    /* A path reads right to left when it is clipped, and the leading slash has
       to stay put. Same treatment as every other path in the app. */
    unicode-bidi: plaintext;
  }
  .claim {
    flex: 1;
    min-width: 0;
    font-size: 13px;
    color: var(--text-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .f:hover .claim {
    color: var(--text);
  }
  /* Quiet, but never absent: this is the promise about where the finding goes. */
  .where {
    flex: none;
    font-size: 10.5px;
    letter-spacing: 0.03em;
    color: var(--text-4);
  }
  .where.sum {
    color: var(--busy);
  }
  .fbody {
    padding: 0 12px 11px 41px;
  }
  .btext {
    margin: 0;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--text-2);
    white-space: pre-wrap;
  }
  .why {
    margin: 7px 0 0;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .slbl {
    margin: 10px 0 4px;
    font-size: 10.5px;
    color: var(--text-4);
  }
  .sug {
    margin: 0;
    padding: 9px 11px;
    border-radius: var(--r-sm);
    background: var(--bg);
    border: 1px solid var(--border);
    font-size: 12px;
    line-height: 1.5;
    color: var(--text-2);
    overflow-x: auto;
    white-space: pre;
  }
</style>
