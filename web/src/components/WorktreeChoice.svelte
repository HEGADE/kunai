<script lang="ts">
  // The one control that asks "where should this work happen": the current
  // checkout, or a worktree of its own. It is shared by the home launcher, the
  // new-session dialog and the sidebar so the three cannot drift into asking the
  // same question three different ways.
  //
  // The framing is t3code's and it is the right one: the choice reads as
  // "Current checkout" against "New worktree", not as a checkbox labelled
  // "worktree". One names a place you know, the other names a place you are
  // making; a checkbox names a git feature.
  //
  // Everything below the choice is optional. A base is preselected, a name is
  // derived from what you typed if you leave it empty, and the setup command is
  // whatever the repository already declares. So the fast path is one tap.
  import { onMount } from 'svelte'
  import {
    slugPreview,
    worktreeBranches,
    worktreeSetup,
    type BranchRef,
    type SetupProposal,
    type WorktreeChoice,
  } from '../lib/worktrees'

  let {
    base = '',
    repo = '',
    value = $bindable<WorktreeChoice>(),
    ondone,
  }: {
    base?: string
    repo?: string
    value: WorktreeChoice
    // ondone fires when the user has finished with the control (Enter in the
    // name field), so a host that opened it in a popover can close it. Without
    // this the only way back to the Start button is a click on empty space,
    // which is fine for picking one item from a list but wrong for a form.
    ondone?: () => void
  } = $props()

  let refs = $state<BranchRef[]>([])
  let defaultBranch = $state('')
  let fromOrigin = $state(true)
  let proposal = $state<SetupProposal | null>(null)
  let loading = $state(false)
  let error = $state('')
  let baseOpen = $state(false)
  let setupOpen = $state(false)
  let loadedFor = ''

  // The branches and the setup command belong to a repository, so they are
  // fetched once per repository rather than once per open. A folder that is not
  // a git repository simply reports that and the control disables itself.
  async function load() {
    if (!repo || loadedFor === repo) return
    loadedFor = repo
    loading = true
    error = ''
    try {
      const [b, s] = await Promise.all([worktreeBranches(base, repo), worktreeSetup(base, repo)])
      refs = b.refs
      defaultBranch = b.default
      fromOrigin = b.from_origin
      proposal = s
      if (!value.base) value = { ...value, base: b.default }
      if (!value.setup && s.command) value = { ...value, setup: s.command }
    } catch (e) {
      error = (e as Error).message
      refs = []
    } finally {
      loading = false
    }
  }

  onMount(load)
  $effect(() => {
    if (repo) load()
  })

  const isRepo = $derived(!loading && !error)
  // The branch this folder is on, so "This checkout" says which one it means.
  const current = $derived(refs.find((r) => r.current)?.name ?? '')
  const baseLabel = $derived(value.base || defaultBranch || 'default branch')
  // What the branch will actually be called, so the choice is not abstract.
  const previewName = $derived(slugPreview(value.name))

  // Enter confirms and gets out of the way. Without it the only way back to the
  // Start button is a click on empty space, which is fine for picking one item
  // from a list but wrong for a form: you finish typing a name and the thing you
  // want next is blocked by the scrim you opened.
  function onNameKey(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault()
      ondone?.()
    }
  }

  function pickBase(name: string) {
    value = { ...value, base: name }
    baseOpen = false
  }
  function setMode(on: boolean) {
    value = { ...value, on }
  }
</script>

<div class="wtc">
  <!-- Two rows, like every other picker on this bar. A segmented control for a
       binary choice was the largest thing on the screen for the smallest decision
       on it, and it read as a dialog rather than a menu. -->
  <div class="modes" role="radiogroup" aria-label="Where should this work happen">
    <button
      class="mode"
      class:on={!value.on}
      role="radio"
      aria-checked={!value.on}
      onclick={() => setMode(false)}
    >
      <span class="mn">This checkout</span>
      {#if current}<span class="md mono">{current}</span>{/if}
    </button>
    <button
      class="mode"
      class:on={value.on}
      role="radio"
      aria-checked={value.on}
      disabled={!isRepo}
      onclick={() => setMode(true)}
    >
      <span class="mn">New worktree</span>
      <span class="md">{error ? 'not a git repository' : 'its own branch, in parallel'}</span>
    </button>
  </div>

  {#if value.on && isRepo}
    <!-- Said in text rather than on hover, because this is where the choice is
         made and a phone has no hover to explain it with. -->
    <p class="why">
      Its own checkout of this repository, so another agent can work here without
      touching the main one. git's own worktree feature.
    </p>
    <!-- Two fields and a line about setup. Anything more here is configuration
         standing between a thought and the work. -->
    <div class="fields">
      <div class="row">
        <span class="lbl">from</span>
        <div class="basewrap">
          <button class="basebtn mono" class:on={baseOpen} onclick={() => (baseOpen = !baseOpen)}>
            {baseLabel}
            {#if fromOrigin && !baseLabel.includes('/')}<span class="hint">· origin</span>{/if}
          </button>
          {#if baseOpen}
            <button class="scrim" onclick={() => (baseOpen = false)} aria-label="Close"></button>
            <div class="basepop">
              {#each refs as ref (ref.name)}
                <!-- A base is a start point, not a checkout, so a branch that is
                     already checked out is still a perfectly good one to branch
                     from. Disabling those made the branch you are on unpickable. -->
                <button
                  class:active={ref.name === value.base}
                  title={ref.name}
                  onclick={() => pickBase(ref.name)}
                >
                  <span class="bn mono">{ref.name}</span>
                  {#if ref.default}<span class="tag">default</span>
                  {:else if ref.current}<span class="tag">current</span>{/if}
                </button>
              {/each}
              {#if refs.length === 0}<p class="empty">No branches to start from.</p>{/if}
            </div>
          {/if}
        </div>
      </div>

      <div class="row">
        <span class="lbl">name</span>
        <input
          class="nameinput mono"
          bind:value={value.name}
          placeholder="optional"
          onkeydown={onNameKey}
          aria-label="Worktree name"
          autocomplete="off"
        />
        {#if previewName}<span class="branchprev mono">kunai/{previewName}</span>{/if}
      </div>

      <!-- The setup command is arbitrary shell run with the server's privileges,
           so it is always shown before it runs. Collapsed to one line, because
           the common case is that the repository already declares the right one
           and you only look when it is wrong. -->
      {#if proposal && proposal.source !== 'none'}
        <div class="row setup">
          <span class="lbl">setup</span>
          {#if setupOpen}
            <textarea
              class="setupedit mono"
              bind:value={value.setup}
              rows="2"
              aria-label="Setup command"
            ></textarea>
          {:else}
            <button class="setupline mono" onclick={() => (setupOpen = true)} title="Edit">
              {value.setup || 'none'}
            </button>
          {/if}
        </div>
        <p class="why">
          {#if proposal.source === 'project'}
            From {proposal.why}. Runs once in the new worktree.
          {:else}
            Suggested from {proposal.why}. Check it before starting.
          {/if}
        </p>
      {:else}
        <p class="why">
          No setup command, so the worktree starts without installed dependencies.
        </p>
      {/if}
    </div>
  {:else if error}
    <p class="why err">{error}</p>
  {/if}
</div>

<style>
  .wtc {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  /* Rows, matching the folder and account pickers on the same bar. */
  .modes {
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .mode {
    display: flex;
    flex-direction: column;
    gap: 1px;
    width: 100%;
    padding: 6px 9px;
    border: 0;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-2);
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .mode:hover:not(:disabled),
  .mode.on {
    background: var(--panel-3);
  }
  .mode.on .mn {
    color: var(--text);
  }
  .mode:disabled {
    cursor: default;
  }
  .mn {
    font-size: 12.5px;
  }
  .md {
    font-size: 11px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .fields {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }
  .lbl {
    width: 40px;
    flex: none;
    color: var(--text-4);
    font-size: 11.5px;
  }

  .basewrap {
    position: relative;
    min-width: 0;
  }
  .basebtn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    max-width: 100%;
    padding: 5px 9px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .basebtn:hover,
  .basebtn.on {
    background: var(--panel);
  }
  .hint {
    color: var(--text-4);
  }

  .scrim {
    position: fixed;
    inset: 0;
    z-index: 30;
    border: 0;
    background: transparent;
    cursor: default;
  }
  .basepop {
    position: absolute;
    z-index: 31;
    top: calc(100% + 4px);
    left: 0;
    min-width: 220px;
    max-height: 240px;
    overflow-y: auto;
    padding: 4px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
  }
  .basepop button {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    width: 100%;
    padding: 6px 8px;
    border: 0;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }
  .basepop button:hover:not(:disabled),
  .basepop button.active {
    background: var(--panel);
  }
  .basepop button:disabled {
    color: var(--text-4);
    cursor: default;
  }
  .bn {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .tag {
    flex: none;
    color: var(--text-4);
    font-size: 10.5px;
  }
  .empty {
    margin: 6px 8px;
    color: var(--text-4);
    font-size: 12px;
  }

  .nameinput {
    flex: 1;
    min-width: 0;
    padding: 5px 9px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text);
    font-size: 12px;
  }
  .nameinput:focus {
    outline: none;
    border-color: var(--border-2);
  }
  .branchprev {
    flex: none;
    color: var(--text-4);
    font-size: 11px;
  }

  .setup {
    align-items: flex-start;
  }
  .setup .lbl {
    padding-top: 6px;
  }
  .setupline,
  .setupedit {
    flex: 1;
    min-width: 0;
    padding: 5px 9px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text);
    font-size: 11.5px;
    text-align: left;
  }
  .setupline {
    cursor: text;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .setupline:hover {
    background: var(--panel);
  }
  .setupedit {
    resize: vertical;
  }
  .setupedit:focus {
    outline: none;
    border-color: var(--border-2);
  }

  .why {
    margin: 0;
    color: var(--text-4);
    font-size: 11.5px;
    line-height: 1.45;
  }
  .why.err {
    color: var(--alert);
  }
</style>
