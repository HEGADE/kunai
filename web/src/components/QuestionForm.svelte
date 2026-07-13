<script lang="ts">
  // Renders the AskUserQuestion tool: 1-4 questions, each with 2-4 options
  // (single- or multi-select) plus a freeform "Other". The answer contract is
  // { [question text]: chosen answer } — multi-select comma-joined — which the
  // server merges into the tool's updatedInput on allow.
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

  const questions = $derived<Question[]>(
    (input as { questions?: Question[] } | null)?.questions ?? [],
  )

  // Selected option labels and freeform text, per question index.
  let selected = $state<string[][]>([])
  let other = $state<string[]>([])
  $effect(() => {
    // Re-init when a new ask arrives (different question set).
    selected = questions.map(() => [])
    other = questions.map(() => '')
  })

  function toggle(qi: number, label: string, multi: boolean) {
    const cur = selected[qi] ?? []
    const next = multi
      ? cur.includes(label)
        ? cur.filter((l) => l !== label)
        : [...cur, label]
      : [label]
    selected = selected.map((s, i) => (i === qi ? next : s))
  }
  function setOther(qi: number, v: string) {
    other = other.map((o, i) => (i === qi ? v : o))
  }

  function answerFor(qi: number): string {
    const parts = [...(selected[qi] ?? [])]
    const o = (other[qi] ?? '').trim()
    if (o) parts.push(o)
    return parts.join(', ')
  }
  const ready = $derived(questions.length > 0 && questions.every((_, i) => answerFor(i) !== ''))

  function submit() {
    if (!ready) return
    const answers: Record<string, string> = {}
    for (let i = 0; i < questions.length; i++) answers[questions[i].question] = answerFor(i)
    onSubmit(answers)
  }
</script>

<div class="qf">
  {#each questions as q, qi (qi)}
    <div class="q">
      <div class="qhead">
        {#if q.header}<span class="chip">{q.header}</span>{/if}
        {#if q.multiSelect}<span class="multi">choose any</span>{/if}
      </div>
      <p class="qtext">{q.question}</p>
      <div class="opts">
        {#each q.options ?? [] as opt (opt.label)}
          {@const on = (selected[qi] ?? []).includes(opt.label)}
          <button class="opt" class:on onclick={() => toggle(qi, opt.label, !!q.multiSelect)}>
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
          value={other[qi] ?? ''}
          oninput={(e) => setOther(qi, (e.target as HTMLInputElement).value)}
        />
      </div>
    </div>
  {/each}

  <div class="actions">
    <button class="skip" onclick={onSkip}>Skip</button>
    <button class="send" disabled={!ready} onclick={submit}>Send answer{questions.length > 1 ? 's' : ''}</button>
  </div>
</div>

<style>
  .qf {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  .q {
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
    margin: 0;
    font-size: 15px;
    line-height: 1.45;
    color: var(--text);
  }
  .opts {
    display: flex;
    flex-direction: column;
    gap: 7px;
  }
  .opt {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    text-align: left;
    padding: 11px 13px;
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
    padding: 10px 13px;
    font-size: 13px;
    color: var(--text);
    outline: none;
  }
  .other:focus {
    border-style: solid;
    border-color: var(--border-2);
  }
  .actions {
    display: flex;
    gap: 9px;
    align-items: center;
  }
  .skip {
    padding: 12px 16px;
    border-radius: var(--r);
    background: var(--panel-2);
    border: 1px solid var(--border);
    color: var(--text-3);
    font-size: 13.5px;
  }
  .skip:hover {
    color: var(--text);
  }
  .send {
    flex: 1;
    padding: 12px;
    border-radius: var(--r);
    background: var(--white);
    color: #0b0b0c;
    font-weight: 600;
    font-size: 14px;
  }
  .send:disabled {
    opacity: 0.45;
  }
</style>
