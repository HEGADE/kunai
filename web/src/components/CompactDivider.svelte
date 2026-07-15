<script lang="ts">
  import { formatTokens } from '../lib/context'

  // Where the conversation was summarised. This is a boundary, not a message:
  // everything above it is no longer in the model's context, and the summary
  // that replaced it is deliberately not shown — it is context, not something
  // anyone wrote, and it runs to tens of thousands of characters.
  //
  // What earns its place is the number: it says why the context meter just
  // dropped, which is otherwise the only visible effect and reads as a glitch.
  let {
    preTokens,
    postTokens,
    trigger,
  }: { preTokens: number; postTokens: number; trigger: string } = $props()

  const shrank = $derived(preTokens > 0 && postTokens > 0 && postTokens < preTokens)
</script>

<div class="rule" role="separator">
  <span class="label mono">
    {trigger === 'auto' ? 'Auto-compacted' : 'Compacted'}
    {#if shrank}
      <span class="nums">{formatTokens(preTokens)} <span class="arr">→</span> {formatTokens(postTokens)}</span>
    {/if}
  </span>
</div>

<style>
  /* A hairline the conversation passes through, so the eye reads it as a seam
     rather than a message. */
  .rule {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 2px 0;
  }
  .rule::before,
  .rule::after {
    content: '';
    flex: 1;
    height: 1px;
    background: var(--border);
  }
  .label {
    display: inline-flex;
    align-items: baseline;
    gap: 7px;
    font-size: 10.5px;
    letter-spacing: 0.04em;
    color: var(--text-4);
    white-space: nowrap;
  }
  .nums {
    color: var(--text-3);
  }
  .arr {
    color: var(--text-4);
  }
</style>
