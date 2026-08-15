<script lang="ts">
  import { tick, setContext, untrack } from 'svelte'
  import { app } from '../lib/app.svelte'
  import { toasts } from '../lib/toast.svelte'
  import { FILE_BASE, fileBaseFor } from '../lib/filebase'
  import { uploadFile, getProviderModels, getShare } from '../lib/api'
  import type { ChatConnection } from '../lib/chat.svelte'
  import type { Attachment, Share } from '../lib/types'
  import { groupTurns } from '../lib/turns'
  import { MODELS, EFFORTS, modelLabel, modelOptionLabel, modelFamily, effortLabel } from '../lib/models'
  import { BYPASS, PERMISSION_MODES, permissionLabel } from '../lib/permissions'
  import type { PermissionMode } from '../lib/types'
  import { setReloadGuard } from '../lib/updater'
  import { visited } from '../lib/visited.svelte'
  import PermissionGate from './PermissionGate.svelte'
  import Context from './Context.svelte'
  import Queued from './Queued.svelte'
  import Previews from './Previews.svelte'
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
  import LiveActivity from './LiveActivity.svelte'
  import Tabs from './Tabs.svelte'
  import SessionInfo from './SessionInfo.svelte'
  import WorktreeCard from './WorktreeCard.svelte'
  import TurnFooter from './TurnFooter.svelte'
  import TurnRail from './TurnRail.svelte'
  import TurnChanges from './TurnChanges.svelte'
  import ShareDialog from './ShareDialog.svelte'
  import Hint from './Hint.svelte'

  let { chat }: { chat: ChatConnection } = $props()

  // An error the server sent is raised as a toast rather than appended to the
  // log, and that is a fix for two things at once.
  //
  // It used to render as a red line after the last turn, INSIDE the scrolling
  // area, so a refusal you needed to see (the server declining to enter Yolo on
  // a shared session, say) arrived wherever you happened to be scrolled to. And
  // it was never cleared, so it stayed at the end of the conversation for the
  // rest of the session, long after it stopped being true.
  //
  // Cleared as it is raised, which makes it a one-shot: the toast now owns how
  // long it lives, and an error is the one kind that waits to be dismissed.
  $effect(() => {
    const line = chat.errorLine
    if (!line) return
    chat.errorLine = ''
    toasts.error(line)
  })

  // An image path the agent wrote resolves against THIS session on ITS machine.
  // Published once here, read wherever markdown is rendered (see lib/filebase).
  // A getter, so a component set up under one session cannot keep serving
  // another's id after the prop changes.
  setContext(FILE_BASE, () => fileBaseFor(chat.origin, chat.sessionId))

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
  // A finding picked in the review view arrives here as the start of a message,
  // so "why do you think this?" carries its subject rather than making you
  // retype the file and line. Consumed once: it is a handoff, not a setting.
  $effect(() => {
    if (!app.reviewAsk) return
    draft = app.reviewAsk
    app.reviewAsk = ''
    queueMicrotask(() => textarea?.focus())
  })
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
  let infoOpen = $state(false)
  let shareOpen = $state(false)
  // The session's link, when it has one. Kept here rather than fetched by the
  // dialog so the button can show that this session is shared without the dialog
  // being open, which is the difference between a share you remember and one you
  // forgot about.
  let share = $state<Share | null>(null)
  $effect(() => {
    const id = chat.sessionId
    getShare(chat.origin, id)
      .then((s) => {
        if (chat.sessionId === id) share = s
      })
      .catch(() => {})
  })

  // Ending a session is destructive (it stops the running turn), and the button
  // is one tap in a busy row, so it arms first: one tap turns it solid red, a
  // second within a few seconds actually closes. It disarms itself if you don't
  // follow through, so a stray tap never ends a session.
  let armClose = $state(false)
  let armTimer: ReturnType<typeof setTimeout> | undefined
  function closeClicked() {
    if (armClose) {
      clearTimeout(armTimer)
      armClose = false
      app.closeSessionActive()
    } else {
      armClose = true
      clearTimeout(armTimer)
      armTimer = setTimeout(() => (armClose = false), 3000)
    }
  }

  // Watching a turn finish is reading it: without this, a session you sat and
  // watched complete would light up "Done · unread" in the sidebar the moment
  // it ended, claiming you missed something you were looking straight at. Only
  // stamped while this tab is the active one and the page is actually visible,
  // because a backgrounded tab is exactly the "away" the unread mark exists for.
  // The turn's own state is the ONLY thing this depends on; the rest is read
  // untracked. An effect whose job is a side effect should name its trigger
  // rather than accidentally subscribing to everything it touches on the way.
  $effect(() => {
    const state = chat.sessionState
    if (state === 'running' || state === 'starting') return
    untrack(() => {
      if (document.visibilityState !== 'visible') return
      if (app.activeId !== chat.sessionId || !app.activeMachineId) return
      visited.touch(app.activeMachineId, chat.sessionId)
    })
  })

  // The rate-limit banner is a statement about a clock, so it has to obey one:
  // `now` ticks so the countdown moves, and so the banner leaves on its own the
  // moment the reset passes. Without this it froze at whatever the last event
  // happened to render ("resets in 0m", for ever).
  let now = $state(Date.now())
  $effect(() => {
    const t = setInterval(() => (now = Date.now()), 30_000)
    return () => clearInterval(t)
  })
  // Limited, and the reset has not passed yet. A wall whose reset time is
  // unknown (0) stays up until a completed turn proves it over.
  const rateLimitedNow = $derived(
    !!chat.rateLimit?.limited && (chat.rateLimit.resetsAt === 0 || now / 1000 < chat.rateLimit.resetsAt),
  )

  function resetRel(unixSec: number): string {
    let s = Math.round(unixSec - now / 1000)
    if (s < 0) s = 0
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    return h ? `${h}h ${m}m` : `${m}m`
  }
  // Whether the live activity stream is expanded. Held here rather than inside
  // the component so it survives the turn's re-renders: a disclosure that closed
  // itself every time a tool call came back would be unusable exactly while you
  // were trying to read it.
  //
  // Closed while the query runs, and that is the point rather than a default
  // nobody revisited.
  //
  // Opening it showed every call at once, which is a growing column of commands
  // where only one of them is happening -- the thing you want to know ("what is
  // it doing NOW") buried in a list of what it already did. Collapsed, the head
  // IS that answer: the current call, named, with a count of what came before.
  // The rest is the record of how the answer was reached, and it belongs behind
  // the click, either here mid-turn or in the ToolGroup summary afterwards.
  let liveOpen = $state(false)
  // Closed again for each new query, so opening it mid-turn to watch something
  // is respected for that turn and forgotten by the next. Keyed on how many
  // turns there are rather than on `running`, because a turn's blocks change
  // constantly while it works and only a NEW turn should reset the choice.
  const liveTurnKey = $derived(allTurns.length)
  $effect(() => {
    void liveTurnKey
    liveOpen = false
  })
  let modeOpen = $state(false)
  let modelOpen = $state(false)
  let effortOpen = $state(false)
  let accountOpen = $state(false)
  // Claude accounts available on this session's machine (first is the default).
  const accounts = $derived(app.machines.find((m) => m.id === app.activeMachineId)?.stats?.clis ?? [])
  // When the session runs on a proxy provider, the model chip should read the
  // provider's real model (e.g. gpt-5.5), not the Claude slot it was spawned
  // under (Opus). Switching Claude tiers is meaningless there (every slot maps
  // to the same model), so the chip becomes a plain label, not a dropdown.
  const providerModel = $derived(
    app.machines.find((m) => m.id === app.activeMachineId)?.stats?.provider_models?.[chat.cli] ?? '',
  )
  // The provider model chip is a live picker: it lists the provider's own models
  // (the sidecar's list, filtered to this model's family) and respawns the
  // session on the pick, so a bad default (e.g. a model the account can't use)
  // is recoverable in one tap.
  let pmOpen = $state(false)
  let pmModels = $state<string[]>([])
  let pmFor = $state('') // the cli pmModels was fetched for, so a provider change refetches
  let pmBusy = $state(false)
  async function openProviderModels() {
    pmOpen = !pmOpen
    // Refetch when the list is empty OR belongs to another provider. The chip is a
    // live $derived, but pmModels was cached forever, so viewing a Codex session and
    // then a Grok one showed the stale gpt list under a grok-4.5 chip.
    if (!pmOpen || (pmModels.length && pmFor === chat.cli)) return
    try {
      pmFor = chat.cli
      const all = await getProviderModels(app.baseForMachine(app.activeMachineId ?? ''), chat.cli)
      const fam = (providerModel.match(/^[a-zA-Z]+/)?.[0] ?? '').toLowerCase()
      // Scope to this model's own family. Never fall back to the full list: a
      // cross-family pick (e.g. grok-4.5 on a Codex session) rewrites the shared
      // provider mapping and reroutes every Codex session to Grok — the bug that
      // sent a Codex provider to the grok proxy. Empty means "no alternatives".
      pmModels = fam ? all.filter((m) => m.toLowerCase().startsWith(fam)) : all
    } catch {
      pmModels = []
    }
  }
  async function pickProviderModel(m: string) {
    pmOpen = false
    if (m === providerModel || pmBusy) return
    pmBusy = true
    // Through the app, not straight to the API: the server respawns the session
    // to bake the new model into its env, so the connection has to be rebuilt and
    // a failure has to reach the bar (see App.switchProviderModel).
    await app.switchProviderModel(m)
    pmBusy = false
  }

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
      } catch (e) {
        // Said, not swallowed. The server refuses what the API cannot read -- a
        // HEIC photo, an AVIF, something too large -- with a sentence that names
        // the format and what to do about it, and this used to drop that on the
        // floor: the file simply never appeared, and sending it again did the
        // same nothing.
        toasts.error(`Could not attach ${f.name}`, (e as Error).message)
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

  // One table, shared with the worktree dialog, so switching a running session
  // and spawning one cannot describe the same mode two different ways.
  const modes = PERMISSION_MODES
  // The pill has room for a word, not a label: "Accept edits" is the mode, but
  // "Edits" is what fits beside the model and the effort.
  const modeLabels: Record<string, string> = { acceptEdits: 'Edits', bypassPermissions: 'Yolo' }
  const modePill = (id: string) => modeLabels[id] ?? permissionLabel(id)

  // Yolo mode is worn by the composer, not just recorded in a pill.
  //
  // A permission mode is a property of the next thing you send, and the composer
  // is where you send it from, so that is where it belongs. The pill was already
  // the honest answer to "what mode is this session in" and it was still the
  // wrong one to rely on: it is one word among four, in the corner, and the state
  // it reports is the one where a mistake cannot be taken back. Colouring what
  // you are typing into means you cannot compose a message without seeing it.
  const yolo = $derived(chat.mode === BYPASS)

  const running = $derived(chat.sessionState === 'running')

  function hasBody(blocks: { type: string; text?: string }[]): boolean {
    return blocks.some(
      (b) => (b.type === 'text' && !!b.text) || b.type === 'tool_use' || (b.type === 'thinking' && !!b.text),
    )
  }
</script>

<div class="screen">
  <!-- One tidy line: the way out (back/home), the open sessions (Tabs), then the
       session's actions. Everything you act on, nothing to merely read: the cwd,
       branch, account and projects moved behind the info button into SessionInfo,
       so the chrome is a single 40px row. It is the topmost element, so it owns
       the phone safe-area. -->
  <header>
    {#if !app.sidebarOpen}
      <!-- The only way back to an open sidebar once it is collapsed: the
           dashboard's rail toggle is not on screen while a chat is up. Desktop
           only (the phone sidebar is not collapsible). -->
      <button class="hbtn rail deskonly" onclick={() => app.toggleSidebar()} aria-label="Show sidebar" title="Show sidebar">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2.5" /><path d="M9.5 4v16" /></svg>
      </button>
    {/if}
    <button class="hbtn back" onclick={() => app.back()} aria-label="Back">
      <svg width="10" height="16" viewBox="0 0 10 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M8 1L2 8l6 7" /></svg>
    </button>
    <button class="hbtn home deskonly" onclick={() => app.back()} aria-label="Home" title="Home">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M3 10.5L12 3l9 7.5" /><path d="M5 9.5V20a1 1 0 001 1h4v-6h4v6h4a1 1 0 001-1V9.5" /></svg>
    </button>
    <Tabs />
    <!-- The session's actions, one tap each instead of buried in a menu. Each
         carries a label (desktop) and its own colour so loop and schedule read
         at a glance; a phone drops to coloured icons. Close is icon-only and
         alert-red, set apart by a hairline, so a terminal action stands out. -->
    <div class="actions">
      <!-- The way back to a review's findings.
           Without it, arguing with the reviewer was a one-way door: "Ask about
           this" swapped the findings for the transcript and nothing anywhere
           swapped them back, so the only way to return to the thing you were
           deciding about was to close the session and reopen it. app.reviewChat
           is only ever true for a review (it is reset whenever the session
           changes), so its truth is the whole condition and Chat needs to know
           nothing else about reviews. -->
      {#if app.reviewChat}
        <button
          class="abtn findings"
          onclick={() => (app.reviewChat = false)}
          aria-label="Back to the findings"
          title="Back to the findings"
        >
          <span class="ic"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6h11M9 12h11M9 18h11" /><path d="M4 6l1 1 2-2" /><path d="M4 12l1 1 2-2" /><path d="M4 18l1 1 2-2" /></svg></span>
          <span class="albl">Findings</span>
        </button>
      {/if}
      <!-- Shared is a state, not just an action, so the button says so at rest:
           a link you forgot you made is the failure this feature has to avoid. -->
      <button
        class="abtn share"
        class:on={!!share}
        onclick={() => (shareOpen = true)}
        aria-label={share ? 'This session is shared' : 'Share this session'}
        title={share ? 'Shared by link — open to manage or stop it' : 'Share this session by link'}
      >
        <span class="ic"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M4 12v7a2 2 0 002 2h12a2 2 0 002-2v-7" /><path d="M16 6l-4-4-4 4" /><path d="M12 2v14" /></svg></span>
        <span class="albl">{share ? 'Shared' : 'Share'}</span>
      </button>
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
      <!-- Where it runs, what branch, which account, the projects: reference, not
           action, so it hides behind one button instead of taking a row. -->
      <button class="abtn info" class:on={infoOpen} onclick={() => (infoOpen = !infoOpen)} aria-label="Session details" title="Folder, branch, account, projects">
        <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9" /><path d="M12 16v-4M12 8h.01" /></svg>
      </button>
      <span class="asep" aria-hidden="true"></span>
      <button
        class="abtn close"
        class:armed={armClose}
        onclick={closeClicked}
        aria-label={armClose ? 'Tap again to close the session' : 'Close session'}
        title={armClose ? 'Tap again to end this session' : 'Close this session'}
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4v8" /><path d="M18 7.5a8 8 0 11-12 0" /></svg>
      </button>
    </div>
    {#if infoOpen}
      <SessionInfo {chat} onClose={() => (infoOpen = false)} />
    {/if}
  </header>

  <!-- A session in a worktree says so, and offers the ways out of it. It sits
       under the chrome rather than inside SessionInfo because merging and
       discarding are actions, and the header holds what you act on. It renders
       nothing at all for an ordinary session, so nothing changes for one. -->
  <WorktreeCard base={chat.apiBase} cwd={chat.cwd} />

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
        <!-- A review's findings used to be pinned here too, in a second
             implementation of the same card. Two surfaces for one thing is how
             they drift, and these had: the route grew severity, bulk actions and
             editing while this one stayed as it was. A review session opens on
             ReviewView (see App.svelte); this pane is now only the conversation
             you switch to in order to argue with it, which is the one thing a
             chat is actually for here. -->
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
            <!-- data-turn anchors the rail's jump target. Queried rather than
                 held as a ref, because the log is windowed and re-keys as older
                 turns are revealed. -->
            <div class="turn user" data-turn={firstVisible + ti}>
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
                  <!-- The reply first, the running activity underneath it.
                       The order is the point. What the agent has SAID so far is
                       the thing being read, so it stays where reading starts and
                       grows downward as prose does; the activity belongs at the
                       bottom edge, next to "Working…", which is where the eye
                       already is and where the newest call appears. Above the
                       reply it was chrome in front of the content, and
                       interleaved with it (which is what this used to do) a
                       paragraph and a command took turns shoving the page down.
                       It collapses up into the ToolGroup summary when the turn
                       ends; see liveOpen. -->
                  {#each turn.blocks.filter((b) => b.type !== 'tool_use') as b, j (j)}
                    <BlockView block={b} {chat} />
                  {/each}
                  <LiveActivity blocks={turn.blocks} {chat} bind:open={liveOpen} />
                {:else}
                  {#if turn.toolCalls > 0}
                    <ToolGroup {turn} {chat} />
                  {/if}
                  {#each turn.answer as b, j (j)}
                    <BlockView block={b} {chat} />
                  {/each}
                  <TurnFooter {turn} isProvider={!!providerModel} />
                {/if}
              </div>
            </div>
          {/if}
          <!-- What this query changed, right under the reply that changed it.
               Self-hides when the turn edited no files. Reverting is not here:
               it is one control in the turn footer, because a revert resets the
               whole repository and this card lists only this turn's files. -->
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

      </div>
    {/if}
  </div>

  <!-- A way back to a question you asked, down the edge of the log. Positioned
       the way the jump button is: absolute within .screen, clearing the composer
       via the measured dock height, because .scroll itself scrolls and anything
       inside it would scroll away with the log it indexes. Outside the
       !atBottom guard, since the whole point is to leave the bottom.
       Self-hides below a handful of prompts and on a phone; see TurnRail. -->
  <TurnRail {turns} {scroller} {firstVisible} />

  {#if !atBottom}
    <button class="jump" style="bottom: {dockH + 14}px" onclick={() => toBottom(true)} aria-label="Scroll to latest">
      <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14M6 13l6 6 6-6" /></svg>
    </button>
  {/if}

  <PermissionGate {chat} />

  <!-- A centred modal rather than an in-chat sheet: this is configuration with
       several decisions in it, not a chat action, and it has to be readable
       while you decide what a stranger may do. -->
  {#if shareOpen}
    <ShareDialog
      base={chat.origin}
      sessionId={chat.sessionId}
      title={chat.title}
      existing={share}
      onclose={() => (shareOpen = false)}
      onchange={(s) => (share = s)} />
  {/if}

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
  {:else if chat.failover === 'deciding'}
    <!-- A failover takes seconds, because deciding where to go means reading each
         account's quota. This is the line whose absence made a failover that
         worked look like one that never fired: the composer named the walled
         account the whole time and nothing said otherwise. It takes the banner's
         slot rather than adding a second bar, because it is the same news. -->
    <div class="ratebanner">
      <span class="rl">
        <span class="fospin" aria-hidden="true"></span>
        Out of quota · finding an account with headroom…
      </span>
    </div>
  {:else if rateLimitedNow && chat.rateLimit}
    <div class="ratebanner">
      <span class="rl">Rate-limited · {chat.rateLimit.window === 'seven_day' ? 'weekly' : '5-hour'} quota resets in {resetRel(chat.rateLimit.resetsAt)}</span>
      <button onclick={() => (schedOpen = true)}>Schedule after reset</button>
    </div>
  {:else if chat.failoverNote}
    <!-- Tried and could not. Worth its own line, because "rate-limited" alone
         cannot tell you whether kunai looked for somewhere else to go. -->
    <div class="ratebanner">
      <span class="rl">{chat.failoverNote}</span>
      <button onclick={() => (chat.failoverNote = '')} aria-label="Dismiss">✕</button>
    </div>
  {/if}

  <div class="dock" bind:clientHeight={dockH}>
    <!-- Why the last thing you asked for did not happen. It belongs here, beside
         the controls that asked, rather than in the sidebar where it used to go
         and where you cannot see it from a chat. -->
    {#if app.actionError}
      <div class="actionbar">
        <span class="etext">{app.actionError}</span>
        <button class="ex" onclick={() => (app.actionError = '')} aria-label="Dismiss">✕</button>
      </div>
    {/if}

    <LoopBar {chat} />
    <!-- What the agent is running, if anything: a dev server it started, and a
         way to reach it from whatever device you are holding. -->
    <Previews base={chat.apiBase} sessionId={chat.sessionId} />
    <Queued {chat} />
    <!-- A session that no longer exists says so INSIDE the composer, because the
         composer is the thing that has stopped working. It was a bordered strip
         above it, which competed with the field, ran wider than it (the field is
         720px centred and the strip was not), and left the model, effort and
         account controls on show underneath doing nothing. Replacing the field's
         contents states it once and removes every control that would lie. -->
    <div
      class="field"
      class:dead={chat.status === 'gone'}
      class:yolo
      class:stacked={chat.queued.length > 0 && chat.status !== 'gone'}
    >
      {#if chat.status === 'gone'}
        <div class="deadrow">
          <div class="deadtext">
            <p class="dhead">This session has ended</p>
            <p class="dsub">kunai restarted, which closes running sessions. The conversation is saved.</p>
          </div>
          <button class="dbtn" onclick={() => app.reopenActive()}>Reopen it</button>
        </div>
      {:else}
      {#if attachments.length}
        <div class="chips">
          {#each attachments as a (a.id)}
            <span class="chip">
              <span class="cn mono">{a.name}</span>
              {#if a.note}<span class="cnote">{a.note}</span>{/if}
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
        <div class="controls">
          <div class="modewrap">
            <button class="seg" class:on={chat.mode !== 'default'} class:open={modeOpen} onclick={() => (modeOpen = !modeOpen)} title="Permission mode">
              {modePill(chat.mode)}
            </button>
            {#if modeOpen}
              <button class="mode-scrim" onclick={() => (modeOpen = false)} aria-label="Close"></button>
              <div class="mode-pop">
                {#each modes as m (m.id)}
                  <!-- The row is a wrapper, not the button, so a mode that needs
                       a longer explanation can carry an info affordance beside
                       its label without nesting one control inside another. -->
                  <div class="mrow" class:grave={m.grave}>
                    <button
                      class="mopt"
                      class:active={chat.mode === m.id}
                      onclick={() => { chat.setMode(m.id); modeOpen = false }}
                    >
                      <span class="ml">{m.label}</span>
                      <span class="mh">{m.hint}</span>
                    </button>
                    {#if m.more}
                      <Hint title={m.label} body={m.more}>
                        <button class="minfo" aria-label="About {m.label}">
                          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9" /><path d="M12 16v-4.5M12 8.2v.2" /></svg>
                        </button>
                      </Hint>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}
          </div>
          <div class="modewrap">
            {#if providerModel}
              <button class="seg mono" class:open={pmOpen} onclick={openProviderModels} title="Model served by this provider">
                <span class="segtext">{providerModel}</span>
              </button>
              {#if pmOpen}
                <button class="mode-scrim" onclick={() => (pmOpen = false)} aria-label="Close"></button>
                <div class="mode-pop">
                  {#if pmModels.length}
                    {#each pmModels as m (m)}
                      <button class:active={m === providerModel} onclick={() => pickProviderModel(m)}>
                        <span class="ml mono">{m}</span>
                      </button>
                    {/each}
                  {:else}
                    <div class="pop-note">Loading models…</div>
                  {/if}
                </div>
              {/if}
            {:else}
              <button class="seg" class:open={modelOpen} onclick={() => (modelOpen = !modelOpen)} title="Model">
                {modelLabel(chat.model)}
              </button>
              {#if modelOpen}
                <button class="mode-scrim" onclick={() => (modelOpen = false)} aria-label="Close"></button>
                <div class="mode-pop">
                  {#each MODELS as m (m.id)}
                    <button
                      class:active={modelFamily(chat.model) === m.id}
                      onclick={() => { chat.setModel(m.id); modelOpen = false }}
                    >
                      <span class="ml">{modelOptionLabel(m.id)}</span>
                      {#if m.hint}<span class="mh">{m.hint}</span>{/if}
                    </button>
                  {/each}
                </div>
              {/if}
            {/if}
          </div>
          <div class="modewrap">
            <button class="seg" class:open={effortOpen} onclick={() => (effortOpen = !effortOpen)} title="Reasoning effort (restarts the session)">
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
              <button class="seg" class:open={accountOpen} onclick={() => (accountOpen = !accountOpen)} title="Claude account (restarts the session)">
                <span class="segtext">{chat.cli || accounts[0]}</span>
              </button>
              {#if accountOpen}
                <button class="mode-scrim" onclick={() => (accountOpen = false)} aria-label="Close"></button>
                <div class="mode-pop right">
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
        </div>
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
      {/if}
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
  /* One row: back/home, the tab strip (flexes and scrolls), then the actions.
     Topmost chrome, so it owns the phone safe-area. */
  header {
    position: relative;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: calc(var(--safe-top) + 7px) 12px 8px 10px;
    background: transparent;
  }
  /* The divider the header sits on: a hairline that fades at both ends, so the
     compact chrome reads as a seam over the canvas rather than a hard rule. */
  header::after {
    content: '';
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    height: 1px;
    background: var(--border-2);
    -webkit-mask-image: linear-gradient(to right, transparent, #000 6%, #000 94%, transparent);
    mask-image: linear-gradient(to right, transparent, #000 6%, #000 94%, transparent);
  }
  /* Chrome buttons are ghosts: no chrome at rest, a panel fill on hover, so the
     row reads as the path plus a few quiet actions rather than a toolbar. */
  .hbtn {
    flex: none;
    width: 26px;
    height: 26px;
    border-radius: 8px;
    background: none;
    border: 1px solid transparent;
    color: var(--text-2);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .hbtn:hover {
    color: var(--text-2);
    background: var(--panel);
    border-color: var(--border);
  }
  /* The session's actions: quiet ghost buttons, each keeping its own icon hue so
     loop and schedule read by colour; a hairline sets the terminal action apart. */
  .actions {
    flex: none;
    display: flex;
    align-items: center;
    gap: 2px;
  }
  .asep {
    flex: none;
    width: 1px;
    height: 16px;
    margin: 0 4px;
    background: var(--border);
  }
  .abtn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 26px;
    padding: 0 10px 0 8px;
    border-radius: 7px;
    background: none;
    border: 1px solid transparent;
    color: var(--text-2);
    font-size: 12px;
    font-weight: 500;
    cursor: pointer;
  }
  .abtn:hover {
    background: var(--panel);
    border-color: var(--border);
    color: var(--text);
  }
  .abtn .ic {
    display: flex;
  }
  /* One hue per action, so the row reads by colour as well as shape. */
  /* The only NAVIGATION in this row, and it reads as one: white rather than a
     hue of its own, because it goes back to where you came from rather than
     doing something to the session. */
  .abtn.findings .ic {
    color: var(--text);
  }
  .abtn.add .ic {
    color: #8698ad;
  }
  .abtn.loop .ic {
    color: var(--busy);
  }
  .abtn.sched .ic {
    color: #a08ac0;
  }
  .abtn.share .ic {
    color: #6f9f8c;
  }
  /* A live share is the one action here whose state you need to see without
     opening it, so it holds a quiet surface at rest rather than only on hover. */
  .abtn.share.on {
    background: var(--panel);
    border-color: var(--border);
    color: var(--text-2);
  }
  /* The info button is a quiet neutral glyph until opened, when it fills like the
     others; it holds the session's context (folder, branch, account, projects). */
  .abtn.info {
    color: var(--text-3);
    padding: 0;
    width: 28px;
    justify-content: center;
  }
  .abtn.info.on {
    color: var(--text);
    background: var(--panel);
    border-color: var(--border);
  }
  .abtn.close {
    color: var(--alert);
    padding: 0;
    width: 28px;
    justify-content: center;
  }
  .abtn.close:hover {
    background: rgba(207, 111, 102, 0.12);
    border-color: transparent;
    color: var(--alert);
  }
  /* Armed: the next tap ends the session, so the button goes solid red to say so. */
  .abtn.close.armed,
  .abtn.close.armed:hover {
    background: var(--alert);
    border-color: transparent;
    color: #16181a;
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
      width: 32px;
      height: 32px;
      padding: 0;
      justify-content: center;
      border-radius: 9px;
    }
    .abtn.close {
      width: 32px;
    }
    .albl {
      display: none;
    }
    /* No room for the separator beside icon-only buttons on a phone. */
    .asep {
      display: none;
    }
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
  /* The same dashed ring the sidebar uses for a working session, on the same
     duty-cycled rotation: this is work in progress, and it is the one thing on
     screen saying so. */
  .fospin {
    display: inline-block;
    vertical-align: -1px;
    width: 10px;
    height: 10px;
    margin-right: 7px;
    border: 1.5px dashed currentColor;
    border-radius: 50%;
    animation: fospin 2.4s steps(12) infinite;
  }
  @keyframes fospin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .fospin {
      animation: none;
    }
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
  /* Why the last action failed, directly above the composer that asked. It is
     transient and dismissible, so unlike the ended state it stays a strip. It
     matches the field's width and centring, which the old one did not: the field
     is 720px centred and this was full-bleed, so it hung off both sides. */
  .actionbar {
    max-width: 720px;
    margin: 0 auto 8px;
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 9px 12px;
    background: var(--panel);
    border: 1px solid var(--alert);
    border-radius: var(--r);
  }
  .actionbar .etext {
    color: var(--alert);
  }
  .etext {
    flex: 1;
    min-width: 0;
    font-size: 12px;
    line-height: 1.45;
    color: var(--text-3);
  }
  .ex {
    flex: none;
    width: 22px;
    height: 22px;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--text-4);
    font-size: 12px;
    cursor: pointer;
  }
  .ex:hover {
    background: var(--panel-3);
    color: var(--text);
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
  /* Queued prompts sit directly on top, so the field gives up its own top arc and
     the two draw as one shape: the arc at the top of the stack, the arc at the
     bottom of the field, hairlines in between. The top border goes too, or the
     seam would be two adjacent 1px lines. */
  .field.stacked {
    border-top-left-radius: 0;
    border-top-right-radius: 0;
    border-top-color: var(--border);
  }
  /* Yolo mode, worn by the field you type into.
     The border and the typed text both carry it, because either alone is
     missable: a border is chrome your eye stops seeing after a minute, and text
     colour alone would not show on an empty composer. --yolo-ink is a brighter
     step than --busy (10.7:1 on the panel, against 4.5 needed) because this is
     prose being read, not a status dot being glanced at. */
  .field.yolo {
    border-color: var(--busy);
  }
  .field.yolo:focus-within {
    border-color: var(--yolo-ink);
  }
  .field.yolo textarea,
  .field.yolo textarea::placeholder {
    color: var(--yolo-ink);
  }
  .field.yolo textarea {
    caret-color: var(--yolo-ink);
  }
  /* The pill says the name; the field says the state. Both, because the name is
     what you learn it by and the colour is what you notice it by. */
  .field.yolo .seg.on {
    color: var(--yolo-ink);
  }
  /* The ended composer. Quieter than a live one, not louder: the border drops
     back to the faintest one available and the fill goes flat, so the thing you
     cannot type into recedes instead of announcing itself. Nothing here can take
     focus, so the focus-within lift is suppressed too. */
  .field.dead {
    background: transparent;
    border-color: var(--border);
    padding: 14px 16px;
  }
  .field.dead:focus-within {
    border-color: var(--border);
  }
  .deadrow {
    display: flex;
    align-items: center;
    gap: 16px;
  }
  .deadtext {
    flex: 1;
    min-width: 0;
  }
  .dhead {
    margin: 0;
    font-size: 13px;
    color: var(--text-2);
  }
  .dsub {
    margin: 3px 0 0;
    font-size: 12px;
    line-height: 1.45;
    color: var(--text-4);
  }
  /* Outlined, not filled. It is the only control left, so it does not need
     white to be found, and white here made a dead session the brightest thing
     on the screen. Reserved accent stays reserved. */
  .dbtn {
    flex: none;
    height: 30px;
    padding: 0 14px;
    border: 1px solid var(--border-2);
    border-radius: 9px;
    background: transparent;
    color: var(--text-2);
    font-size: 12px;
    cursor: pointer;
    transition:
      border-color 0.12s,
      color 0.12s,
      background 0.12s;
  }
  .dbtn:hover {
    background: var(--panel-2);
    border-color: var(--text-4);
    color: var(--text);
  }
  @media (max-width: 560px) {
    .deadrow {
      align-items: flex-start;
      flex-direction: column;
      gap: 12px;
    }
    .dbtn {
      align-self: stretch;
    }
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    padding: 2px 0 8px;
  }
  /* What was done to make it sendable, beside the name. Quiet: it is a fact to
     have, not a warning. */
  .cnote {
    color: var(--text-4);
    font-size: 11px;
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
  /* The session controls (mode / model / effort / account) read as one quiet
     strip of session state, not four competing capsules: text-only segments
     divided by hairlines, with the pill affordance appearing only on hover or
     while a segment's menu is open. */
  .controls {
    display: inline-flex;
    align-items: center;
    margin-left: 4px;
    min-width: 0; /* allow the strip to shrink so a long account name can't push send off-screen */
  }
  .modewrap {
    position: relative;
    display: inline-flex;
    align-items: center;
    min-width: 0;
  }
  /* Long dynamic labels (account name, provider model) truncate instead of
     overflowing the row. The account "claude-teams-max-shorya" used to shove the
     send button off the right edge on a phone. */
  .segtext {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 130px;
  }
  @media (max-width: 560px) {
    .segtext {
      max-width: 84px;
    }
  }
  .controls > .modewrap + .modewrap::before {
    content: '';
    width: 1px;
    height: 13px;
    margin: 0 3px;
    background: var(--border-2);
  }
  .seg {
    display: inline-flex;
    align-items: center;
    height: 30px;
    padding: 0 9px;
    border-radius: 8px;
    color: var(--text-3);
    font-size: 12.5px;
    font-weight: 500;
    white-space: nowrap;
    min-width: 0; /* let a truncating .segtext child shrink instead of overflowing */
    transition: color 0.12s, background 0.12s;
  }
  .seg:hover,
  .seg.open {
    color: var(--text);
    background: var(--panel-2);
  }
  .seg.on {
    color: var(--text);
    font-weight: 550;
  }
  /* A provider's model chip reads as data (mono), and its dropdown lists that
     provider's own models. */
  .seg.mono {
    font-family: var(--mono);
    font-size: 12px;
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
    min-width: 248px;
    /* Never exceed the viewport: the account name can be long, and on a phone a
       left-anchored menu ran off the right edge. */
    max-width: calc(100vw - 24px);
    padding: 5px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
  }
  /* The account segment is the rightmost control, so its menu opens leftward from
     the right edge instead of growing off-screen to the right. */
  .mode-pop.right {
    left: auto;
    right: 0;
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
  /* A row is a wrapper so a mode can carry an info button beside its label.
     Everything above still targets .mode-pop button, which the option inside the
     row still is, so the other menus in this component are untouched. */
  .mrow {
    display: flex;
    align-items: center;
    gap: 2px;
  }
  /* width:auto is load-bearing: .mode-pop button sets width:100%, which inside a
     flex row means 100% of the row and then the info button on top, so the label
     got squeezed into a two-line column. */
  .mrow .mopt {
    flex: 1;
    width: auto;
    min-width: 0;
  }
  .mrow .ml {
    white-space: nowrap;
  }
  /* Set apart by a rule rather than by colour: it is the last row and a different
     kind of choice, and painting it red would make the menu look like an error
     state every time it opens. */
  .mrow.grave {
    margin-top: 4px;
    padding-top: 4px;
    border-top: 1px solid var(--border);
  }
  .mrow.grave .mopt.active .ml,
  .mrow.grave:hover .ml {
    color: var(--busy);
  }
  /* The explanation, for the one mode whose one-line hint cannot carry it.
     Quiet at rest: it is there for the first time you meet this mode, not
     something to read past on every open.
     Scoped through .mrow deliberately: the generic `.mode-pop button` rule above
     sets width:100% and a column layout for the menu options, and it outranks a
     bare .minfo, so the icon took the whole row and squeezed the label into a
     one-word-per-line column. */
  .mrow .minfo {
    flex: none;
    width: 26px;
    height: 26px;
    padding: 0;
    display: inline-flex;
    flex-direction: row;
    align-items: center;
    justify-content: center;
    border-radius: 7px;
    color: var(--text-4);
  }
  .mrow .minfo:hover {
    background: var(--panel-3);
    color: var(--text-2);
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
