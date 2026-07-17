<script lang="ts">
  import type { ChatConnection } from '../lib/chat.svelte'
  import { usd } from '../lib/loop'

  // Starting a self-prompting run. Everything here is arranged around one fact:
  // the person filling this in is about to walk away, and the loop will spend
  // their money while they are gone. So the limits are not settings tucked at the
  // bottom. They are the middle of the form, under the sentence that says what
  // they actually do, and there is no way to switch them off.
  let { chat, onClose }: { chat: ChatConnection; onClose: () => void } = $props()

  let task = $state('')
  let iters = $state(10)
  let budget = $state(2)
  let promise = $state('DONE')

  const ready = $derived(task.trim().length > 0)

  function start() {
    if (!ready) return
    chat.startLoop({
      prompt: task.trim(),
      promise: promise.trim(),
      max_iters: iters,
      max_usd: budget,
    })
    onClose()
  }
</script>

<div class="sheet">
  <div class="head">
    <span class="title">Run in a loop</span>
    <button class="x" onclick={onClose} aria-label="Close">✕</button>
  </div>
  <p class="lede">
    Claude reads this task again every time a turn ends, and keeps working without you. It carries
    on if you close the tab, and accepts its own file edits while it runs so it never stalls waiting
    for a click.
  </p>

  <textarea
    bind:value={task}
    rows="4"
    placeholder="Keep fixing the failing tests until the whole suite passes."
  ></textarea>

  <!-- The structural claim of this form: two limits, equal billing, and the loop
       ends at whichever arrives first. That is the actual rule, so it is the
       heading rather than a footnote. The two numbers live side by side split by
       a rule, not each in its own box — one enclosure, so the eye lands here. -->
  <div class="limits">
    <span class="eyebrow">Stops at whichever comes first</span>
    <div class="pair">
      <label class="lim">
        <input type="number" class="mono num" bind:value={iters} min="1" max="200" />
        <span class="unit">iterations</span>
      </label>
      <span class="split" aria-hidden="true"></span>
      <label class="lim">
        <span class="pre mono">$</span>
        <input type="number" class="mono num" bind:value={budget} min="0.25" max="50" step="0.25" />
        <span class="unit">of spend</span>
      </label>
    </div>
    <p class="fine">Both are hard. The loop ends itself at the first one it reaches: {iters} turns or {usd(budget)}.</p>
  </div>

  <label class="promise">
    <span class="plabel">Or the moment Claude says</span>
    <input type="text" class="mono" bind:value={promise} placeholder="DONE" spellcheck="false" />
  </label>

  <button class="go" disabled={!ready} onclick={start}>Start loop</button>
</div>

<style>
  .sheet {
    display: flex;
    flex-direction: column;
    gap: 11px;
    padding: 15px 17px 14px;
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .title {
    font-size: 14px;
    font-weight: 600;
    color: var(--text);
  }
  .x {
    width: 26px;
    height: 26px;
    border-radius: 50%;
    color: var(--text-4);
    font-size: 12px;
  }
  .x:hover {
    color: var(--text);
    background: var(--panel-3);
  }
  .lede {
    margin: -5px 0 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--text-4);
  }
  textarea {
    width: 100%;
    padding: 10px 11px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    color: var(--text);
    font: inherit;
    font-size: 13px;
    line-height: 1.5;
    resize: vertical;
  }
  textarea:focus-visible {
    outline: none;
    border-color: var(--border-2);
  }
  textarea::placeholder {
    color: var(--text-4);
  }

  /* The limits get the only enclosure in the form. Nothing else is boxed, so the
     eye lands here, which is the one place it should land before you walk away.
     The two numbers sit inside that one frame, split by a hairline rather than
     each in its own box — the ceiling reads as one thing, which it is. */
  .limits {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 11px 4px 12px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r-sm);
  }
  .eyebrow {
    padding: 0 10px;
    font-size: 9.5px;
    letter-spacing: 0.11em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .pair {
    display: flex;
    align-items: stretch;
  }
  .lim {
    flex: 1;
    display: flex;
    align-items: baseline;
    gap: 7px;
    padding: 2px 16px;
    cursor: text;
    min-width: 0;
  }
  .pre {
    flex: none;
    font-size: 18px;
    color: var(--text-3);
  }
  .lim:focus-within .pre {
    color: var(--text-2);
  }
  .num {
    min-width: 1ch;
    field-sizing: content;
    max-width: 100%;
    background: none;
    border: none;
    color: var(--text);
    font-size: 22px;
    letter-spacing: -0.01em;
    padding: 0;
  }
  .num:focus-visible {
    outline: none;
  }
  /* The spinners add chrome and invite fiddling; the number is the point. */
  .num::-webkit-outer-spin-button,
  .num::-webkit-inner-spin-button {
    appearance: none;
    margin: 0;
  }
  .num[type='number'] {
    -moz-appearance: textfield;
    appearance: textfield;
  }
  .unit {
    flex: none;
    font-size: 11px;
    color: var(--text-4);
  }
  .split {
    flex: none;
    width: 1px;
    align-self: center;
    height: 26px;
    background: var(--border);
  }
  .fine {
    margin: 0;
    padding: 0 10px;
    font-size: 11.5px;
    line-height: 1.45;
    color: var(--text-3);
  }

  .promise {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .plabel {
    flex: none;
    font-size: 12.5px;
    color: var(--text-3);
  }
  .promise input {
    flex: 1;
    min-width: 0;
    padding: 7px 10px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text);
    font-size: 12.5px;
  }
  .promise input:focus-visible {
    outline: none;
    border-color: var(--border-2);
  }

  .go {
    height: 40px;
    border-radius: var(--r);
    background: var(--white);
    color: #0b0b0c;
    font-weight: 600;
    font-size: 13.5px;
  }
  .go:disabled {
    background: var(--panel-3);
    color: var(--text-4);
  }
</style>
