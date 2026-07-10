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
  <button class="head" onclick={() => (open = !open)}>
    <span class="name mono">{name}</span>
    {#if summary}<span class="sum mono">{summary}</span>{/if}
    <span class="chev">{open ? '−' : '+'}</span>
  </button>
  {#if open}<pre class="body mono">{pretty}</pre>{/if}
</div>

<style>
  .tool {
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--panel);
    overflow: hidden;
  }
  .head {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 9px 12px;
    text-align: left;
    font-size: 12.5px;
  }
  .name {
    font-weight: 500;
    color: var(--text);
    flex: none;
  }
  .sum {
    color: var(--text-3);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
    min-width: 0;
  }
  .chev {
    color: var(--text-4);
    flex: none;
    font-family: var(--mono);
  }
  .body {
    margin: 0;
    padding: 11px 12px;
    border-top: 1px solid var(--border);
    font-size: 12px;
    line-height: 1.55;
    color: var(--text-2);
    white-space: pre-wrap;
    word-break: break-word;
    background: var(--bg);
  }
</style>
