<script lang="ts">
  // AskUserQuestion, rendered as a floating card (the same lane as the add-
  // project / loop / schedule sheets) so it reads as a distinct surface, not a
  // full-width band. It's a one-question-at-a-time wizard so it stays small and
  // never overflows: each step shows a single question (its options scroll
  // within a bounded panel if long), and Back / Skip / Continue move through
  // them. The last step submits. The answer contract is
  // { [question text]: chosen answer } (multi-select comma-joined). Dismissing
  // (the ✕, Escape, or an all-skip) declines the whole ask; fully skipped
  // questions are simply omitted.
  type Option = { label: string; description?: string }
  type Question = { question: string; header?: string; options?: Option[]; multiSelect?: boolean }

  let {
    input,
    onSubmit,
    onSkip,
  }: {
    input: unknown
    onSubmit: (answers: Record<string, string>) => void
    onSkip: () => void
  } = $props()

  const questions = $derived<Question[]>((input as { questions?: Question[] } | null)?.questions ?? [])
  const multiStep = $derived(questions.length > 1)

  let step = $state(0)
  let selected = $state<string[][]>([])
  let other = $state<string[]>([])
  $effect(() => {
    // Reset when a new ask arrives.
    selected = questions.map(() => [])
    other = questions.map(() => '')
    step = 0
  })

  // Esc dismisses the whole ask, the same as the ✕ — the desktop reflex for a
  // dialog you don't want to answer.
  $effect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onSkip()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  const q = $derived<Question | undefined>(questions[step])
  const last = $derived(step >= questions.length - 1)

  function toggle(label: string, multi: boolean) {
    const cur = selected[step] ?? []
    const next = multi ? (cur.includes(label) ? cur.filter((l) => l !== label) : [...cur, label]) : [label]
    selected = selected.map((s, i) => (i === step ? next : s))
  }
  function setOther(v: string) {
    other = other.map((o, i) => (i === step ? v : o))
  }

  function answerFor(i: number): string {
    const parts = [...(selected[i] ?? [])]
    const o = (other[i] ?? '').trim()
    if (o) parts.push(o)
    return parts.join(', ')
  }
  const answered = $derived(answerFor(step) !== '')

  function advance() {
    if (!last) step += 1
    else finish()
  }
  function skip() {
    // Clear this question's answer, then move on.
    selected = selected.map((s, i) => (i === step ? [] : s))
    other = other.map((o, i) => (i === step ? '' : o))
    advance()
  }
  function finish() {
    const answers: Record<string, string> = {}
    for (let i = 0; i < questions.length; i++) if (answerFor(i)) answers[questions[i].question] = answerFor(i)
    if (Object.keys(answers).length === 0) onSkip()
    else onSubmit(answers)
  }
</script>

{#if q}
  <div class="qwrap" role="dialog" aria-label="Claude is asking">
    <div class="head">
      <span class="k">Claude is asking</span>
      <div class="right">
        {#if multiStep}<span class="count mono">{step + 1} / {questions.length}</span>{/if}
        <button class="x" onclick={onSkip} aria-label="Dismiss" title="Dismiss without answering">✕</button>
      </div>
    </div>

    <div class="body">
      <div class="qhead">
        {#if q.header}<span class="chip">{q.header}</span>{/if}
        {#if q.multiSelect}<span class="multi">Choose any</span>{/if}
      </div>
      <p class="qtext">{q.question}</p>
      <div class="opts">
        {#each q.options ?? [] as opt (opt.label)}
          {@const on = (selected[step] ?? []).includes(opt.label)}
          <button class="opt" class:on onclick={() => toggle(opt.label, !!q.multiSelect)}>
            <span class="mark" class:box={q.multiSelect}></span>
            <span class="otext">
              <span class="olabel">{opt.label}</span>
              {#if opt.description}<span class="odesc">{opt.description}</span>{/if}
            </span>
          </button>
        {/each}
        <input
          class="other"
          placeholder="Type another answer…"
          value={other[step] ?? ''}
          oninput={(e) => setOther((e.target as HTMLInputElement).value)}
        />
      </div>
    </div>

    <div class="foot">
      {#if step > 0}
        <button class="ghost" onclick={() => (step -= 1)}>Back</button>
      {/if}
      {#if multiStep}
        <button class="ghost" onclick={skip}>Skip question</button>
      {/if}
      <button class="cont" disabled={!answered} onclick={advance}>
        {last ? 'Submit' : 'Continue'}
      </button>
    </div>
  </div>
{/if}

<style>
  /* The card itself — same floating frame as the add-project / loop / schedule
     sheets (border, radius, shadow, rise), so an ask reads as one surface. */
  .qwrap {
    max-width: 720px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    max-height: min(56vh, 480px);
    background: var(--panel);
    border: 1px solid var(--border-2);
    border-radius: var(--r-lg);
    box-shadow: 0 14px 44px -16px rgba(0, 0, 0, 0.72);
    animation: floatUp 0.16s ease-out both;
    padding: 13px 17px 14px;
  }
  @keyframes floatUp {
    from {
      opacity: 0;
      transform: translateY(8px);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .qwrap {
      animation: none;
    }
  }

  .head {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 11px;
  }
  .k {
    font-size: 12px;
    font-weight: 550;
    color: var(--text-3);
  }
  .right {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .count {
    font-size: 11px;
    color: var(--text-4);
  }
  /* The dismiss the ask was missing: ✕ (and Esc) decline the whole thing. */
  .x {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    margin: -4px -6px -4px 0;
    border-radius: 50%;
    color: var(--text-3);
    font-size: 12px;
  }
  .x:hover {
    color: var(--text);
    background: var(--panel-3);
  }

  .body {
    flex: 1 1 auto;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    display: flex;
    flex-direction: column;
    gap: 9px;
  }
  .qhead {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .chip {
    padding: 2px 8px;
    border-radius: 5px;
    background: var(--panel-3);
    color: var(--text-3);
    font-size: 10px;
    font-weight: 550;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .multi {
    font-size: 10.5px;
    color: var(--text-4);
  }
  .qtext {
    margin: 0 0 2px;
    font-size: 15px;
    line-height: 1.45;
    color: var(--text);
  }
  .opts {
    display: flex;
    flex-direction: column;
    gap: 7px;
    padding-bottom: 2px;
  }
  .opt {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    text-align: left;
    padding: 10px 12px;
    border-radius: var(--r);
    background: var(--panel-2);
    border: 1px solid var(--border);
  }
  .opt:hover {
    border-color: var(--border-2);
  }
  .opt.on {
    border-color: var(--white);
    background: var(--panel-3);
  }
  .mark {
    flex: none;
    width: 15px;
    height: 15px;
    margin-top: 2px;
    border-radius: 50%;
    border: 1.5px solid var(--border-2);
  }
  .mark.box {
    border-radius: 4px;
  }
  .opt.on .mark {
    border-color: var(--white);
    background: var(--white);
    box-shadow: inset 0 0 0 3px #0b0b0c;
  }
  .opt.on .mark.box {
    box-shadow: inset 0 0 0 2.5px #0b0b0c;
  }
  .otext {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .olabel {
    font-size: 13.5px;
    font-weight: 550;
    color: var(--text);
  }
  .odesc {
    font-size: 12px;
    line-height: 1.45;
    color: var(--text-3);
  }
  .other {
    background: none;
    border: 1px dashed var(--border);
    border-radius: var(--r);
    padding: 9px 12px;
    font-size: 13px;
    color: var(--text);
    outline: none;
  }
  .other:focus {
    border-style: solid;
    border-color: var(--border-2);
  }
  .foot {
    flex: none;
    display: flex;
    gap: 9px;
    align-items: center;
    padding-top: 12px;
  }
  .ghost {
    padding: 11px 15px;
    border-radius: var(--r);
    background: var(--panel-2);
    border: 1px solid var(--border);
    color: var(--text-3);
    font-size: 13.5px;
  }
  .ghost:hover {
    color: var(--text);
  }
  .cont {
    flex: 1;
    padding: 12px;
    border-radius: var(--r);
    background: var(--white);
    color: #0b0b0c;
    font-weight: 600;
    font-size: 14px;
  }
  .cont:disabled {
    opacity: 0.4;
  }
</style>
