<script lang="ts">
  import type { ChatConnection } from '../lib/chat.svelte'

  // Work stacked up behind the running turn. Numbered because the order is real:
  // these run one after another, in this order, as each turn finishes.
  let { chat }: { chat: ChatConnection } = $props()
</script>

{#if chat.queued.length}
  <div class="queue">
    {#each chat.queued as q, i (q.queue_id)}
      <div class="row">
        <span class="n mono">{i + 1}</span>
        <span class="txt">{q.text || (q.attachments?.length ? 'Attached files' : '')}</span>
        {#if q.attachments?.length}
          <span class="clip" title="{q.attachments.length} file(s)">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21.44 11.05l-9.19 9.19a5 5 0 01-7.07-7.07l9.19-9.19a3 3 0 014.24 4.24l-9.2 9.19a1 1 0 01-1.41-1.41l8.49-8.49" /></svg>
          </span>
        {/if}
        <button class="x" onclick={() => chat.cancelQueued(q.queue_id)} aria-label="Cancel queued prompt" title="Cancel">
          <svg width="9" height="9" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M1 1l8 8M9 1l-8 8" /></svg>
        </button>
      </div>
    {/each}
  </div>
{/if}

<style>
  .queue {
    max-width: 720px;
    margin: 0 auto 6px;
    width: 100%;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 7px 10px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    font-size: 12.5px;
    color: var(--text-3);
  }
  .n {
    flex: none;
    width: 12px;
    text-align: center;
    font-size: 10.5px;
    color: var(--text-4);
  }
  .txt {
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .clip {
    flex: none;
    display: flex;
    color: var(--text-4);
  }
  .x {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: 4px;
    color: var(--text-4);
  }
  .x:hover {
    color: var(--text);
    background: var(--panel-3);
  }
</style>
