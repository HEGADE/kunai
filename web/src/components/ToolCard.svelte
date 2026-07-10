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

<div class="tool" class:open>
  <button class="head" onclick={() => (open = !open)}>
    <span class="ic">
      <svg width="9" height="9" viewBox="0 0 8 8" fill="currentColor"><path d="M2 0l4 4-4 4z" /></svg>
    </span>
    <span class="name">{name}</span>
    {#if summary}<span class="sum mono">{summary}</span>{/if}
  </button>
  {#if open}<pre class="body mono">{pretty}</pre>{/if}
</div>

<style>
  .tool {
    border-radius: var(--r-sm);
  }
  .head {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 7px 8px;
    text-align: left;
    border-radius: var(--r-sm);
    font-size: 13px;
    color: var(--text-2);
  }
  .head:hover {
    background: var(--panel);
  }
  .ic {
    flex: none;
    display: inline-flex;
    color: var(--text-4);
    transition: transform 0.15s;
  }
  .open .ic {
    transform: rotate(90deg);
    color: var(--text-3);
  }
  .name {
    flex: none;
    font-weight: 550;
    color: var(--text);
  }
  .sum {
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
    min-width: 0;
    font-size: 12px;
  }
  .body {
    margin: 4px 0 2px;
    padding: 11px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    font-size: 12px;
    line-height: 1.55;
    color: var(--text-2);
    white-space: pre-wrap;
    word-break: break-word;
    overflow-x: auto;
  }
</style>
