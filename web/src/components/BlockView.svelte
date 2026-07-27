<script lang="ts">
  import type { Block, ToolResult } from '../lib/types'
  import Markdown from './Markdown.svelte'
  import ToolCard from './ToolCard.svelte'

  // Everything this needs from a connection, and nothing else. Taking the whole
  // ChatConnection meant the guest page could not render a turn without one, and
  // a guest has no accounts, no fleet and no permission to answer anything. Both
  // connections satisfy this, so a shared conversation renders through exactly
  // the same components as a local one.
  interface BlockSource {
    toolResults: Record<string, ToolResult>
    agentBlocks?: Record<string, Block[]>
    agentStreaming?: Record<string, string>
  }

  let { block, chat }: { block: Block; chat: BlockSource } = $props()
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
    nested={block.id ? (chat.agentBlocks?.[block.id] ?? []) : []}
    nestedStreaming={block.id ? (chat.agentStreaming?.[block.id] ?? '') : ''}
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
