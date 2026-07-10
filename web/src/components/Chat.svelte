<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { uploadFile } from '../lib/api'
  import type { ChatConnection } from '../lib/chat.svelte'
  import type { Attachment } from '../lib/types'
  import ToolCard from './ToolCard.svelte'
  import PermissionGate from './PermissionGate.svelte'

  let { chat }: { chat: ChatConnection } = $props()

  let draft = $state('')
  let scroller = $state<HTMLElement | null>(null)
  let textarea = $state<HTMLTextAreaElement | null>(null)
  let fileInput = $state<HTMLInputElement | null>(null)
  let attachments = $state<Attachment[]>([])
  let uploading = $state(false)

  async function onFiles(e: Event) {
    const input = e.target as HTMLInputElement
    if (!input.files?.length) return
    uploading = true
    for (const f of Array.from(input.files)) {
      try {
        attachments = [...attachments, await uploadFile(f)]
      } catch {
        /* skip failed upload */
      }
    }
    uploading = false
    input.value = ''
  }
  function removeAttachment(id: string) {
    attachments = attachments.filter((a) => a.id !== id)
  }

  $effect(() => {
    chat.items.length
    chat.streaming
    chat.pending.length
    if (scroller) queueMicrotask(() => scroller && (scroller.scrollTop = scroller.scrollHeight))
  })

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
    textarea.style.height = Math.min(textarea.scrollHeight, 140) + 'px'
  }

  const running = $derived(chat.sessionState === 'running')

  // An assistant turn is worth showing only if it renders something.
  function hasBody(blocks: { type: string; text?: string }[]): boolean {
    return blocks.some(
      (b) => (b.type === 'text' && !!b.text) || b.type === 'tool_use' || (b.type === 'thinking' && !!b.text),
    )
  }
  // The wire encodes real state: offline, working, needs-you, or live-idle.
  const wire = $derived(
    chat.status !== 'online'
      ? 'offline'
      : chat.sessionState === 'running'
        ? 'running'
        : chat.sessionState === 'awaiting_permission'
          ? 'awaiting'
          : 'idle',
  )
  const readout = $derived(
    { offline: 'reconnecting', running: 'working', awaiting: 'needs you', idle: 'live' }[wire],
  )
</script>

<div class="screen">
  <header>
    <button class="back" onclick={() => app.back()} aria-label="Back">‹</button>
    <div class="ident">
      <span class="title mono">{chat.title || chat.cwd.split('/').slice(-1)[0] || 'session'}</span>
      <span class="path mono">{chat.cwd}</span>
    </div>
    <span class="readout mono" data-state={wire}>{readout}</span>
  </header>
  <div class="wire" data-state={wire}></div>

  <div class="scroll" bind:this={scroller}>
    <div class="log">
      {#each chat.items as item, i (i)}
        {#if item.role === 'user'}
          <div class="turn you">
            <div class="who"><span class="mk">❯</span><span class="label">you</span></div>
            <div class="body you-body">{item.text}</div>
          </div>
        {:else if hasBody(item.blocks)}
          <div class="turn claude">
            <div class="who"><span class="mk">◆</span><span class="label">claude</span></div>
            <div class="body">
              {#each item.blocks as b, j (j)}
                {#if b.type === 'text' && b.text}
                  <p class="prose">{b.text}</p>
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
        <div class="turn claude">
          <div class="who"><span class="mk">◆</span><span class="label">claude</span></div>
          <div class="body">
            {#if chat.thinking}<div class="thinking mono">{chat.thinking}</div>{/if}
            {#if chat.streaming}
              <p class="prose">{chat.streaming}<span class="cursor"></span></p>
            {:else if running}
              <span class="cursor solo"></span>
            {/if}
          </div>
        </div>
      {/if}

      {#if chat.errorLine}
        <div class="err mono">! {chat.errorLine}</div>
      {/if}
    </div>
  </div>

  <PermissionGate {chat} />

  <div class="composer-wrap">
    {#if attachments.length}
      <div class="chips">
        {#each attachments as a (a.id)}
          <span class="chip mono">
            <span class="ci">{a.media_type.startsWith('image/') ? '▣' : '⎘'}</span>
            <span class="cn">{a.name}</span>
            <button class="cx" onclick={() => removeAttachment(a.id)} aria-label="Remove">✕</button>
          </span>
        {/each}
      </div>
    {/if}
    <div class="composer">
      <button class="attach" onclick={() => fileInput?.click()} aria-label="Attach file">
        {uploading ? '…' : '＋'}
      </button>
      <input type="file" multiple bind:this={fileInput} onchange={onFiles} hidden />
      <textarea
        bind:this={textarea}
        bind:value={draft}
        oninput={grow}
        onkeydown={onKey}
        rows="1"
        placeholder={chat.status === 'online' ? 'message claude' : 'reconnecting…'}
      ></textarea>
      {#if running}
        <button class="stop" onclick={() => chat.interrupt()} aria-label="Stop">◼</button>
      {:else}
        <button
          class="send"
          onclick={send}
          disabled={(!draft.trim() && attachments.length === 0) || chat.status !== 'online'}
          aria-label="Send">↵</button>
      {/if}
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
    padding: calc(var(--safe-top) + 13px) 14px 11px;
    background: var(--bg);
  }
  .back {
    font-size: 28px;
    line-height: 1;
    color: var(--ink-dim);
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
    font-size: 14px;
    font-weight: 600;
    color: var(--ink);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .path {
    font-size: 10.5px;
    color: var(--ink-faint);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    text-align: left;
  }
  .readout {
    flex: none;
    font-size: 10px;
    letter-spacing: 0.16em;
    text-transform: uppercase;
    padding: 4px 9px;
    border-radius: 100px;
    border: 1px solid var(--line);
    color: var(--ink-dim);
  }
  .readout[data-state='idle'] {
    color: var(--wire);
    border-color: var(--wire-dim);
  }
  .readout[data-state='running'] {
    color: var(--wire);
  }
  .readout[data-state='awaiting'] {
    color: var(--amber);
    border-color: var(--amber-dim);
  }
  .readout[data-state='offline'] {
    color: var(--stop);
  }

  /* The wire: a live line that IS the connection. */
  .wire {
    height: 2px;
    position: relative;
    overflow: hidden;
    background: var(--line);
    flex: none;
  }
  .wire[data-state='idle'] {
    background: linear-gradient(90deg, transparent, var(--wire-dim) 40%, var(--wire-dim) 60%, transparent);
  }
  .wire[data-state='running']::after {
    content: '';
    position: absolute;
    top: 0;
    bottom: 0;
    width: 45%;
    background: linear-gradient(90deg, transparent, var(--wire), transparent);
    animation: travel 1.15s linear infinite;
  }
  @keyframes travel {
    from {
      transform: translateX(-120%);
    }
    to {
      transform: translateX(320%);
    }
  }
  .wire[data-state='awaiting'] {
    background: var(--amber);
    animation: dim 1.1s ease-in-out infinite;
  }
  .wire[data-state='offline'] {
    background: repeating-linear-gradient(90deg, var(--stop) 0 5px, transparent 5px 11px);
    animation: dim 0.9s ease-in-out infinite;
  }
  @keyframes dim {
    50% {
      opacity: 0.35;
    }
  }

  .scroll {
    flex: 1;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
  }
  .log {
    padding: 8px 16px 12px;
  }
  .turn {
    padding: 12px 0;
    border-bottom: 1px solid var(--line);
  }
  .turn:last-child {
    border-bottom: none;
  }
  .who {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 7px;
  }
  .mk {
    font-family: var(--mono);
    font-size: 13px;
    line-height: 1;
  }
  .you .mk {
    color: var(--wire);
  }
  .claude .mk {
    color: var(--ink-dim);
  }
  .body {
    display: flex;
    flex-direction: column;
    gap: 9px;
  }
  .you-body {
    color: var(--ink);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    padding: 2px 0 2px 12px;
    border-left: 2px solid var(--wire-dim);
  }
  .prose {
    margin: 0;
    color: var(--ink);
    line-height: 1.62;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .thinking {
    font-size: 12.5px;
    color: var(--ink-faint);
    padding-left: 12px;
    border-left: 2px solid var(--line);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .cursor {
    display: inline-block;
    width: 8px;
    height: 15px;
    margin-left: 2px;
    background: var(--wire);
    vertical-align: text-bottom;
    animation: blink 1s steps(2) infinite;
  }
  .cursor.solo {
    margin-left: 12px;
  }
  @keyframes blink {
    50% {
      opacity: 0;
    }
  }
  .err {
    color: var(--stop);
    font-size: 12.5px;
    padding: 10px 0;
  }

  .composer-wrap {
    border-top: 1px solid var(--line);
    background: var(--bg);
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    padding: 9px 14px 0;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    max-width: 62%;
    padding: 4px 6px 4px 10px;
    background: var(--bg-2);
    border: 1px solid var(--line);
    border-radius: 100px;
    font-size: 11.5px;
    color: var(--ink-dim);
  }
  .chip .ci {
    color: var(--wire);
    flex: none;
  }
  .chip .cn {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .chip .cx {
    color: var(--ink-faint);
    padding: 0 2px;
    flex: none;
  }
  .composer {
    display: flex;
    align-items: flex-end;
    gap: 9px;
    padding: 10px 14px calc(var(--safe-bottom) + 10px);
  }
  .attach {
    flex: none;
    width: 40px;
    height: 40px;
    border-radius: var(--r);
    font-size: 20px;
    color: var(--ink-dim);
    background: var(--bg-2);
    border: 1px solid var(--line);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .attach:active {
    border-color: var(--wire);
    color: var(--wire);
  }
  textarea {
    flex: 1;
    resize: none;
    background: var(--bg-2);
    border: 1px solid var(--line);
    border-radius: var(--r);
    padding: 11px 13px;
    font-size: 15px;
    line-height: 1.4;
    max-height: 140px;
    outline: none;
  }
  textarea:focus {
    border-color: var(--wire);
  }
  .send,
  .stop {
    flex: none;
    width: 40px;
    height: 40px;
    border-radius: var(--r);
    font-size: 17px;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .send {
    background: var(--wire);
    color: #06222a;
    font-weight: 700;
  }
  .send:disabled {
    background: var(--bg-3);
    color: var(--ink-faint);
  }
  .stop {
    background: var(--stop-dim);
    color: var(--stop);
    font-size: 13px;
  }
</style>
