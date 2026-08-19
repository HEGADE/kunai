<script lang="ts">
  import { untrack } from 'svelte'
  import { app } from '../lib/app.svelte'
  import type { AccountInfo } from '../lib/types'
  import {
    fetchAccounts,
    startAccountLogin,
    finishAccountLogin,
    accountLoginStatus,
    cancelAccountLogin,
    removeAccount,
  } from '../lib/api'

  // Manage the Claude accounts a machine can run sessions on. Accounts are
  // per-machine (each keeps its own login and transcripts), so everything is
  // scoped to the selected machine. The list reads like a small credential
  // roster: a status dot, the name, and its role; a signed-out account is dimmed
  // because you cannot switch a session onto it.
  //
  // A section of Settings rather than a page of its own. It was both for a
  // while, and that was the worst of the two: this UI, with the real sign-in
  // flow, AND a second list of the same accounts inside Settings that linked
  // across to here. The machine now comes from Settings, which is also what
  // stops two machine pickers from disagreeing on screen.
  let { machineId }: { machineId: string } = $props()
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
  //
  // PROVIDERS ARE FILTERED OUT, and that is a correctness fix rather than
  // tidiness. `stats.clis` is what a New Session picker offers, which is
  // accounts AND providers (cliNames on the server appends providerList);
  // /api/accounts is accounts alone. Seeded raw, this listed Codex and Grok as
  // Claude subscriptions for the second before the fetch landed, and then
  // dropped them, so the section both said something false and jumped as it
  // corrected itself. provider_models is keyed by provider name, which is
  // exactly the set to remove.
  function seedFromCache() {
    const stats = machine?.stats
    const providers = new Set(Object.keys(stats?.provider_models ?? {}))
    const names = (stats?.clis ?? []).filter((n) => !providers.has(n))
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

  // A newer CLI can complete the login on its own: if the browser is on this
  // machine, it hits the CLI's localhost callback directly and no code is ever
  // pasted. So while the paste box is shown, poll for that — and finish
  // hands-free when it happens, rather than waiting on a paste that won't come.
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
        const res = await accountLoginStatus(base, id)
        if (res.done) {
          stopPolling()
          loginId = '' // already registered server-side; don't cancel it on reset
          reset()
          await load()
        }
      } catch {
        // A transient poll failure is harmless; the next tick retries, and the
        // manual paste is always available as a fallback.
      }
    }, 2000)
    return stopPolling
  })

  function reset() {
    stopPolling()
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


  {#if error}
    <p class="st-note bad">{error}</p>
  {:else}
    <!-- The accounts and the way to add one live in ONE card. They were two
         containers with two different borders, one of them dashed, which made a
         list of two things look like two unrelated widgets. -->
    <div class="st-card">
      {#if loading}
        <div class="st-row" aria-hidden="true">
          <span class="st-dot"></span><span class="skname"></span>
        </div>
      {:else}
        {#each accounts as a (a.name)}
          <div class="st-row" class:off={a.ready === false}>
            <span
              class="st-dot"
              class:on={a.ready === true}
              class:checking={a.ready === undefined}></span>
            <span class="st-k">
              <span class="st-name">{a.name}</span>
              {#if statusText(a)}<span class="st-sub-text">{statusText(a)}</span>{/if}
            </span>
            {#if a.default}<span class="st-badge">default</span>{/if}
            {#if !a.default}
              <button class="st-btn ghost danger" onclick={() => remove(a)} aria-label="Remove {a.name}">
                Remove
              </button>
            {/if}
          </div>
        {/each}
      {/if}

      {#if step === 'idle'}
        <button class="st-row add" onclick={() => (step = 'name')}>
          <span class="plus" aria-hidden="true">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
          </span>
          <span class="st-k">
            <span class="st-name">Add account</span>
            <span class="st-sub-text">Sign in another Claude subscription</span>
          </span>
        </button>
      {/if}
    </div>
  {/if}

  {#if step !== 'idle'}
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
          account: they sign in, and only the code comes back. Copy <b>all</b> of what
          the page gives back, including everything after the <code class="hash">#</code>.
          If it ends on a "can't reach the site" error instead, that is expected too:
          copy the whole address out of the browser bar.
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

<style>
  /* A column rather than a sheet. The width is what a sheet was giving for free
     and a full-width page is not: prose and forms stop being readable past
     about this, so the constraint stays even though the modal that imposed it
     is gone. */
  /* The roster and the add row come from settings.css. What stays here is only
     what is specific to this section: the skeleton, the dimming of a signed-out
     account, and the add row's plus. */
  .off {
    opacity: 0.55;
  }
  .st-dot.checking {
    background: var(--text-4);
    opacity: 0.5;
  }
  .skname {
    flex: 1;
    height: 11px;
    max-width: 120px;
    border-radius: 4px;
    background: var(--panel-2);
  }
  /* The add row is a row of the same card, not a dashed slot beside it: two
     containers with two different borders made a list of two things look like
     two unrelated widgets. */
  .add {
    width: 100%;
    background: none;
  }
  .add:hover {
    background: var(--panel-2);
  }
  .plus {
    flex: none;
    width: 24px;
    height: 24px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 7px;
    border: 1px solid var(--border);
    color: var(--text-3);
  }
  .add:hover .plus {
    color: var(--text-2);
    border-color: var(--border-2);
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
  /* The one character people leave behind when they copy the code. */
  .subtle .hash {
    font-family: var(--mono);
    color: var(--text-2);
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
