<script lang="ts">
  // AskUserQuestion, rendered as a compact one-question-at-a-time wizard so it
  // stays small and never overflows: each step shows a single question (its
  // options scroll within a bounded panel if long), and Back / Skip / Continue
  // move through them. The last step submits. The answer contract is
  // { [question text]: chosen answer } (multi-select comma-joined); an all-skip
  // declines. Fully skipped questions are simply omitted.
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

  let step = $state(0)
  let selected = $state<string[][]>([])
  let other = $state<string[]>([])
  $effect(() => {
    // Reset when a new ask arrives.
    selected = questions.map(() => [])
    other = questions.map(() => '')
    step = 0
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
  <div class="qwrap">
    <div class="qtop">
      <span class="k">Claude is asking</span>
      <div class="dots">
        {#each questions as _, i (i)}
          <span class="dot" class:on={i === step} class:done={i < step}></span>
        {/each}
      </div>
    </div>

    <div class="qbody">
      <div class="qhead">
        {#if q.header}<span class="chip">{q.header}</span>{/if}
        {#if q.multiSelect}<span class="multi">choose any</span>{/if}
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
          placeholder="Something else…"
          value={other[step] ?? ''}
          oninput={(e) => setOther((e.target as HTMLInputElement).value)}
        />
      </div>
    </div>

    <div class="qfoot">
      {#if step > 0}
        <button class="ghost" onclick={() => (step -= 1)} aria-label="Back">Back</button>
      {/if}
      <button class="ghost" onclick={skip}>Skip</button>
      <button class="cont" disabled={!answered} onclick={advance}>
        {last ? 'Submit' : 'Continue'}
      </button>
    </div>
  </div>
{/if}

<style>
  .qwrap {
    max-width: 720px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    max-height: min(52vh, 460px);
    padding: 13px 20px calc(var(--safe-bottom) + 12px);
  }
  .qtop {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 10px;
  }
  .k {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-3);
  }
  .dots {
    display: flex;
    gap: 5px;
  }
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--panel-3);
  }
  .dot.done {
    background: var(--text-4);
  }
  .dot.on {
    background: var(--white);
  }
  .qbody {
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
  .qfoot {
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
