<script lang="ts">
  import { untrack } from 'svelte'
  import { app } from '../lib/app.svelte'
  import type { AccountInfo } from '../lib/types'
  import {
    fetchAccounts,
    startAccountLogin,
    finishAccountLogin,
    cancelAccountLogin,
    removeAccount,
  } from '../lib/api'

  // Manage the Claude accounts a machine can run sessions on. Accounts are
  // per-machine (each keeps its own login + transcripts), so everything here is
  // scoped to the selected machine's origin. Adding one drives `claude auth login`
  // on that machine and hands you the sign-in link — no terminal, no paths.

  let machineId = $state(app.activeMachineId ?? app.machines[0]?.id ?? '')
  const base = $derived(app.baseForMachine(machineId))
  const machine = $derived(app.machines.find((m) => m.id === machineId) ?? null)

  let accounts = $state<AccountInfo[]>([])
  let loading = $state(true)
  let error = $state('')

  // The add flow is a small state machine: name it, sign in, done.
  type Step = 'idle' | 'name' | 'link' | 'saving'
  let step = $state<Step>('idle')
  let name = $state('')
  let loginId = $state('')
  let url = $state('')
  let code = $state('')
  let busy = $state(false)
  let flowError = $state('')

  async function load() {
    loading = true
    error = ''
    try {
      accounts = await fetchAccounts(base)
    } catch (e) {
      error = (e as Error).message
    } finally {
      loading = false
    }
  }
  $effect(() => {
    void base // re-scope when the machine changes; untrack so the flow state the
    untrack(() => {
      // reset/load touch doesn't make this effect depend on it (which would loop).
      reset()
      load()
    })
  })

  function reset() {
    if (loginId) cancelAccountLogin(base, loginId).catch(() => {})
    step = 'idle'
    name = ''
    loginId = ''
    url = ''
    code = ''
    flowError = ''
    busy = false
  }

  async function beginLink() {
    if (!name.trim() || busy) return
    busy = true
    flowError = ''
    try {
      const res = await startAccountLogin(base, name.trim())
      loginId = res.login_id
      url = res.url
      step = 'link'
    } catch (e) {
      flowError = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function complete() {
    if (!code.trim() || busy) return
    busy = true
    flowError = ''
    step = 'saving'
    try {
      await finishAccountLogin(base, loginId, code.trim())
      reset()
      await load()
    } catch (e) {
      flowError = (e as Error).message
      step = 'link'
      busy = false
    }
  }

  async function remove(a: AccountInfo) {
    if (a.default) return
    try {
      await removeAccount(base, a.name)
      await load()
    } catch (e) {
      error = (e as Error).message
    }
  }
</script>

<div class="backdrop" onclick={() => app.closeAccounts()} role="presentation">
<section class="sheet" role="dialog" aria-label="Claude accounts" onclick={(e) => e.stopPropagation()}>
  <header class="top">
    <button class="back" onclick={() => app.closeAccounts()} aria-label="Back">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6" /></svg>
    </button>
    <div class="titles">
      <h1>Claude accounts</h1>
      <p>Run more than one Claude on this machine (say a personal and a work
        subscription) and pick which one a session uses. When one's limit runs
        out, switch to the other.</p>
    </div>
  </header>

  {#if app.machines.length > 1}
    <label class="mrow">
      <span class="mlabel">Machine</span>
      <select bind:value={machineId}>
        {#each app.machines as m (m.id)}
          <option value={m.id}>{m.label}{m.self ? ' (this one)' : ''}</option>
        {/each}
      </select>
    </label>
  {/if}

  <div class="body">
    {#if loading}
      <p class="state">Loading accounts…</p>
    {:else if error}
      <p class="state err">{error}</p>
    {:else}
      <ul class="list">
        {#each accounts as a (a.name)}
          <li class="acct">
            <span class="dot" class:on={a.ready} title={a.ready ? 'Signed in' : 'Signed out'}></span>
            <span class="nm">{a.name}</span>
            {#if a.default}<span class="tag">Default</span>{/if}
            {#if !a.ready}<span class="warn">Signed out</span>{/if}
            <span class="sp"></span>
            {#if !a.default}
              <button class="rm" onclick={() => remove(a)} aria-label="Remove {a.name}">Remove</button>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}

    <!-- Add flow -->
    {#if step === 'idle'}
      <button class="add" onclick={() => (step = 'name')}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
        Add account
      </button>
    {:else}
      <div class="flow">
        <div class="steps">
          <span class="step" class:on={step === 'name'} class:done={step !== 'name'}>1 · Name</span>
          <span class="bar"></span>
          <span class="step" class:on={step !== 'name'}>2 · Sign in</span>
        </div>

        {#if step === 'name'}
          <label class="field">
            <span class="flabel">What do you want to call this account?</span>
            <input
              placeholder="Work"
              bind:value={name}
              onkeydown={(e) => e.key === 'Enter' && beginLink()}
              autofocus />
            <span class="hint">A label only, so you can tell your accounts apart.</span>
          </label>
          <div class="actions">
            <button class="ghost" onclick={reset}>Cancel</button>
            <button class="primary" disabled={!name.trim() || busy} onclick={beginLink}>
              {busy ? 'Starting…' : 'Continue'}
            </button>
          </div>
        {:else}
          <p class="lead">
            Open the sign-in page, log in as the account you want to add
            {#if machine}<b>on {machine.label}</b>{/if}, then paste the code Claude
            gives you back here.
          </p>
          <a class="link" href={url} target="_blank" rel="noopener noreferrer">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 01-2 2H5a2 2 0 01-2-2V8a2 2 0 012-2h6" /><path d="M15 3h6v6" /><path d="M10 14L21 3" /></svg>
            Open the sign-in page
          </a>
          <label class="field">
            <span class="flabel">Paste the code</span>
            <input
              placeholder="Paste the code from the browser"
              bind:value={code}
              onkeydown={(e) => e.key === 'Enter' && complete()}
              disabled={step === 'saving'} />
          </label>
          {#if flowError}<p class="flowerr">{flowError}</p>{/if}
          <div class="actions">
            <button class="ghost" onclick={reset} disabled={step === 'saving'}>Cancel</button>
            <button class="primary" disabled={!code.trim() || step === 'saving'} onclick={complete}>
              {step === 'saving' ? 'Signing in…' : 'Add account'}
            </button>
          </div>
        {/if}
      </div>
    {/if}
  </div>
</section>
</div>

<style>
  /* One flex-centered backdrop with the sheet as a child (mirrors Settings): iOS
     Safari collapses two sibling fixed elements sized with inset:0 + margin:auto
     to near-zero height, which showed only the dark scrim. */
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 60;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 20px;
  }
  .sheet {
    width: 100%;
    max-width: 560px;
    max-height: min(88dvh, 780px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
    border: 1px solid var(--border-2);
    border-radius: 16px;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    box-shadow: 0 24px 70px -24px rgba(0, 0, 0, 0.75);
    padding: 22px;
  }
  .top {
    display: flex;
    gap: 12px;
    align-items: flex-start;
  }
  .back {
    flex: none;
    width: 34px;
    height: 34px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 9px;
    color: var(--text-3);
    margin-top: 2px;
  }
  .back:hover {
    background: var(--panel);
    color: var(--text);
  }
  .titles h1 {
    margin: 0 0 6px;
    font-size: 21px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }
  .titles p {
    margin: 0;
    font-size: 13.5px;
    line-height: 1.55;
    color: var(--text-3);
    max-width: 44ch;
  }
  .mrow {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 20px 0 0;
  }
  .mlabel {
    font-size: 12px;
    color: var(--text-4);
  }
  select {
    flex: 1;
    height: 36px;
    padding: 0 10px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 9px;
    color: var(--text);
    font-size: 13.5px;
  }
  .body {
    margin-top: 22px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .acct {
    display: flex;
    align-items: center;
    gap: 11px;
    padding: 13px 14px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
  }
  .dot {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .dot.on {
    background: var(--live);
  }
  .nm {
    font-size: 14.5px;
    font-weight: 550;
    color: var(--text);
  }
  .tag {
    font-size: 10.5px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--text-4);
    border: 1px solid var(--border-2);
    border-radius: 100px;
    padding: 1px 8px;
  }
  .warn {
    font-size: 11.5px;
    color: var(--busy);
  }
  .sp {
    flex: 1;
  }
  .rm {
    font-size: 12px;
    color: var(--text-4);
    padding: 4px 8px;
    border-radius: 7px;
  }
  .rm:hover {
    color: var(--alert);
    background: var(--panel-2);
  }
  .state {
    font-size: 13px;
    color: var(--text-4);
    padding: 4px 2px;
  }
  .state.err {
    color: var(--alert);
  }

  .add {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    height: 44px;
    border: 1px dashed var(--border-2);
    border-radius: 12px;
    color: var(--text-2);
    font-size: 14px;
    font-weight: 500;
  }
  .add:hover {
    border-color: var(--text-4);
    color: var(--text);
    background: var(--panel);
  }

  /* Add flow: a two-step card. The numbering is real (name, then sign in), so it
     doubles as a progress rail. */
  .flow {
    border: 1px solid var(--border);
    border-radius: 14px;
    background: var(--panel);
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .steps {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .steps .step.on {
    color: var(--text);
  }
  .steps .step.done {
    color: var(--text-3);
  }
  .steps .bar {
    flex: 1;
    height: 1px;
    background: var(--border-2);
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .flabel {
    font-size: 13px;
    color: var(--text-2);
  }
  .field input {
    height: 42px;
    padding: 0 13px;
    background: var(--bg);
    border: 1px solid var(--border-2);
    border-radius: 10px;
    color: var(--text);
    font-size: 15px;
  }
  .field input:focus {
    outline: none;
    border-color: var(--text-4);
  }
  .hint {
    font-size: 11.5px;
    color: var(--text-4);
  }
  .lead {
    margin: 0;
    font-size: 13.5px;
    line-height: 1.55;
    color: var(--text-3);
  }
  .link {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 9px;
    height: 46px;
    border-radius: 11px;
    background: var(--text);
    color: var(--bg);
    font-size: 14.5px;
    font-weight: 550;
    text-decoration: none;
  }
  .link:hover {
    opacity: 0.9;
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
    height: 38px;
    padding: 0 14px;
    border-radius: 9px;
    color: var(--text-3);
    font-size: 13.5px;
  }
  .ghost:hover {
    color: var(--text);
    background: var(--panel-2);
  }
  .primary {
    height: 38px;
    padding: 0 16px;
    border-radius: 9px;
    background: var(--text);
    color: var(--bg);
    font-size: 13.5px;
    font-weight: 550;
  }
  .primary:disabled {
    opacity: 0.4;
  }
</style>
