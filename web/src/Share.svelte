<script lang="ts">
  import { setContext } from 'svelte'
  // The page somebody sees when they open a share link: one conversation, and
  // nothing else about the machine it runs on.
  //
  // It reuses the owner's renderers (Markdown, BlockView) so a shared turn reads
  // exactly like a local one and there is no second renderer to keep honest. It
  // does not reuse the owner's shell: no sidebar, no fleet, no settings, because
  // none of that exists from here.
  import { GuestConnection } from './lib/guest.svelte'
  import Markdown from './components/Markdown.svelte'
  import BlockView from './components/BlockView.svelte'
  import Spinner from './components/Spinner.svelte'
  import Lightbox from './components/Lightbox.svelte'
  import { FILE_BASE, sharedImageBase } from './lib/filebase'
  import FileChips from './components/FileChips.svelte'
  import type { Attachment } from './lib/types'

  // The token is the last path segment of /s/<token>.
  const token = decodeURIComponent(location.pathname.replace(/^.*\/s\//, '').replace(/\/$/, ''))
  const g = new GuestConnection(token)
  // Pictures the conversation drew. Not the owner's file route, which is
  // owner-only and off this gate on purpose; see lib/filebase.ts.
  setContext(FILE_BASE, () => sharedImageBase(token))

  let draft = $state('')
  let name = $state('')
  let code = $state('')
  let asking = $state(false)
  let pollTimer: ReturnType<typeof setInterval> | undefined

  // Once a code is showing, watch for the owner's approval so the composer
  // appears without the guest reloading and wondering whether it worked.
  $effect(() => {
    if (!code || g.canSend) {
      clearInterval(pollTimer)
      return
    }
    pollTimer = setInterval(() => void g.pollPaired(), 2500)
    return () => clearInterval(pollTimer)
  })

  const running = $derived(g.sessionState === 'running')
  const awaiting = $derived(g.sessionState === 'awaiting_permission')

  async function askToJoin() {
    asking = true
    try {
      code = await g.pair(name.trim())
    } catch (e) {
      g.errorLine = e instanceof Error ? e.message : String(e)
    } finally {
      asking = false
    }
  }

  // Pictures staged for the next message. A guest sends a screenshot far more
  // often than any other kind of file, and the server accepts nothing else.
  let files = $state<Attachment[]>([])
  let fileInput = $state<HTMLInputElement | null>(null)
  let upErr = $state('')
  let uploading = $state(false)

  function send() {
    if (!draft.trim() && !files.length) return
    g.send(draft, files)
    draft = ''
    files = []
  }

  async function addFiles(list: FileList | File[]) {
    upErr = ''
    uploading = true
    try {
      for (const f of Array.from(list)) {
        try {
          files = [...files, await g.upload(f)]
        } catch (e) {
          // Named rather than swallowed: the server refuses non-images and
          // oversize ones on purpose, and a picture that silently did not attach
          // is worse than one that says why.
          upErr = e instanceof Error ? e.message : 'that file could not be sent'
        }
      }
    } finally {
      uploading = false
    }
  }

  function onFiles(e: Event) {
    const input = e.currentTarget as HTMLInputElement
    if (input.files?.length) void addFiles(input.files)
    input.value = '' // so choosing the same picture twice still fires
  }

  // Paste is how a screenshot usually arrives, so it is worth taking directly
  // rather than making somebody save the file first.
  function onPaste(e: ClipboardEvent) {
    const imgs = Array.from(e.clipboardData?.items ?? [])
      .filter((i) => i.kind === 'file' && i.type.startsWith('image/'))
      .map((i) => i.getAsFile())
      .filter((f): f is File => !!f)
    if (imgs.length) {
      e.preventDefault()
      void addFiles(imgs)
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      send()
    }
  }

  // Following the conversation, without racing the layout.
  //
  // There was no scrolling logic here at all, which failed the one thing a shared
  // link is for: somebody opens it to see what is happening NOW and landed at the
  // top of the transcript, then had to scroll a whole conversation to find the
  // live end of it. A streaming reply did the same in reverse, growing below the
  // fold while they watched a stationary screen.
  //
  // Scroll POSITION cannot decide whether the reader has moved away, which is
  // what two earlier attempts here got wrong. The column grows under them
  // constantly -- a picture finishing its download long after the markup that
  // holds it, a reply streaming -- and every scroll-event heuristic ended up
  // reading "the content got taller" as "they scrolled up", so the page opened
  // halfway through its own transcript with a Latest button already showing.
  //
  // So intent comes only from things that unambiguously are intent (a wheel, a
  // drag, a key), and growth is handled separately by watching the column resize.
  let scroller = $state<HTMLElement | null>(null)
  let column = $state<HTMLElement | null>(null)
  let pinned = $state(true)

  // Generous: a momentum scroll rarely stops exactly at the end, and being a few
  // pixels short must not read as "they went to look at something".
  const NEAR_BOTTOM = 64

  function atBottom(): boolean {
    const el = scroller
    return !el || el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM
  }

  // Called only from real input, so a reflow can never be mistaken for a reader.
  function readerMoved() {
    pinned = atBottom()
  }

  // scrollend fires for a programmatic scroll too, so each jump we make is
  // counted and the matching event is spent rather than read as the reader
  // moving. Without this the sequence was: jump to the end, an image finishes
  // and grows the column, scrollend arrives, position is no longer the bottom,
  // and the page unpins itself the instant after it pinned.
  let ownScrolls = 0

  function scrollSettled() {
    if (ownScrolls > 0) {
      ownScrolls--
      return
    }
    readerMoved()
  }

  function toBottom(smooth = false) {
    const el = scroller
    if (!el) return
    ownScrolls++
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
    pinned = true
  }

  // The column resizing covers every way content arrives, including the one that
  // defeated the earlier versions: an image loading seconds after its markup,
  // when nothing in the item list has changed for an effect to notice.
  $effect(() => {
    const el = column
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => {
      if (pinned) toBottom()
    })
    ro.observe(el)
    return () => ro.disconnect()
  })

  function expiry(ts: number): string {
    const s = ts - Math.floor(Date.now() / 1000)
    if (s <= 0) return 'expired'
    if (s < 3600) return `${Math.round(s / 60)} min left`
    if (s < 86400) return `${Math.round(s / 3600)} h left`
    return `${Math.round(s / 86400)} d left`
  }
</script>

<!-- A guest sees pictures too, and the viewer is what makes one legible: without
     it, clicking to expand would do nothing here while working in the owner's
     view, which is worse than not offering it. -->
<Lightbox />

<div class="page">
  <header>
    <span class="dot" class:live={running} class:ask={awaiting}></span>
    <h1>{g.info?.title || 'Shared session'}</h1>
    {#if g.info}
      <span class="meta mono">{expiry(g.info.expires_at)}</span>
    {/if}
  </header>

  {#if g.gone}
    <!-- Terminal, and said plainly. A guest cannot fix this and should not be
         left watching a spinner that will never resolve. -->
    <div class="center">
      <p class="big">{g.gone}</p>
      <p class="sub">Ask whoever sent it for a new link.</p>
    </div>
  {:else if !g.info}
    <div class="center"><Spinner /></div>
  {:else}
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <!-- These listeners read intent, they do not add an interaction: the region
         is already scrollable and keyboard-reachable as a scroll container. -->
    <main
      bind:this={scroller}
      onwheel={readerMoved}
      ontouchmove={readerMoved}
      onkeydown={readerMoved}
      onscrollend={scrollSettled}
    >
      <div class="col" bind:this={column}>
      {#each g.items as item, i (item.seq ?? i)}
        {#if item.role === 'user'}
          <div class="msg user" class:guest={item.from === 'guest'}>
            <Markdown text={item.text} />
          </div>
          <!-- What rode along with it. Metadata only, the same as the owner's
               view: the bytes went to the model and are never served back. -->
          {#if item.files?.length}
            <div class="sentfiles"><FileChips files={item.files} /></div>
          {/if}
        {:else if item.role === 'assistant'}
          <div class="msg reply">
            {#each item.blocks as b, bi (bi)}
              <BlockView block={b} chat={g} />
            {/each}
          </div>
        {:else if item.role === 'compact'}
          <div class="seam"><span>the conversation was summarised here</span></div>
        {/if}
      {/each}

      {#if g.thinking}
        <div class="msg reply thinking"><Markdown text={g.thinking} live /></div>
      {/if}
      {#if g.streaming}
        <div class="msg reply"><Markdown text={g.streaming} live /></div>
      {:else if running && !g.thinking}
        <!-- The same line the owner sees, and the guest needs it more.
             Sending left nothing at all between the message and the first token:
             a turn that thinks for half a minute before writing anything is
             indistinguishable from one that was never delivered, and the guest
             cannot check the session any other way. The header dot and the Stop
             button did change, but neither is where somebody is looking after
             they press Send. -->
        <div class="msg reply"><span class="working">Working…</span></div>
      {/if}
      {#if awaiting}
        <p class="waiting">Waiting for the owner to approve something.</p>
      {/if}
      {#if g.errorLine}<p class="err">{g.errorLine}</p>{/if}
      </div>
    </main>

    <footer>
      <!-- Only while the reader is away from the end, so it is an offer rather
           than furniture. Anchored to the footer's own top edge rather than a
           fixed offset from the bottom: the footer is a composer for one guest,
           a join box for another and one line for a third, and a guessed offset
           put the pill on top of whichever was tallest. -->
      {#if !pinned}
        <button class="jump" onclick={() => toBottom(true)} aria-label="Jump to the latest message">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14" /><path d="M6 13l6 6 6-6" /></svg>
          Latest
        </button>
      {/if}
      {#if g.info.tier === 'view'}
        <p class="ro">You are watching this session. Only its owner can send to it.</p>
      {:else if g.canSend}
        <div class="composer">
          {#if files.length}
            <div class="staged">
              <FileChips {files} />
              <button class="clear" onclick={() => (files = [])} aria-label="Remove the attached images">Clear</button>
            </div>
          {/if}
          {#if upErr}<p class="uperr">{upErr}</p>{/if}
          <textarea
            bind:value={draft}
            onkeydown={onKey}
            onpaste={onPaste}
            rows="2"
            placeholder="Send a message"
            aria-label="Send a message"></textarea>
          <div class="cbar">
            <input type="file" accept="image/*" multiple bind:this={fileInput} onchange={onFiles} hidden />
            <button
              class="attach"
              onclick={() => fileInput?.click()}
              disabled={uploading}
              title="Attach an image"
              aria-label="Attach an image"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.5l-8.4 8.4a5 5 0 01-7.1-7.1l8.5-8.5a3.3 3.3 0 014.7 4.7l-8.5 8.5a1.7 1.7 0 01-2.3-2.3l7.8-7.8" /></svg>
            </button>
            <span class="hint">Every file change still needs the owner's approval.</span>
            <span class="spacer"></span>
            {#if running}
              <button class="ghost" onclick={() => g.stop()}>Stop</button>
            {/if}
            <button class="go" disabled={(!draft.trim() && !files.length) || uploading} onclick={send}>Send</button>
          </div>
        </div>
      {:else if g.info.taken}
        <p class="ro">Someone else is already sending to this session. You can still watch.</p>
      {:else if code}
        <div class="pairbox">
          <p>Read this to the person who sent you the link:</p>
          <p class="code mono">{code}</p>
          <p class="hint">Waiting for them to let you in.</p>
        </div>
      {:else}
        <div class="pairbox">
          <p>You can watch this session. To send to it, ask the owner to let you in.</p>
          <div class="askrow">
            <input bind:value={name} placeholder="Your name" aria-label="Your name" maxlength="40" />
            <button class="go" disabled={asking} onclick={askToJoin}>
              {#if asking}<Spinner />{/if}
              Ask to join
            </button>
          </div>
        </div>
      {/if}
    </footer>
  {/if}
</div>

<style>
  .page {
    position: relative;
    display: flex;
    flex-direction: column;
    height: 100dvh;
    max-width: 820px;
    margin: 0 auto;
    background: var(--bg);
    color: var(--text);
  }
  header {
    display: flex;
    align-items: center;
    gap: 9px;
    flex: none;
    padding: calc(var(--safe-top) + 12px) 16px 10px;
    border-bottom: 1px solid var(--border);
  }
  h1 {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: 13.5px;
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .dot {
    flex: none;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .dot.live {
    background: var(--live);
  }
  .dot.ask {
    background: var(--busy);
  }
  .meta {
    flex: none;
    font-size: 11px;
    color: var(--text-4);
  }
  main {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    /* Momentum scrolling must not drag the page behind it on a phone. */
    overscroll-behavior: contain;
  }
  /* The thing whose height is watched. main's own box never changes, so the
     observer has to sit on the content inside it. */
  .col {
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  /* Floats just above the footer rather than sitting in the column, so it never
     moves the conversation to announce itself. */
  .jump {
    position: absolute;
    left: 50%;
    transform: translateX(-50%);
    bottom: 100%;
    margin-bottom: 10px;
    z-index: 5;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 7px 13px;
    border-radius: 100px;
    border: 1px solid var(--border-2);
    background: var(--panel-2);
    color: var(--text-2);
    font-size: 12.5px;
    box-shadow: 0 6px 20px rgba(0, 0, 0, 0.45);
  }
  .jump:hover {
    color: var(--text);
  }
  .center {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
  }
  .big {
    margin: 0;
    font-size: 15px;
    color: var(--text-2);
  }
  .sub {
    margin: 0;
    font-size: 12.5px;
    color: var(--text-4);
  }
  .msg.user {
    align-self: flex-end;
    max-width: 88%;
    padding: 9px 13px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: 14px 14px 4px 14px;
    font-size: 13.5px;
  }
  /* A guest's own message is marked, so a conversation with two people in it
     reads as one. */
  .msg.user.guest {
    border-color: var(--border-2);
  }
  .msg.reply {
    font-family: var(--serif);
    font-size: 15.5px;
    line-height: 1.62;
  }
  .working {
    font-size: 14px;
    color: var(--text-3);
    animation: soften 1.6s ease-in-out infinite;
  }
  @keyframes soften {
    50% {
      opacity: 0.45;
    }
  }
  .thinking {
    color: var(--text-4);
    font-style: italic;
  }
  .seam {
    display: flex;
    align-items: center;
    gap: 10px;
    color: var(--text-4);
    font-size: 11px;
  }
  .seam::before,
  .seam::after {
    content: '';
    flex: 1;
    height: 1px;
    background: var(--border);
  }
  .waiting {
    margin: 0;
    font-size: 12.5px;
    color: var(--busy);
  }
  .err {
    margin: 0;
    font-size: 12.5px;
    color: var(--alert);
  }
  footer {
    position: relative; /* the jump pill anchors to this edge */
    flex: none;
    padding: 10px 16px calc(var(--safe-bottom) + 14px);
  }
  .ro,
  .hint {
    margin: 0;
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-4);
  }
  .sentfiles {
    display: flex;
    justify-content: flex-end;
    margin: -4px 0 2px;
  }
  .staged {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 0 2px 6px;
  }
  .clear {
    flex: none;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .clear:hover {
    color: var(--text-2);
  }
  .uperr {
    margin: 0 2px 6px;
    font-size: 12px;
    color: var(--alert);
  }
  .attach {
    flex: none;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    margin-left: -4px;
    border-radius: var(--r-sm);
    color: var(--text-4);
  }
  .attach:hover:not(:disabled) {
    color: var(--text-2);
    background: var(--panel);
  }
  .attach:disabled {
    opacity: 0.5;
  }
  .composer {
    display: flex;
    flex-direction: column;
    background: var(--panel-2);
    border: 1px solid #64666c;
    border-radius: 16px;
    padding: 12px 14px 9px;
  }
  .composer:focus-within {
    border-color: var(--text-3);
  }
  textarea {
    width: 100%;
    border: 0;
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 14px;
    resize: none;
    outline: none;
  }
  .cbar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding-top: 8px;
  }
  .spacer {
    flex: 1;
  }
  .pairbox {
    display: flex;
    flex-direction: column;
    gap: 9px;
    padding: 13px 14px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r);
  }
  .pairbox p {
    margin: 0;
    font-size: 12.5px;
    line-height: 1.55;
    color: var(--text-3);
  }
  .askrow {
    display: flex;
    gap: 8px;
  }
  .askrow input {
    flex: 1;
    min-width: 0;
    height: 34px;
    padding: 0 11px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text);
    font-size: 13px;
  }
  /* Read aloud or off a screen, so it is set large and widely tracked. */
  .code {
    align-self: flex-start;
    padding: 6px 14px;
    background: var(--panel-3);
    border-radius: 8px;
    color: var(--text) !important;
    font-size: 21px;
    letter-spacing: 0.2em;
  }
  .go,
  .ghost {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 7px;
    height: 34px;
    padding: 0 14px;
    border-radius: 8px;
    border: 1px solid transparent;
    font-size: 12.5px;
    font-weight: 500;
    cursor: pointer;
  }
  .go {
    background: var(--white);
    color: #0b0b0c;
  }
  .go:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .ghost {
    background: transparent;
    border-color: var(--border);
    color: var(--text-2);
  }

  /* Phone. Every change here is about a thumb rather than a cursor.
     The caveat about the owner's approval is a promise worth keeping, so it moves
     to its own row rather than being squeezed to nothing or hidden; the controls
     take the row below it at a real tap size. */
  @media (max-width: 560px) {
    .col {
      padding: 12px;
      gap: 13px;
    }
    .cbar {
      flex-wrap: wrap;
      row-gap: 8px;
    }
    .hint {
      order: -1;
      flex: 1 0 100%;
    }
    .spacer {
      flex: 1;
    }
    /* 40px clears the 44px target with the row's own gap around it, and Send
       stays the widest thing on the row so it is hard to miss. */
    .go,
    .ghost {
      height: 40px;
      padding: 0 18px;
      font-size: 13.5px;
    }
    .attach {
      width: 40px;
      height: 40px;
    }
    /* A picture must not take the whole screen. The desktop cap is 460px, which
       on an 844px phone is more than half of it before the caption. */
    .page :global(.imgstage img) {
      max-height: 300px;
    }
    /* A user bubble at 88% leaves almost no gutter to read the alignment by. */
    .msg.user {
      max-width: 92%;
    }
    /* The join box is a one-time action holding a permanent seat. On a phone it
       was taking a sixth of the screen, every screen, from somebody who may only
       ever want to watch -- so it gets the room a control needs and no more, and
       the sentence explaining it drops to one line. */
    .pairbox {
      gap: 7px;
      padding: 10px 11px;
      border-radius: var(--r-sm);
    }
    .pairbox p {
      font-size: 12.5px;
      line-height: 1.4;
    }
    .askrow input {
      height: 40px;
    }
    /* Read-only and taken notices are one quiet line, not a paragraph block. */
    .ro {
      font-size: 12.5px;
      line-height: 1.45;
    }
  }
</style>
