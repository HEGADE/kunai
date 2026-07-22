<script lang="ts">
  import { untrack } from 'svelte'
  import { app } from '../lib/app.svelte'
  import type { Provider } from '../lib/types'
  import { getProviders, saveProvider, removeProvider } from '../lib/api'

  // Manage the proxy-backed model sources a machine can run sessions on: Codex,
  // Grok, Kimi and friends, reached by pointing the ordinary `claude` agent at a
  // local CLIProxyAPI. The agent is unchanged; only the model behind it differs.
  // Everything is per-machine, like accounts, so it is scoped to the selection.
  let machineId = $state(app.activeMachineId ?? app.machines[0]?.id ?? '')
  const base = $derived(app.baseForMachine(machineId))
  const machine = $derived(app.machines.find((m) => m.id === machineId) ?? null)

  let providers = $state<Provider[]>([])
  let loading = $state(true)
  let error = $state('')

  // The add form. Base URL and token default to a single local CLIProxyAPI, which
  // is the common case; one model string fills all three Claude slots so whatever
  // model you pick in the composer resolves to this provider.
  let adding = $state(false)
  let name = $state('')
  let baseURL = $state('http://127.0.0.1:8317')
  let token = $state('sk-dummy')
  let model = $state('')
  let busy = $state(false)
  let formError = $state('')

  async function load() {
    error = ''
    loading = providers.length === 0
    try {
      providers = await getProviders(base)
    } catch (e) {
      error = (e as Error).message
    } finally {
      loading = false
    }
  }
  $effect(() => {
    void base
    untrack(() => {
      reset()
      providers = []
      load()
    })
  })

  function reset() {
    adding = false
    name = ''
    baseURL = 'http://127.0.0.1:8317'
    token = 'sk-dummy'
    model = ''
    busy = false
    formError = ''
  }

  const canSave = $derived(!!name.trim() && !!baseURL.trim() && !!model.trim())

  async function save() {
    if (!canSave || busy) return
    busy = true
    formError = ''
    const m = model.trim()
    try {
      await saveProvider(base, {
        name: name.trim(),
        base_url: baseURL.trim(),
        token: token.trim(),
        models: { opus: m, sonnet: m, haiku: m },
      })
      reset()
      await load()
      app.refresh() // so the New Session picker and account pill pick it up
    } catch (e) {
      formError = (e as Error).message
      busy = false
    }
  }

  async function remove(p: Provider) {
    try {
      await removeProvider(base, p.name)
      await load()
      app.refresh()
    } catch (e) {
      error = (e as Error).message
    }
  }

  // The model shown per row is what the opus slot maps to (all slots share it).
  const modelOf = (p: Provider): string => p.models?.opus ?? Object.values(p.models ?? {})[0] ?? ''
</script>

<div class="backdrop" onclick={() => app.closeProviders()} role="presentation">
<section class="sheet" role="dialog" aria-label="Model providers" onclick={(e) => e.stopPropagation()}>
  <header class="top">
    <button class="back" onclick={() => app.closeProviders()} aria-label="Back">
      <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6" /></svg>
    </button>
    <h1>Model providers</h1>
    {#if app.machines.length > 1}
      <label class="mpick">
        <select bind:value={machineId} aria-label="Machine">
          {#each app.machines as m (m.id)}
            <option value={m.id}>{m.label}{m.self ? ' · this machine' : ''}</option>
          {/each}
        </select>
        <svg class="mchev" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6" /></svg>
      </label>
    {/if}
  </header>

  <p class="lede">
    Run non-Claude models (Codex, Grok, Kimi) on {machine ? machine.label : 'this machine'}
    through a local
    <a href="https://github.com/router-for-me/CLIProxyAPI" target="_blank" rel="noopener noreferrer">CLIProxyAPI</a>.
    Each one shows up beside your accounts as a pick for a session. Start the proxy
    and log in there first; kunai only points the agent at it.
  </p>

  {#if error}
    <p class="state err">{error}</p>
  {:else if loading}
    <div class="roster" aria-hidden="true">
      <div class="row"><span class="nm skname"></span></div>
    </div>
  {:else if providers.length}
    <div class="roster">
      {#each providers as p (p.name)}
        <div class="row">
          <span class="nm">{p.name}</span>
          {#if modelOf(p)}<span class="model mono">{modelOf(p)}</span>{/if}
          <button class="rm" onclick={() => remove(p)} aria-label="Remove {p.name}" title="Remove {p.name}">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18" /><path d="M8 6V4a2 2 0 012-2h4a2 2 0 012 2v2" /><path d="M6 6l1 14a2 2 0 002 2h6a2 2 0 002-2l1-14" /></svg>
          </button>
        </div>
      {/each}
    </div>
  {:else}
    <p class="state">No providers yet. Add one below to run another model.</p>
  {/if}

  {#if !adding}
    <button class="add" onclick={() => (adding = true)}>
      <span class="plus">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
      </span>
      <span class="addtext">
        <span class="at">Add provider</span>
        <span class="as">Point the agent at a proxied model</span>
      </span>
    </button>
  {:else}
    <div class="flow">
      <div class="fhead"><span class="fstep">New provider</span></div>
      <label class="field">
        <span class="flabel">Name</span>
        <input placeholder="Kimi K3" bind:value={name} autofocus />
        <span class="hint">Shown in the picker beside your accounts.</span>
      </label>
      <label class="field">
        <span class="flabel">Model</span>
        <input class="mono" placeholder="kimi-k3" bind:value={model} onkeydown={(e) => e.key === 'Enter' && save()} />
        <span class="hint">The upstream model string the proxy routes to. Fills every model slot.</span>
      </label>
      <label class="field">
        <span class="flabel">Proxy base URL</span>
        <input class="mono" bind:value={baseURL} />
      </label>
      <label class="field">
        <span class="flabel">Proxy token</span>
        <input class="mono" bind:value={token} />
        <span class="hint">Whatever key the proxy expects; often a placeholder.</span>
      </label>
      {#if formError}<p class="flowerr">{formError}</p>{/if}
      <div class="actions">
        <button class="ghost" onclick={reset} disabled={busy}>Cancel</button>
        <button class="primary" disabled={!canSave || busy} onclick={save}>
          {busy ? 'Saving…' : 'Add provider'}
        </button>
      </div>
    </div>
  {/if}
</section>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 60;
    background: rgba(0, 0, 0, 0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 20px;
  }
  .sheet {
    width: 100%;
    max-width: 500px;
    max-height: min(90dvh, 800px);
    display: flex;
    flex-direction: column;
    background: var(--bg-raised, var(--bg));
    border: 1px solid var(--border-2);
    border-radius: 20px;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    box-shadow: 0 30px 80px -30px rgba(0, 0, 0, 0.8);
    padding: 20px 22px 24px;
  }
  .top {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .back {
    flex: none;
    width: 34px;
    height: 34px;
    margin-left: -6px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 10px;
    color: var(--text-3);
  }
  .back:hover {
    background: var(--panel);
    color: var(--text);
  }
  .top h1 {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: 19px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }
  .mpick {
    flex: none;
    position: relative;
    display: inline-flex;
    align-items: center;
  }
  .mpick select {
    appearance: none;
    -webkit-appearance: none;
    height: 32px;
    padding: 0 28px 0 12px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 100px;
    color: var(--text-2);
    font-size: 12.5px;
    max-width: 150px;
  }
  .mchev {
    position: absolute;
    right: 10px;
    color: var(--text-4);
    pointer-events: none;
  }
  .lede {
    margin: 13px 2px 18px;
    font-size: 13px;
    line-height: 1.6;
    color: var(--text-3);
  }
  .lede a {
    color: var(--text-2);
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  .roster {
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    background: var(--panel);
    overflow: hidden;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 14px 16px;
    min-height: 52px;
  }
  .row + .row {
    border-top: 1px solid var(--border);
  }
  .nm {
    flex: 1;
    min-width: 0;
    font-size: 14.5px;
    font-weight: 550;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .model {
    flex: none;
    font-size: 11.5px;
    color: var(--text-3);
    background: var(--panel-3);
    border-radius: 6px;
    padding: 2px 8px;
    max-width: 45%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .rm {
    flex: none;
    width: 30px;
    height: 30px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 8px;
    color: var(--text-4);
    transition: color 0.12s, background 0.12s;
  }
  .rm:hover,
  .rm:active {
    color: var(--alert);
    background: var(--panel-2);
  }
  .skname {
    height: 11px;
    width: 120px;
    border-radius: 4px;
    background: var(--panel-3);
    animation: pulse 1.1s ease-in-out infinite;
  }
  @keyframes pulse {
    0%,
    100% {
      opacity: 0.3;
    }
    50% {
      opacity: 1;
    }
  }
  .state {
    font-size: 13px;
    color: var(--text-4);
    padding: 14px 4px;
  }
  .state.err {
    color: var(--alert);
  }

  .add {
    display: flex;
    align-items: center;
    gap: 12px;
    width: 100%;
    text-align: left;
    margin-top: 12px;
    padding: 13px 16px;
    border: 1px dashed var(--border-2);
    border-radius: var(--r-lg);
    color: var(--text-2);
    transition: border-color 0.12s, background 0.12s;
  }
  .add:hover {
    border-color: var(--text-4);
    background: var(--panel);
  }
  .plus {
    flex: none;
    width: 30px;
    height: 30px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 9px;
    border: 1px solid var(--border-2);
    color: var(--text-3);
  }
  .add:hover .plus {
    color: var(--text-2);
    border-color: var(--text-4);
  }
  .addtext {
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .at {
    font-size: 14px;
    font-weight: 600;
    color: var(--text);
  }
  .as {
    font-size: 11.5px;
    color: var(--text-4);
  }

  .flow {
    margin-top: 12px;
    border: 1px solid var(--border-2);
    border-radius: var(--r-lg);
    background: var(--panel);
    padding: 16px 17px 17px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .fhead {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
  }
  .fstep {
    font-size: 14.5px;
    font-weight: 600;
    color: var(--text);
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .flabel {
    font-size: 12.5px;
    color: var(--text-3);
  }
  .field input {
    height: 44px;
    padding: 0 13px;
    background: var(--bg);
    border: 1px solid var(--border-2);
    border-radius: 11px;
    color: var(--text);
    font-size: 15.5px;
    width: 100%;
  }
  .field input.mono {
    font-family: var(--mono);
    font-size: 14px;
    letter-spacing: 0.01em;
  }
  .field input:focus {
    outline: none;
    border-color: var(--text-4);
  }
  .hint {
    font-size: 11.5px;
    color: var(--text-4);
  }
  .flowerr {
    margin: 0;
    font-size: 12.5px;
    color: var(--alert);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
  .ghost {
    height: 40px;
    padding: 0 15px;
    border-radius: 11px;
    color: var(--text-3);
    font-size: 13.5px;
  }
  .ghost:hover {
    color: var(--text);
    background: var(--panel-2);
  }
  .primary {
    height: 40px;
    padding: 0 18px;
    border-radius: 11px;
    background: var(--text);
    color: var(--bg);
    font-size: 13.5px;
    font-weight: 600;
  }
  .primary:disabled {
    opacity: 0.4;
  }
</style>
