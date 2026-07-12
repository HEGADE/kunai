<script lang="ts">
  import type { Turn } from '../lib/turns'
  import type { ChatConnection } from '../lib/chat.svelte'
  import ToolIcon from './tools/ToolIcon.svelte'
  import BlockView from './BlockView.svelte'

  let { turn, chat }: { turn: Turn; chat: ChatConnection } = $props()
  let open = $state(false)

  const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`
  const summary = $derived(
    [plural(turn.toolCalls, 'tool call'), turn.messages ? plural(turn.messages, 'message') : '']
      .filter(Boolean)
      .join(', '),
  )
</script>

<div class="group" class:open>
  <button class="head" onclick={() => (open = !open)}>
    <span class="car" aria-hidden="true">
      <svg width="9" height="9" viewBox="0 0 8 8" fill="currentColor"><path d="M2 0l4 4-4 4z" /></svg>
    </span>
    <span class="label">{summary}</span>
    <span class="icons" aria-hidden="true">
      {#each turn.toolNames as n (n)}
        <span class="ti"><ToolIcon name={n} size={12} /></span>
      {/each}
    </span>
  </button>
  {#if open}
    <div class="body">
      {#each turn.activity as b, j (j)}
        <BlockView block={b} {chat} />
      {/each}
    </div>
  {/if}
</div>

<style>
  .group {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    width: fit-content;
    max-width: 100%;
    padding: 3px 4px;
    margin: -3px -4px;
    text-align: left;
    color: var(--text-3);
    font-size: 13px;
  }
  .head:hover {
    color: var(--text-2);
  }
  .car {
    flex: none;
    display: inline-flex;
    color: var(--text-4);
    transition: transform 0.15s;
  }
  .open .car {
    transform: rotate(90deg);
    color: var(--text-3);
  }
  .label {
    flex: none;
    font-weight: 500;
  }
  .icons {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    color: var(--text-4);
    padding-left: 2px;
  }
  .ti {
    display: inline-flex;
  }
  .body {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
</style>
