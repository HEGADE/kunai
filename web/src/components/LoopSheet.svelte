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
       heading rather than a footnote. -->
  <div class="limits">
    <span class="eyebrow">Stops at whichever comes first</span>
    <div class="pair">
      <label class="lim">
        <input type="number" class="mono" bind:value={iters} min="1" max="200" />
        <span class="unit">iterations</span>
      </label>
      <label class="lim">
        <input type="number" class="mono" bind:value={budget} min="0.25" max="50" step="0.25" />
        <span class="unit">dollars</span>
      </label>
    </div>
    <p class="fine">
      Hard limits. At most {iters} turns or {usd(budget)}, whichever it hits first, then it stops on
      its own.
    </p>
  </div>

  <label class="promise">
    <span class="plabel">Or as soon as Claude says</span>
    <input type="text" class="mono" bind:value={promise} placeholder="DONE" spellcheck="false" />
  </label>

  <button class="go" disabled={!ready} onclick={start}>Start loop</button>
</div>

<style>
  .sheet {
    display: flex;
    flex-direction: column;
    gap: 11px;
    max-width: 720px;
    width: 100%;
    margin: 0 auto;
    padding: 14px 16px 12px;
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
     eye lands here, which is the one place it should land before you walk away. */
  .limits {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 11px 12px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r-sm);
  }
  .eyebrow {
    font-size: 9.5px;
    letter-spacing: 0.11em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .pair {
    display: flex;
    gap: 8px;
  }
  .lim {
    flex: 1;
    display: flex;
    align-items: baseline;
    gap: 7px;
    padding: 8px 10px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 6px;
    cursor: text;
  }
  .lim:focus-within {
    border-color: var(--border-2);
  }
  .lim input {
    width: 100%;
    min-width: 0;
    background: none;
    border: none;
    color: var(--text);
    font-size: 15px;
    padding: 0;
  }
  .lim input:focus-visible {
    outline: none;
  }
  /* The spinners add chrome and invite fiddling; the number is the point. */
  .lim input::-webkit-outer-spin-button,
  .lim input::-webkit-inner-spin-button {
    appearance: none;
    margin: 0;
  }
  .lim input[type='number'] {
    -moz-appearance: textfield;
    appearance: textfield;
  }
  .unit {
    flex: none;
    font-size: 11px;
    color: var(--text-4);
  }
  .fine {
    margin: 0;
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
