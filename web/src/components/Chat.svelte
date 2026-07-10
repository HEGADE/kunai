<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { uploadFile } from '../lib/api'
  import type { ChatConnection } from '../lib/chat.svelte'
  import type { Attachment } from '../lib/types'
  import ToolCard from './ToolCard.svelte'
  import PermissionGate from './PermissionGate.svelte'
  import Markdown from './Markdown.svelte'

  let { chat }: { chat: ChatConnection } = $props()

  let draft = $state('')
  let scroller = $state<HTMLElement | null>(null)
  let textarea = $state<HTMLTextAreaElement | null>(null)
  let fileInput = $state<HTMLInputElement | null>(null)
  let attachments = $state<Attachment[]>([])
  let uploading = $state(false)

  $effect(() => {
    chat.items.length
    chat.streaming
    chat.pending.length
    if (scroller) queueMicrotask(() => scroller && (scroller.scrollTop = scroller.scrollHeight))
  })

  async function onFiles(e: Event) {
    const input = e.target as HTMLInputElement
    if (!input.files?.length) return
    uploading = true
    for (const f of Array.from(input.files)) {
      try {
        attachments = [...attachments, await uploadFile(f)]
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

  const running = $derived(chat.sessionState === 'running')
  const status = $derived(
    chat.status !== 'online'
      ? { k: 'offline', t: 'offline' }
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
    <button class="back" onclick={() => app.back()} aria-label="Back">‹</button>
    <div class="ident">
      <span class="title">{chat.title || chat.cwd.split('/').slice(-1)[0] || 'session'}</span>
      <span class="path mono">{chat.cwd}</span>
    </div>
    <span class="status">
      <span class="sdot" data-k={status.k}></span>{status.t}
    </span>
  </header>

  <div class="scroll" bind:this={scroller}>
    <div class="log">
      {#each chat.items as item, i (i)}
        {#if item.role === 'user'}
          <div class="turn">
            <div class="role">You</div>
            <div class="user">{item.text}</div>
          </div>
        {:else if hasBody(item.blocks)}
          <div class="turn">
            <div class="role">Claude</div>
            <div class="assistant">
              {#each item.blocks as b, j (j)}
                {#if b.type === 'text' && b.text}
                  <Markdown text={b.text} />
                {:else if b.type === 'tool_use'}
                  <ToolCard name={b.name ?? 'tool'} input={b.input} />
                {:else if b.type === 'thinking' && b.text}
                  <div class="thinking mono">{b.text}</div>
                {/if}
              {/each}
            </div>
          </div>
        {/if}
      {/each}

      {#if chat.thinking || chat.streaming || running}
        <div class="turn">
          <div class="role">Claude</div>
          <div class="assistant">
            {#if chat.thinking}<div class="thinking mono">{chat.thinking}</div>{/if}
            {#if chat.streaming}
              <Markdown text={chat.streaming} />
            {:else if running}
              <span class="caret solo"></span>
            {/if}
          </div>
        </div>
      {/if}

      {#if chat.errorLine}<div class="err mono">{chat.errorLine}</div>{/if}
    </div>
  </div>

  <PermissionGate {chat} />

  <div class="dock">
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
        placeholder={chat.status === 'online' ? 'Message Claude…' : 'Reconnecting…'}
      ></textarea>
      <div class="bar">
        <button class="attach" onclick={() => fileInput?.click()} aria-label="Attach file" title="Attach">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M21.44 11.05l-9.19 9.19a5 5 0 01-7.07-7.07l9.19-9.19a3 3 0 014.24 4.24l-9.2 9.19a1 1 0 01-1.41-1.41l8.49-8.49" /></svg>
        </button>
        <input type="file" multiple bind:this={fileInput} onchange={onFiles} hidden />
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
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  header {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: calc(var(--safe-top) + 14px) 20px 13px;
    border-bottom: 1px solid var(--border);
  }
  .back {
    font-size: 26px;
    line-height: 1;
    color: var(--text-2);
    padding: 0 4px 4px;
    flex: none;
  }
  .ident {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .title {
    font-size: 14.5px;
    font-weight: 550;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .path {
    font-size: 10.5px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    text-align: left;
  }
  .status {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 7px;
    font-size: 12px;
    color: var(--text-3);
  }
  .sdot {
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

  .scroll {
    flex: 1;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
  }
  .log {
    max-width: 720px;
    margin: 0 auto;
    padding: 26px 20px 20px;
    display: flex;
    flex-direction: column;
    gap: 26px;
  }
  .role {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-3);
    margin-bottom: 8px;
  }
  .user {
    color: var(--text);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    padding: 12px 14px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r);
  }
  .assistant {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .prose {
    margin: 0;
    color: var(--text);
    line-height: 1.7;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .thinking {
    font-size: 12.5px;
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

  .dock {
    border-top: 1px solid var(--border);
    padding: 12px 18px calc(var(--safe-bottom) + 14px);
  }
  .field {
    max-width: 720px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    padding: 8px 10px 8px 14px;
    transition: border-color 0.12s;
  }
  .field:focus-within {
    border-color: var(--border-2);
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
    font-size: 14.5px;
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
  .send,
  .stop {
    width: 32px;
    height: 32px;
    border-radius: var(--r-sm);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .send {
    background: var(--panel-2);
    color: var(--text-4);
    font-size: 15px;
    font-weight: 600;
  }
  .send.ready {
    background: var(--white);
    color: #0b0b0c;
  }
  .stop {
    background: var(--panel-2);
    color: var(--text-2);
  }
  .sq {
    width: 9px;
    height: 9px;
    border-radius: 2px;
    background: currentColor;
  }

  @media (min-width: 861px) {
    .back {
      display: none;
    }
  }
</style>
