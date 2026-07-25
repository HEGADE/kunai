<script lang="ts">
  import type { Block } from '../lib/types'
  import type { ChatConnection } from '../lib/chat.svelte'
  import Markdown from './Markdown.svelte'
  import ToolCard from './ToolCard.svelte'

  let { block, chat }: { block: Block; chat: ChatConnection } = $props()
</script>

{#if block.type === 'text' && block.text}
  <Markdown text={block.text} />
{:else if block.type === 'tool_use'}
  <!-- An Agent call also carries what its subagent did (keyed by this call's id),
       so that work threads under the card that spawned it. -->
  <ToolCard
    name={block.name ?? 'tool'}
    input={block.input}
    result={block.id ? chat.toolResults[block.id] : undefined}
    nested={block.id ? (chat.agentBlocks[block.id] ?? []) : []}
    nestedStreaming={block.id ? (chat.agentStreaming[block.id] ?? '') : ''}
    nestedResults={chat.toolResults}
  />
{:else if block.type === 'thinking' && block.text}
  <div class="thinking mono">{block.text}</div>
{/if}

<style>
  .thinking {
    font-size: 13.5px;
    color: var(--text-4);
    padding-left: 12px;
    border-left: 1px solid var(--border-2);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
</style>
