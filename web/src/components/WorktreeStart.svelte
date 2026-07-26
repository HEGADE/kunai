<script lang="ts">
  // Starting a worktree from a project heading.
  //
  // It began as a bare list of branches that started a session the moment you
  // picked one. That got the branch right and everything else wrong: a worktree
  // exists to hold a piece of work, so the first thing to say about it is what
  // the work IS, and there was nowhere to say it. Then it was a small popover
  // hanging off the button, which had room for the prompt and nothing else.
  //
  // It is a dialog now because of what a worktree is FOR. You open one to send an
  // agent off on its own for a while, unattended, and every choice that decides
  // how that goes is made at spawn: the account or provider that runs it, the
  // model, the reasoning effort, and the permission mode. Effort and mode in
  // particular cannot be changed into afterwards in any way that helps -- the CLI
  // takes both as spawn flags, so a mode set later misses the first tool call,
  // which for an unattended agent is exactly the one that matters. A popover with
  // no room for them meant every worktree started on the defaults and stopped on
  // its first file write.
  //
  // Centred rather than anchored for the same reason: this is a decision you stop
  // and make, not a menu you flick through.
  import { getProviderModels } from '../lib/api'
  import { app, type StartSpec } from '../lib/app.svelte'
  import { projectName } from '../lib/grouping'
  import { DEFAULT_EFFORT, DEFAULT_MODEL, EFFORTS, MODELS, modelOptionLabel } from '../lib/models'
  import { PERMISSION_MODES } from '../lib/permissions'
  import { fetchQuery, keys, peek, SLOW_TTL, DEFAULT_TTL } from '../lib/query.svelte'
  import {
    chosenCli as resolveCli,
    isProvider,
    providerModelChoices,
    providerModelToSend,
    showEffort,
  } from '../lib/spawnoptions'
  import SegMenu, { type SegOption } from './SegMenu.svelte'
  import Spinner from './Spinner.svelte'
  import type { PermissionMode } from '../lib/types'
  import {
    slugPreview,
    worktreeBranches,
    worktreeSetup,
    type BranchList,
    type BranchRef,
    type SetupProposal,
  } from '../lib/worktrees'

  let {
    base = '',
    machineId,
    repo,
    busy = false,
    onstart,
    onclose,
  }: {
    // base is the machine's origin; repo is the main checkout this cuts from.
    base?: string
    machineId: string
    repo: string
    busy?: boolean
    onstart: (prompt: string, spec: StartSpec) => void
    onclose: () => void
  } = $props()

  let prompt = $state('')
  let name = $state('')
  let baseBranch = $state('')
  let model = $state(DEFAULT_MODEL)
  let effort = $state(DEFAULT_EFFORT)
  // Worktrees deliberately start in acceptEdits rather than the app-wide Auto.
  // This is the same trade the scheduler and a loop already make: Auto still
  // stops to ask about a risky action, and for an agent working alone in its own
  // checkout that is a hang rather than caution. The isolation is what makes it
  // safe to be looser here: the edits land on a branch of its own, in a directory
  // of its own, and your checkout is untouched whatever it does.
  let mode = $state<PermissionMode>('acceptEdits')
  // Empty means the machine's default account, which is what a single-account
  // machine always wants.
  let cli = $state('')

  // The branch you are on first, then the default, then the rest.
  const sortRefs = (rs: BranchRef[]) =>
    [...rs].sort(
      (x, y) => Number(!!y.current) - Number(!!x.current) || Number(!!y.default) - Number(!!x.default),
    )

  let refs = $state<BranchRef[]>([])
  let proposal = $state<SetupProposal | null>(null)
  let error = $state('')
  let loading = $state(true)
  let box: HTMLDivElement | null = $state(null)

  // Accounts and providers come from the machine's stats as one list, because
  // from here they are one decision: which brain runs this. Only offered when the
  // machine has a real choice to make.
  const stats = $derived(app.machines.find((m) => m.id === machineId)?.stats ?? null)
  const clis = $derived(stats?.clis ?? [])
  // Which of those names are proxy-backed providers. The map is keyed by provider
  // name, so it is the discriminator already on the wire; nothing new is needed
  // to tell a Codex from a Claude account.
  const providerModelOf = $derived(stats?.provider_models ?? {})
  // The provider rules live in lib/spawnoptions so this dialog and the New Session
  // dialog cannot answer them differently, which is exactly what happened when
  // they were written inline here.
  const chosenCli = $derived(resolveCli(cli, clis))
  const onProvider = $derived(isProvider(chosenCli, providerModelOf))

  // A provider serves real upstream model ids, not Claude tiers, so the Claude
  // tier row means nothing there: picking "Opus 5" for a Codex account chose
  // nothing at all, it just left whatever the provider was already mapped to.
  // The models are fetched per provider because only the proxy knows what that
  // login can actually serve.
  let providerModels = $state<string[]>([])
  let providerModel = $state('')
  let modelsFor = ''
  $effect(() => {
    const name = chosenCli
    if (!onProvider || modelsFor === name) return
    modelsFor = name
    // Preselect what the provider is already on, so the row opens on the truth
    // rather than on nothing.
    providerModel = providerModelOf[name] ?? ''
    providerModels = []
    fetchQuery(keys.providerModels(base, name), () => getProviderModels(base, name), {
      ttl: DEFAULT_TTL,
    })
      .then((ms) => (providerModels = ms))
      .catch(() => (providerModels = []))
  })
  // What to offer: whatever the proxy listed, plus the current model if the list
  // came back without it (or came back empty, which is what a provider whose
  // login has lapsed looks like). Never an empty row.
  const modelChoices = $derived(providerModelChoices(providerModel, providerModels))

  // The four control strips, each built where its data is rather than inline in
  // the markup, so the bar reads as a bar.
  const branchOptions = $derived<SegOption[]>(
    // Nothing is disabled. A base is a start point, not a checkout: the new
    // worktree gets a new branch, so branching from one already checked out is
    // fine and git is happy to do it.
    refs.map((r) => ({
      id: r.name,
      label: r.name,
      mono: true,
      hint: r.current ? 'you are here' : r.default ? 'default' : undefined,
    })),
  )
  const accountOptions = $derived<SegOption[]>(
    clis.map((c) => ({
      id: c,
      label: c,
      hint: c in providerModelOf ? providerModelOf[c] : undefined,
    })),
  )
  const modelOptions = $derived<SegOption[]>(
    onProvider
      ? modelChoices.map((m) => ({ id: m, label: m, mono: true }))
      : MODELS.map((m) => ({ id: m.id, label: modelOptionLabel(m.id), hint: m.hint })),
  )
  const effortOptions = $derived<SegOption[]>(
    EFFORTS.map((e) => ({ id: e.id, label: e.label, hint: e.hint })),
  )
  const permissionOptions = $derived<SegOption[]>(
    PERMISSION_MODES.map((m) => ({ id: m.id, label: m.label, hint: m.hint })),
  )

  const repoLabel = $derived(projectName(repo))
  const defaultBranch = $derived(refs.find((r) => r.default)?.name ?? '')
  const baseLabel = $derived(baseBranch || defaultBranch || 'default')
  const preview = $derived(slugPreview(name))
  const canStart = $derived(!loading && !error && !busy)

  $effect(() => {
    // Seed from whatever the cache already knows, before awaiting anything, so
    // reopening this on a repository you opened a moment ago shows a real branch
    // list rather than "reading branches…" for something that has not changed.
    // Inside the effect rather than at init so it tracks base and repo instead of
    // capturing whatever they were when the component was created.
    const seeded = peek<BranchList>(keys.branches(base, repo))?.data
    if (seeded) {
      refs = sortRefs(seeded.refs)
      baseBranch = seeded.refs.find((r) => r.current)?.name || seeded.default
      loading = false
    }
    Promise.all([
      fetchQuery(keys.branches(base, repo), () => worktreeBranches(base, repo), { ttl: SLOW_TTL }),
      fetchQuery(keys.setup(base, repo), () => worktreeSetup(base, repo), { ttl: SLOW_TTL }),
    ])
      .then(([b, s]) => {
        refs = sortRefs(b.refs)
        // Preselect where you are standing, not the repository's default.
        // Silently cutting from main while you worked on a feature branch was the
        // whole complaint that started this.
        baseBranch = refs.find((r) => r.current)?.name || b.default
        proposal = s
      })
      .catch((e) => (error = (e as Error).message))
      .finally(() => (loading = false))
  })

  // Focus the prompt on open: the dialog exists to be typed into, and one you
  // have to click before typing has wasted the click.
  $effect(() => {
    box?.querySelector('textarea')?.focus()
  })

  function start() {
    if (!canStart) return
    onstart(prompt.trim(), {
      // Only send an account the machine actually offers; empty just means its
      // default, and a name it does not have would strand the create.
      cli: clis.includes(cli) ? cli : undefined,
      model,
      effort,
      mode,
      providerModel: providerModelToSend(chosenCli, providerModelOf, providerModel),
      wt: {
        on: true,
        base: baseBranch,
        name: name.trim(),
        // Undefined, not the resolved command: it is shown here but never edited,
        // so the repository stays the authority and nothing is remembered on its
        // behalf from a dialog that only reported what it found.
        setup: undefined,
      },
    })
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      onclose()
      return
    }
    // Enter in the prompt starts; Shift+Enter is a newline. A task is usually one
    // line, and every other composer in kunai already reads this way.
    if (e.key === 'Enter' && !e.shiftKey && (e.target as HTMLElement)?.tagName !== 'BUTTON') {
      e.preventDefault()
      start()
    }
  }
</script>

<div class="backdrop" onclick={onclose} role="presentation">
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="modal wtstart"
    bind:this={box}
    onclick={(e) => e.stopPropagation()}
    onkeydown={onKey}
    role="dialog"
    aria-modal="true"
    aria-label="New worktree in {repoLabel}"
    tabindex="-1"
  >
    <header>
      <svg class="fork" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 3v12M6 21a2 2 0 100-4 2 2 0 000 4zM6 7a2 2 0 100-4 2 2 0 000 4zM18 11a2 2 0 100-4 2 2 0 000 4zM18 9v2a4 4 0 01-4 4H6" /></svg>
      <h2>New worktree in <span class="mono">{repoLabel}</span></h2>
      <button class="close" onclick={onclose} aria-label="Close">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M18 6L6 18M6 6l12 12" /></svg>
      </button>
    </header>

    {#if error}
      <p class="note err">{error}</p>
    {:else}
      <!-- One box holding the prompt and a bar of settings under it: the shape
           the session composer already has, because these are settings on the
           thing you are writing rather than a form to fill in. Rows of chips
           state every option at once, which is only worth the space when
           choosing is the task; here writing the prompt is the task. -->
      <div class="body">
        <!-- What the worktree IS: where it cuts from, what it is called. A slim
             line of its own above the prompt, because it is addressing rather
             than composing, and because crowding it into the settings bar
             truncated every label on that bar down to "Cla…" and "Op…". -->
        <div class="addr">
          <SegMenu
            value={baseBranch}
            options={branchOptions}
            label={loading ? 'reading branches…' : `from ${baseLabel}`}
            title="Which branch to cut from"
            mono
            up={false}
            disabled={loading}
            onpick={(b) => (baseBranch = b)}
          />
          <input
            class="name mono"
            bind:value={name}
            placeholder="name (optional)"
            title={preview ? `Creates kunai/${preview}` : 'Left empty, the prompt names the branch'}
            aria-label="Worktree name"
            autocomplete="off"
          />
          {#if preview}<span class="prev mono">kunai/{preview}</span>{/if}
        </div>

        <!-- One box holding the prompt and a bar of settings under it: the shape
             the session composer already has, because these are settings on the
             thing you are writing rather than a form to fill in. Rows of chips
             stated every option at once, which is only worth the space when
             choosing is the task; here writing the prompt is the task. -->
        <div class="composer">
          <textarea
            bind:value={prompt}
            placeholder="What should this agent work on?"
            rows="3"
            aria-label="First prompt"
          ></textarea>

          <div class="bar">
            <!-- How the agent runs. Every one of these is spawn-time, which is
                 why they are asked here rather than left to the composer. -->
            <div class="controls">
              {#if clis.length > 1}
                <SegMenu
                  value={chosenCli}
                  options={accountOptions}
                  title="Which account or provider runs it"
                  onpick={(c) => (cli = c)}
                />
              {/if}
              <SegMenu
                value={onProvider ? providerModel : model}
                options={modelOptions}
                label={onProvider && !providerModel ? 'reading models…' : undefined}
                title="Model"
                mono={onProvider}
                onpick={(m) => (onProvider ? (providerModel = m) : (model = m))}
              />
              <!-- Effort is a Claude reasoning level. A provider's model does its
                   own thinking and never sees the flag, so the control would
                   change nothing. -->
              {#if showEffort(chosenCli, providerModelOf)}
                <SegMenu
                  value={effort}
                  options={effortOptions}
                  title="Reasoning effort"
                  onpick={(e) => (effort = e)}
                />
              {/if}
              <SegMenu
                value={mode}
                options={permissionOptions}
                title="Permission mode"
                note="It runs in its own checkout on its own branch, so this one is untouched whatever it does."
                onpick={(m) => (mode = m as PermissionMode)}
              />
            </div>

            <span class="spacer"></span>

            <button class="go" disabled={!canStart} onclick={start}>
              {#if busy}<Spinner />{/if}
              {busy ? 'Starting…' : 'Start'}
            </button>
          </div>
        </div>

        <!-- The setup command is arbitrary shell run with the server's
             privileges, so it is never a surprise. Reported, not edited: the
             launcher's picker is where you change it. -->
        {#if proposal && proposal.source !== 'none' && proposal.command}
          <p class="setup mono" title={proposal.command}>
            <span class="slbl">setup</span>{proposal.command}
          </p>
        {/if}
      </div>

    {/if}
  </div>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: calc(var(--safe-top) + 16px) 16px 16px;
    background: rgba(0, 0, 0, 0.55);
    backdrop-filter: blur(2px);
  }
  .modal {
    display: flex;
    flex-direction: column;
    width: 100%;
    max-width: 580px;
    max-height: 100%;
    font-family: var(--sans);
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r-lg, 14px);
    box-shadow: 0 24px 60px -18px rgba(0, 0, 0, 0.8);
  }
  .modal:focus {
    outline: none;
  }

  header {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 13px 14px;
    border-bottom: 1px solid var(--border);
  }
  .fork {
    flex: none;
    color: var(--text-4);
  }
  h2 {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: 13.5px;
    font-weight: 500;
    color: var(--text-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  h2 .mono {
    color: var(--text);
  }
  .close {
    flex: none;
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    border: 0;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-4);
    cursor: pointer;
  }
  .close:hover {
    background: var(--panel-3);
    color: var(--text);
  }

  .body {
    display: flex;
    flex-direction: column;
    gap: 9px;
    padding: 13px 14px 14px;
  }

  /* Addressing, not composing: which branch and what to call it. Quiet, and on
     its own line, because crowding it into the settings bar truncated every
     label on that bar to "Cla…" and "Op…". */
  .addr {
    display: flex;
    align-items: center;
    gap: 4px;
    min-width: 0;
    padding: 0 2px;
  }

  /* The session composer's shape: one rounded field whose own edge defines it,
     with the settings on a bar inside it rather than stacked underneath. */
  .composer {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border);
    border-radius: var(--r, 12px);
    background: transparent;
  }
  .composer:focus-within {
    border-color: var(--border-2);
  }
  textarea {
    width: 100%;
    box-sizing: border-box;
    padding: 11px 12px 4px;
    border: 0;
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 13.5px;
    line-height: 1.5;
    resize: none;
  }
  textarea::placeholder {
    color: var(--text-4);
  }
  textarea:focus {
    outline: none;
  }

  .bar {
    display: flex;
    align-items: center;
    gap: 3px;
    padding: 5px 6px 6px;
    min-width: 0;
  }
  .spacer {
    flex: 1;
    min-width: 0;
  }
  .controls {
    display: inline-flex;
    align-items: center;
    min-width: 0;
  }
  /* Hairlines between the settings, so the strip reads as one control rather
     than as loose buttons. Same rule the composer uses. */
  .controls > :global(.segwrap + .segwrap)::before {
    content: '';
    width: 1px;
    height: 13px;
    margin: 0 2px;
    background: var(--border-2);
  }

  .name {
    flex: 1;
    min-width: 80px;
    padding: 4px 8px;
    border: 0;
    border-radius: 8px;
    background: transparent;
    color: var(--text);
    font-size: 11.5px;
  }
  .name::placeholder {
    color: var(--text-4);
  }
  .name:hover,
  .name:focus {
    background: var(--panel-3);
    outline: none;
  }
  .prev {
    flex: none;
    font-size: 10.5px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 150px;
  }

  .go {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin-left: 5px;
    padding: 6px 14px;
    border: 0;
    border-radius: 999px;
    background: var(--text);
    color: var(--bg);
    font: inherit;
    font-size: 12.5px;
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
    padding: 0 3px;
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
    margin: 14px;
    font-size: 12px;
    color: var(--text-4);
  }
  .note.err {
    color: var(--alert);
  }

  /* On a phone the bar cannot hold the branch, the name and four settings on one
     line, so it wraps: the worktree's own facts first, then how it runs, with
     Start claiming the end of the second row. */
  /* On a phone the settings do not fit beside Start on one line, so the bar
     wraps and Start claims the end of the second row. */
  @media (max-width: 560px) {
    .bar {
      flex-wrap: wrap;
      row-gap: 4px;
    }
    .controls {
      flex-wrap: wrap;
    }
    .prev {
      display: none;
    }
  }
</style>
