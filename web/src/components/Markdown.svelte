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

  // The wrap toggle's glyph: a line that turns back on itself.
  const WRAP_SVG =
    '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18"/><path d="M3 12h13a3.5 3.5 0 010 7h-2.5"/><path d="M16 16.5L13.5 19l2.5 2.5"/><path d="M3 18h5"/></svg>'

  // Wrapping is a preference, not a property of one block: somebody who wants
  // long lines wrapped wants them wrapped everywhere, and having to set it again
  // on the next fence is the kind of per-instance state that makes a control
  // feel broken. Kept on the document so every block already on screen turns
  // over at once, and in localStorage so it survives a reload.
  const WRAP_KEY = 'kunai.codeWrap'

  export function codeWrapOn(): boolean {
    try {
      return localStorage.getItem(WRAP_KEY) === '1'
    } catch {
      return false // private mode, or storage disabled: wrapping off is the old behaviour
    }
  }

  function applyCodeWrap(on: boolean) {
    if (typeof document === 'undefined') return
    document.documentElement.toggleAttribute('data-code-wrap', on)
  }

  export function setCodeWrap(on: boolean) {
    try {
      localStorage.setItem(WRAP_KEY, on ? '1' : '0')
    } catch {
      // Not being able to remember it is not a reason to refuse to do it.
    }
    applyCodeWrap(on)
  }

  // Applied once at module load, before the first block paints, so a reload does
  // not show every fence unwrapped for a frame and then reflow.
  applyCodeWrap(codeWrapOn())

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
          `<button class="cwcopy" data-wrap aria-label="Wrap long lines" title="Wrap long lines">${WRAP_SVG}</button>` +
          `<button class="cwcopy" data-copy aria-label="Copy code">${COPY_SVG}</button></div>` +
          `<pre><code class="hljs">${body}</code></pre></div>`
        )
      },
    },
  })

  export function render(
    src: string,
    opts: { highlight?: boolean; fileBase?: string } = {},
  ): string {
    const parser = opts.highlight === false ? marked : richMarked
    let html = DOMPurify.sanitize(parser.parse(src ?? '', { async: false }) as string)
    html = withScrollableTables(html)
    return withImageFrames(html, opts.fileBase ?? '')
  }

  // Give every table its own horizontal scroller.
  //
  // A table cannot both size itself to its contents and scroll: constraining the
  // box so it can scroll also constrains the layout inside it, and the columns
  // get squeezed back to their longest word. Measured on a phone, that made the
  // prose column 120px wide and every row 120px tall; the same table in a wrapper
  // is 45px a row, so nearly three times as much of it is readable at once. The
  // cost is a wider sideways scroll, which is the cheap direction here.
  //
  // The wrapper is what scrolls, so the message column never grows with it.
  //
  // Done on the sanitized HTML for the same reason withLocalImages is: real
  // elements, not a regex over markup. Guarded by a substring test because this
  // runs on every streaming delta, and a DOM round-trip per keystroke is exactly
  // the cost the live path avoids highlighting to save.
  function withScrollableTables(html: string): string {
    if (!html.includes('<table')) return html
    const tpl = document.createElement('template')
    tpl.innerHTML = html
    for (const table of Array.from(tpl.content.querySelectorAll('table'))) {
      const wrap = document.createElement('div')
      wrap.className = 'tablewrap'
      table.replaceWith(wrap)
      wrap.appendChild(table)
    }
    return tpl.innerHTML
  }

  // Point an image at a path on the machine to the endpoint that can actually
  // serve it.
  //
  // An agent writing ![shot](/tmp/x.png) produced an <img> whose src the browser
  // resolved against kunai's own origin -- and kunai answers every unmatched path
  // with the app shell, so the image element received HTML and showed a broken
  // icon. It failed identically in a browser on the machine itself; this was
  // never about which device was looking.
  //
  // Rewritten on the already-sanitized HTML, walking real elements rather than
  // running a regex over markup, so a src containing quotes or angle brackets
  // cannot be used to reshape the document on the way through.
  // …and give it somewhere to live: a frame with the actions a picture needs.
  //
  // Rendering the image was only half of it. A picture arrives at whatever size
  // the model drew it, in a message column narrower than that, with no way to see
  // it properly and no way to keep it -- and the one thing you want to do with a
  // picture somebody made for you is save it. So each image gets a figure with a
  // caption and a hover toolbar, and the actions are wired by delegation in the
  // component below.
  //
  // Built with real DOM nodes rather than string concatenation, both because
  // that is how withLocalImages already worked (a src containing quotes or angle
  // brackets cannot reshape the document on the way through) and because the alt
  // text is model-written: setting it with textContent means a caption cannot
  // become markup no matter what it says.
  function withImageFrames(html: string, base: string): string {
    if (!html.includes('<img')) return html
    const tpl = document.createElement('template')
    tpl.innerHTML = html
    for (const img of Array.from(tpl.content.querySelectorAll('img'))) {
      if (img.closest('.imgfr')) continue // already framed, if this ever runs twice
      resolveImageSrc(img, base)
      frameImage(img)
    }
    return tpl.innerHTML
  }

  // Point an image at a path on the machine to the endpoint that can actually
  // serve it.
  //
  // An agent writing ![shot](/tmp/x.png) produced an <img> whose src the browser
  // resolved against kunai's own origin -- and kunai answers every unmatched path
  // with the app shell, so the image element received HTML and showed a broken
  // icon. It failed identically in a browser on the machine itself; this was
  // never about which device was looking.
  function resolveImageSrc(img: HTMLImageElement, base: string) {
    img.setAttribute('loading', 'lazy')
    const src = img.getAttribute('src') ?? ''
    // Anything already addressable is left alone: a real URL, an inline data
    // URI, and the endpoint's own output if this ever runs twice. Without a base
    // (the guest view has no session to read files from) a bare path is left
    // exactly as it was rather than pointed somewhere that would refuse it.
    if (!base || !src || /^[a-z][a-z0-9+.-]*:/i.test(src) || src.startsWith(base)) return
    img.setAttribute('src', base + encodeURIComponent(src))
  }

  function frameImage(img: HTMLImageElement) {
    const alt = img.getAttribute('alt') ?? ''
    const fig = document.createElement('figure')
    fig.className = 'imgfr'
    img.replaceWith(fig)

    const stage = document.createElement('div')
    stage.className = 'imgstage'
    stage.appendChild(img)
    fig.appendChild(stage)

    const acts = document.createElement('div')
    acts.className = 'imgacts'
    acts.appendChild(iconButton('img-open', 'View full size', EXPAND_SVG))
    acts.appendChild(iconButton('img-save', 'Download image', SAVE_SVG))
    stage.appendChild(acts)

    if (alt) {
      const cap = document.createElement('figcaption')
      cap.className = 'imgcap'
      cap.textContent = alt // never innerHTML: this is model-written prose
      fig.appendChild(cap)
    }
  }

  function iconButton(flag: string, label: string, svg: string): HTMLButtonElement {
    const b = document.createElement('button')
    b.className = 'imgbtn'
    b.type = 'button'
    b.setAttribute('data-' + flag, '')
    b.setAttribute('aria-label', label)
    b.title = label
    b.innerHTML = svg // a constant defined above, never anything from the model
    return b
  }

  const EXPAND_SVG =
    '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 3H3v6"/><path d="M15 21h6v-6"/><path d="M3 3l7 7"/><path d="M21 21l-7-7"/></svg>'
  const SAVE_SVG =
    '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12"/><path d="M7 11l5 5 5-5"/><path d="M4 20h16"/></svg>'
</script>

<script lang="ts">
  import { getContext } from 'svelte'
  import { copyText } from '../lib/clipboard'
  import { FILE_BASE, type FileBase } from '../lib/filebase'
  import { fileNameFor, saveImage } from '../lib/imageActions'
  import { lightbox } from '../lib/lightbox.svelte'

  let { text, live = false }: { text: string; live?: boolean } = $props()
  // Which session's files an image may come from. Taken from context rather than
  // threaded through props: Markdown is rendered from half a dozen places, most
  // of which have no idea which session they belong to, and passing it down every
  // one of those chains to reach an <img> would be a lot of plumbing for a rare
  // tag. A getter, not a string, so switching tabs cannot leave it stale.
  const fileBase = getContext<FileBase | undefined>(FILE_BASE)
  const html = $derived(render(text, { highlight: !live, fileBase: fileBase?.() }))

  // Copy handler via delegation — safe because committed blocks have stable text
  // (this component only re-derives html when `text` changes).
  function onClick(e: MouseEvent) {
    if (onImageClick(e)) return
    const wrap = (e.target as HTMLElement).closest('[data-wrap]') as HTMLElement | null
    if (wrap) {
      setCodeWrap(!codeWrapOn())
      return
    }
    const btn = (e.target as HTMLElement).closest('[data-copy]') as HTMLElement | null
    if (!btn) return
    const code = btn.closest('.codewrap')?.querySelector('code')?.textContent ?? ''
    // Was `navigator.clipboard?.writeText(code).then(...)`, which reads as guarded
    // and is not: off a secure context the optional chain yields undefined and
    // .then() on it throws, uncaught, inside a click handler.
    void copyText(code).then((ok) => {
      if (!ok) return
      btn.setAttribute('data-copied', '')
      setTimeout(() => btn.removeAttribute('data-copied'), 1200)
    })
  }

  // The image actions, by delegation for the same reason the copy button is:
  // the figures live inside {@html}, so there is no component per picture to
  // hang a handler on. Returns true when it consumed the click.
  function onImageClick(e: MouseEvent): boolean {
    const t = e.target as HTMLElement
    const save = t.closest('[data-img-save]') as HTMLElement | null
    if (save) {
      const img = save.closest('.imgfr')?.querySelector('img')
      if (img) void download(img, save)
      return true
    }
    // Expanding is offered as a button AND on the picture itself: the button
    // says the gesture exists, and clicking the image is what people try first.
    const open = t.closest('[data-img-open]') as HTMLElement | null
    const img = open ? open.closest('.imgfr')?.querySelector('img') : (t.closest('.imgstage img') as HTMLImageElement | null)
    if (img) {
      lightbox.show(img.currentSrc || img.src, img.getAttribute('alt') ?? '')
      return true
    }
    return false
  }

  async function download(img: HTMLImageElement, btn: HTMLElement) {
    const src = img.currentSrc || img.src
    btn.setAttribute('data-busy', '')
    try {
      await saveImage(src, fileNameFor(src, img.getAttribute('alt') ?? ''))
    } finally {
      btn.removeAttribute('data-busy')
    }
  }

  // A picture that will not load must say so.
  //
  // The file route refuses anything outside the session's folders (403) and
  // anything that is not a raster image (415), and an agent can write a markdown
  // image for a path that is either. Left alone that is the browser's broken-image
  // glyph, which says nothing about which of those happened, so the frame is
  // marked and the caption explains itself instead.
  //
  // Captured rather than bubbled: an <img> error event does not bubble, so a
  // listener on the container only ever sees it on the capture phase.
  function onError(e: Event) {
    const img = e.target as HTMLElement
    if (img?.tagName !== 'IMG') return
    img.closest('.imgfr')?.setAttribute('data-broken', '')
  }
</script>

<div class="md" onclick={onClick} onerrorcapture={onError} role="presentation">{@html html}</div>

<style>
  .md {
    color: var(--text);
    font-family: var(--sans);
    font-size: 15.5px;
    line-height: 1.68;
    letter-spacing: -0.006em;
    overflow-wrap: anywhere;
  }
  .md :global(> :first-child) {
    margin-top: 0;
  }
  .md :global(> :last-child) {
    margin-bottom: 0;
  }
  .md :global(p) {
    margin: 0 0 14px;
  }
  .md :global(h1),
  .md :global(h2),
  .md :global(h3),
  .md :global(h4) {
    font-family: var(--sans);
    font-weight: 600;
    line-height: 1.32;
    letter-spacing: -0.014em;
    margin: 24px 0 9px;
  }
  .md :global(h1) {
    font-size: 19.5px;
  }
  .md :global(h2) {
    font-size: 17px;
  }
  .md :global(h3) {
    font-size: 15px;
  }
  .md :global(h4) {
    font-size: 13px;
    font-weight: 600;
    letter-spacing: 0.03em;
    text-transform: uppercase;
    color: var(--text-3);
    font-family: var(--sans);
  }
  .md :global(ul),
  .md :global(ol) {
    margin: 0 0 14px;
    padding-left: 20px;
  }
  .md :global(li) {
    margin: 3px 0;
    padding-left: 3px;
  }
  .md :global(li::marker) {
    color: var(--text-4);
  }
  /* Tight nested lists and single-paragraph items — no doubled spacing. */
  .md :global(li > p) {
    margin: 0 0 6px;
  }
  .md :global(li :last-child) {
    margin-bottom: 0;
  }
  .md :global(ul ul),
  .md :global(ul ol),
  .md :global(ol ul),
  .md :global(ol ol) {
    margin: 3px 0 3px;
  }
  .md :global(strong) {
    font-weight: 640;
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
    margin: 0 0 14px;
    padding: 2px 0 2px 15px;
    border-left: 2px solid var(--border-2);
    color: var(--text-2);
  }
  /* inline code — a soft, borderless chip so dense inline code reads as text,
     not a row of hard boxes. Scales with the surrounding type. */
  .md :global(:not(pre) > code) {
    font-family: var(--mono);
    font-size: 0.83em;
    padding: 0.1em 0.38em;
    background: rgba(255, 255, 255, 0.06);
    border-radius: 4px;
    color: var(--text);
    white-space: break-spaces;
  }
  /* Links and headings that wrap inline code keep their own emphasis. */
  .md :global(a > code) {
    color: inherit;
  }
  /* code blocks */
  .md :global(pre) {
    margin: 0 0 14px;
    padding: 12px 14px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    overflow-x: auto;
    line-height: 1.55;
  }
  .md :global(pre code) {
    font-family: var(--mono);
    font-size: 13.5px;
    color: var(--text);
    background: none;
    border: none;
    padding: 0;
  }
  /* Committed blocks: a bar (language + copy) above the code; the wrapper owns
     the box so the inner <pre> is unstyled. Streaming's bare <pre> keeps the
     box rules above. */
  .md :global(.codewrap) {
    margin: 0 0 14px;
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
  /* Wrapping is set on the document, so every block on screen turns over
     together rather than one at a time as you scroll past them. The horizontal
     scrollbar goes with it: keeping it would leave a scrollable axis with
     nothing off the edge to scroll to. */
  :global(html[data-code-wrap]) .md :global(pre) {
    overflow-x: visible;
  }
  :global(html[data-code-wrap]) .md :global(pre code) {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  /* Lit while it is on, so the button reports the state it is in rather than
     only the action it performs. */
  :global(html[data-code-wrap]) .md :global(.cwcopy[data-wrap]) {
    color: var(--text);
    background: var(--panel-3);
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
  /* A table scrolls sideways rather than being crushed into the column width.

     `width: 100%` told a seven-column table to fit a phone, and the container's
     `overflow-wrap: anywhere` (right for prose, so a long URL cannot push the
     chat wider) let it obey: with a break legal between any two characters, the
     narrowest a cell can be is ONE character, so the browser stacked "Panel
     score" down the page a letter at a time and a header row grew taller than
     the screen. The two rules are only wrong together, which is why this looked
     fine on a laptop.

     The wrapper is what scrolls, and the table inside it is left free to size to
     its contents. Making the table itself the scroller reads as simpler and is
     the well-known fix, but it constrains the layout inside as well as the box:
     the columns collapse back to their longest word, which cost three times the
     row height on a phone. */
  .md :global(.tablewrap) {
    overflow-x: auto;
    /* A sideways swipe on a table must not turn into the browser's back gesture
       or drag the page with it. */
    overscroll-behavior-x: contain;
    margin: 0 0 12px;
  }
  .md :global(table) {
    width: max-content;
    border-collapse: collapse;
    margin: 0;
    font-size: 13px;
  }
  .md :global(th),
  .md :global(td) {
    border: 1px solid var(--border);
    padding: 7px 10px;
    text-align: left;
    /* Undo the container's `anywhere` for cells: a word may not be split, so the
       narrowest a column can be is its longest word rather than one glyph. */
    overflow-wrap: normal;
    word-break: normal;
  }
  /* A header is a short label naming the column, so it reads as one line. Long
     enough to matter and it widens the column, which now costs a scroll rather
     than a wrecked layout. */
  .md :global(th) {
    background: var(--panel);
    font-weight: 550;
    white-space: nowrap;
  }
  /* A prose cell wraps at a readable measure instead of running to one long line
     and making the table several screens wide. Only binds now that the wrapper
     does the scrolling: while the table was the scroller this had no effect at
     all, because the columns were already squeezed below it. */
  .md :global(td) {
    max-width: 46ch;
  }
  .md :global(img) {
    max-width: 100%;
    border-radius: var(--r-sm);
  }

  /* A picture and the things you do with it.
     The frame is a quiet surface rather than a card: the image supplies its own
     edges, so a border around it would be a second one. */
  .md :global(figure.imgfr) {
    margin: 14px 0;
  }
  .md :global(.imgstage) {
    position: relative;
    display: inline-block;
    max-width: 100%;
    line-height: 0; /* an inline image otherwise leaves a descender gap below it */
    border-radius: var(--r-sm);
    background: var(--panel);
  }
  .md :global(.imgstage img) {
    display: block;
    /* Capped so one picture cannot take the whole column and push the rest of
       the reply off screen; full size is one click away. In px rather than vh so
       it does not resize under you when a phone's address bar hides. */
    max-height: 460px;
    width: auto;
    cursor: zoom-in;
  }
  /* The actions appear on hover, the same bargain the turn footer makes: they
     are always there when wanted and never furniture when not. On a touch screen
     there is no hover to reveal them with, so they simply stay. */
  .md :global(.imgacts) {
    position: absolute;
    top: 8px;
    right: 8px;
    display: flex;
    gap: 6px;
    opacity: 0;
    transition: opacity 0.13s;
  }
  .md :global(.imgstage:hover .imgacts),
  .md :global(.imgacts:focus-within) {
    opacity: 1;
  }
  @media (hover: none) {
    .md :global(.imgacts) {
      opacity: 1;
    }
  }
  .md :global(.imgbtn) {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border-radius: 7px;
    /* Opaque enough to stay legible over whatever the picture happens to be
       behind it, which is the one thing chrome over an image has to survive. */
    background: rgba(18, 18, 20, 0.82);
    border: 1px solid var(--border);
    color: var(--text-2);
    cursor: pointer;
  }
  .md :global(.imgbtn:hover) {
    color: var(--text);
    border-color: var(--border-2);
  }
  .md :global(.imgbtn[data-busy]) {
    opacity: 0.5;
  }
  .md :global(figcaption.imgcap) {
    margin-top: 7px;
    font-size: 12.5px;
    line-height: 1.45;
    color: var(--text-3);
  }
  /* A picture that would not load. The frame says so in words, because the
     browser's own broken glyph does not say which of "outside this session's
     folders" and "not an image kunai will serve" happened. */
  .md :global(figure.imgfr[data-broken] .imgstage) {
    display: block;
    line-height: 1.5;
    padding: 14px;
    border: 1px dashed var(--border-2);
  }
  .md :global(figure.imgfr[data-broken] .imgstage img),
  .md :global(figure.imgfr[data-broken] .imgacts) {
    display: none;
  }
  .md :global(figure.imgfr[data-broken] .imgstage::after) {
    content: 'This image could not be loaded. It may be outside the session’s folders, or not an image kunai can show.';
    font-size: 12.5px;
    color: var(--text-3);
  }
</style>
