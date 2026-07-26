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
  import type { PermissionMode } from '../lib/types'
  import {
    slugPreview,
    worktreeBranches,
    worktreeSetup,
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

  let refs = $state<BranchRef[]>([])
  let proposal = $state<SetupProposal | null>(null)
  let error = $state('')
  let loading = $state(true)
  let pickerOpen = $state(false)
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
  const chosenCli = $derived(cli || clis[0] || '')
  const onProvider = $derived(chosenCli in providerModelOf)

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
    getProviderModels(base, name)
      .then((ms) => (providerModels = ms))
      .catch(() => (providerModels = []))
  })
  // What to offer: whatever the proxy listed, plus the current model if the list
  // came back without it (or came back empty, which is what a provider whose
  // login has lapsed looks like). Never an empty row.
  const modelChoices = $derived(
    providerModel && !providerModels.includes(providerModel)
      ? [providerModel, ...providerModels]
      : providerModels,
  )

  const repoLabel = $derived(projectName(repo))
  const defaultBranch = $derived(refs.find((r) => r.default)?.name ?? '')
  const baseLabel = $derived(baseBranch || defaultBranch || 'default')
  const preview = $derived(slugPreview(name))
  const canStart = $derived(!loading && !error && !busy)

  $effect(() => {
    Promise.all([worktreeBranches(base, repo), worktreeSetup(base, repo)])
      .then(([b, s]) => {
        // The branch you are on first, then the default, then the rest.
        refs = [...b.refs].sort(
          (x, y) =>
            Number(!!y.current) - Number(!!x.current) || Number(!!y.default) - Number(!!x.default),
        )
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
      // Only on a provider, and only when it differs from what that provider is
      // already mapped to: sending it pins the mapping for the provider's next
      // session too, so it should not be written back when nothing was chosen.
      providerModel:
        onProvider && providerModel && providerModel !== providerModelOf[chosenCli]
          ? providerModel
          : undefined,
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
      pickerOpen ? (pickerOpen = false) : onclose()
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
      <div class="body">
        <textarea
          bind:value={prompt}
          placeholder="What should this agent work on?"
          rows="3"
          aria-label="First prompt"
        ></textarea>

        <!-- Where the branch comes from and what it is called: the two facts that
             are about the worktree itself rather than about the agent. -->
        <div class="line">
          <span class="lbl">Branch</span>
          <div class="grow">
            <div class="basewrap">
              <button
                class="basebtn"
                disabled={loading}
                onclick={() => (pickerOpen = !pickerOpen)}
                title="Which branch to cut from"
              >
                <span class="from">from</span>
                <span class="bl mono">{loading ? '…' : baseLabel}</span>
                <svg class="chev" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6" /></svg>
              </button>
              {#if pickerOpen}
                <button class="pscrim" onclick={() => (pickerOpen = false)} aria-label="Close"></button>
                <div class="pop">
                  {#each refs as ref (ref.name)}
                    <!-- Nothing is disabled. A base is a start point, not a
                         checkout: the new worktree gets a new branch, so
                         branching from one already checked out is fine. -->
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
            <input
              class="name mono"
              bind:value={name}
              placeholder="name (optional)"
              aria-label="Worktree name"
              autocomplete="off"
            />
            <!-- Said only when there is something to say. With no name typed the
                 placeholder already carries it, and a line repeating "optional"
                 under a field marked optional is noise. -->
            {#if preview}<span class="prev mono">kunai/{preview}</span>{/if}
          </div>
        </div>

        <!-- And how the agent runs. All four are spawn-time, which is why they
             are asked here rather than left to the composer. -->
        {#if clis.length > 1}
          <div class="line">
            <span class="lbl">Account</span>
            <div class="chips">
              {#each clis as c (c)}
                <!-- The first is the machine's default, so it reads as selected
                     before anything is chosen. -->
                <button class="chip" class:on={(cli || clis[0]) === c} onclick={() => (cli = c)}>
                  {c}
                </button>
              {/each}
            </div>
          </div>
        {/if}

        <!-- The model row follows the account, because they are not independent
             choices. A provider serves one real model id and knows nothing about
             Claude's tiers, so offering Opus/Sonnet/Haiku beside a Codex account
             was offering four buttons that all did the same nothing. -->
        <div class="line">
          <span class="lbl">Model</span>
          <div class="chips">
            {#if onProvider}
              {#each modelChoices as m (m)}
                <button class="chip mono sm" class:on={providerModel === m} onclick={() => (providerModel = m)}>
                  {m}
                </button>
              {/each}
              {#if modelChoices.length === 0}
                <span class="quiet">Reading what {chosenCli} can serve…</span>
              {/if}
            {:else}
              {#each MODELS as m (m.id)}
                <button class="chip" class:on={model === m.id} title={m.hint ?? ''} onclick={() => (model = m.id)}>
                  {modelOptionLabel(m.id)}
                </button>
              {/each}
            {/if}
          </div>
        </div>

        <!-- Effort is a Claude reasoning level. A provider's model does its own
             thinking and never sees this flag, so the row would be four buttons
             that change nothing. -->
        {#if !onProvider}
          <div class="line">
            <span class="lbl">Effort</span>
            <div class="chips">
              {#each EFFORTS as e (e.id)}
                <button class="chip" class:on={effort === e.id} title={e.hint ?? ''} onclick={() => (effort = e.id)}>
                  {e.label}
                </button>
              {/each}
            </div>
          </div>
        {/if}

        <div class="line">
          <span class="lbl">Permission</span>
          <div class="chips">
            {#each PERMISSION_MODES as p (p.id)}
              <button class="chip" class:on={mode === p.id} title={p.hint} onclick={() => (mode = p.id)}>
                {p.label}
              </button>
            {/each}
          </div>
        </div>
        <p class="why">
          {PERMISSION_MODES.find((p) => p.id === mode)?.hint}. It runs in its own
          checkout on its own branch, so this one is untouched whatever it does.
        </p>

        <!-- The setup command is arbitrary shell run with the server's
             privileges, so it is never a surprise. Reported, not edited: the
             launcher's picker is where you change it. -->
        {#if proposal && proposal.source !== 'none' && proposal.command}
          <p class="setup mono" title={proposal.command}>
            <span class="slbl">setup</span>{proposal.command}
          </p>
        {/if}
      </div>

      <footer>
        <button class="ghost" onclick={onclose}>Cancel</button>
        <button class="go" disabled={!canStart} onclick={start}>
          {busy ? 'Starting…' : 'Start'}
        </button>
      </footer>
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
    max-width: 540px;
    max-height: 100%;
    font-family: var(--sans);
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r-lg, 14px);
    box-shadow: 0 24px 60px -18px rgba(0, 0, 0, 0.8);
    overflow: hidden;
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
    gap: 11px;
    padding: 13px 14px;
    overflow-y: auto;
  }

  textarea {
    width: 100%;
    box-sizing: border-box;
    padding: 9px 10px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 13.5px;
    line-height: 1.5;
    resize: vertical;
  }
  textarea::placeholder {
    color: var(--text-4);
  }
  textarea:focus {
    outline: none;
    border-color: var(--border-2);
  }

  /* One label column, so the four settings read as a list of decisions rather
     than a wall of chips. */
  .line {
    display: flex;
    align-items: baseline;
    gap: 10px;
    min-width: 0;
  }
  .lbl {
    flex: none;
    width: 74px;
    padding-top: 5px;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .grow,
  .chips {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 5px;
    flex: 1;
    min-width: 0;
  }

  .chip {
    padding: 4px 10px;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: transparent;
    color: var(--text-3, var(--text-2));
    font: inherit;
    font-size: 11.5px;
    cursor: pointer;
    white-space: nowrap;
  }
  .chip:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .chip.on {
    background: var(--text);
    border-color: var(--text);
    color: var(--bg);
  }
  /* A provider's model is a real id like gpt-5.5-codex, which is longer than a
     tier name and is data rather than a label. */
  .chip.sm {
    font-size: 11px;
  }
  .quiet {
    font-size: 11.5px;
    color: var(--text-4);
  }

  .basewrap {
    position: relative;
    flex: none;
  }
  .basebtn {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    max-width: 100%;
    padding: 4px 9px;
    border: 1px solid var(--border);
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
    left: 0;
    min-width: 230px;
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

  .name {
    flex: 1;
    min-width: 90px;
    padding: 4px 9px;
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
    flex: none;
    font-size: 11px;
    color: var(--text-4);
  }

  .why,
  .setup {
    margin: -4px 0 0 84px;
    font-size: 11px;
    color: var(--text-4);
    line-height: 1.45;
  }
  .setup {
    display: flex;
    gap: 7px;
    margin-left: 0;
    padding-top: 2px;
    border-top: 1px solid var(--border);
    font-size: 10.5px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .slbl {
    flex: none;
    opacity: 0.7;
  }

  footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 11px 14px;
    border-top: 1px solid var(--border);
  }
  .ghost,
  .go {
    padding: 6px 15px;
    border-radius: var(--r-sm);
    font: inherit;
    font-size: 12.5px;
    cursor: pointer;
  }
  .ghost {
    border: 1px solid var(--border);
    background: transparent;
    color: var(--text-2);
  }
  .ghost:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .go {
    border: 0;
    background: var(--text);
    color: var(--bg);
    font-weight: 500;
  }
  .go:disabled {
    background: var(--panel-3);
    color: var(--text-4);
    cursor: default;
  }

  .note {
    margin: 14px;
    font-size: 12px;
    color: var(--text-4);
  }
  .note.err {
    color: var(--alert);
  }

  /* On a phone the label column costs more than it explains, so the settings
     stack and the chips get the full width. */
  @media (max-width: 560px) {
    .line {
      flex-direction: column;
      align-items: stretch;
      gap: 5px;
    }
    .lbl {
      width: auto;
      padding-top: 0;
    }
    .why {
      margin-left: 0;
    }
  }
</style>
