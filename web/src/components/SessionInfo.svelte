<script lang="ts">
  import type { ChatConnection } from '../lib/chat.svelte'

  // The session's context, gathered into one place off the header: where it runs,
  // what branch, which account, and every codebase it spans. None of this is an
  // action, so it lived awkwardly in the chrome (the cwd took a whole row, the
  // project count and the account were stray pills elsewhere). Here it is
  // reference you reach for on demand, which is how often it is actually needed.
  let { chat, onClose }: { chat: ChatConnection; onClose: () => void } = $props()

  // The branch belongs to the codebase the session started in; fall back to the
  // first project it has context for.
  const branch = $derived(
    chat.projects.find((p) => p.path === chat.cwd)?.branch || chat.projects[0]?.branch || '',
  )

  // The kunai session id IS the claude session UUID (sessions spawn with
  // --session-id and their transcript is <id>.jsonl), so this line resumes the
  // exact conversation from a terminal on the machine it runs on.
  const resumeCmd = $derived(`claude --resume ${chat.sessionId}`)

  // Which row last confirmed a copy ('' = none). One timer, one at a time.
  let copied = $state('')
  let copyTimer: ReturnType<typeof setTimeout> | undefined
  async function copy(key: string, text: string) {
    try {
      await navigator.clipboard.writeText(text)
      copied = key
      clearTimeout(copyTimer)
      copyTimer = setTimeout(() => (copied = ''), 1200)
    } catch {
      // No clipboard (insecure origin): the row just doesn't confirm.
    }
  }
</script>

<button class="scrim" aria-label="Close" onclick={onClose}></button>
<div class="pop mono" role="dialog" aria-label="Session details">
  <div class="row">
    <span class="k">Folder</span>
    <button class="v path" onclick={() => copy('path', chat.cwd)} title="Copy path">
      <span class="ptxt">{chat.cwd}</span>
      {#if copied === 'path'}
        <svg class="ok" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5" /></svg>
      {:else}
        <svg class="cp" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V5a2 2 0 012-2h8" /></svg>
      {/if}
    </button>
  </div>
  {#if branch}
    <div class="row"><span class="k">Branch</span><span class="v br">{branch}</span></div>
  {/if}
  {#if chat.cli}
    <div class="row"><span class="k">Account</span><span class="v">{chat.cli}</span></div>
  {/if}
  <div class="row">
    <span class="k">Resume</span>
    <button class="v path" onclick={() => copy('resume', resumeCmd)} title="Copy resume command">
      <span class="ptxt">{resumeCmd}</span>
      {#if copied === 'resume'}
        <svg class="ok" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5" /></svg>
      {:else}
        <svg class="cp" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V5a2 2 0 012-2h8" /></svg>
      {/if}
    </button>
  </div>
  {#if chat.projects.length}
    <div class="hr"></div>
    <div class="row top">
      <span class="k">Project{chat.projects.length > 1 ? 's' : ''}</span>
      <div class="projs">
        {#each chat.projects as p (p.path)}
          <span class="proj" title={p.path}>{p.name}</span>
        {/each}
      </div>
    </div>
  {/if}
</div>

<style>
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 40;
    background: none;
    border: 0;
  }
  .pop {
    position: absolute;
    top: calc(100% - 2px);
    right: 12px;
    z-index: 41;
    width: 300px;
    max-width: calc(100vw - 24px);
    padding: 11px 12px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: 11px;
    box-shadow: 0 14px 34px rgba(0, 0, 0, 0.5);
  }
  .row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 0;
    min-width: 0;
  }
  .row.top {
    align-items: flex-start;
  }
  .k {
    flex: none;
    width: 62px;
    font-size: 10px;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-4);
    padding-top: 1px;
  }
  .v {
    min-width: 0;
    font-size: 11.5px;
    color: var(--text-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    /* Paths read left-to-right but keep their tail; plaintext holds the slash. */
    unicode-bidi: plaintext;
  }
  .br {
    color: var(--live);
  }
  /* The folder is the one row you reach for often (to copy), so it is a button. */
  .path {
    display: flex;
    align-items: center;
    gap: 7px;
    border: 0;
    background: none;
    padding: 0;
    cursor: pointer;
    color: var(--text-2);
    text-align: left;
  }
  .path:hover {
    color: var(--text);
  }
  .ptxt {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .cp {
    color: var(--text-4);
    flex: none;
  }
  .ok {
    color: var(--live);
    flex: none;
  }
  .hr {
    height: 1px;
    background: var(--border);
    margin: 6px 0;
  }
  .projs {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    min-width: 0;
  }
  .proj {
    font-size: 11px;
    color: var(--text-2);
    background: var(--panel-3);
    border-radius: 5px;
    padding: 1px 7px;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
