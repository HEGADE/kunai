<script lang="ts">
  // Starting a worktree from a project heading: a small composer, not a menu.
  //
  // This began as a bare list of branches that started a session the moment you
  // picked one. That got the branch right and everything else wrong: a worktree
  // exists to hold a piece of work, so the first thing you have to say about it
  // is what the work IS, and the old flow gave you nowhere to say it. You landed
  // in an empty session and typed there, which meant the branch had already been
  // named from nothing.
  //
  // So the panel asks for the task first and treats the rest as settings on it:
  // the base branch sits in the header where it can be changed without leaving,
  // the name is optional because the prompt already implies one, and Start is the
  // only commitment. The prompt does double duty, since the server names the
  // branch from it (worktree.NameFromPrompt) when no name is given.
  //
  // Fixed-position for the same reason Hint and the old menu were: this hangs off
  // a button inside the sidebar's scrolling list, and anything positioned within
  // that list is clipped by the scroll container.
  import { projectName } from '../lib/grouping'
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
    repo,
    anchor,
    busy = false,
    onstart,
    onclose,
  }: {
    // base is the machine's origin; repo is the main checkout this cuts from.
    base?: string
    repo: string
    // anchor is the button this belongs to, so the panel can be placed against it.
    anchor: HTMLElement | null
    busy?: boolean
    onstart: (prompt: string, choice: WorktreeChoice) => void
    onclose: () => void
  } = $props()

  let prompt = $state('')
  let name = $state('')
  let baseBranch = $state('')
  let refs = $state<BranchRef[]>([])
  let proposal = $state<SetupProposal | null>(null)
  let error = $state('')
  let loading = $state(true)
  let pickerOpen = $state(false)
  let at = $state<{ top: number; left: number } | null>(null)
  let box: HTMLDivElement | null = $state(null)

  const width = 320
  const margin = 10
  // What the panel needs below the anchor before it stops flipping above. Not the
  // exact height, which depends on whether the repo declares a setup command; the
  // flip only has to be right about "is there room for a composer here".
  const roomNeeded = 250

  const repoLabel = $derived(projectName(repo))
  const currentBranch = $derived(refs.find((r) => r.current)?.name ?? '')
  const defaultBranch = $derived(refs.find((r) => r.default)?.name ?? '')
  const baseLabel = $derived(baseBranch || defaultBranch || 'default')
  const preview = $derived(slugPreview(name))
  const canStart = $derived(!loading && !error && !busy)

  function place() {
    if (!anchor) return
    const r = anchor.getBoundingClientRect()
    const below = r.bottom + 6
    const room = window.innerHeight - below
    at = {
      top: room > roomNeeded ? below : Math.max(margin, window.innerHeight - roomNeeded - margin),
      left: Math.min(Math.max(margin, r.left - width / 2), window.innerWidth - width - margin),
    }
  }

  $effect(() => {
    place()
    Promise.all([worktreeBranches(base, repo), worktreeSetup(base, repo)])
      .then(([b, s]) => {
        // The branch you are on first, then the default, then the rest. Cutting
        // from where you are is the common case, and it was the case that used to
        // be impossible here.
        refs = [...b.refs].sort(
          (x, y) =>
            Number(!!y.current) - Number(!!x.current) || Number(!!y.default) - Number(!!x.default),
        )
        // Preselect where you are standing, not the repository's default. Silently
        // cutting from main while you worked on a feature branch was the whole
        // complaint that started this.
        baseBranch = refs.find((r) => r.current)?.name || b.default
        proposal = s
      })
      .catch((e) => (error = (e as Error).message))
      .finally(() => (loading = false))
  })

  // Focus the prompt on open: the panel exists to be typed into, and a composer
  // you have to click before typing is a composer that wasted the click.
  $effect(() => {
    box?.querySelector('textarea')?.focus()
  })

  function start() {
    if (!canStart) return
    onstart(prompt.trim(), {
      on: true,
      base: baseBranch,
      name: name.trim(),
      // Undefined, not the resolved command: it is shown here but never edited,
      // so the repository stays the authority and nothing is remembered on its
      // behalf from a panel that only reported what it found.
      setup: undefined,
    })
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      pickerOpen ? (pickerOpen = false) : onclose()
      return
    }
    // Enter starts, Shift+Enter is a newline: a task is usually one line, and the
    // composer everywhere else in kunai already reads this way.
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      start()
    }
  }
</script>

<svelte:window on:resize={place} />

<button class="scrim" onclick={onclose} aria-label="Close"></button>
{#if at}
  <div
    class="panel"
    bind:this={box}
    style="top:{at.top}px; left:{at.left}px; width:{width}px"
    onkeydown={onKey}
    role="dialog"
    aria-label="New worktree in {repoLabel}"
    tabindex="-1"
  >
    <!-- Header: what this is, and the one setting worth changing without leaving
         the panel. -->
    <div class="head">
      <svg class="fork" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 3v12M6 21a2 2 0 100-4 2 2 0 000 4zM6 7a2 2 0 100-4 2 2 0 000 4zM18 11a2 2 0 100-4 2 2 0 000 4zM18 9v2a4 4 0 01-4 4H6" /></svg>
      <span class="repo mono">{repoLabel}</span>
      <div class="basewrap">
        <button
          class="basebtn"
          disabled={loading || !!error}
          onclick={() => (pickerOpen = !pickerOpen)}
          title={loading ? 'Reading branches' : 'Which branch to cut from'}
        >
          <span class="from">from</span>
          <span class="bl mono">{loading ? '…' : baseLabel}</span>
          <svg class="chev" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6" /></svg>
        </button>
        {#if pickerOpen}
          <button class="pscrim" onclick={() => (pickerOpen = false)} aria-label="Close"></button>
          <div class="pop">
            {#each refs as ref (ref.name)}
              <!-- Nothing is disabled. A base is a start point, not a checkout:
                   the new worktree gets a new branch, so branching from one that
                   is already checked out is fine and git is happy to do it. -->
              <button
                class="opt"
                class:active={ref.name === baseBranch}
                onclick={() => {
                  baseBranch = ref.name
                  pickerOpen = false
                }}
              >
                <span class="bn mono">{ref.name}</span>
                {#if ref.current}<span class="tag">you are here</span>
                {:else if ref.default}<span class="tag">default</span>{/if}
              </button>
            {/each}
            {#if refs.length === 0}<p class="note">No branches to start from.</p>{/if}
          </div>
        {/if}
      </div>
    </div>

    {#if error}
      <p class="note err">{error}</p>
    {:else}
      <textarea
        bind:value={prompt}
        placeholder="What should this agent work on?"
        rows="3"
        aria-label="First prompt"
      ></textarea>

      <div class="foot">
        <!-- Optional, and it looks it. The prompt above already describes the
             work, and the server names the branch from that, so a name here is
             for the times you want to call it something else. -->
        <input
          class="name mono"
          bind:value={name}
          placeholder="name (optional)"
          aria-label="Worktree name"
          autocomplete="off"
        />
        <button class="go" disabled={!canStart} onclick={start}>
          {busy ? 'Starting…' : 'Start'}
        </button>
      </div>

      <!-- Said only when there is something to say. With no name typed the
           placeholder above already carries it, and a line repeating "this is
           optional" under a field marked optional is noise. -->
      {#if preview}
        <p class="prev mono" title="The branch this will create">kunai/{preview}</p>
      {/if}

      <!-- The setup command is arbitrary shell run with the server's privileges,
           so it is never a surprise. Reported, not edited: the launcher's picker
           is where you change it, and repeating that here would make this panel
           the dialog it exists to avoid. -->
      {#if proposal && proposal.source !== 'none' && proposal.command}
        <p class="setup mono" title={proposal.command}>
          <span class="slbl">setup</span>{proposal.command}
        </p>
      {/if}
    {/if}
  </div>
{/if}

<style>
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 59;
    border: 0;
    background: transparent;
    cursor: default;
  }
  .panel {
    position: fixed;
    z-index: 60;
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 9px;
    font-family: var(--sans);
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 18px 44px -14px rgba(0, 0, 0, 0.72);
  }
  .panel:focus {
    outline: none;
  }

  .head {
    display: flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
    padding: 1px 2px 8px;
    border-bottom: 1px solid var(--border);
  }
  .fork {
    flex: none;
    color: var(--text-4);
  }
  .repo {
    flex: 1;
    min-width: 0;
    font-size: 12px;
    color: var(--text-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .basewrap {
    position: relative;
    flex: none;
    max-width: 55%;
  }
  .basebtn {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    max-width: 100%;
    padding: 3px 6px;
    border: 0;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 11.5px;
    cursor: pointer;
  }
  .basebtn:hover:not(:disabled) {
    background: var(--panel-3);
  }
  .basebtn:disabled {
    color: var(--text-4);
    cursor: default;
  }
  .from {
    color: var(--text-4);
  }
  .bl {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .chev {
    flex: none;
    color: var(--text-4);
  }

  .pscrim {
    position: fixed;
    inset: 0;
    z-index: 61;
    border: 0;
    background: transparent;
    cursor: default;
  }
  .pop {
    position: absolute;
    z-index: 62;
    top: calc(100% + 5px);
    right: 0;
    min-width: 220px;
    max-height: 220px;
    overflow-y: auto;
    padding: 4px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
  }
  .opt {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    width: 100%;
    padding: 6px 8px;
    border: 0;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-2);
    font: inherit;
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }
  .opt:hover,
  .opt.active {
    background: var(--panel-3);
    color: var(--text);
  }
  .bn {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .tag {
    flex: none;
    font-size: 10.5px;
    color: var(--text-4);
  }

  textarea {
    width: 100%;
    box-sizing: border-box;
    padding: 6px 7px;
    border: 0;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 13px;
    line-height: 1.5;
    resize: none;
  }
  textarea::placeholder {
    color: var(--text-4);
  }
  textarea:focus {
    outline: none;
  }

  .foot {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }
  .name {
    flex: 1;
    min-width: 0;
    padding: 5px 8px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text);
    font-size: 11.5px;
  }
  .name:focus {
    outline: none;
    border-color: var(--border-2);
  }
  .prev {
    margin: -2px 0 0;
    padding: 0 2px;
    font-size: 11px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .go {
    flex: none;
    padding: 5px 13px;
    border: 0;
    border-radius: var(--r-sm);
    background: var(--text);
    color: var(--bg);
    font: inherit;
    font-size: 12px;
    font-weight: 500;
    cursor: pointer;
  }
  .go:disabled {
    background: var(--panel-3);
    color: var(--text-4);
    cursor: default;
  }

  .setup {
    display: flex;
    gap: 7px;
    margin: 0;
    padding: 0 2px;
    font-size: 10.5px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .slbl {
    flex: none;
    opacity: 0.7;
  }

  .note {
    margin: 4px 2px;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .note.err {
    color: var(--alert);
  }
</style>
