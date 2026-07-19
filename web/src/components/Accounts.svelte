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

  // Manage the Claude accounts a machine can run sessions on. Each account is an
  // identity with its own monogram sigil; adding one mints that identity as you
  // name it, then hands you the sign-in link. Accounts are per-machine (each keeps
  // its own login + transcripts), so everything is scoped to the selected machine.
  let machineId = $state(app.activeMachineId ?? app.machines[0]?.id ?? '')
  const base = $derived(app.baseForMachine(machineId))
  const machine = $derived(app.machines.find((m) => m.id === machineId) ?? null)

  let accounts = $state<AccountInfo[]>([])
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

  // A monogram: up to two initials from the account name, the identity sigil.
  function initials(n: string): string {
    const parts = n.trim().split(/\s+/).filter(Boolean)
    if (!parts.length) return '?'
    if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
    return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase()
  }

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
      <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6" /></svg>
    </button>
    <div class="htext">
      <span class="eyebrow">Identities</span>
      <h1>Claude accounts</h1>
    </div>
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

  <div class="body">
    {#if loading}
      <p class="state">Loading accounts…</p>
    {:else if error}
      <p class="state err">{error}</p>
    {:else}
      <ul class="ids">
        {#each accounts as a (a.name)}
          <li class="id" class:muted={!a.ready}>
            <span class="sigil" class:def={a.default}>{initials(a.name)}</span>
            <span class="meta">
              <span class="nm">{a.name}</span>
              <span class="sub">
                {#if a.default}Default account{:else if a.ready}Signed in{:else}Signed out{/if}
              </span>
            </span>
            {#if a.ready}<span class="live" title="Signed in"></span>{/if}
            {#if !a.default}
              <button class="rm" onclick={() => remove(a)} aria-label="Remove {a.name}">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18" /><path d="M8 6V4a2 2 0 012-2h4a2 2 0 012 2v2" /><path d="M6 6l1 14a2 2 0 002 2h6a2 2 0 002-2l1-14" /></svg>
              </button>
            {/if}
          </li>
        {/each}

        {#if step === 'idle'}
          <li>
            <button class="addrow" onclick={() => (step = 'name')}>
              <span class="sigil add">
                <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
              </span>
              <span class="meta">
                <span class="nm">Add account</span>
                <span class="sub">Sign in another Claude subscription</span>
              </span>
            </button>
          </li>
        {/if}
      </ul>
    {/if}

    {#if step !== 'idle'}
      <div class="flow">
        {#if step === 'name'}
          <div class="mint">
            <span class="sigil big" class:empty={!name.trim()}>{name.trim() ? initials(name) : '?'}</span>
            <div class="mintf">
              <label class="flabel" for="acctname">Name this identity</label>
              <input
                id="acctname"
                placeholder="Work"
                bind:value={name}
                onkeydown={(e) => e.key === 'Enter' && beginLink()}
                autofocus />
              <span class="hint">A label only, so you can tell your accounts apart.</span>
            </div>
          </div>
          <div class="actions">
            <button class="ghost" onclick={reset}>Cancel</button>
            <button class="primary" disabled={!name.trim() || busy} onclick={beginLink}>
              {busy ? 'Preparing…' : 'Continue'}
            </button>
          </div>
        {:else}
          <div class="signin">
            <span class="sigil big" class:def={true}>{initials(name)}</span>
            <p class="lead">
              Almost there. Open the sign-in page, log in as the account you want
              <b>{name}</b> to be, then paste the code Claude gives you back.
            </p>
          </div>
          <a class="cta" href={url} target="_blank" rel="noopener noreferrer">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 01-2 2H5a2 2 0 01-2-2V8a2 2 0 012-2h6" /><path d="M15 3h6v6" /><path d="M10 14L21 3" /></svg>
            Open the sign-in page
          </a>
          <label class="codef">
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
  </div>
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
    max-width: 540px;
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
    gap: 12px;
  }
  .back {
    flex: none;
    width: 34px;
    height: 34px;
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
  .htext {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .eyebrow {
    font-family: var(--mono);
    font-size: 10.5px;
    letter-spacing: 0.14em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .htext h1 {
    margin: 0;
    font-size: 20px;
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
    margin: 14px 2px 20px;
    font-size: 13.5px;
    line-height: 1.6;
    color: var(--text-3);
  }

  .body {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .ids {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 9px;
  }

  /* The signature: an identity row. A monogram sigil carries the account's
     identity; the name and status sit beside it. */
  .id {
    display: flex;
    align-items: center;
    gap: 13px;
    padding: 12px 14px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 15px;
    transition: border-color 0.12s;
  }
  .id.muted .nm {
    color: var(--text-2);
  }
  .sigil {
    flex: none;
    width: 42px;
    height: 42px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 13px;
    border: 1px solid var(--border-2);
    background: var(--panel-2);
    font-family: var(--mono);
    font-size: 15px;
    font-weight: 600;
    letter-spacing: 0.02em;
    color: var(--text-2);
  }
  /* The default account's sigil is inverted: it is the machine's home identity. */
  .sigil.def {
    background: var(--text);
    color: var(--bg);
    border-color: var(--text);
  }
  .sigil.add {
    border-style: dashed;
    color: var(--text-4);
    background: none;
  }
  .meta {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .nm {
    font-size: 15px;
    font-weight: 600;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sub {
    font-size: 12px;
    color: var(--text-4);
  }
  .live {
    flex: none;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--live);
    box-shadow: 0 0 0 3px color-mix(in oklab, var(--live) 18%, transparent);
  }
  .rm {
    flex: none;
    width: 32px;
    height: 32px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 9px;
    color: var(--text-4);
  }
  .rm:hover {
    color: var(--alert);
    background: var(--panel-2);
  }

  /* The add row reads as an empty identity slot, consistent with the list. */
  .addrow {
    display: flex;
    align-items: center;
    gap: 13px;
    width: 100%;
    text-align: left;
    padding: 12px 14px;
    border: 1px dashed var(--border-2);
    border-radius: 15px;
    color: var(--text-2);
  }
  .addrow:hover {
    border-color: var(--text-4);
    background: var(--panel);
  }
  .addrow:hover .sigil.add {
    color: var(--text-2);
    border-color: var(--text-4);
  }

  .state {
    font-size: 13px;
    color: var(--text-4);
    padding: 6px 2px;
  }
  .state.err {
    color: var(--alert);
  }

  /* The mint / sign-in ceremony. */
  .flow {
    border: 1px solid var(--border-2);
    border-radius: 17px;
    background: var(--panel);
    padding: 18px;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  .mint,
  .signin {
    display: flex;
    align-items: center;
    gap: 15px;
  }
  .signin {
    align-items: flex-start;
  }
  .sigil.big {
    width: 56px;
    height: 56px;
    border-radius: 17px;
    font-size: 20px;
  }
  .sigil.big.empty {
    color: var(--text-4);
    background: var(--panel-2);
  }
  .mintf {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 5px;
  }
  .flabel {
    font-size: 12.5px;
    color: var(--text-3);
  }
  .mintf input,
  .code {
    height: 42px;
    padding: 0 13px;
    background: var(--bg);
    border: 1px solid var(--border-2);
    border-radius: 11px;
    color: var(--text);
    font-size: 15.5px;
    width: 100%;
  }
  .mintf input:focus,
  .code:focus {
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
  .lead b {
    color: var(--text);
    font-weight: 600;
  }
  .cta {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 9px;
    height: 48px;
    border-radius: 13px;
    background: var(--text);
    color: var(--bg);
    font-size: 15px;
    font-weight: 600;
    text-decoration: none;
  }
  .cta:hover {
    opacity: 0.92;
  }
  .codef {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .code {
    font-family: var(--mono);
    letter-spacing: 0.04em;
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
