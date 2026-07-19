<script lang="ts">
  import { tick } from 'svelte'
  import { app } from '../lib/app.svelte'
  import { uploadFile } from '../lib/api'
  import type { ChatConnection } from '../lib/chat.svelte'
  import type { Attachment } from '../lib/types'
  import { groupTurns } from '../lib/turns'
  import { MODELS, EFFORTS, modelLabel, effortLabel } from '../lib/models'
  import { setReloadGuard } from '../lib/updater'
  import PermissionGate from './PermissionGate.svelte'
  import Context from './Context.svelte'
  import Queued from './Queued.svelte'
  import FileChips from './FileChips.svelte'
  import ProjectCard from './ProjectCard.svelte'
  import CompactDivider from './CompactDivider.svelte'
  import LoopCard from './LoopCard.svelte'
  import LoopBar from './LoopBar.svelte'
  import LoopSheet from './LoopSheet.svelte'
  import AddProject from './AddProject.svelte'
  import Markdown from './Markdown.svelte'
  import BlockView from './BlockView.svelte'
  import ScheduleAfter from './ScheduleAfter.svelte'
  import ToolGroup from './ToolGroup.svelte'
  import TurnFooter from './TurnFooter.svelte'
  import TurnChanges from './TurnChanges.svelte'

  let { chat }: { chat: ChatConnection } = $props()

  // Group the flat item stream into turns so a turn's tool activity can collapse
  // behind one summary and carry a files-changed footer.
  const allTurns = $derived(groupTurns(chat.items))

  // Windowed rendering: a long conversation arrives all at once over the socket,
  // but mounting every turn (with syntax highlighting and diffs) is what makes
  // opening a big session janky and stream-from-the-top. So we only mount a
  // trailing window of turns — the session opens instantly at the bottom — and
  // reveal older turns as the user scrolls up (see maybeReveal). firstVisible is
  // the absolute index of the oldest mounted turn; keys stay absolute so
  // revealing prepends without re-rendering what's already there.
  const WINDOW = 20 // turns mounted initially / kept while pinned to the bottom
  const STEP = 20 // turns revealed per scroll-up
  const REVEAL_AT = 200 // px from the top that triggers a reveal
  let firstVisible = $state(0)
  const turns = $derived(allTurns.slice(firstVisible))


  let draft = $state('')
  let scroller = $state<HTMLElement | null>(null)
  let textarea = $state<HTMLTextAreaElement | null>(null)
  let fileInput = $state<HTMLInputElement | null>(null)
  let attachments = $state<Attachment[]>([])
  let uploading = $state(false)

  // Hold an auto-update reload while there's unsent work in the composer, so a
  // deploy never wipes a half-typed prompt out from under you. Clears back to
  // "always safe" when this chat unmounts.
  $effect(() => {
    setReloadGuard(() => draft.trim() !== '' || attachments.length > 0)
    return () => setReloadGuard(() => false)
  })

  let schedOpen = $state(false)
  let addProjOpen = $state(false)
  let loopOpen = $state(false)

  function resetRel(unixSec: number): string {
    let s = Math.round(unixSec - Date.now() / 1000)
    if (s < 0) s = 0
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    return h ? `${h}h ${m}m` : `${m}m`
  }
  let modeOpen = $state(false)
  let modelOpen = $state(false)
  let effortOpen = $state(false)
  let accountOpen = $state(false)
  // Claude accounts available on this session's machine (first is the default).
  const accounts = $derived(app.machines.find((m) => m.id === app.activeMachineId)?.stats?.clis ?? [])

  // Scrolling: open at the latest message, follow the stream while pinned to the
  // bottom, and surface a jump-to-bottom button once the user scrolls up.
  let dockH = $state(0)
  let atBottom = $state(true)
  // The connection whose window we've already initialised (once its backlog landed).
  let initedFor: ChatConnection | undefined

  function nearBottom(): boolean {
    if (!scroller) return true
    return scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 90
  }
  function onScroll() {
    atBottom = nearBottom()
    maybeReveal()
  }
  function toBottom(smooth = false) {
    if (!scroller) return
    scroller.scrollTo({ top: scroller.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
    atBottom = true
  }

  // Reveal older turns when the user scrolls near the top of the mounted window.
  // Anchor by the distance from the bottom: turns inserted above then slide in
  // without moving whatever the user is currently reading. Two sources feed it:
  // more already-loaded turns from the seed, then, once those run out, older turns
  // paged from disk (reverse infinite scroll) so scrollback reaches the session's
  // start even though resume only seeds the tail.
  let revealing = false
  async function maybeReveal() {
    if (revealing || !scroller || scroller.scrollTop > REVEAL_AT) return
    if (firstVisible === 0) {
      // Top of the loaded turns: pull an older page from disk if there is one.
      if (!chat.hasMoreHistory || chat.loadingOlder) return
      revealing = true
      const fromBottom = scroller.scrollHeight - scroller.scrollTop
      const before = allTurns.length
      await chat.loadOlder() // prepends older items; allTurns grows at the front
      await tick()
      // Mount a step of the freshly-paged turns right away (the rest reveal on
      // further scroll), anchored by distance-from-bottom so the read point holds.
      firstVisible = Math.max(0, allTurns.length - before - STEP)
      await tick()
      scroller.scrollTop = scroller.scrollHeight - fromBottom
      revealing = false
      return
    }
    revealing = true
    const fromBottom = scroller.scrollHeight - scroller.scrollTop
    firstVisible = Math.max(0, firstVisible - STEP)
    await tick()
    scroller.scrollTop = scroller.scrollHeight - fromBottom
    revealing = false
  }

  // Mount the window once a session's backlog has fully arrived (chat.ready):
  // only the trailing WINDOW of turns, pinned to the bottom, in a single paint.
  // Gating on ready is what removes the old stream-from-the-top jitter.
  $effect(() => {
    if (!chat.ready || chat === initedFor) return
    initedFor = chat
    firstVisible = Math.max(0, allTurns.length - WINDOW)
    atBottom = true
    requestAnimationFrame(() => requestAnimationFrame(() => toBottom(false)))
  })

  // Follow live content only while pinned to the bottom. The window only ever
  // grows (new turns append, reveals prepend) and is never trimmed, so what the
  // user is reading never shifts underneath them.
  $effect(() => {
    chat.items.length
    chat.streaming
    chat.thinking
    chat.sessionState // so the "Working…" line, which appears on state change alone, is followed too
    chat.pending.length
    if (atBottom) requestAnimationFrame(() => toBottom(false))
  })

  async function addFiles(files: File[]) {
    if (!files.length) return
    uploading = true
    for (const f of files) {
      try {
        attachments = [...attachments, await uploadFile(chat.origin, f)]
      } catch {
        /* skip */
      }
    }
    uploading = false
  }
  async function onFiles(e: Event) {
    const input = e.target as HTMLInputElement
    await addFiles(Array.from(input.files ?? []))
    input.value = ''
  }

  // Paste screenshots/photos from the clipboard (desktop and mobile). Listens on
  // the window so Cmd/Ctrl+V works whether or not the composer is focused; the
  // composer is the only text field, so hijacking image pastes is safe. Text
  // pastes fall through untouched (we only preventDefault when we took images).
  function onPaste(e: ClipboardEvent) {
    const items = e.clipboardData?.items
    if (!items) return
    const imgs: File[] = []
    for (const it of items) {
      if (it.kind === 'file' && it.type.startsWith('image/')) {
        const f = it.getAsFile()
        if (f) {
          imgs.push(
            f.name ? f : new File([f], `pasted-${Date.now()}.${f.type.split('/')[1] || 'png'}`, { type: f.type }),
          )
        }
      }
    }
    if (!imgs.length) return
    e.preventDefault()
    addFiles(imgs)
  }
  $effect(() => {
    window.addEventListener('paste', onPaste)
    return () => window.removeEventListener('paste', onPaste)
  })
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
    // Sending is an explicit action, so always snap to the bottom: you want to
    // see your message land and the "Working…" line, even if you'd scrolled up to
    // read. Re-pin so the reply then follows as it streams. tick() first so the
    // new content is laid out and scrollHeight includes it.
    atBottom = true
    tick().then(() => toBottom())
  }
  // On a physical keyboard, Enter sends and Shift+Enter inserts a newline. On a
  // touch device there is no Shift key, so Enter must insert a newline (the
  // native textarea behavior) and sending is done with the arrow button — the
  // standard mobile-chat convention.
  const isTouch =
    typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches
  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey && !isTouch) {
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
    <button class="hbtn home deskonly" onclick={() => app.back()} aria-label="Home" title="Home">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M3 10.5L12 3l9 7.5" /><path d="M5 9.5V20a1 1 0 001 1h4v-6h4v6h4a1 1 0 001-1V9.5" /></svg>
    </button>
    <!-- The tab above owns this session's name and status, so the header carries
         what the tab can't: where it is running. -->
    <div class="htitle" title={chat.cwd}>
      <span class="tpath mono">{chat.cwd}</span>
      {#if chat.projects.length}
        <button class="wspace" onclick={() => (addProjOpen = true)} title={chat.projects.map((p) => p.path).join('\n')}>
          +{chat.projects.length} project{chat.projects.length > 1 ? 's' : ''}
        </button>
      {/if}
    </div>
    <!-- The session's actions, one tap each instead of buried in a menu. Each
         carries a label (desktop) and its own colour so loop and schedule read
         at a glance; a phone drops to coloured icons to fit beside the path.
         Close is icon-only and alert-red so a terminal action stands apart. -->
    <div class="actions">
      <button class="abtn add" onclick={() => (addProjOpen = true)} aria-label="Add project" title="Add another project to this session">
        <span class="ic"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" /><path d="M12 11v4M10 13h4" /></svg></span>
        <span class="albl">Add project</span>
      </button>
      {#if chat.loop?.state === 'running'}
        <button class="abtn loop on" onclick={() => chat.stopLoop()} aria-label="Stop the loop" title="Stop the loop">
          <span class="ic"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 11-3-6.7" /><path d="M21 3v5h-5" /></svg></span>
          <span class="albl">Stop</span>
        </button>
      {:else}
        <button class="abtn loop" onclick={() => (loopOpen = true)} aria-label="Run in a loop" title="Run this prompt in a loop">
          <span class="ic"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 11-3-6.7" /><path d="M21 3v5h-5" /></svg></span>
          <span class="albl">Loop</span>
        </button>
      {/if}
      <button class="abtn sched" onclick={() => (schedOpen = true)} aria-label="Schedule a prompt" title="Schedule a prompt for later">
        <span class="ic"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="13" r="8" /><path d="M12 9v4l2.5 1.5" /><path d="M5 3L2 6M22 6l-3-3" /></svg></span>
        <span class="albl">Schedule</span>
      </button>
      <button class="abtn close" onclick={() => app.closeSessionActive()} aria-label="Close session" title="Close this session">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4v8" /><path d="M18 7.5a8 8 0 11-12 0" /></svg>
      </button>
    </div>
  </header>

  <div class="scroll" bind:this={scroller} onscroll={onScroll}>
    <!-- Wait for the backlog to fully arrive, then mount it in one paint (see the
         init effect). This is what keeps opening a long session smooth. -->
    {#if chat.ready}
      {#if chat.items.length === 0 && !chat.streaming && !chat.thinking && !running}
        <div class="blank">
          <p class="b1">{chat.cwd.split('/').slice(-1)[0] || 'session'}</p>
          <p class="b2 mono">{chat.cwd}</p>
          <p class="b3">Send a message to start.</p>
        </div>
      {/if}
      <div class="log">
        {#each turns as turn, ti (firstVisible + ti)}
          {@const live = firstVisible + ti === allTurns.length - 1 && (running || !!chat.streaming || !!chat.thinking)}
          {#if turn.project}
            <div class="turn"><ProjectCard project={turn.project} /></div>
          {/if}
          {#if turn.loop}
            <div class="turn"><LoopCard loop={turn.loop} /></div>
          {/if}
          {#if turn.compact}
            <div class="turn">
              <CompactDivider
                preTokens={turn.compact.preTokens}
                postTokens={turn.compact.postTokens}
                trigger={turn.compact.trigger}
              />
            </div>
          {/if}
          {#if turn.user !== undefined}
            <div class="turn user">
              <div class="ubbl">
                {#if turn.userFiles?.length}
                  <FileChips files={turn.userFiles} />
                {/if}
                {#if turn.user}<span class="utext">{turn.user}</span>{/if}
              </div>
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
                  <TurnFooter {turn} />
                {/if}
              </div>
            </div>
          {/if}
          <!-- What this query changed, right under the reply that changed it.
               Self-hides when the turn edited no files. -->
          <TurnChanges {turn} />
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
    {/if}
  </div>

  {#if !atBottom}
    <button class="jump" style="bottom: {dockH + 14}px" onclick={() => toBottom(true)} aria-label="Scroll to latest">
      <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14M6 13l6 6 6-6" /></svg>
    </button>
  {/if}

  <PermissionGate {chat} />

  {#if addProjOpen}
    <div class="floater">
      <AddProject {chat} onClose={() => (addProjOpen = false)} />
    </div>
  {/if}

  {#if loopOpen}
    <div class="floater">
      <LoopSheet {chat} onClose={() => (loopOpen = false)} />
    </div>
  {/if}

  {#if schedOpen}
    <div class="floater">
      <ScheduleAfter
        machineId={app.activeMachineId ?? ''}
        sessionId={app.activeId ?? ''}
        cwd={chat.cwd}
        window={chat.rateLimit?.window ?? 'five_hour'}
        resetsAt={chat.rateLimit?.resetsAt ?? 0}
        onClose={() => (schedOpen = false)}
      />
    </div>
  {:else if chat.rateLimit?.limited}
    <div class="ratebanner">
      <span class="rl">Rate-limited · {chat.rateLimit.window === 'seven_day' ? 'weekly' : '5-hour'} quota resets in {resetRel(chat.rateLimit.resetsAt)}</span>
      <button onclick={() => (schedOpen = true)}>Schedule after reset</button>
    </div>
  {/if}

  <div class="dock" bind:clientHeight={dockH}>
    <LoopBar {chat} />
    <Queued {chat} />
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
        enterkeyhint={isTouch ? 'enter' : 'send'}
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
        <div class="modewrap">
          <button class="mode" onclick={() => (effortOpen = !effortOpen)} title="Reasoning effort (restarts the session)">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><rect x="3" y="13" width="4" height="8" rx="1" /><rect x="10" y="8" width="4" height="13" rx="1" /><rect x="17" y="3" width="4" height="18" rx="1" /></svg>
            {effortLabel(chat.effort)}
          </button>
          {#if effortOpen}
            <button class="mode-scrim" onclick={() => (effortOpen = false)} aria-label="Close"></button>
            <div class="mode-pop">
              <div class="pop-note">Restarts the session (resumes the conversation).</div>
              {#each EFFORTS as e (e.id)}
                <button
                  class:active={chat.effort === e.id}
                  onclick={() => { if (chat.effort !== e.id) app.restartWithEffort(e.id); effortOpen = false }}
                >
                  <span class="ml">{e.label}</span>
                  {#if e.hint}<span class="mh">{e.hint}</span>{/if}
                </button>
              {/each}
            </div>
          {/if}
        </div>
        {#if accounts.length > 1}
          <div class="modewrap">
            <button class="mode" onclick={() => (accountOpen = !accountOpen)} title="Claude account (restarts the session)">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="8" r="3.6" /><path d="M5 21v-1a6 6 0 016-6h2a6 6 0 016 6v1" /></svg>
              {chat.cli || accounts[0]}
            </button>
            {#if accountOpen}
              <button class="mode-scrim" onclick={() => (accountOpen = false)} aria-label="Close"></button>
              <div class="mode-pop">
                <div class="pop-note">Switches the account and resumes here. The new account re-reads the conversation once.</div>
                {#each accounts as a (a)}
                  <button class:active={(chat.cli || accounts[0]) === a} onclick={() => { if ((chat.cli || accounts[0]) !== a) app.switchAccount(a); accountOpen = false }}>
                    <span class="ml">{a}</span>
                  </button>
                {/each}
              </div>
            {/if}
          </div>
        {/if}
        <span class="spacer"></span>
        <Context
          tokens={chat.contextTokens}
          model={chat.model}
          onCompact={() => chat.sendPrompt('/compact')}
        />
        <!-- While a turn runs you can still send: it queues behind it. Stop stays
             alongside, so stopping and stacking up work are separate choices. -->
        {#if running}
          <button class="stop" onclick={() => chat.interrupt()} aria-label="Stop"><span class="sq"></span></button>
        {/if}
        <button
          class="send"
          class:ready={draft.trim() || attachments.length}
          onclick={send}
          disabled={(!draft.trim() && attachments.length === 0) || chat.status !== 'online'}
          aria-label={running ? 'Queue message' : 'Send'}
          title={running ? 'Queue this for when the current turn finishes' : 'Send'}>↑</button>
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
    /* The tab strip sits above and already clears the status bar, so the header
       must not inset again or it double-spaces. */
    padding: 12px 14px;
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
  /* The session's action row: labelled, colour-coded pills on desktop so loop
     and schedule are unmistakable; each icon keeps its own hue. */
  .actions {
    flex: none;
    display: flex;
    align-items: center;
    gap: 5px;
  }
  .abtn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 32px;
    padding: 0 12px 0 9px;
    border-radius: 100px;
    background: var(--panel);
    border: 1px solid var(--border);
    color: var(--text-2);
    font-size: 12.5px;
    font-weight: 500;
    cursor: pointer;
  }
  .abtn:hover {
    border-color: var(--border-2);
    color: var(--text);
  }
  .abtn .ic {
    display: flex;
  }
  /* One hue per action, so the row reads by colour as well as shape. */
  .abtn.add .ic {
    color: #8698ad;
  }
  .abtn.loop .ic {
    color: var(--busy);
  }
  .abtn.sched .ic {
    color: #a08ac0;
  }
  .abtn.close {
    color: var(--alert);
    padding: 0;
    width: 32px;
    justify-content: center;
  }
  /* A loop is running: the toggle both signals (amber fill) and stops it. */
  .abtn.loop.on {
    color: var(--busy);
    border-color: rgba(198, 161, 94, 0.4);
    background: rgba(198, 161, 94, 0.1);
  }
  .abtn.loop.on:hover {
    border-color: rgba(198, 161, 94, 0.6);
  }
  /* A phone header can't hold four labelled pills beside the path, so there the
     actions drop to coloured icon buttons — the hue and clearer glyphs carry
     the meaning. */
  @media (max-width: 860px) {
    .abtn {
      width: 34px;
      height: 34px;
      padding: 0;
      justify-content: center;
      border-radius: 50%;
    }
    .abtn.close {
      width: 34px;
    }
    .albl {
      display: none;
    }
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
  /* Paths keep their tail visible (rtl trick); plaintext stops the leading
     slash jumping to the end. */
  .tpath {
    font-size: 12px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    unicode-bidi: plaintext;
    text-align: left;
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
    display: flex;
    flex-direction: column;
    gap: 9px;
    color: var(--text);
    font-size: 16px;
    line-height: 1.5;
    padding: 12px 16px;
    background: var(--panel-3);
    border-radius: 18px;
    border-bottom-right-radius: 6px;
  }
  /* The bubble is a column now so attached files can sit above the message; the
     text keeps its own wrapping. */
  .utext {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
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

  .ratebanner {
    max-width: 720px;
    margin: 0 auto;
    width: 100%;
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 9px 20px;
    font-size: 12.5px;
    color: var(--text-2);
  }
  .ratebanner .rl {
    flex: 1;
    min-width: 0;
  }
  .ratebanner button {
    flex: none;
    padding: 7px 13px;
    border-radius: 100px;
    background: var(--panel-3);
    border: 1px solid var(--border-2);
    color: var(--text);
    font-size: 12.5px;
    font-weight: 550;
  }
  .ratebanner button:hover {
    background: var(--panel-2);
  }
  /* The sheets (add-project, loop, schedule) rise from the composer as a
     floating card in its own lane — not a full-width band. Each component's
     root gets the card; the wrapper only provides the gutter. */
  .floater {
    padding: 0 16px 6px;
  }
  @media (min-width: 861px) {
    .floater {
      padding: 0 24px 8px;
    }
  }
  .floater > :global(*) {
    max-width: 720px;
    margin: 0 auto;
    background: var(--panel);
    border: 1px solid var(--border-2);
    border-radius: var(--r-lg);
    box-shadow: 0 14px 44px -16px rgba(0, 0, 0, 0.72);
    animation: floatUp 0.16s ease-out both;
  }
  @keyframes floatUp {
    from {
      opacity: 0;
      transform: translateY(8px);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .floater > :global(*) {
      animation: none;
    }
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
    /* Start a couple of lines tall so the box has room to think in, then grow
       with the text up to the cap. The JS auto-size sets an inline height; this
       floor keeps an empty composer comfortably sized. */
    min-height: 58px;
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
  .pop-note {
    padding: 6px 11px 8px;
    margin-bottom: 4px;
    font-size: 11px;
    color: var(--text-4);
    border-bottom: 1px solid var(--border);
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
  .stop {
    margin-right: 6px;
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
