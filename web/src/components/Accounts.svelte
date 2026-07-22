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
  // per-machine (each keeps its own login and transcripts), so everything is
  // scoped to the selected machine. The list reads like a small credential
  // roster: a status dot, the name, and its role; a signed-out account is dimmed
  // because you cannot switch a session onto it.
  let machineId = $state(app.activeMachineId ?? app.machines[0]?.id ?? '')
  const base = $derived(app.baseForMachine(machineId))
  const machine = $derived(app.machines.find((m) => m.id === machineId) ?? null)

  // A row's `ready` is undefined while its signed-in check is still in flight, so
  // the dot can show "checking" instead of guessing. Cached names paint at once;
  // fetchAccounts fills the real status in.
  type Row = { name: string; default: boolean; ready?: boolean }
  let accounts = $state<Row[]>([])
  let loading = $state(true)
  let error = $state('')

  type Step = 'idle' | 'name' | 'link' | 'saving'
  let step = $state<Step>('idle')
  let name = $state('')
  let loginId = $state('')
  let url = $state('')
  let code = $state('')
  let busy = $state(false)
  let flowError = $state('')

  // Seed rows from the machine's cached account names so the list paints the
  // instant it opens, with status still resolving. The names ship in /api/stats
  // only when the machine has a real choice (>1 account); a single-account
  // machine has none cached, so it falls through to the skeleton.
  function seedFromCache() {
    const names = machine?.stats?.clis ?? []
    accounts = names.map((n, i) => ({ name: n, default: i === 0 }))
  }

  async function load() {
    error = ''
    seedFromCache()
    loading = accounts.length === 0
    try {
      accounts = await fetchAccounts(base)
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

  async function remove(a: Row) {
    if (a.default) return
    try {
      await removeAccount(base, a.name)
      await load()
    } catch (e) {
      error = (e as Error).message
    }
  }

  const statusText = (a: Row): string =>
    a.ready === undefined ? 'checking' : a.ready ? '' : 'signed out'
</script>

<div class="backdrop" onclick={() => app.closeAccounts()} role="presentation">
<section class="sheet" role="dialog" aria-label="Claude accounts" onclick={(e) => e.stopPropagation()}>
  <header class="top">
    <button class="back" onclick={() => app.closeAccounts()} aria-label="Back">
      <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6" /></svg>
    </button>
    <h1>Claude accounts</h1>
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
    Run more than one Claude on {machine ? machine.label : 'this machine'} (a personal
    and a work subscription, say) and choose which one each session runs on. When one
    hits its limit, switch a session to the other.
  </p>

  {#if error}
    <p class="state err">{error}</p>
  {:else if loading}
    <div class="roster" aria-hidden="true">
      {#each [0, 1] as i (i)}
        <div class="row"><span class="dot checking"></span><span class="skname"></span></div>
      {/each}
    </div>
  {:else}
    <div class="roster">
      {#each accounts as a (a.name)}
        <div class="row" class:off={a.ready === false}>
          <span
            class="dot"
            class:on={a.ready === true}
            class:hollow={a.ready === false}
            class:checking={a.ready === undefined}></span>
          <span class="nm">{a.name}</span>
          {#if a.default}<span class="tag">default</span>{/if}
          {#if statusText(a)}<span class="status">{statusText(a)}</span>{/if}
          {#if !a.default}
            <button class="rm" onclick={() => remove(a)} aria-label="Remove {a.name}" title="Remove {a.name}">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18" /><path d="M8 6V4a2 2 0 012-2h4a2 2 0 012 2v2" /><path d="M6 6l1 14a2 2 0 002 2h6a2 2 0 002-2l1-14" /></svg>
            </button>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  {#if step === 'idle'}
    <button class="add" onclick={() => (step = 'name')}>
      <span class="plus">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
      </span>
      <span class="addtext">
        <span class="at">Add account</span>
        <span class="as">Sign in another Claude subscription</span>
      </span>
    </button>
  {:else}
    <div class="flow">
      {#if step === 'name'}
        <div class="fhead"><span class="fstep">New account</span><span class="fnum">Step 1 of 2</span></div>
        <label class="field">
          <span class="flabel">Name this account</span>
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
            {busy ? 'Preparing…' : 'Continue'}
          </button>
        </div>
      {:else}
        <div class="fhead"><span class="fstep">Sign in {name}</span><span class="fnum">Step 2 of 2</span></div>
        <p class="lead">
          Open the sign-in page, log in as the account you want <b>{name}</b> to be,
          then paste the code it gives back.
        </p>
        <p class="subtle">
          The link opens Claude, not kunai, so it is safe to send to whoever owns the
          account: they sign in, and only the code comes back. If the page ends on a
          "can't reach the site" error, that is expected. Copy the whole address from
          the browser bar and paste it below.
        </p>
        <a class="cta" href={url} target="_blank" rel="noopener noreferrer">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 01-2 2H5a2 2 0 01-2-2V8a2 2 0 012-2h6" /><path d="M15 3h6v6" /><path d="M10 14L21 3" /></svg>
          Open the sign-in page
        </a>
        <label class="field">
          <span class="flabel">Paste the code</span>
          <input
            class="code"
            placeholder="paste it here"
            bind:value={code}
            onkeydown={(e) => e.key === 'Enter' && complete()}
            disabled={step === 'saving'} />
        </label>
        {#if flowError}<p class="flowerr">{flowError}</p>{/if}
        <div class="actions">
          <button class="ghost" onclick={reset} disabled={step === 'saving'}>Cancel</button>
          <button class="primary" disabled={!code.trim() || step === 'saving'} onclick={complete}>
            {step === 'saving' ? 'Signing in…' : 'Finish'}
          </button>
        </div>
      {/if}
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

  /* The roster: one framed panel, hairline-divided rows. A single account reads
     as one line, not a card, so a machine's whole identity list is legible in a
     glance. */
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
  .row.off .nm {
    color: var(--text-3);
  }

  /* The status dot is the whole signal: filled green when signed in, a hollow
     ring when signed out, a soft pulse while the check is still in flight. */
  .dot {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    box-sizing: border-box;
  }
  .dot.on {
    background: var(--live);
    box-shadow: 0 0 0 3px color-mix(in oklab, var(--live) 16%, transparent);
  }
  .dot.hollow {
    border: 1.5px solid var(--text-4);
  }
  .dot.checking {
    background: var(--text-3);
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
  .tag {
    flex: none;
    font-family: var(--mono);
    font-size: 10px;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--text-4);
    padding: 3px 7px;
    border: 1px solid var(--border-2);
    border-radius: 6px;
  }
  .status {
    flex: none;
    font-family: var(--mono);
    font-size: 11.5px;
    color: var(--text-3);
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
    width: 96px;
    border-radius: 4px;
    background: var(--panel-3);
    animation: pulse 1.1s ease-in-out infinite;
  }

  .state {
    font-size: 13px;
    color: var(--text-4);
    padding: 14px 4px;
  }
  .state.err {
    color: var(--alert);
  }

  /* Add is a distinct dashed slot below the list, not a row in it. */
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

  /* The two-step add flow. Numbering is real here: name, then sign in. */
  .flow {
    margin-top: 12px;
    border: 1px solid var(--border-2);
    border-radius: var(--r-lg);
    background: var(--panel);
    padding: 16px 17px 17px;
    display: flex;
    flex-direction: column;
    gap: 15px;
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
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .fnum {
    flex: none;
    font-family: var(--mono);
    font-size: 10.5px;
    letter-spacing: 0.06em;
    text-transform: uppercase;
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
  .field input:focus {
    outline: none;
    border-color: var(--text-4);
  }
  .code {
    font-family: var(--mono);
    letter-spacing: 0.04em;
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
  .lead b {
    color: var(--text);
    font-weight: 600;
  }
  .subtle {
    margin: 8px 0 0;
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
