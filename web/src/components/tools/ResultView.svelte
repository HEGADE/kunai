<script lang="ts">
  import type { ToolResult } from '../../lib/types'
  import { shapeOf, stripLineNumbers } from '../../lib/outputShape'
  import CodeView from './CodeView.svelte'

  // Tool output, rendered as what it actually is.
  //
  // This was one grey `<pre>` whatever came back, which wasted three things the
  // app already has: a syntax highlighter, a diff renderer, and the path the tool
  // was given. Reading a Go file looked like reading a log, and reading a unified
  // diff looked like nothing at all, which matters more now that a review begins
  // by reading one.
  //
  // `path` is passed by the caller when the tool had one (a Read, an Edit), and
  // is only a hint: a diff is recognised from the content, because it arrives
  // from Bash and from `git show` where there is no path to go on.
  let {
    result,
    path = '',
    maxLines = 18,
  }: { result: ToolResult; path?: string; maxLines?: number } = $props()

  let expanded = $state(false)
  // The Read tool prefixes every line with `   12→`; highlighting that colours
  // the numbers as code and puts an arrow at the start of every line.
  const text = $derived(stripLineNumbers(result.content.replace(/\n$/, '')))
  const lines = $derived(text ? text.split('\n') : [])
  const clamped = $derived(!expanded && lines.length > maxLines)
  const shown = $derived(clamped ? lines.slice(0, maxLines).join('\n') : text)
  // An error is always plain: the message is prose, and highlighting it as the
  // language of the file it came from would be actively misleading.
  const shape = $derived(result.isError ? { kind: 'text' as const } : shapeOf(text, path))
</script>

<div class="rv" class:err={result.isError}>
  <div class="bar">
    <span class="dot"></span>
    <span class="label">{result.isError ? 'Error' : 'Output'}</span>
    {#if lines.length}<span class="meta mono">{lines.length} lines</span>{/if}
  </div>
  {#if text}
    {#if shape.kind === 'diff'}
      <!-- A unified diff reads as red and green or it does not read at all. The
           highlighter's own diff language does this without needing the
           before/after pair DiffView wants, which output does not carry. -->
      <CodeView code={shown} lang="diff" label="diff" maxLines={clamped ? maxLines : lines.length} />
    {:else if shape.kind === 'code'}
      <CodeView code={shown} lang={shape.lang} maxLines={clamped ? maxLines : lines.length} />
    {:else}
      <pre class="out mono">{shown}</pre>
    {/if}
    {#if clamped}
      <button class="more" onclick={() => (expanded = true)}>Show {lines.length - maxLines} more lines</button>
    {:else if expanded && lines.length > maxLines}
      <button class="more" onclick={() => (expanded = false)}>Collapse</button>
    {/if}
    {#if result.truncated}<div class="trunc">output truncated</div>{/if}
  {:else}
    <div class="empty mono">(no output)</div>
  {/if}
</div>

<style>
  .rv {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    overflow: hidden;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 11px;
    border-bottom: 1px solid var(--border);
  }
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .rv.err .dot {
    background: var(--alert);
  }
  .label {
    font-size: 11px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .rv.err .label {
    color: var(--alert);
  }
  .meta {
    margin-left: auto;
    font-size: 10.5px;
    color: var(--text-4);
  }
  .out {
    margin: 0;
    padding: 10px 12px;
    overflow-x: auto;
    font-size: 12.5px;
    line-height: 1.5;
    color: var(--text-2);
    white-space: pre;
  }
  .empty {
    padding: 9px 12px;
    font-size: 12px;
    color: var(--text-4);
  }
  .more {
    width: 100%;
    padding: 6px;
    border-top: 1px solid var(--border);
    font-size: 11.5px;
    color: var(--text-3);
  }
  .more:hover {
    color: var(--text);
    background: var(--panel);
  }
  .trunc {
    padding: 5px 12px;
    font-size: 10.5px;
    color: var(--text-4);
    border-top: 1px solid var(--border);
  }
</style>
