<script lang="ts">
  import { highlightToHtml, langLabel } from '../../lib/highlight'

  let {
    code,
    lang,
    label,
    maxLines = 22,
  }: { code: string; lang?: string; label?: string; maxLines?: number } = $props()

  let expanded = $state(false)
  let copied = $state(false)

  const lines = $derived(code.replace(/\n$/, '').split('\n'))
  const clamped = $derived(!expanded && lines.length > maxLines)
  const shown = $derived(clamped ? lines.slice(0, maxLines).join('\n') : code.replace(/\n$/, ''))
  const html = $derived(highlightToHtml(shown, lang))
  const tag = $derived(label ?? langLabel(lang))

  async function copy() {
    try {
      await navigator.clipboard.writeText(code)
      copied = true
      setTimeout(() => (copied = false), 1200)
    } catch {
      /* clipboard unavailable */
    }
  }
</script>

<div class="cv">
  <div class="bar">
    {#if tag}<span class="lang">{tag}</span>{/if}
    <span class="sp"></span>
    <button class="copy" onclick={copy} aria-label="Copy code">
      {#if copied}
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5" /></svg>
      {:else}
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V5a2 2 0 012-2h8" /></svg>
      {/if}
    </button>
  </div>
  <pre><code class="hljs">{@html html}</code></pre>
  {#if clamped}
    <button class="more" onclick={() => (expanded = true)}>Show {lines.length - maxLines} more lines</button>
  {:else if expanded && lines.length > maxLines}
    <button class="more" onclick={() => (expanded = false)}>Collapse</button>
  {/if}
</div>

<style>
  .cv {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    overflow: hidden;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 8px 5px 11px;
    border-bottom: 1px solid var(--border);
  }
  .lang {
    font-family: var(--mono);
    font-size: 10px;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .sp {
    flex: 1;
  }
  .copy {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 22px;
    border-radius: 5px;
    color: var(--text-4);
  }
  .copy:hover {
    color: var(--text);
    background: var(--panel-2);
  }
  pre {
    margin: 0;
    padding: 11px 13px;
    overflow-x: auto;
    line-height: 1.55;
  }
  code {
    font-family: var(--mono);
    font-size: 12.5px;
    color: var(--text);
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
</style>
