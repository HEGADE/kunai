<script lang="ts" module>
  import { marked, Marked } from 'marked'
  import DOMPurify from 'dompurify'
  import { highlightToHtml, langLabel } from '../lib/highlight'

  marked.setOptions({ gfm: true, breaks: true })

  // Open links in a new tab (we're a PWA — don't navigate away from the session).
  DOMPurify.addHook('afterSanitizeAttributes', (node) => {
    if (node.tagName === 'A') {
      node.setAttribute('target', '_blank')
      node.setAttribute('rel', 'noopener noreferrer')
    }
  })

  const COPY_SVG =
    '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 012-2h8"/></svg>'

  // A dedicated instance so the streaming path (plain `marked`) never pays for
  // highlighting; only committed blocks use this renderer.
  const richMarked = new Marked({ gfm: true, breaks: true })
  richMarked.use({
    renderer: {
      code(token: { text: string; lang?: string }) {
        const lang = (token.lang ?? '').trim().split(/\s+/)[0]
        const label = langLabel(lang)
        const body = highlightToHtml(token.text, lang)
        return (
          `<div class="codewrap">` +
          `<div class="cwbar">${label ? `<span class="cwlang">${label}</span>` : ''}` +
          `<span class="cwsp"></span>` +
          `<button class="cwcopy" data-copy aria-label="Copy code">${COPY_SVG}</button></div>` +
          `<pre><code class="hljs">${body}</code></pre></div>`
        )
      },
    },
  })

  export function render(src: string, opts: { highlight?: boolean } = {}): string {
    const parser = opts.highlight === false ? marked : richMarked
    return DOMPurify.sanitize(parser.parse(src ?? '', { async: false }) as string)
  }
</script>

<script lang="ts">
  let { text, live = false }: { text: string; live?: boolean } = $props()
  const html = $derived(render(text, { highlight: !live }))

  // Copy handler via delegation — safe because committed blocks have stable text
  // (this component only re-derives html when `text` changes).
  function onClick(e: MouseEvent) {
    const btn = (e.target as HTMLElement).closest('[data-copy]') as HTMLElement | null
    if (!btn) return
    const code = btn.closest('.codewrap')?.querySelector('code')?.textContent ?? ''
    navigator.clipboard?.writeText(code).then(() => {
      btn.setAttribute('data-copied', '')
      setTimeout(() => btn.removeAttribute('data-copied'), 1200)
    })
  }
</script>

<div class="md" onclick={onClick} role="presentation">{@html html}</div>

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
  /* Committed blocks: a bar (language + copy) above the code; the wrapper owns
     the box so the inner <pre> is unstyled. Streaming's bare <pre> keeps the
     box rules above. */
  .md :global(.codewrap) {
    margin: 0 0 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    overflow: hidden;
  }
  .md :global(.codewrap pre) {
    margin: 0;
    border: none;
    border-radius: 0;
    background: none;
  }
  .md :global(.cwbar) {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 8px 5px 12px;
    border-bottom: 1px solid var(--border);
  }
  .md :global(.cwlang) {
    font-family: var(--mono);
    font-size: 10px;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .md :global(.cwsp) {
    flex: 1;
  }
  .md :global(.cwcopy) {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 22px;
    border-radius: 5px;
    color: var(--text-4);
    cursor: pointer;
  }
  .md :global(.cwcopy:hover) {
    color: var(--text);
    background: var(--panel-2);
  }
  .md :global(.cwcopy[data-copied]) {
    color: var(--live);
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
