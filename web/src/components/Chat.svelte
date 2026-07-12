<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { uploadFile } from '../lib/api'
  import type { ChatConnection } from '../lib/chat.svelte'
  import type { Attachment } from '../lib/types'
  import { groupTurns } from '../lib/turns'
  import { MODELS, modelLabel } from '../lib/models'
  import PermissionGate from './PermissionGate.svelte'
  import Markdown from './Markdown.svelte'
  import BlockView from './BlockView.svelte'
  import ToolGroup from './ToolGroup.svelte'
  import TurnFooter from './TurnFooter.svelte'

  let { chat }: { chat: ChatConnection } = $props()

  // Group the flat item stream into turns so a turn's tool activity can collapse
  // behind one summary and carry a files-changed footer.
  const turns = $derived(groupTurns(chat.items))

  let draft = $state('')
  let scroller = $state<HTMLElement | null>(null)
  let textarea = $state<HTMLTextAreaElement | null>(null)
  let fileInput = $state<HTMLInputElement | null>(null)
  let attachments = $state<Attachment[]>([])
  let uploading = $state(false)
  let menuOpen = $state(false)
  let modeOpen = $state(false)
  let modelOpen = $state(false)

  // Scrolling: open at the latest message, follow the stream while pinned to the
  // bottom, and surface a jump-to-bottom button once the user scrolls up.
  let dockH = $state(0)
  let atBottom = $state(true)
  let prevChat: ChatConnection | undefined

  function nearBottom(): boolean {
    if (!scroller) return true
    return scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 90
  }
  function onScroll() {
    atBottom = nearBottom()
  }
  function toBottom(smooth = false) {
    if (!scroller) return
    scroller.scrollTo({ top: scroller.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
    atBottom = true
  }

  // Jump to the latest whenever a new session opens (history streams in async,
  // so the follow-effect below keeps us pinned as it fills).
  $effect(() => {
    if (chat !== prevChat) {
      prevChat = chat
      atBottom = true
      requestAnimationFrame(() => requestAnimationFrame(() => toBottom(false)))
    }
  })

  // Follow new content only while the user is at the bottom — never yank the
  // view away from something they scrolled up to read.
  $effect(() => {
    chat.items.length
    chat.streaming
    chat.pending.length
    if (atBottom) requestAnimationFrame(() => toBottom(false))
  })

  async function onFiles(e: Event) {
    const input = e.target as HTMLInputElement
    if (!input.files?.length) return
    uploading = true
    for (const f of Array.from(input.files)) {
      try {
        attachments = [...attachments, await uploadFile(chat.origin, f)]
      } catch {
        /* skip */
      }
    }
    uploading = false
    input.value = ''
  }
  function removeAttachment(id: string) {
    attachments = attachments.filter((a) => a.id !== id)
  }
  function send() {
    const t = draft.trim()
    if ((!t && attachments.length === 0) || chat.status !== 'online') return
    chat.sendPrompt(t, attachments)
    draft = ''
    attachments = []
    if (textarea) textarea.style.height = 'auto'
  }
  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }
  function grow() {
    if (!textarea) return
    textarea.style.height = 'auto'
    textarea.style.height = Math.min(textarea.scrollHeight, 160) + 'px'
  }

  const modeLabels: Record<string, string> = {
    default: 'Ask',
    acceptEdits: 'Edits',
    auto: 'Auto',
    plan: 'Plan',
  }
  const modes = [
    { id: 'default', label: 'Ask', hint: 'Approve each tool call' },
    { id: 'auto', label: 'Auto', hint: 'Approve safe actions automatically' },
    { id: 'acceptEdits', label: 'Accept edits', hint: 'Auto-approve file edits' },
    { id: 'plan', label: 'Plan', hint: 'Read-only planning' },
  ] as const

  const running = $derived(chat.sessionState === 'running')
  const status = $derived(
    chat.status !== 'online'
      ? { k: 'offline', t: 'offline' }
      : chat.sessionState === 'starting'
        ? { k: 'busy', t: 'starting' }
        : chat.sessionState === 'running'
          ? { k: 'busy', t: 'working' }
          : chat.sessionState === 'awaiting_permission'
            ? { k: 'busy', t: 'needs you' }
            : { k: 'live', t: 'idle' },
  )

  function hasBody(blocks: { type: string; text?: string }[]): boolean {
    return blocks.some(
      (b) => (b.type === 'text' && !!b.text) || b.type === 'tool_use' || (b.type === 'thinking' && !!b.text),
    )
  }
</script>

<div class="screen">
  <header>
    <button class="hbtn back" onclick={() => app.back()} aria-label="Back">
      <svg width="10" height="16" viewBox="0 0 10 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M8 1L2 8l6 7" /></svg>
    </button>
    <button class="hbtn rail deskonly" onclick={() => app.toggleSidebar()} aria-label="Toggle sidebar" title="Toggle sidebar">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2.5" /><path d="M9.5 4v16" /></svg>
    </button>
    <button class="hbtn home deskonly" onclick={() => app.back()} aria-label="Home" title="Home">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M3 10.5L12 3l9 7.5" /><path d="M5 9.5V20a1 1 0 001 1h4v-6h4v6h4a1 1 0 001-1V9.5" /></svg>
    </button>
    <div class="htitle" title={chat.cwd}>
      <span class="sdot" data-k={status.k}></span>
      <span class="tname">{chat.title || chat.cwd.split('/').slice(-1)[0] || 'session'}</span>
    </div>
    <button class="hbtn menu" onclick={() => (menuOpen = !menuOpen)} aria-label="Menu">⋯</button>
    {#if menuOpen}
      <button class="menu-scrim" onclick={() => (menuOpen = false)} aria-label="Close menu"></button>
      <div class="menu-pop">
        {#if running}
          <button onclick={() => { chat.interrupt(); menuOpen = false }}>Interrupt</button>
        {/if}
        <button class="danger" onclick={() => { app.closeSessionActive(); menuOpen = false }}>Close session</button>
      </div>
    {/if}
  </header>

  <div class="scroll" bind:this={scroller} onscroll={onScroll}>
    {#if chat.items.length === 0 && !chat.streaming && !chat.thinking && !running}
      <div class="blank">
        <p class="b1">{chat.cwd.split('/').slice(-1)[0] || 'session'}</p>
        <p class="b2 mono">{chat.cwd}</p>
        <p class="b3">Send a message to start.</p>
      </div>
    {/if}
    <div class="log">
      {#each turns as turn, ti (ti)}
        {@const live = ti === turns.length - 1 && (running || !!chat.streaming || !!chat.thinking)}
        {#if turn.user !== undefined}
          <div class="turn user">
            <div class="ubbl">{turn.user}</div>
          </div>
        {/if}
        {#if turn.hasAssistant && hasBody(turn.blocks)}
          <div class="turn">
            <div class="assistant">
              {#if live}
                {#each turn.blocks as b, j (j)}
                  <BlockView block={b} {chat} />
                {/each}
              {:else}
                {#if turn.toolCalls > 0}
                  <ToolGroup {turn} {chat} />
                {/if}
                {#each turn.answer as b, j (j)}
                  <BlockView block={b} {chat} />
                {/each}
                {#if turn.toolCalls > 0}
                  <TurnFooter {turn} />
                {/if}
              {/if}
            </div>
          </div>
        {/if}
      {/each}

      {#if chat.thinking || chat.streaming || running}
        <div class="turn">
          <div class="assistant">
            {#if chat.thinking}<div class="thinking mono">{chat.thinking}</div>{/if}
            {#if chat.streaming}
              <Markdown text={chat.streaming} live />
            {:else if running}
              <span class="working">Working…</span>
            {/if}
          </div>
        </div>
      {/if}

      {#if chat.errorLine}<div class="err mono">{chat.errorLine}</div>{/if}
    </div>
  </div>

  {#if !atBottom}
    <button class="jump" style="bottom: {dockH + 14}px" onclick={() => toBottom(true)} aria-label="Scroll to latest">
      <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14M6 13l6 6 6-6" /></svg>
    </button>
  {/if}

  <PermissionGate {chat} />

  <div class="dock" bind:clientHeight={dockH}>
    <div class="field">
      {#if attachments.length}
        <div class="chips">
          {#each attachments as a (a.id)}
            <span class="chip">
              <span class="cn mono">{a.name}</span>
              <button class="cx" onclick={() => removeAttachment(a.id)} aria-label="Remove">✕</button>
            </span>
          {/each}
        </div>
      {/if}
      <textarea
        bind:this={textarea}
        bind:value={draft}
        oninput={grow}
        onkeydown={onKey}
        rows="1"
        enterkeyhint="send"
        autocomplete="off"
        autocapitalize="sentences"
        placeholder={chat.status === 'online' ? 'Message Claude…' : 'Reconnecting…'}
      ></textarea>
      <div class="bar">
        <button class="attach" onclick={() => fileInput?.click()} aria-label="Attach file" title="Attach">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M21.44 11.05l-9.19 9.19a5 5 0 01-7.07-7.07l9.19-9.19a3 3 0 014.24 4.24l-9.2 9.19a1 1 0 01-1.41-1.41l8.49-8.49" /></svg>
        </button>
        <input type="file" multiple bind:this={fileInput} onchange={onFiles} hidden />
        <div class="modewrap">
          <button class="mode" class:on={chat.mode !== 'default'} onclick={() => (modeOpen = !modeOpen)}>
            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><path d="M13 2L4.5 13.5h5L11 22l8.5-11.5h-5z" /></svg>
            {modeLabels[chat.mode] ?? chat.mode}
          </button>
          {#if modeOpen}
            <button class="mode-scrim" onclick={() => (modeOpen = false)} aria-label="Close"></button>
            <div class="mode-pop">
              {#each modes as m (m.id)}
                <button
                  class:active={chat.mode === m.id}
                  onclick={() => { chat.setMode(m.id); modeOpen = false }}
                >
                  <span class="ml">{m.label}</span>
                  <span class="mh">{m.hint}</span>
                </button>
              {/each}
            </div>
          {/if}
        </div>
        <div class="modewrap">
          <button class="mode" onclick={() => (modelOpen = !modelOpen)} title="Model">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 2l1.9 5.8L20 9.7l-5.1 1.6L12 17l-2.9-5.7L4 9.7l6.1-1.9z" /></svg>
            {modelLabel(chat.model)}
          </button>
          {#if modelOpen}
            <button class="mode-scrim" onclick={() => (modelOpen = false)} aria-label="Close"></button>
            <div class="mode-pop">
              {#each MODELS as m (m.id)}
                <button
                  class:active={modelLabel(chat.model) === m.label}
                  onclick={() => { chat.setModel(m.id); modelOpen = false }}
                >
                  <span class="ml">{m.label}</span>
                  {#if m.hint}<span class="mh">{m.hint}</span>{/if}
                </button>
              {/each}
            </div>
          {/if}
        </div>
        <span class="spacer"></span>
        {#if running}
          <button class="stop" onclick={() => chat.interrupt()} aria-label="Stop"><span class="sq"></span></button>
        {:else}
          <button
            class="send"
            class:ready={draft.trim() || attachments.length}
            onclick={send}
            disabled={(!draft.trim() && attachments.length === 0) || chat.status !== 'online'}
            aria-label="Send">↑</button>
        {/if}
      </div>
    </div>
  </div>
</div>

<style>
  .screen {
    position: relative;
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  .jump {
    position: absolute;
    right: 20px;
    z-index: 6;
    width: 38px;
    height: 38px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--text-2);
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    box-shadow: 0 8px 24px -8px rgba(0, 0, 0, 0.65);
    animation: jumpin 0.14s ease-out;
  }
  .jump:hover {
    color: var(--text);
    background: var(--panel-3);
  }
  @keyframes jumpin {
    from {
      opacity: 0;
      transform: translateY(6px);
    }
  }
  header {
    position: relative;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: calc(var(--safe-top) + 12px) 14px 12px;
    background: transparent;
  }
  .hbtn {
    flex: none;
    width: 36px;
    height: 36px;
    border-radius: 50%;
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-2);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .hbtn:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .menu {
    font-size: 18px;
    line-height: 1;
    letter-spacing: 0.06em;
  }
  /* Plain left-aligned title — no pill box. */
  .htitle {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 0 6px;
  }
  .tname {
    font-size: 15px;
    font-weight: 600;
    letter-spacing: -0.01em;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sdot {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .sdot[data-k='live'] {
    background: var(--live);
  }
  .sdot[data-k='busy'] {
    background: var(--busy);
  }
  .sdot[data-k='offline'] {
    background: var(--alert);
  }
  .menu-scrim {
    position: fixed;
    inset: 0;
    z-index: 30;
    background: none;
  }
  .menu-pop {
    position: absolute;
    z-index: 31;
    top: calc(100% - 4px);
    right: 12px;
    min-width: 168px;
    padding: 5px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
  }
  .menu-pop button {
    width: 100%;
    text-align: left;
    padding: 9px 11px;
    border-radius: var(--r-sm);
    font-size: 13.5px;
    color: var(--text);
  }
  .menu-pop button:hover {
    background: var(--panel-3);
  }
  .menu-pop .danger {
    color: var(--alert);
  }

  .scroll {
    flex: 1;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    position: relative;
  }
  .blank {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    text-align: center;
    padding: 0 34px;
    pointer-events: none;
  }
  .b1 {
    font-size: 16px;
    font-weight: 600;
    color: var(--text);
    margin: 0 0 4px;
  }
  .b2 {
    font-size: 11px;
    color: var(--text-4);
    margin: 0 0 14px;
    max-width: 100%;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .b3 {
    font-size: 13.5px;
    color: var(--text-3);
    margin: 0;
  }
  .log {
    max-width: 720px;
    margin: 0 auto;
    padding: 24px 20px 20px;
    display: flex;
    flex-direction: column;
    gap: 24px;
  }
  .turn.user {
    display: flex;
    justify-content: flex-end;
  }
  .ubbl {
    max-width: 82%;
    color: var(--text);
    font-size: 16px;
    line-height: 1.5;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    padding: 12px 16px;
    background: var(--panel-3);
    border-radius: 18px;
    border-bottom-right-radius: 6px;
  }
  .assistant {
    display: flex;
    flex-direction: column;
    gap: 12px;
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
  .prose {
    margin: 0;
    color: var(--text);
    line-height: 1.7;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .thinking {
    font-size: 13.5px;
    color: var(--text-4);
    padding-left: 12px;
    border-left: 1px solid var(--border-2);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .caret {
    display: inline-block;
    width: 2px;
    height: 1.05em;
    margin-left: 2px;
    background: var(--text-2);
    vertical-align: text-bottom;
    animation: blink 1.05s steps(2) infinite;
  }
  .caret.solo {
    height: 15px;
  }
  @keyframes blink {
    50% {
      opacity: 0;
    }
  }
  .err {
    color: var(--alert);
    font-size: 12.5px;
  }

  /* The composer floats on the chat canvas — no full-width divider or band
     beneath it; the field's own edge defines it. */
  .dock {
    padding: 6px 16px calc(var(--safe-bottom) + 12px);
  }
  @media (min-width: 861px) {
    .dock {
      padding: 6px 24px 20px;
    }
  }
  .field {
    max-width: 720px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    background: var(--panel);
    border: 1px solid var(--border-2);
    border-radius: 20px;
    padding: 9px 10px 9px 16px;
    transition: border-color 0.12s;
  }
  .field:focus-within {
    border-color: var(--text-4);
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    padding: 2px 0 8px;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    max-width: 60%;
    padding: 4px 7px 4px 10px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: 100px;
    font-size: 11.5px;
    color: var(--text-2);
  }
  .cn {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .cx {
    color: var(--text-4);
  }
  textarea {
    width: 100%;
    resize: none;
    background: none;
    border: none;
    padding: 4px 0 2px;
    font-size: 16px;
    line-height: 1.5;
    max-height: 180px;
    outline: none;
  }
  textarea::placeholder {
    color: var(--text-4);
  }
  .bar {
    display: flex;
    align-items: center;
    padding-top: 4px;
  }
  .spacer {
    flex: 1;
  }
  .attach {
    width: 32px;
    height: 32px;
    border-radius: var(--r-sm);
    color: var(--text-3);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .attach:hover {
    color: var(--text);
    background: var(--panel-2);
  }
  .modewrap {
    position: relative;
    margin-left: 2px;
  }
  .mode {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    height: 32px;
    padding: 0 12px;
    border-radius: 100px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    color: var(--text-2);
    font-size: 12.5px;
    font-weight: 500;
  }
  .mode:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .mode.on {
    color: var(--text);
    border-color: var(--border-2);
  }
  .mode-scrim {
    position: fixed;
    inset: 0;
    z-index: 30;
  }
  .mode-pop {
    position: absolute;
    z-index: 31;
    bottom: calc(100% + 8px);
    left: 0;
    min-width: 230px;
    padding: 5px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
  }
  .mode-pop button {
    width: 100%;
    display: flex;
    flex-direction: column;
    gap: 1px;
    text-align: left;
    padding: 8px 11px;
    border-radius: var(--r-sm);
  }
  .mode-pop button:hover {
    background: var(--panel-3);
  }
  .mode-pop button.active .ml {
    color: var(--text);
  }
  .mode-pop button.active::after {
    content: '';
  }
  .ml {
    font-size: 13.5px;
    font-weight: 550;
    color: var(--text-2);
  }
  .mode-pop button.active {
    background: var(--panel-3);
  }
  .mh {
    font-size: 11.5px;
    color: var(--text-4);
  }
  .send,
  .stop {
    width: 34px;
    height: 34px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: background 0.12s, color 0.12s;
  }
  .send {
    background: var(--panel-3);
    color: var(--text-4);
    font-size: 16px;
    font-weight: 600;
  }
  .send.ready {
    background: var(--white);
    color: #0b0b0c;
  }
  .stop {
    background: var(--white);
    color: #0b0b0c;
  }
  .sq {
    width: 9px;
    height: 9px;
    border-radius: 2px;
    background: currentColor;
  }

  /* Desktop-only header controls (sidebar toggle + home); hidden on phones,
     where the single back button already returns to the session list. */
  .deskonly {
    display: none;
  }
  @media (min-width: 861px) {
    .back {
      display: none;
    }
    .deskonly {
      display: flex;
    }
  }
</style>
