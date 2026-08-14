<script lang="ts">
  import type { Severity } from '../../lib/api'
  import type { FindingEdit } from '../../lib/review'
  import { SEVERITIES, severityLabel, severityHint } from '../../lib/severity'

  // Rewriting a finding before it is posted.
  //
  // Pulled out of the card because it is a different mode with different rules:
  // a form you are filling in, not a claim you are reading. Inlining it meant
  // one component held both, and the card's own layout had to keep stepping
  // around a state it is not in most of the time.
  //
  // Only the words and the judgement may change. The anchor stays server-side:
  // it decides which line of somebody's pull request the comment lands on.
  let {
    initial,
    original,
    onsave,
    oncancel,
    onrevert,
  }: {
    initial: FindingEdit
    // The reviewer's own words, so an edit that restores them is recorded as no
    // edit at all rather than as a rewrite of identical text.
    original: FindingEdit
    onsave: (next: FindingEdit) => void
    oncancel: () => void
    onrevert?: () => void
  } = $props()

  let title = $state(initial.title)
  let body = $state(initial.body)
  let severity = $state<Severity>(initial.severity)

  const unchanged = $derived(
    title === original.title && body === original.body && severity === original.severity,
  )

  function save() {
    if (unchanged) {
      oncancel()
      onrevert?.()
      return
    }
    onsave({ title, body, severity })
  }
</script>

<div class="editor">
  <input class="t" bind:value={title} placeholder="What is wrong, in one line" />
  <textarea class="b" bind:value={body} rows="5" placeholder="Why it is wrong, and what breaks"></textarea>

  <div class="sev">
    {#each SEVERITIES as s (s)}
      <button class="pick sev-{s}" class:on={severity === s} title={severityHint(s)} onclick={() => (severity = s)}>
        {severityLabel(s)}
      </button>
    {/each}
  </div>

  <div class="acts">
    <button class="save" onclick={save}>Save</button>
    <button class="quiet" onclick={oncancel}>Cancel</button>
    {#if onrevert}
      <button class="quiet" onclick={onrevert}>Restore the original</button>
    {/if}
  </div>
</div>

<style>
  .editor {
    display: flex;
    flex-direction: column;
    gap: 9px;
    margin-top: 10px;
  }
  .t,
  .b {
    width: 100%;
    padding: 9px 11px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    color: var(--text);
    font-family: inherit;
    font-size: 13px;
    line-height: 1.6;
    outline: none;
    resize: vertical;
  }
  .t {
    font-size: 14.5px;
    font-weight: 550;
  }
  .t:focus,
  .b:focus {
    border-color: var(--border-2);
  }
  .sev {
    display: flex;
    gap: 6px;
  }
  .pick {
    padding: 5px 12px;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--text-4);
    font-size: 11.5px;
    font-weight: 550;
  }
  .pick:hover {
    color: var(--text-2);
  }
  .pick.on {
    color: var(--sev-pick);
    border-color: var(--sev-pick);
  }
  .pick.sev-blocker {
    --sev-pick: var(--alert);
  }
  .pick.sev-major {
    --sev-pick: var(--busy);
  }
  .pick.sev-minor {
    --sev-pick: var(--text-2);
  }
  .acts {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .save {
    padding: 6px 15px;
    border-radius: var(--r-sm);
    background: var(--white);
    color: #0b0b0c;
    font-size: 12.5px;
    font-weight: 550;
  }
  .quiet {
    font-size: 11.5px;
    color: var(--text-4);
  }
  .quiet:hover {
    color: var(--text-2);
  }
</style>
