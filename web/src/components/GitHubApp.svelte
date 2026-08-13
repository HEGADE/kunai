<script lang="ts">
  import { untrack } from 'svelte'
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
  // Replacing a working key is a rare job. The form used to sit open under a
  // working App, which made the section look like setup you had not finished.
  let showKey = $state(false)

  const base = $derived(app.baseForMachine(machineId))

  // Which account and model reviews run on. Its own setting, because a review is
  // chunky and arrives on somebody else's schedule: pointed at a second account
  // or a provider it can never wall the session you are sitting in.
  let rcfg = $state<ReviewConfig>({})
  const accounts = $derived(app.machines.find((m) => m.id === machineId)?.stats?.clis ?? [])

  // Asked WITH the check, because this is the one screen where somebody is
  // looking at the setup and can act on the answer. The dashboard's poll asks
  // without it: the check costs two round trips to github.com.
  //
  // Guarded on the VALUES rather than left to the effect's own dependencies,
  // and that is not belt-and-braces. Measured: this effect re-ran four times in
  // sixteen seconds with `machineId` and `base` identical every time, on ONE
  // component instance (onMount fired once, so it was not a remount).
  //
  // What re-triggered it was reading the PROP. Settings passes
  // `machineId={selM.id}`, and selM is derived from app.machines, which the
  // store replaces on its poll beat; the prop is invalidated even though the id
  // is the same string. The neighbouring sections were immune only because
  // their effects happen to read `base` and never the prop: measured at 1 fetch
  // on load and 0 while idle, against this one's 3 and 2.
  //
  // The visible cost was the status line: every re-run set `checking`, so the
  // row flipped from "Working" back to "Checking with GitHub…" for the second or
  // two the two GitHub round trips take, every few seconds, for ever.
  //
  // Remembering what was last loaded fixes it whatever the cause, which an
  // untrack around the body alone would not: the effect still runs, it just
  // finds nothing to do.
  let checking = $state(false)
  let loadedFor = ''
  $effect(() => {
    const want = `${machineId}|${base}`
    if (loadedFor === want) return
    loadedFor = want
    untrack(() => {
      checking = true
      githubApp(base, true)
        .then((s) => (appState = s))
        .catch(() => (appState = { configured: false }))
        .finally(() => (checking = false))
      reviewConfig(base)
        .then((c) => (rcfg = c))
        .catch(() => (rcfg = {}))
    })
  })

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
      showKey = false
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

<!-- The App itself: what GitHub said when asked, and the way to replace or
     remove it. A status is a row with a dot, like every other status in this
     app, rather than a coloured word floating in a paragraph. -->
<div class="st-card">
  {#if appState?.configured}
    <div class="st-row">
      <span
        class="st-dot"
        class:on={!checking && !appState.error && !!appState.check?.orgs?.length}
        class:bad={!!appState.error}
        class:warn={!checking && !appState.error && !appState.check?.orgs?.length}
      ></span>
      <span class="st-k">
        <span class="st-name">
          {#if checking}
            Checking with GitHub…
          {:else if appState.error}
            Not working
          {:else if appState.check?.orgs?.length}
            Working
          {:else}
            Not finished
          {/if}
        </span>
        <!-- Wraps. This line carries an App name, a list of organisations and a
             numeric id, and as an unwrapped flex row it ran off the side of the
             page. -->
        <span class="st-sub-text" class:bad={!!appState.error}>
          {#if appState.error}
            {appState.error}
          {:else if appState.check?.orgs?.length}
            {appState.check.name ? appState.check.name + ' · ' : ''}installed on {appState.check.orgs.join(', ')}{appState.app_id
              ? ` · App ${appState.app_id}`
              : ''}
          {:else if appState.app_id}
            App {appState.app_id}
          {/if}
        </span>
      </span>
      <button class="st-btn ghost danger" onclick={clear} disabled={busy}>Remove</button>
    </div>

    {#if appState.check?.warning}
      <div class="st-row">
        <span class="st-dot warn"></span>
        <span class="st-k">
          <span class="st-sub-text warn">{appState.check.warning}</span>
        </span>
        {#if appState.check.install_url}
          <a class="st-btn" href={appState.check.install_url} target="_blank" rel="noreferrer">Install it</a>
        {/if}
      </div>
    {:else if appState.check?.partial}
      <!-- The setting behind the confusing failure: everything reports
           configured and one repository still refuses because it was never
           ticked. -->
      <div class="st-row">
        <span class="st-dot warn"></span>
        <span class="st-k">
          <span class="st-sub-text warn">
            This App covers only selected repositories on at least one organisation.
            A repository that was not ticked will refuse, even though everything
            here looks right.
          </span>
        </span>
        {#if appState.check.install_url}
          <a class="st-btn" href={appState.check.install_url} target="_blank" rel="noreferrer">Change</a>
        {/if}
      </div>
    {/if}
  {:else}
    <div class="st-row">
      <span class="st-dot"></span>
      <span class="st-k">
        <span class="st-name">No App yet</span>
        <!-- One line. What to go and do is a note under the card: a row states a
             state, and a paragraph inside one leaves its status dot floating in
             the middle of four lines of prose. -->
        <span class="st-sub-text">Reviews cannot be posted until this machine has one.</span>
      </span>
    </div>
  {/if}

  <!-- Replacing a working key is a rare job, so it stays shut until asked for
       rather than sitting open under a working App. -->
  {#if !appState?.configured || showKey}
    <div class="st-form">
      <div class="st-field">
        <span class="st-label">App id</span>
        <input class="st-input mono" placeholder="4426586" bind:value={appId} autocomplete="off" />
      </div>
      <div class="st-field">
        <span class="st-label">Private key</span>
        <textarea
          class="st-textarea mono"
          placeholder="-----BEGIN RSA PRIVATE KEY-----"
          bind:value={key}
          spellcheck="false"
          autocomplete="off"
        ></textarea>
      </div>
      <div class="st-actions">
        <button class="st-btn solid" onclick={save} disabled={busy || !appId.trim() || !key.trim()}>
          {busy ? 'Checking with GitHub…' : appState?.configured ? 'Replace key' : 'Save and check'}
        </button>
        {#if appState?.configured}
          <button class="st-btn ghost" onclick={() => (showKey = false)}>Cancel</button>
        {/if}
      </div>
    </div>
  {:else}
    <div class="st-row">
      <span class="st-k">
        <span class="st-name">Private key</span>
        <span class="st-sub-text">
          Give each machine its own. An App holds several and revokes them one at a
          time, so a lost laptop costs one key rather than a redistribution.
        </span>
      </span>
      <button class="st-btn" onclick={() => (showKey = true)}>Replace</button>
    </div>
  {/if}
</div>

{#if appState?.configured}
  <!-- No box. This is a sentence and two menus; wrapping it in a bordered card
       was a container drawn because a container was what was on offer. -->
  <div class="st-sub">Reviews run on</div>
  <div class="st-form">
      <p class="st-sub-text">
        A review is long and arrives when a colleague opens a pull request, not when
        you are ready for it. Point it at a second account or a provider and it can
        never spend the window you are working in.
      </p>
      <div class="st-pair">
        <label class="st-field">
          <span class="st-label">Account</span>
          <select
            class="st-select"
            value={rcfg.cli ?? ''}
            onchange={(e) => saveReviewCfg({ cli: e.currentTarget.value })}
          >
            <option value="">Default{accounts[0] ? ` (${accounts[0]})` : ''}</option>
            {#each accounts as a (a)}
              <option value={a}>{a}</option>
            {/each}
          </select>
        </label>
        <label class="st-field">
          <span class="st-label">Model</span>
          <select
            class="st-select"
            value={rcfg.model ?? ''}
            onchange={(e) => saveReviewCfg({ model: e.currentTarget.value })}
          >
            <option value="">Default</option>
            {#each MODELS as m (m.id)}
              <option value={m.id}>{modelLabel(m.id)}</option>
            {/each}
          </select>
        </label>
      </div>
  </div>
{/if}

<div class="st-sub">Your handle</div>
<div class="st-form">
  <p class="st-sub-text">
    Named in the reviews you request. With one bot identity shared across the team,
    this is the only thing that says who to ask about a finding.
  </p>
  <div class="st-pair">
    <label class="st-field">
      <span class="st-label">GitHub username</span>
      <input class="st-input mono" placeholder="shorya" bind:value={who} autocomplete="off" />
    </label>
  </div>
  <div class="st-actions">
    <button class="st-btn quiet" onclick={saveWho}>Save</button>
  </div>
</div>

{#if !appState?.configured}
  <p class="st-note">
    Reviews post as a bot rather than under your own account, which is what the App
    is for. Register one with pull requests read and write, contents read and
    metadata read, and leave webhooks off: kunai only ever calls out. Both fields
    are checked against GitHub before they are saved, so a mismatched pair is
    refused here rather than at your first review.
  </p>
{/if}

{#if err}<p class="st-note bad">{err}</p>{/if}
{#if saved}<p class="st-note">{saved}</p>{/if}

<style>
  /* Everything here comes from settings.css. What is left is the one thing that
     is specific to this section. */
  a.st-btn {
    text-decoration: none;
    display: inline-flex;
    align-items: center;
  }
</style>
