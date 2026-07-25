<script lang="ts">
  // Which branch a new worktree cuts from, asked where the worktree is started.
  //
  // The sidebar's worktree button used to take every default, and the default was
  // the repository's default branch, so it cut from main whatever you happened to
  // be working on. That is the one thing about a worktree you actually need to
  // choose, and it was the one thing not being asked.
  //
  // Fixed-position for the same reason Hint is: these hang off buttons inside the
  // sidebar's scrolling list, and anything positioned within it is clipped by the
  // scroll container.
  import { worktreeBranches, type BranchRef } from '../lib/worktrees'

  let {
    base = '',
    repo = '',
    anchor,
    onpick,
    onclose,
  }: {
    base?: string
    repo: string
    // anchor is the element the menu belongs to, so it can be placed against it.
    anchor: HTMLElement | null
    onpick: (branch: string) => void
    onclose: () => void
  } = $props()

  let refs = $state<BranchRef[]>([])
  let error = $state('')
  let loading = $state(true)
  let at = $state<{ top: number; left: number } | null>(null)

  const width = 260
  const margin = 10

  function place() {
    if (!anchor) return
    const r = anchor.getBoundingClientRect()
    const below = r.bottom + 6
    const room = window.innerHeight - below
    at = {
      // Above when there is no room below, which is where a heading near the
      // bottom of the list puts it.
      top: room > 200 ? below : Math.max(margin, r.top - 6 - 240),
      left: Math.min(Math.max(margin, r.left - width / 2), window.innerWidth - width - margin),
    }
  }

  $effect(() => {
    place()
    worktreeBranches(base, repo)
      .then((b) => {
        // The branch you are on first: cutting from where you are is the common
        // case, and it was the case that used to be impossible here.
        refs = [...b.refs].sort((x, y) => Number(!!y.current) - Number(!!x.current))
      })
      .catch((e) => (error = (e as Error).message))
      .finally(() => (loading = false))
  })
</script>

<button class="scrim" onclick={onclose} aria-label="Close"></button>
{#if at}
  <div class="menu" style="top:{at.top}px; left:{at.left}px; width:{width}px" role="menu">
    <p class="head">New worktree from</p>
    {#if loading}
      <p class="note">reading branches…</p>
    {:else if error}
      <p class="note err">{error}</p>
    {:else}
      <div class="list">
        {#each refs as ref (ref.name)}
          <!-- Nothing is disabled here. A base is a start point, not a checkout:
               the new worktree gets a new branch, so branching from one that is
               already checked out is fine, and git is happy to do it. Treating
               "in use" as a reason to refuse made the branch you are on, which is
               the most likely thing to want, the one thing you could not pick. -->
          <button class="opt" onclick={() => onpick(ref.name)}>
            <span class="bn mono">{ref.name}</span>
            {#if ref.current}<span class="tag">you are here</span>
            {:else if ref.default}<span class="tag">default</span>{/if}
          </button>
        {/each}
        {#if refs.length === 0}<p class="note">No branches to start from.</p>{/if}
      </div>
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
  .menu {
    position: fixed;
    z-index: 60;
    font-family: var(--sans);
    padding: 5px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
  }
  .head {
    margin: 3px 8px 5px;
    font-size: 11px;
    color: var(--text-4);
  }
  .list {
    max-height: 240px;
    overflow-y: auto;
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
  .opt:hover:not(:disabled) {
    background: var(--panel-3);
    color: var(--text);
  }
  .opt:disabled {
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
    font-size: 10.5px;
    color: var(--text-4);
  }
  .note {
    margin: 6px 8px;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .note.err {
    color: var(--alert);
  }
</style>
