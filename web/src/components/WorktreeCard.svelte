<script lang="ts">
  // What a session in a worktree shows about where it is and how to land it.
  //
  // It is a quiet strip rather than a panel: for most of a session's life the
  // answer is "on its own branch, nothing to land yet", and that does not deserve
  // chrome. It grows an action row only once there is something to land, and
  // speaks up only when the setup failed, which is the one state that changes how
  // you should read everything else the agent says.
  import {
    branchName,
    canLand,
    deleteWorktree,
    listWorktrees,
    mergeWorktree,
    pullRequestWorktree,
    summarise,
    type Worktree,
  } from '../lib/worktrees'

  // base is the owning machine's origin; a worktree lives on one machine, and
  // the client always talks to a session's own machine rather than assuming this
  // origin is it.
  let { base = '', cwd = '' }: { base?: string; cwd?: string } = $props()

  let wt = $state<Worktree | null>(null)
  let busy = $state('')
  let note = $state('')
  let err = $state('')
  let open = $state(false)
  let confirmDiscard = $state(false)

  const basename = $derived(wt ? branchName(wt.branch) : '')

  async function load() {
    if (!cwd) return
    try {
      const all = await listWorktrees(base)
      wt = all.find((w) => w.path === cwd) ?? null
    } catch {
      // A machine that cannot answer simply shows nothing; this is context, not
      // an operation the user asked for, so a failure must not raise an error.
      wt = null
    }
  }

  $effect(() => {
    if (cwd) load()
  })

  // While setup runs there is something to watch, so poll; otherwise the state
  // only changes when the agent commits, which a manual refresh covers.
  $effect(() => {
    if (wt?.setup.state !== 'running') return
    const t = setInterval(load, 1500)
    return () => clearInterval(t)
  })

  async function act(kind: string, fn: () => Promise<string>) {
    if (busy) return
    busy = kind
    err = ''
    note = ''
    try {
      note = await fn()
    } catch (e) {
      err = (e as Error).message
    } finally {
      busy = ''
      await load()
    }
  }

  const merge = () =>
    act('merge', async () => {
      const res = await mergeWorktree(base, cwd)
      if (res.already_merged) return 'Nothing to merge; it is already on ' + res.base + '.'
      return `Merged ${res.commits} commit${res.commits === 1 ? '' : 's'} into ${res.base}.`
    })

  const openPR = () =>
    act('pr', async () => {
      const res = await pullRequestWorktree(base, cwd)
      return res.url || 'Pull request opened.'
    })

  const discard = () =>
    act('discard', async () => {
      await deleteWorktree(base, cwd, { force: true })
      return 'Worktree removed.'
    })
</script>

{#if wt}
  <div class="wtcard" class:bad={wt.setup.state === 'failed' || wt.setup.state === 'timed_out'}>
    <button class="head" onclick={() => (open = !open)} aria-expanded={open}>
      <svg class="ic" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M6 3v12M6 21a2 2 0 100-4 2 2 0 000 4zM6 7a2 2 0 100-4 2 2 0 000 4zM18 11a2 2 0 100-4 2 2 0 000 4zM18 9v2a4 4 0 01-4 4H6" /></svg>
      <span class="branch mono">{basename}</span>
      <span class="from mono">from {wt.base}</span>
      <span class="sum">{summarise(wt)}</span>
      <span class="chev" class:open>›</span>
    </button>

    {#if open}
      <div class="body">
        <dl class="facts mono">
          <div><dt>worktree</dt><dd>{wt.path}</dd></div>
          <div><dt>main</dt><dd>{wt.repo}</dd></div>
          <div><dt>branch</dt><dd>{wt.branch}</dd></div>
        </dl>

        {#if wt.setup.state === 'failed' || wt.setup.state === 'timed_out'}
          <!-- Loud, because everything downstream reads differently when the
               dependencies are not there: a failing build is the setup, not the code. -->
          <p class="warn">
            Setup failed{wt.setup.exit_code ? ` (exit ${wt.setup.exit_code})` : ''}. Dependencies may
            be missing.
          </p>
          {#if wt.setup.output}<pre class="out mono">{wt.setup.output}</pre>{/if}
        {:else if wt.setup.state === 'running'}
          <p class="note">Preparing: <code class="mono">{wt.setup.command}</code></p>
        {/if}

        {#if wt.shared?.length}
          <p class="note">
            Shared with the main checkout, so writing through one changes the original:
            {#each wt.shared as p, i (p)}<code class="mono">{p}</code>{i < wt.shared.length - 1
                ? ', '
                : ''}{/each}
          </p>
        {/if}

        {#if wt.status?.files?.length}
          <ul class="files mono">
            {#each (wt.status.files ?? []).slice(0, 12) as f (f)}<li>{f}</li>{/each}
            {#if (wt.status.files ?? []).length > 12}
              <li class="more">and {(wt.status.files ?? []).length - 12} more</li>
            {/if}
          </ul>
        {/if}

        <div class="acts">
          {#if canLand(wt)}
            <button disabled={!!busy} onclick={merge}>
              {busy === 'merge' ? 'Merging…' : `Merge into ${wt.base.replace(/^origin\//, '')}`}
            </button>
            <button disabled={!!busy} onclick={openPR}>
              {busy === 'pr' ? 'Opening…' : 'Pull request'}
            </button>
          {/if}
          <span class="spacer"></span>
          {#if confirmDiscard}
            <!-- The confirmation names what goes, because "are you sure" is not a
                 question anyone can answer. -->
            <span class="danger">
              Delete this worktree{wt.status?.ahead
                ? `, losing ${wt.status.ahead} unmerged commit${wt.status.ahead === 1 ? '' : 's'}`
                : ''}{wt.status?.dirty ? ` and ${wt.status.dirty} uncommitted change${wt.status.dirty === 1 ? '' : 's'}` : ''}?
            </span>
            <button class="no" onclick={() => (confirmDiscard = false)}>Keep</button>
            <button class="yes" disabled={!!busy} onclick={discard}>
              {busy === 'discard' ? 'Deleting…' : 'Delete'}
            </button>
          {:else}
            <button class="quiet" onclick={() => (confirmDiscard = true)}>Discard</button>
          {/if}
        </div>

        {#if note}<p class="ok">{note}</p>{/if}
        {#if err}<p class="warn">{err}</p>{/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .wtcard {
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--panel);
    overflow: hidden;
  }
  .wtcard.bad {
    border-color: color-mix(in srgb, var(--alert) 45%, var(--border));
  }

  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 7px 10px;
    border: 0;
    background: transparent;
    color: var(--text-2);
    font: inherit;
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }
  .head:hover {
    background: var(--panel-2);
  }
  .ic {
    flex: none;
    color: var(--text-4);
  }
  .branch {
    color: var(--text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .from,
  .sum {
    color: var(--text-4);
    font-size: 11px;
    white-space: nowrap;
  }
  .sum {
    margin-left: auto;
  }
  .chev {
    flex: none;
    color: var(--text-4);
    transition: transform 0.12s ease;
  }
  .chev.open {
    transform: rotate(90deg);
  }

  .body {
    display: flex;
    flex-direction: column;
    gap: 9px;
    padding: 4px 10px 10px;
    border-top: 1px solid var(--border);
  }

  .facts {
    margin: 4px 0 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: 11px;
  }
  .facts div {
    display: flex;
    gap: 8px;
    min-width: 0;
  }
  .facts dt {
    width: 62px;
    flex: none;
    color: var(--text-4);
  }
  .facts dd {
    margin: 0;
    color: var(--text-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    unicode-bidi: plaintext;
  }

  .note,
  .warn,
  .ok {
    margin: 0;
    font-size: 11.5px;
    line-height: 1.5;
  }
  .note {
    color: var(--text-4);
  }
  .warn {
    color: var(--alert);
  }
  .ok {
    color: var(--live);
  }
  code {
    color: var(--text-2);
    font-size: 11px;
  }
  .out {
    margin: 0;
    max-height: 140px;
    overflow: auto;
    padding: 7px 9px;
    border-radius: var(--r-sm);
    background: var(--bg);
    color: var(--text-3);
    font-size: 10.5px;
    line-height: 1.5;
    white-space: pre-wrap;
  }

  .files {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 1px;
    font-size: 11px;
    color: var(--text-3);
  }
  .files .more {
    color: var(--text-4);
  }

  .acts {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    padding-top: 3px;
  }
  .acts button {
    padding: 5px 10px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-2);
    font: inherit;
    font-size: 11.5px;
    cursor: pointer;
  }
  .acts button:hover:not(:disabled) {
    background: var(--panel-2);
    color: var(--text);
  }
  .acts button:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .acts .quiet {
    border-color: transparent;
    color: var(--text-4);
  }
  .acts .yes {
    border-color: color-mix(in srgb, var(--alert) 55%, var(--border));
    color: var(--alert);
  }
  .spacer {
    flex: 1;
  }
  .danger {
    color: var(--text-2);
    font-size: 11.5px;
  }
</style>
