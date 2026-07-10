<script lang="ts" module>
  import { marked } from 'marked'
  import DOMPurify from 'dompurify'

  marked.setOptions({ gfm: true, breaks: true })

  // Open links in a new tab (we're a PWA — don't navigate away from the session).
  DOMPurify.addHook('afterSanitizeAttributes', (node) => {
    if (node.tagName === 'A') {
      node.setAttribute('target', '_blank')
      node.setAttribute('rel', 'noopener noreferrer')
    }
  })

  export function render(src: string): string {
    return DOMPurify.sanitize(marked.parse(src ?? '', { async: false }) as string)
  }
</script>

<script lang="ts">
  let { text }: { text: string } = $props()
  const html = $derived(render(text))
</script>

<div class="md">{@html html}</div>

<style>
  .md {
    color: var(--text);
    font-family: var(--serif);
    font-size: 16.5px;
    line-height: 1.62;
    overflow-wrap: anywhere;
  }
  .md :global(> :first-child) {
    margin-top: 0;
  }
  .md :global(> :last-child) {
    margin-bottom: 0;
  }
  .md :global(p) {
    margin: 0 0 12px;
  }
  .md :global(h1),
  .md :global(h2),
  .md :global(h3),
  .md :global(h4) {
    font-family: var(--serif);
    font-weight: 600;
    line-height: 1.25;
    margin: 24px 0 11px;
  }
  .md :global(h1) {
    font-size: 23px;
  }
  .md :global(h2) {
    font-size: 20px;
  }
  .md :global(h3) {
    font-size: 17px;
  }
  .md :global(h4) {
    font-size: 15.5px;
    color: var(--text-2);
  }
  .md :global(ul),
  .md :global(ol) {
    margin: 0 0 12px;
    padding-left: 22px;
  }
  .md :global(li) {
    margin: 4px 0;
  }
  .md :global(li::marker) {
    color: var(--text-3);
  }
  .md :global(strong) {
    font-weight: 650;
    color: var(--text);
  }
  .md :global(em) {
    font-style: italic;
  }
  .md :global(a) {
    color: var(--text);
    text-decoration: underline;
    text-underline-offset: 2px;
    text-decoration-color: var(--text-3);
  }
  .md :global(a:hover) {
    text-decoration-color: var(--text);
  }
  .md :global(hr) {
    border: none;
    border-top: 1px solid var(--border);
    margin: 18px 0;
  }
  .md :global(blockquote) {
    margin: 0 0 12px;
    padding: 2px 0 2px 14px;
    border-left: 2px solid var(--border-2);
    color: var(--text-2);
  }
  /* inline code */
  .md :global(:not(pre) > code) {
    font-family: var(--mono);
    font-size: 0.86em;
    padding: 1.5px 5px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: 5px;
    white-space: break-spaces;
  }
  /* code blocks */
  .md :global(pre) {
    margin: 0 0 12px;
    padding: 12px 14px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    overflow-x: auto;
    line-height: 1.55;
  }
  .md :global(pre code) {
    font-family: var(--mono);
    font-size: 12.5px;
    color: var(--text);
    background: none;
    border: none;
    padding: 0;
  }
  .md :global(table) {
    width: 100%;
    border-collapse: collapse;
    margin: 0 0 12px;
    font-size: 13px;
  }
  .md :global(th),
  .md :global(td) {
    border: 1px solid var(--border);
    padding: 7px 10px;
    text-align: left;
  }
  .md :global(th) {
    background: var(--panel);
    font-weight: 550;
  }
  .md :global(img) {
    max-width: 100%;
    border-radius: var(--r-sm);
  }
</style>
