<script lang="ts">
  import { untrack } from 'svelte'
  import { app } from '../lib/app.svelte'
  import type { Provider } from '../lib/types'
  import {
    getProviders,
    saveProvider,
    removeProvider,
    getProviderModels,
    startProviderLogin,
    finishProviderLogin,
    providerLoginStatus,
    cancelProviderLogin,
  } from '../lib/api'

  // Add a non-Claude model (Codex/Grok/Kimi) with no terminal and no config:
  // pick a provider, sign in once in the browser, pick a model. The only human
  // step is the OAuth authorize, bridged exactly like a Claude account login.
  //
  // What kunai does with that login is deliberately NOT said on screen, because
  // there is no longer one answer: Codex and Grok go through kunai's own
  // in-process proxy (default on), while Kimi still needs the downloaded
  // CLIProxyAPI sidecar, as does either of the others if its login is missing.
  // The lede used to promise a local CLIProxyAPI, which stopped being true for
  // the two providers anybody actually picks -- and was an implementation
  // detail either way.
  // A section of Settings. The machine comes from there, so two pickers can
  // never disagree about which machine is on screen.
  let { machineId }: { machineId: string } = $props()
  const base = $derived(app.baseForMachine(machineId))
  const machine = $derived(app.machines.find((m) => m.id === machineId) ?? null)

  // Provider kinds kunai can sign into. `pfx` filters the sidecar's model list
  // down to this provider's own models for the model picker.
  const TYPES = [
    { id: 'codex', name: 'Codex', hint: 'OpenAI / ChatGPT', pfx: ['gpt', 'codex', 'o1', 'o3', 'o4'] },
    { id: 'xai', name: 'Grok', hint: 'xAI', pfx: ['grok'] },
    { id: 'kimi', name: 'Kimi', hint: 'Moonshot', pfx: ['kimi', 'moonshot'] },
  ]

  let providers = $state<Provider[]>([])
  let loading = $state(true)
  let error = $state('')

  type Step = 'idle' | 'type' | 'link' | 'model' | 'saving'
  let step = $state<Step>('idle')
  let picked = $state<(typeof TYPES)[number] | null>(null)
  let loginId = $state('')
  let url = $state('')
  let code = $state('')
  let busy = $state(false)
  let flowError = $state('')
  let models = $state<string[]>([])
  let chosenModel = $state('')

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
    stopPolling()
    if (loginId) cancelProviderLogin(base, loginId).catch(() => {})
    step = 'idle'
    picked = null
    loginId = ''
    url = ''
    code = ''
    busy = false
    flowError = ''
    models = []
    chosenModel = ''
  }

  // A signed-in provider whose browser hit the local callback directly finishes
  // with no paste, so poll for that and advance hands-free.
  let pollTimer: ReturnType<typeof setInterval> | undefined
  function stopPolling() {
    clearInterval(pollTimer)
    pollTimer = undefined
  }
  $effect(() => {
    if (step !== 'link' || !loginId) return
    const id = loginId
    pollTimer = setInterval(async () => {
      try {
        const res = await providerLoginStatus(base, id)
        if (res.done) {
          stopPolling()
          loginId = '' // consumed server-side; don't cancel on reset
          await toModelStep()
        }
      } catch {
        /* transient; next tick retries, paste is the fallback */
      }
    }, 2000)
    return stopPolling
  })

  // Step 1 -> 2: pick a provider kind and kick off its login.
  async function beginType(t: (typeof TYPES)[number]) {
    if (busy) return
    picked = t
    busy = true
    flowError = ''
    try {
      const res = await startProviderLogin(base, t.id)
      loginId = res.login_id
      url = res.url
      step = 'link'
    } catch (e) {
      flowError = (e as Error).message
      step = 'type'
    } finally {
      busy = false
    }
  }

  // Deliver the pasted callback, then advance to model selection.
  async function complete() {
    if (!code.trim() || busy) return
    busy = true
    flowError = ''
    try {
      await finishProviderLogin(base, loginId, code.trim())
      stopPolling()
      loginId = ''
      await toModelStep()
    } catch (e) {
      flowError = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function toModelStep() {
    step = 'model'
    busy = true
    try {
      const all = await getProviderModels(base)
      const mine = all.filter((m) => picked?.pfx.some((p) => m.toLowerCase().startsWith(p)))
      models = mine.length ? mine : all
      chosenModel = models[0] ?? ''
    } catch {
      models = []
    } finally {
      busy = false
    }
  }

  // A unique display name, so a second Codex doesn't collide with the first.
  function uniqueName(baseName: string): string {
    const taken = new Set(providers.map((p) => p.name.toLowerCase()))
    if (!taken.has(baseName.toLowerCase())) return baseName
    for (let i = 2; i < 100; i++) if (!taken.has(`${baseName} ${i}`.toLowerCase())) return `${baseName} ${i}`
    return baseName
  }

  async function save() {
    if (!chosenModel || busy) return
    busy = true
    flowError = ''
    step = 'saving'
    try {
      // Blank base_url/token: kunai points this at its managed sidecar.
      await saveProvider(base, {
        name: uniqueName(picked?.name ?? 'Provider'),
        base_url: '',
        token: '',
        models: { opus: chosenModel, sonnet: chosenModel, haiku: chosenModel },
      })
      reset()
      await load()
      app.refresh()
    } catch (e) {
      flowError = (e as Error).message
      step = 'model'
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

  const modelOf = (p: Provider): string => p.models?.opus ?? Object.values(p.models ?? {})[0] ?? ''
</script>

<!-- The section header says what providers are. This says the one thing it
     cannot fit and that people get wrong: the agent is unchanged, only the model
     behind it differs. -->
<p class="lede">
    Sign in once and kunai talks to the provider for you. Tools, edits and
    commands work exactly as they do on Claude; only the model behind them
    changes.
  </p>

  {#if error}
    <p class="state err">{error}</p>
  {:else if loading}
    <div class="roster" aria-hidden="true"><div class="row"><span class="nm skname"></span></div></div>
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

  {#if step === 'idle'}
    <button class="add" onclick={() => (step = 'type')}>
      <span class="plus"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg></span>
      <span class="addtext"><span class="at">Add provider</span><span class="as">Sign in to Codex, Grok or Kimi</span></span>
    </button>
  {:else}
    <div class="flow">
      {#if step === 'type'}
        <div class="fhead"><span class="fstep">Choose a provider</span></div>
        <div class="types">
          {#each TYPES as t (t.id)}
            <button class="type" disabled={busy} onclick={() => beginType(t)}>
              <span class="tn">{t.name}</span>
              <span class="th">{t.hint}</span>
            </button>
          {/each}
        </div>
        {#if flowError}<p class="flowerr">{flowError}</p>{/if}
        <div class="actions"><button class="ghost" onclick={reset}>Cancel</button></div>
      {:else if step === 'link'}
        <div class="fhead"><span class="fstep">Sign in to {picked?.name}</span></div>
        <p class="lead">Open the sign-in page, authorize the account, then paste the URL it lands on back here.</p>
        <p class="subtle">
          The link opens the provider, not kunai, so credentials never touch it. The page
          may end on a "can't reach this site" error — that is expected; copy the whole
          address from the browser bar and paste it below.
        </p>
        <a class="cta" href={url} target="_blank" rel="noopener noreferrer">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 01-2 2H5a2 2 0 01-2-2V8a2 2 0 012-2h6" /><path d="M15 3h6v6" /><path d="M10 14L21 3" /></svg>
          Open the sign-in page
        </a>
        <label class="field">
          <span class="flabel">Paste the callback URL or code</span>
          <input class="mono" placeholder="paste it here" bind:value={code} onkeydown={(e) => e.key === 'Enter' && complete()} />
        </label>
        {#if flowError}<p class="flowerr">{flowError}</p>{/if}
        <div class="actions">
          <button class="ghost" onclick={reset} disabled={busy}>Cancel</button>
          <button class="primary" disabled={!code.trim() || busy} onclick={complete}>{busy ? 'Signing in…' : 'Continue'}</button>
        </div>
      {:else}
        <div class="fhead"><span class="fstep">Pick a model for {picked?.name}</span></div>
        {#if models.length}
          <label class="field">
            <span class="flabel">Model</span>
            <div class="selectwrap">
              <select class="mono" bind:value={chosenModel}>
                {#each models as m (m)}<option value={m}>{m}</option>{/each}
              </select>
              <svg class="mchev" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6" /></svg>
            </div>
            <span class="hint">Every in-app model slot maps to this, so any pick runs it.</span>
          </label>
        {:else}
          <p class="subtle">No models reported yet. The sign-in may still be settling — cancel and try again in a moment.</p>
        {/if}
        {#if flowError}<p class="flowerr">{flowError}</p>{/if}
        <div class="actions">
          <button class="ghost" onclick={reset} disabled={step === 'saving'}>Cancel</button>
          <button class="primary" disabled={!chosenModel || step === 'saving'} onclick={save}>{step === 'saving' ? 'Adding…' : 'Add provider'}</button>
        </div>
      {/if}
    </div>
  {/if}

<style>
  /* A column rather than a sheet. The width is the one thing the sheet was
     giving for free that a full-width page does not: prose and forms stop being
     readable much past this, so the constraint stays even though the modal that
     imposed it is gone. */
  .wrap {
    max-width: 620px;
    margin: 0 auto;
    padding: 20px 16px calc(40px + var(--safe-bottom));
  }
  .lede {
    margin: 13px 2px 18px;
    font-size: 13px;
    line-height: 1.6;
    color: var(--text-3);
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
  .types {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .type {
    display: flex;
    flex-direction: column;
    gap: 1px;
    align-items: flex-start;
    padding: 12px 14px;
    border: 1px solid var(--border-2);
    border-radius: 11px;
    background: var(--bg);
    text-align: left;
    transition: border-color 0.12s, background 0.12s;
  }
  .type:hover:not(:disabled) {
    border-color: var(--text-4);
    background: var(--panel-2);
  }
  .type:disabled {
    opacity: 0.5;
  }
  .tn {
    font-size: 14px;
    font-weight: 600;
    color: var(--text);
  }
  .th {
    font-size: 11.5px;
    color: var(--text-4);
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
  }
  .field input:focus {
    outline: none;
    border-color: var(--text-4);
  }
  .selectwrap {
    position: relative;
    display: flex;
    align-items: center;
  }
  .selectwrap select {
    appearance: none;
    -webkit-appearance: none;
    height: 44px;
    width: 100%;
    padding: 0 34px 0 13px;
    background: var(--bg);
    border: 1px solid var(--border-2);
    border-radius: 11px;
    color: var(--text);
    font-size: 14px;
  }
  .selectwrap .mchev {
    right: 13px;
  }
  .hint {
    font-size: 11.5px;
    color: var(--text-4);
  }
  .lead {
    margin: 0;
    font-size: 13px;
    line-height: 1.55;
    color: var(--text-3);
  }
  .subtle {
    margin: 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--text-4);
  }
  .cta {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 9px;
    height: 46px;
    border-radius: 12px;
    background: var(--text);
    color: var(--bg);
    font-size: 14.5px;
    font-weight: 600;
    text-decoration: none;
  }
  .cta:hover {
    opacity: 0.92;
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
