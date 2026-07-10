<script lang="ts">
  let { name, input }: { name: string; input: unknown } = $props()
  let open = $state(false)

  const summary = $derived.by(() => {
    const i = (input ?? {}) as Record<string, unknown>
    if (typeof i.command === 'string') return i.command
    if (typeof i.file_path === 'string') return i.file_path
    if (typeof i.path === 'string') return i.path
    if (typeof i.pattern === 'string') return i.pattern
    if (typeof i.url === 'string') return i.url
    return ''
  })
  const pretty = $derived(JSON.stringify(input ?? {}, null, 2))
</script>

<div class="tool">
  <button class="head mono" onclick={() => (open = !open)}>
    <span class="tick">▶</span>
    <span class="name">{name}</span>
    {#if summary}<span class="sum">{summary}</span>{/if}
    <span class="chev">{open ? '−' : '+'}</span>
  </button>
  {#if open}
    <pre class="body mono">{pretty}</pre>
  {/if}
</div>

<style>
  .tool {
    border: 1px solid var(--line);
    border-left: 2px solid var(--wire);
    border-radius: var(--r-sm);
    background: var(--bg-2);
    overflow: hidden;
  }
  .head {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 9px 11px;
    text-align: left;
    font-size: 12.5px;
  }
  .tick {
    color: var(--wire);
    font-size: 9px;
    flex: none;
  }
  .name {
    font-weight: 600;
    color: var(--wire-bright);
    flex: none;
  }
  .sum {
    color: var(--ink-dim);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
    min-width: 0;
  }
  .chev {
    color: var(--ink-faint);
    flex: none;
  }
  .body {
    margin: 0;
    padding: 11px;
    border-top: 1px solid var(--line);
    font-size: 12px;
    line-height: 1.55;
    color: var(--ink-dim);
    white-space: pre-wrap;
    word-break: break-word;
    background: var(--bg);
  }
</style>
