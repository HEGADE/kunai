<script lang="ts">
  import { getContext } from 'svelte'
  import type { ToolResult } from '../../lib/types'
  import { imageResultPath } from '../../lib/toolMeta'
  import { FILE_BASE, type FileBase } from '../../lib/filebase'
  import ImageFigure from '../ImageFigure.svelte'

  // `path` is the file the tool was pointed at, when it was pointed at one. It
  // is the only place an image's name exists, since the result carries none.
  let {
    result,
    path = '',
    maxLines = 18,
  }: { result: ToolResult; path?: string; maxLines?: number } = $props()

  let expanded = $state(false)
  const text = $derived(result.content.replace(/\n$/, ''))
  const lines = $derived(text ? text.split('\n') : [])
  const clamped = $derived(!expanded && lines.length > maxLines)
  const shown = $derived(clamped ? lines.slice(0, maxLines).join('\n') : text)

  // A tool that returned a picture shows the picture.
  //
  // Reading an image rendered as the literal text "[image]", which is the marker
  // the server leaves where the bytes were -- correct on the wire and useless on
  // screen, and worst in exactly the case this exists for: looking at a machine
  // you are not sitting at. The file is on that machine and there is already a
  // route that serves it, so nothing has to be sent; the tool's own input says
  // which file, and the marker says there was one.
  const fileBase = getContext<FileBase | undefined>(FILE_BASE)
  const imagePath = $derived(result.isError ? '' : imageResultPath(text, path))
  const imageSrc = $derived.by(() => {
    const base = fileBase?.()
    return base && imagePath ? base + encodeURIComponent(imagePath) : ''
  })
</script>

<div class="rv" class:err={result.isError}>
  <div class="bar">
    <span class="dot"></span>
    <span class="label">{result.isError ? 'Error' : 'Output'}</span>
    {#if lines.length}<span class="meta mono">{lines.length} lines</span>{/if}
  </div>
  {#if imageSrc}
    <div class="shot">
      <ImageFigure src={imageSrc} alt={imagePath.split('/').pop() ?? 'image'} caption={imagePath} />
    </div>
  {:else if text}
    <pre class="out mono">{shown}</pre>
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
  /* Capped, and scrolled rather than truncated.
     A tool's output is reference material inside a conversation, not the
     conversation, and an uncapped one buys a `git log` or a test run the whole
     screen: everything the agent said after it is pushed a page down, so opening
     one card costs you the thread you were reading. A fixed ceiling keeps the
     card the same size whatever is in it, and the content is all still there,
     one scroll away. In px rather than vh so the card does not resize under you
     when a phone's address bar hides.
     The ceiling clears the 18-line clamp above it on purpose: below that, a
     collapsed result offered a scrollbar AND a "show 182 more lines" button at
     the same time, which are two answers to one question. Collapsed, the clamp
     is the only affordance; expanded, this is. */
  .out {
    margin: 0;
    padding: 10px 12px;
    max-height: 372px;
    overflow: auto;
    overscroll-behavior: contain; /* scrolling to the end must not scroll the log */
    font-size: 12.5px;
    line-height: 1.5;
    color: var(--text-2);
    white-space: pre;
  }
  /* The frame carries its own top margin for prose; inside the result box it
     sits under a bar instead, so the padding is the box's and the margin goes. */
  .shot {
    padding: 10px 12px;
  }
  .shot :global(.imgfr) {
    margin: 0;
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
