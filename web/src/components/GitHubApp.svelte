<script lang="ts">
  import { app } from '../lib/app.svelte'
  import {
    githubApp,
    setGitHubApp,
    reviewConfig,
    setReviewConfig,
    type GitHubAppState,
    type ReviewConfig,
  } from '../lib/api'
  import { handle, setHandle } from '../lib/reviewer'
  import { MODELS, modelLabel } from '../lib/models'

  // The GitHub App this machine posts reviews as.
  //
  // Two things are being set here and they are deliberately independent. The App
  // is shared: everybody's kunai posts as the same kunai[bot], which is what
  // makes reviews consistently attributed on an org's pull requests. The handle
  // is personal: it names who asked for a review, because a shared identity
  // otherwise makes every review anonymous.
  //
  // The private key is write-only from here. It is never returned by the server,
  // so this shows whether one is configured and nothing about what it is.
  let { machineId }: { machineId: string } = $props()

  let appState = $state<GitHubAppState | null>(null)
  let appId = $state('')
  let key = $state('')
  let who = $state(handle())
  let busy = $state(false)
  let err = $state('')
  let saved = $state('')

  const base = $derived(app.baseForMachine(machineId))

  // Which account and model reviews run on. Its own setting, because a review is
  // chunky and arrives on somebody else's schedule: pointed at a second account
  // or a provider it can never wall the session you are sitting in.
  let rcfg = $state<ReviewConfig>({})
  const accounts = $derived(app.machines.find((m) => m.id === machineId)?.stats?.clis ?? [])

  // Asked WITH the check, because this is the one screen where somebody is
  // looking at the setup and can act on the answer. The dashboard's poll asks
  // without it: the check costs two round trips to github.com.
  $effect(() => {
    void machineId
    checking = true
    githubApp(base, true)
      .then((s) => (appState = s))
      .catch(() => (appState = { configured: false }))
      .finally(() => (checking = false))
    reviewConfig(base)
      .then((c) => (rcfg = c))
      .catch(() => (rcfg = {}))
  })
  let checking = $state(false)

  async function saveReviewCfg(patch: ReviewConfig) {
    const next = { ...rcfg, ...patch }
    rcfg = next
    try {
      rcfg = await setReviewConfig(base, next)
      saved = 'Saved'
      setTimeout(() => (saved = ''), 2500)
    } catch (e) {
      err = (e as Error).message
    }
  }

  async function save() {
    if (busy) return
    busy = true
    err = ''
    saved = ''
    try {
      appState = await setGitHubApp(base, { app_id: appId, private_key: key })
      // Cleared on success: there is no reason to keep a private key in a text
      // box, and leaving it there invites a screenshot.
      appId = ''
      key = ''
      saved = 'Saved'
      setTimeout(() => (saved = ''), 2500)
    } catch (e) {
      err = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function clear() {
    busy = true
    err = ''
    try {
      appState = await setGitHubApp(base, { clear: true })
    } catch (e) {
      err = (e as Error).message
    } finally {
      busy = false
    }
  }

  function saveWho() {
    setHandle(who)
    saved = 'Saved'
    setTimeout(() => (saved = ''), 2500)
  }
</script>

<div class="sec">GitHub</div>

<p class="lead">
  Reviews are posted by a GitHub App, so they appear as a bot on your team's pull
  requests rather than under your own account. The App needs no webhook: kunai
  only ever calls out.
</p>

{#if appState?.configured}
  <!-- "Configured" alone was the message being shown while nothing worked: it
       reported that two files exist on disk, which is true of a key from the
       wrong App and of an App installed on nothing. What it says now is what
       GitHub answered when asked. -->
  <div class="row">
    {#if checking}
      <span class="quiet">Checking with GitHub…</span>
    {:else if appState.error}
      <span class="bad">Not working</span>
    {:else if appState.check?.orgs?.length}
      <span class="ok">Working</span>
      <span class="aid">
        {appState.check.name ? appState.check.name + ' · ' : ''}installed on {appState.check.orgs.join(', ')}
      </span>
    {:else}
      <span class="warn">Not finished</span>
    {/if}
    {#if appState.app_id}<span class="mono aid">App {appState.app_id}</span>{/if}
    <button class="mini" onclick={clear} disabled={busy}>Remove</button>
  </div>

  {#if appState.error}
    <p class="err">{appState.error}</p>
  {:else if appState.check?.warning}
    <p class="warnline">
      {appState.check.warning}
      {#if appState.check.install_url}
        <a href={appState.check.install_url} target="_blank" rel="noreferrer">Install it &rarr;</a>
      {/if}
    </p>
  {:else if appState.check?.partial}
    <!-- The setting behind the confusing failure: everything reports configured
         and one repository still refuses because it was never ticked. -->
    <p class="lead quiet">
      This App covers only selected repositories on at least one organisation. A
      repository that was not ticked will refuse, even though everything here
      looks right.
      {#if appState.check.install_url}
        <a href={appState.check.install_url} target="_blank" rel="noreferrer">Change what it covers &rarr;</a>
      {/if}
    </p>
  {/if}
{:else}
  <p class="lead quiet">
    Register an App on your organisation with pull requests read and write,
    contents read, and metadata read. Leave webhooks off, then paste its id and a
    private key here. kunai checks both against GitHub before saving, so a
    mismatched pair is refused here rather than at the first review.
  </p>
{/if}

<div class="fields">
  <input class="min mono" placeholder="App id" bind:value={appId} autocomplete="off" />
  <textarea
    class="min mono key"
    placeholder="-----BEGIN RSA PRIVATE KEY-----"
    bind:value={key}
    spellcheck="false"
    autocomplete="off"
  ></textarea>
  <button class="save" onclick={save} disabled={busy || !appId.trim() || !key.trim()}>
    {appState?.configured ? 'Replace key' : 'Save'}
  </button>
</div>

<p class="lead quiet">
  Give each machine its own key. An App can hold several and revoke them one at a
  time, so a lost laptop costs one key instead of a redistribution to everybody.
</p>

{#if appState?.configured}
  <div class="sec">Reviews run on</div>
  <p class="lead">
    A review is long and arrives when a colleague opens a pull request, not when
    you are ready for it. Point it at a second account or a provider and it can
    never spend the window you are working in.
  </p>
  <div class="picks">
    <label class="pick">
      <span class="plbl">Account</span>
      <select class="min" value={rcfg.cli ?? ''} onchange={(e) => saveReviewCfg({ cli: e.currentTarget.value })}>
        <option value="">Default{accounts[0] ? ` (${accounts[0]})` : ''}</option>
        {#each accounts as a (a)}
          <option value={a}>{a}</option>
        {/each}
      </select>
    </label>
    <label class="pick">
      <span class="plbl">Model</span>
      <select class="min" value={rcfg.model ?? ''} onchange={(e) => saveReviewCfg({ model: e.currentTarget.value })}>
        <option value="">Default</option>
        {#each MODELS as m (m.id)}
          <option value={m.id}>{modelLabel(m.id)}</option>
        {/each}
      </select>
    </label>
  </div>
{/if}

<div class="sec">Your GitHub handle</div>
<p class="lead">
  Named in the reviews you request. With one bot identity shared across the team,
  this is the only thing that says who to ask about a finding.
</p>
<div class="fields">
  <input class="min mono" placeholder="shorya" bind:value={who} autocomplete="off" />
  <button class="save" onclick={saveWho}>Save</button>
</div>

{#if err}<p class="err">{err}</p>{/if}
{#if saved}<p class="ok inline">{saved}</p>{/if}

<style>
  .sec {
    font-size: 11.5px;
    font-weight: 550;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-3);
    padding: 20px 0 8px;
  }
  .lead {
    margin: 0 0 10px;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--text-3);
  }
  .quiet {
    color: var(--text-4);
    font-size: 11.5px;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 10px;
  }
  .ok {
    font-size: 12.5px;
    color: var(--live);
  }
  /* The same two status colours the rest of the app uses: amber is "needs you",
     red is "broken". No new hue for a setup screen. */
  .warn {
    font-size: 12.5px;
    color: var(--busy);
  }
  .bad {
    font-size: 12.5px;
    color: var(--alert);
  }
  .warnline {
    margin: 0 0 10px;
    font-size: 12px;
    line-height: 1.6;
    color: var(--busy);
  }
  .warnline a,
  .lead a {
    color: inherit;
    text-underline-offset: 2px;
  }
  .ok.inline {
    display: block;
    margin: 8px 0 0;
  }
  .aid {
    font-size: 11.5px;
    color: var(--text-4);
  }
  .fields {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-bottom: 10px;
  }
  .picks {
    display: flex;
    gap: 10px;
    margin-bottom: 10px;
    flex-wrap: wrap;
  }
  .pick {
    display: flex;
    flex-direction: column;
    gap: 5px;
    min-width: 160px;
  }
  .plbl {
    font-size: 11px;
    color: var(--text-4);
  }
  select.min {
    appearance: none;
    cursor: pointer;
  }
  .min {
    width: 100%;
    padding: 9px 11px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    color: var(--text);
    font-size: 12.5px;
    outline: none;
  }
  .min:focus {
    border-color: var(--border-2);
  }
  .key {
    min-height: 76px;
    resize: vertical;
    line-height: 1.45;
  }
  .save,
  .mini {
    align-self: flex-start;
    padding: 7px 14px;
    border-radius: var(--r-sm);
    background: var(--panel-3);
    color: var(--text-2);
    font-size: 12.5px;
    font-weight: 500;
  }
  .save:hover,
  .mini:hover {
    color: var(--text);
  }
  .save:disabled {
    opacity: 0.5;
  }
  .err {
    margin: 8px 0 0;
    font-size: 12px;
    color: var(--alert);
  }
</style>
