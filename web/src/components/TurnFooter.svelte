<script lang="ts">
  import { copyText } from '../lib/clipboard'
  import type { Turn } from '../lib/turns'
  import type { Block } from '../lib/types'
  import type { RevertPreview } from '../lib/api'
  import { formatDuration, formatTokens, formatCost } from '../lib/format'
  import { app } from '../lib/app.svelte'
  import Skeleton from './Skeleton.svelte'
  import Spinner from './Spinner.svelte'

  // isProvider: the session runs a non-Claude model through a proxy. The CLI still
  // prices tokens at Claude's rates, so its cost figure is fiction there and is
  // hidden; the token counts are real usage and stay.
  let { turn, isProvider = false }: { turn: Turn; isProvider?: boolean } = $props()

  // Undoing this turn's file changes.
  //
  // Every turn already has a git snapshot taken before it ran, and until now
  // there was no way to reach one: the engine, the endpoints and the store were
  // all built and nothing in the UI called them. It belongs here because this
  // footer is attached to the turn whose changes it would undo, right beside the
  // card that lists which files those were.
  //
  // It asks first, and what it asks with comes from the server, because a revert
  // is a whole-REPOSITORY reset rather than a per-turn one: it also discards every
  // later turn's edits, anything changed in an editor since, and untracked files
  // anywhere in the repo. This footer could list the files this turn's own tool
  // calls touched and that list would be short, reassuring and wrong.
  const chat = $derived(app.chat)
  const canRevert = $derived(!!chat?.hasCheckpoint(turn.userSeq))
  let asking = $state(false)
  let preview = $state<RevertPreview | null>(null)
  let previewErr = $state('')
  let busy = $state(false)
  let done = $state(false)
  // A revert captures a safety snapshot first, so it is genuinely undoable. The
  // changed-files card already offered this before reverting moved here, and
  // dropping it would have been a silent regression of something that worked.
  const undoable = $derived(turn.userSeq != null && chat?.reverted[turn.userSeq] != null)

  async function undoIt() {
    if (turn.userSeq == null || !chat || busy) return
    busy = true
    try {
      await chat.undo(turn.userSeq)
    } catch (e) {
      previewErr = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function ask() {
    if (turn.userSeq == null || !chat) return
    asking = true
    preview = null
    previewErr = ''
    try {
      preview = await chat.previewRevert(turn.userSeq)
    } catch (e) {
      previewErr = (e as Error).message
    }
  }

  async function confirmRevert() {
    if (turn.userSeq == null || !chat || busy) return
    busy = true
    try {
      await chat.revert(turn.userSeq)
      asking = false
      done = true
      setTimeout(() => (done = false), 2400)
    } catch (e) {
      previewErr = (e as Error).message
    } finally {
      busy = false
    }
  }

  // Nothing to undo is its own answer, and a quieter one than a list of nothing.
  const nothingToDo = $derived(
    !!preview && preview.changed.length === 0 && preview.removed.length === 0,
  )

  // What the agent wrote this turn, as markdown, for the clipboard. The answer
  // is the trailing text the view leaves visible, which is what you are looking
  // at when you reach for copy. A turn that ended on tool activity has no answer,
  // so fall back to every text block: still only what the agent said, never what
  // it ran.
  const textOf = (bs: Block[]) =>
    bs
      .filter((b) => b.type === 'text' && b.text?.trim())
      .map((b) => b.text!.trim())
      .join('\n\n')
  const reply = $derived(textOf(turn.answer) || textOf(turn.blocks))

  let copied = $state(false)
  let copyTimer: ReturnType<typeof setTimeout> | undefined
  async function copyReply() {
    if (!reply) return
    try {
      if (!(await copyText(reply))) return
      copied = true
      clearTimeout(copyTimer)
      copyTimer = setTimeout(() => (copied = false), 1200)
    } catch {
      // No clipboard (an insecure origin, or permission refused). Saying nothing
      // is right here: the button simply does not confirm.
    }
  }

  const duration = $derived(turn.durationMs != null ? formatDuration(turn.durationMs) : '')
  const cost = $derived(turn.costUsd && !isProvider ? formatCost(turn.costUsd) : '')
  // A turn re-sends the conversation on every tool call, so its total is
  // dominated by re-reads. Split them out: "new" is what the model read fresh
  // and pays full price for, "cached" is the same context read back cheaply.
  const fresh = $derived(turn.newTokens ? formatTokens(turn.newTokens) : '')
  const cached = $derived(turn.cachedTokens ? formatTokens(turn.cachedTokens) : '')
  const meta = $derived(
    [duration, fresh && `${fresh} new`, cached && `${cached} cached`, cost].filter(Boolean).join(' · '),
  )
  const hasSplit = $derived(!!(turn.newTokens || turn.cachedTokens || turn.outputTokens))
  let explain = $state(false)
</script>

<!-- The footer also carries copy and revert, so it appears for a turn that
     reported no numbers rather than leaving that turn with no way to take its
     text or undo its changes. -->
{#if meta || reply || canRevert}
  <div class="footer">
    {#if meta}<span class="dur mono">{meta}</span>{/if}
    {#if reply}
      <button class="copy" class:done={copied} onclick={copyReply} title="Copy this reply as markdown">
        {#if copied}
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5" /></svg>
          Copied
        {:else}
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V5a2 2 0 012-2h8" /></svg>
          Copy
        {/if}
      </button>
    {/if}
    {#if undoable}
      <button class="rbtn undo" disabled={busy} onclick={undoIt} title="Put back what that revert undid">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><path d="M21 7v6h-6" /><path d="M3 17a9 9 0 019-9 9 9 0 016.7 3L21 13" /></svg>
        {busy ? 'Undoing…' : 'Undo revert'}
      </button>
    {:else if canRevert}
      <span class="rev">
        <button class="rbtn" class:done onclick={ask} title="Undo the file changes from this turn onwards">
          {#if done}
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5" /></svg>
            Reverted
          {:else}
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7v6h6" /><path d="M21 17a9 9 0 00-9-9 9 9 0 00-6.7 3L3 13" /></svg>
            Revert
          {/if}
        </button>
        {#if asking}
          <button class="scrim" onclick={() => (asking = false)} aria-label="Close"></button>
          <div class="pop wide">
            {#if previewErr}
              <p class="note err">{previewErr}</p>
            {:else if !preview}
              <!-- The shape of the answer, not a spinner: this is a title plus a
                   list of paths, so placeholder rows of the right size keep the
                   panel from jumping when the real list lands. -->
              <div class="skwrap">
                <Skeleton rows={4} height={13} gap={7} label="Asking git what this would change" />
              </div>
            {:else if nothingToDo}
              <p class="note">
                Nothing to undo: the repository already matches how it was before
                this turn.
              </p>
            {:else}
              <p class="rtitle">Restore the repository to before this turn?</p>
              <!-- Named rather than counted, because the point of asking is that
                   you can see something you did not expect. -->
              {#if preview.changed.length}
                <div class="rlist">
                  {#each preview.changed.slice(0, 12) as c (c.path)}
                    <div class="rrow">
                      <span class="rst" data-st={c.status}>{c.status}</span>
                      <span class="rpath mono">{c.path}</span>
                    </div>
                  {/each}
                  {#if preview.changed.length > 12}
                    <p class="more mono">+{preview.changed.length - 12} more</p>
                  {/if}
                </div>
              {/if}
              {#if preview.removed.length}
                <p class="note warn">
                  {preview.removed.length} untracked
                  {preview.removed.length === 1 ? 'file' : 'files'} will be deleted:
                  <span class="mono">{preview.removed.slice(0, 3).join(', ')}</span>{preview.removed.length > 3 ? `, +${preview.removed.length - 3} more` : ''}
                </p>
              {/if}
              <p class="note">
                This resets the whole repository, so anything changed after this
                turn goes too, including edits you made yourself. The conversation
                is untouched.
              </p>
              <div class="ract">
                <button class="rcancel" onclick={() => (asking = false)}>Cancel</button>
                <button class="rgo" disabled={busy} onclick={confirmRevert}>
                  {#if busy}<Spinner size={11} />{/if}
                  {busy ? 'Reverting…' : 'Revert'}
                </button>
              </div>
            {/if}
          </div>
        {/if}
      </span>
    {/if}
    {#if hasSplit}
      <span class="info">
        <button class="ibtn" onclick={() => (explain = !explain)} aria-label="What these numbers mean" title="What these numbers mean">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9" /><path d="M12 11v5" /><path d="M12 7.6v.1" /></svg>
        </button>
        {#if explain}
          <button class="scrim" onclick={() => (explain = false)} aria-label="Close"></button>
          <div class="pop">
            <div class="prow"><span>New</span><span class="mono">{formatTokens(turn.newTokens ?? 0)}</span></div>
            <div class="prow"><span>Cached</span><span class="mono">{formatTokens(turn.cachedTokens ?? 0)}</span></div>
            <div class="prow"><span>Output</span><span class="mono">{formatTokens(turn.outputTokens ?? 0)}</span></div>
            <p class="note">
              Claude re-sends the whole conversation on every tool call, so a long
              turn reads the same context many times over. Those re-reads are
              cached and cost a fraction of new input, which is why the cached
              number runs far ahead of the price.
            </p>
          </div>
        {/if}
      </span>
    {/if}
  </div>
{/if}

<style>
  .footer {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 7px;
    padding-top: 2px;
  }
  .dur {
    flex: none;
    font-size: 11.5px;
    color: var(--text-3);
    padding-right: 2px;
  }
  .info {
    position: relative;
    display: inline-flex;
    margin-left: -3px;
  }
  .rev {
    position: relative;
    display: inline-flex;
  }
  .rbtn {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 3px 7px;
    border: 0;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-4);
    font: inherit;
    font-size: 11.5px;
    cursor: pointer;
  }
  .rbtn:hover {
    background: var(--panel-2);
    color: var(--text-2);
  }
  .rbtn.done {
    color: var(--live);
  }
  .rbtn.undo {
    color: var(--text-3);
  }
  .pop.wide {
    width: 340px;
    max-width: calc(100vw - 28px);
  }
  .rtitle {
    margin: 0 0 7px;
    font-size: 12.5px;
    color: var(--text);
  }
  .skwrap {
    padding: 4px 2px;
  }
  .rlist {
    display: flex;
    flex-direction: column;
    gap: 2px;
    max-height: 168px;
    overflow-y: auto;
    margin-bottom: 7px;
  }
  .rrow {
    display: flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
  }
  /* git's own letters, coloured the way a diff is: what comes back, what goes. */
  .rst {
    flex: none;
    width: 11px;
    font-family: var(--mono);
    font-size: 10.5px;
    color: var(--text-4);
  }
  .rst[data-st='A'] {
    color: var(--alert);
  }
  .rst[data-st='D'] {
    color: var(--live);
  }
  .rpath {
    min-width: 0;
    font-size: 11px;
    color: var(--text-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    direction: rtl;
    text-align: left;
    unicode-bidi: plaintext;
  }
  .more {
    margin: 2px 0 0 18px;
    font-size: 10.5px;
    color: var(--text-4);
  }
  .note.warn {
    color: var(--busy);
  }
  .note.err {
    color: var(--alert);
  }
  .ract {
    display: flex;
    justify-content: flex-end;
    gap: 7px;
    margin-top: 9px;
  }
  .rcancel,
  .rgo {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 5px 12px;
    border-radius: var(--r-sm);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
  }
  .rcancel {
    border: 1px solid var(--border);
    background: transparent;
    color: var(--text-2);
  }
  .rcancel:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .rgo {
    border: 0;
    background: var(--alert);
    color: var(--bg);
    font-weight: 500;
  }
  .rgo:disabled {
    background: var(--panel-3);
    color: var(--text-4);
    cursor: default;
  }
  .ibtn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: 50%;
    color: var(--text-4);
  }
  .ibtn:hover {
    color: var(--text-2);
  }
  /* Copy is something you reach for, not something you consult, so unlike the
     info dot beside it it carries its word and a real hit area. Quiet enough to
     belong to the footer, legible enough to find without hunting. */
  .copy {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    height: 26px;
    padding: 0 9px;
    border-radius: 7px;
    color: var(--text-3);
    font-size: 11.5px;
    font-weight: 500;
    transition: color 0.12s, background 0.12s;
  }
  .copy:hover {
    color: var(--text);
    background: var(--panel-2);
  }
  /* Confirmation happens in place: the button says what it did rather than
     throwing a toast at a screen you are already looking at. */
  .copy.done {
    color: var(--text);
  }
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 30;
  }
  .pop {
    position: absolute;
    z-index: 31;
    bottom: calc(100% + 7px);
    left: -8px;
    width: 262px;
    padding: 11px 12px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
    text-align: left;
  }
  .prow {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    font-size: 12.5px;
    color: var(--text-3);
    padding-bottom: 5px;
  }
  .prow .mono {
    color: var(--text-2);
  }
  .note {
    margin: 6px 0 0;
    padding-top: 9px;
    border-top: 1px solid var(--border);
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-4);
  }
</style>
