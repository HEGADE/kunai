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

  function send() {
    if (!draft.trim()) return
    g.send(draft)
    draft = ''
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      send()
    }
  }

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
    <main>
      {#each g.items as item, i (item.seq ?? i)}
        {#if item.role === 'user'}
          <div class="msg user" class:guest={item.from === 'guest'}>
            <Markdown text={item.text} />
          </div>
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
      {/if}
      {#if awaiting}
        <p class="waiting">Waiting for the owner to approve something.</p>
      {/if}
      {#if g.errorLine}<p class="err">{g.errorLine}</p>{/if}
    </main>

    <footer>
      {#if g.info.tier === 'view'}
        <p class="ro">You are watching this session. Only its owner can send to it.</p>
      {:else if g.canSend}
        <div class="composer">
          <textarea
            bind:value={draft}
            onkeydown={onKey}
            rows="2"
            placeholder="Send a message"
            aria-label="Send a message"></textarea>
          <div class="cbar">
            <span class="hint">Every file change still needs the owner's approval.</span>
            <span class="spacer"></span>
            {#if running}
              <button class="ghost" onclick={() => g.stop()}>Stop</button>
            {/if}
            <button class="go" disabled={!draft.trim()} onclick={send}>Send</button>
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
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 16px;
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
</style>
