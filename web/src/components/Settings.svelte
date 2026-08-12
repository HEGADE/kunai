<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { updateAvailable } from '../lib/update'
  import { enablePush, disablePush, isSubscribed, pushState } from '../lib/push'
  import { setKeepAwake, setThermal, setLid, setFailover } from '../lib/api'
  import type { Machine } from '../lib/types'
  import { SETTINGS_SECTIONS, type SettingsSection } from '../lib/app.svelte'
  import GitHubApp from './GitHubApp.svelte'
  import LanAccess from './LanAccess.svelte'
  import Accounts from './Accounts.svelte'
  import Providers from './Providers.svelte'
  import Channels from './Channels.svelte'
  import Page from './Page.svelte'

  // Settings, as a place rather than a sheet.
  //
  // It was a 720px modal holding one scrolling column of everything, and the
  // clutter was structural rather than visual: it tangled two different scopes
  // into that column. Notifications belong to the BROWSER you are holding.
  // Everything under them belongs to ONE MACHINE, chosen by a chip picker
  // halfway down, after which the rest of the page silently re-scoped. Nothing
  // on screen said which switches followed that picker, so the answer to "is
  // this setting global?" was to read the source.
  //
  // The rail fixes that by making scope the organising principle: its group
  // headings say whose settings each section changes, and the machine group is
  // headed by the machine's own name. Picking a different machine renames it,
  // and everything under it is exactly what follows.
  //
  // Accounts, Providers and Channels are sections here rather than pages of
  // their own. Accounts existing in both was the worst of it: the page had the
  // real sign-in flow, and Settings had a second list of the same accounts with
  // a link across to the page. Two surfaces for one idea is a question the
  // reader has to answer before they can do anything.
  const section = $derived(app.settingsSection)
  const go = (s: SettingsSection) => app.setSettingsSection(s)

  // What each section is, said once. The blurb is not decoration: a settings
  // page that lists only nouns makes you open every one to find the switch you
  // came for.
  const SECTIONS: Record<SettingsSection, { title: string; blurb: string }> = {
    notifications: {
      title: 'Notifications',
      blurb: 'Whether this browser is told when a session finishes or needs you.',
    },
    machines: {
      title: 'Machines',
      blurb: 'Every machine running kunai on your tailnet, and what each one is running.',
    },
    accounts: {
      title: 'Accounts',
      blurb: 'The Claude subscriptions this machine can run sessions on.',
    },
    providers: {
      title: 'Providers',
      blurb: 'Non-Claude models this machine can run the same agent on.',
    },
    channels: {
      title: 'Channels',
      blurb: 'Ways to reach this machine other than the app.',
    },
    network: {
      title: 'Network',
      blurb: 'Who on your network can reach this machine, and how they prove it.',
    },
    unattended: {
      title: 'Unattended',
      blurb: 'What this machine may do while nobody is watching it.',
    },
    reviews: {
      title: 'Reviews',
      blurb: 'The GitHub App this machine posts pull request reviews as.',
    },
  }
  // Which sections belong to a machine rather than to this browser. Drives the
  // rail's grouping, so the split is stated once instead of being implied by
  // whatever order things happen to appear in.
  const MACHINE_SECTIONS = SETTINGS_SECTIONS.filter(
    (s) => s !== 'notifications' && s !== 'machines',
  )

  const supported = pushState() !== 'unsupported'

  let on = $state(false)
  let busy = $state(false)
  let hint = $state('')

  // Machines
  let newLabel = $state('')
  let newUrl = $state('')
  let adding = $state(false)
  let discovering = $state(false)
  let machErr = $state('')
  // Adding a machine or an account is a once-a-year job, so the forms stay shut
  // until asked for. Left open they are permanent clutter in a page you came to
  // flip one switch in.
  let showAddMachine = $state(false)
  let showAddAcct = $state<Record<string, boolean>>({})

  // Which machine the machine-scoped sections are about. Defaults to this one,
  // the same way the dashboard's stats picker does.
  let pickedM = $state('')
  const selM = $derived(
    app.machines.find((m) => m.id === pickedM) ??
      app.machines.find((m) => m.self) ??
      app.machines[0] ??
      null,
  )

  async function addMachine() {
    const url = newUrl.trim()
    if (!url || adding) return
    adding = true
    machErr = ''
    try {
      await app.addMachine(newLabel.trim(), url)
      newLabel = ''
      newUrl = ''
      showAddMachine = false
    } catch (e) {
      machErr = (e as Error).message
    } finally {
      adding = false
    }
  }
  async function discover() {
    if (discovering) return
    discovering = true
    machErr = ''
    try {
      await app.discover()
    } catch (e) {
      machErr = (e as Error).message
    } finally {
      discovering = false
    }
  }

  // Updating a machine. This lives here as well as on the home screen on purpose:
  // the home screen's banner was the ONLY way to update, so a refactor that dropped
  // it left a machine unable to offer the update that would fix it -- which is
  // exactly what happened. Settings is where you go to act on a machine, so an
  // update control belongs here regardless.
  const outdated = (m: Machine) =>
    updateAvailable(m.stats?.kunai_version, app.latestVersion, m.stats?.channel)
  const updateLabel = (m: Machine) => {
    const progress = app.updateProgress[m.id] ?? -1
    if (!app.updating[m.id]) return app.updateError[m.id] ? 'Retry' : 'Update'
    if (progress >= 1) return 'Restarting…'
    if (progress >= 0) return `${Math.round(progress * 100)}%`
    return 'Updating…'
  }

  // Per-machine account auto-failover (opt-in). Rolls a rate-limited session onto
  // the account with the most headroom.
  let foBusy = $state<Record<string, boolean>>({})
  async function toggleFailover(m: Machine) {
    if (foBusy[m.id]) return
    foBusy = { ...foBusy, [m.id]: true }
    machErr = ''
    try {
      await setFailover(m.url, !m.stats?.failover)
      await app.refresh()
    } catch (e) {
      machErr = (e as Error).message
    } finally {
      const b = { ...foBusy }
      delete b[m.id]
      foBusy = b
    }
  }

  // Per-machine keep-awake. Toggles that machine's own /api/awake, then refreshes
  // the fan-out so the switch reflects the machine's resolved state.
  let awBusy = $state<Record<string, boolean>>({})
  async function toggleAwake(m: Machine) {
    if (awBusy[m.id]) return
    awBusy = { ...awBusy, [m.id]: true }
    machErr = ''
    try {
      await setKeepAwake(m.url, !m.stats?.keep_awake)
      await app.refresh()
    } catch (e) {
      machErr = (e as Error).message
    } finally {
      const b = { ...awBusy }
      delete b[m.id]
      awBusy = b
    }
  }

  // Per-machine thermal guard. The switch flips enabled; the number fields edit
  // the thresholds and commit on change. Each posts to that machine's own
  // /api/thermal and refreshes the fan-out.
  let thBusy = $state<Record<string, boolean>>({})
  async function saveThermal(
    m: Machine,
    patch: Partial<{ enabled: boolean; soft_c: number; max_hours: number; hard_c: number; action: 'sleep' | 'poweroff' }>,
  ) {
    if (thBusy[m.id]) return
    thBusy = { ...thBusy, [m.id]: true }
    machErr = ''
    try {
      await setThermal(m.url, {
        enabled: patch.enabled ?? m.stats?.thermal_guard ?? false,
        soft_c: patch.soft_c ?? m.stats?.thermal_soft_c ?? 90,
        max_hours: patch.max_hours ?? m.stats?.thermal_max_hours ?? 0,
        hard_c: patch.hard_c ?? m.stats?.thermal_hard_c ?? 0,
        action: patch.action ?? (m.stats?.thermal_action as 'sleep' | 'poweroff') ?? 'sleep',
      })
      await app.refresh()
    } catch (e) {
      machErr = (e as Error).message
    } finally {
      const b = { ...thBusy }
      delete b[m.id]
      thBusy = b
    }
  }

  // Per-machine lid-closed hold (privileged). Same shape as keep-awake.
  let lidBusy = $state<Record<string, boolean>>({})
  async function toggleLid(m: Machine) {
    if (lidBusy[m.id]) return
    lidBusy = { ...lidBusy, [m.id]: true }
    machErr = ''
    try {
      await setLid(m.url, !m.stats?.keep_lid)
      await app.refresh()
    } catch (e) {
      machErr = (e as Error).message
    } finally {
      const b = { ...lidBusy }
      delete b[m.id]
      lidBusy = b
    }
  }

  // The list of Claude accounts, adding one, and removing one all used to live
  // here as a second implementation: a raw roster of names and config folders,
  // plus a form that asked you to type a path. Accounts.svelte already does all
  // of that with a real sign-in flow, so this section renders THAT and keeps
  // only the one thing that is genuinely a setting rather than a credential:
  // what happens when an account hits its wall.

  // Reflect the real subscription state, not just permission: a device can be
  // "granted" yet turned off.
  $effect(() => {
    isSubscribed().then((v) => (on = v))
  })

  async function toggle() {
    if (busy) return
    busy = true
    hint = ''
    try {
      if (on) {
        const err = await disablePush()
        if (err) hint = err
        else on = false
      } else {
        const err = await enablePush()
        if (err) hint = err
        else on = true
      }
    } finally {
      busy = false
    }
  }

  // The subtitle names what the open section changes, which is the same job the
  // rail's group headings do and the only one that survives a phone, where the
  // rail is a strip of chips with no room for a heading above them.
  const scopeSub = $derived(
    section === 'notifications'
      ? 'This device'
      : section === 'machines'
        ? `${app.machines.length} machine${app.machines.length === 1 ? '' : 's'}`
        : (selM?.label ?? ''),
  )
</script>

<Page title="Settings">
  <div class="layout">
    <nav class="rail" aria-label="Settings sections">
      <!-- The group headings are the point: they say whose settings the links
           under them change. Without that, a page of switches cannot tell you
           which ones follow the machine picker, which is what made the old
           column unreadable. -->
      <div class="rgroup">This device</div>
      <button class="rlink" class:on={section === 'notifications'} onclick={() => go('notifications')}>
        Notifications
      </button>

      <div class="rgroup">Fleet</div>
      <button class="rlink" class:on={section === 'machines'} onclick={() => go('machines')}>
        Machines
        <span class="rcount mono">{app.machines.length}</span>
      </button>

      {#if selM}
        <!-- The machine's own name as the heading. A static word like "Machine"
             would be one more thing that does not say which one. -->
        <div class="rgroup mach">
          <span class="rdot" class:live={selM.online}></span>
          <span class="rmach">{selM.label}</span>
        </div>
        {#each MACHINE_SECTIONS as s (s)}
          <button class="rlink" class:on={section === s} onclick={() => go(s)}>{SECTIONS[s].title}</button>
        {/each}
      {/if}
    </nav>

    <div class="panel">
      <!-- Every section opens the same way: what it is, and one line saying what
           it decides. A settings page that lists only nouns makes you open all
           of them to find the switch you came for. -->
      <header class="shead">
        <h2>{SECTIONS[section].title}</h2>
        <p>{SECTIONS[section].blurb}</p>
      </header>

      {#if machErr}<p class="err">{machErr}</p>{/if}

      {#if section === 'notifications'}
        <div class="card">
          <div class="row">
            <span class="rk">
              <span class="rname">Push notifications</span>
              <span class="rsub">
                {#if !supported}
                  This browser cannot receive them.
                {:else}
                  A wake-up only. No part of a conversation leaves your tailnet.
                {/if}
              </span>
            </span>
            {#if supported}
              <button
                class="switch"
                class:on
                onclick={toggle}
                disabled={busy}
                role="switch"
                aria-checked={on}
                aria-label="Toggle notifications"
              >
                <span class="knob"></span>
              </button>
            {/if}
          </div>
        </div>
        {#if hint}<p class="note">{hint}</p>{/if}
        <p class="note">
          Notifications are per device, so turning them on here does not turn them on
          anywhere else you use kunai.
        </p>

      {:else if section === 'machines'}
        <div class="card">
          {#each app.machines as m (m.id)}
            <!-- Selecting a machine is what everything under its name in the
                 rail then follows, so the row says so rather than leaving you
                 to infer it from a highlight. -->
            <div class="row mrow" class:sel={selM?.id === m.id}>
              <button class="mpick" onclick={() => (pickedM = m.id)} aria-label="Settings for {m.label}">
                <span class="rk">
                  <span class="mtop">
                    <span class="rdot" class:live={m.online}></span>
                    <span class="rname">{m.label}</span>
                    {#if m.self}<span class="tag">this one</span>{/if}
                    {#if selM?.id === m.id && app.machines.length > 1}<span class="tag on">showing</span>{/if}
                  </span>
                  <span class="rsub mono">
                    {#if m.stats}
                      kunai {m.stats.kunai_version || '—'} · claude {m.stats.claude_version || '—'}{m.stats.arch ? ` · ${m.stats.os}/${m.stats.arch}` : ''}
                    {:else if !m.online}
                      Offline
                    {:else}
                      {m.url || 'this machine'}
                    {/if}
                  </span>
                </span>
              </button>
              <span class="macts">
                {#if outdated(m)}
                  <button class="btn solid" disabled={app.updating[m.id]} onclick={() => app.updateMachine(m.id)}>
                    {updateLabel(m)}
                  </button>
                {/if}
                {#if !m.self}
                  <button class="btn ghost" onclick={() => app.removeMachine(m.id)}>Remove</button>
                {/if}
              </span>
              {#if app.updateError[m.id]}
                <p class="err small">Update failed: {app.updateError[m.id]}</p>
              {/if}
            </div>
          {/each}
        </div>

        <!-- Finding and adding machines is fleet management rather than a
             setting, so it sits under the list it changes instead of among the
             switches. -->
        <div class="foot">
          <button class="btn" onclick={discover} disabled={discovering}>
            {discovering ? 'Scanning the tailnet…' : 'Find machines'}
          </button>
          <button class="btn ghost" onclick={() => (showAddMachine = !showAddMachine)}>
            {showAddMachine ? 'Cancel' : 'Add by address'}
          </button>
        </div>
        {#if showAddMachine}
          <div class="card pad">
            <input class="min" placeholder="Label" bind:value={newLabel} autocomplete="off" />
            <input
              class="min mono"
              placeholder="https://host.tailnet.ts.net:8443"
              bind:value={newUrl}
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              onkeydown={(e) => e.key === 'Enter' && addMachine()}
            />
            <button class="btn solid" onclick={addMachine} disabled={adding || !newUrl.trim()}>
              {adding ? 'Adding…' : 'Add machine'}
            </button>
          </div>
        {/if}
        <p class="note">
          Machines on your tailnet are found on their own. Adding by address is for one
          that discovery cannot see.
        </p>

      {:else if !selM}
        <p class="note">No machines yet.</p>
      {:else if !selM.online}
        <p class="note">{selM.label} is offline. Nothing here can be changed until it is back.</p>

      {:else if section === 'accounts'}
        <!-- The accounts themselves, with their real sign-in flow. Settings used
             to carry a second, worse copy of this list beside a link to it. -->
        <Accounts machineId={selM.id} />
        {#if selM.stats?.clis && selM.stats.clis.length > 1}
          <div class="card">
            <div class="row">
              <span class="rk">
                <span class="rname">Move to another account at the limit</span>
                <span class="rsub">
                  When one hits its 5-hour or weekly wall, carry on from the account with
                  the most left, Claude or provider.
                </span>
              </span>
              <button
                class="switch"
                class:on={selM.stats.failover}
                onclick={() => toggleFailover(selM)}
                disabled={foBusy[selM.id]}
                role="switch"
                aria-checked={selM.stats.failover ?? false}
                aria-label="Toggle account auto-failover"
              >
                <span class="knob"></span>
              </button>
            </div>
          </div>
        {/if}

      {:else if section === 'providers'}
        <Providers machineId={selM.id} />

      {:else if section === 'channels'}
        <Channels machineId={selM.id} />

      {:else if section === 'network'}
        <LanAccess base={selM.url} label={selM.label} />

      {:else if section === 'unattended'}
        <div class="card">
          {#if selM.stats?.keep_awake_supported}
            <div class="row">
              <span class="rk">
                <span class="rname">Stay awake while locked</span>
                <span class="rsub">Needs the lid open and power.</span>
              </span>
              <button
                class="switch"
                class:on={selM.stats.keep_awake}
                onclick={() => toggleAwake(selM)}
                disabled={awBusy[selM.id]}
                role="switch"
                aria-checked={selM.stats.keep_awake}
                aria-label="Toggle keep awake"
              >
                <span class="knob"></span>
              </button>
            </div>
          {/if}
          {#if selM.stats?.keep_lid_supported}
            <div class="row">
              <span class="rk">
                <span class="rname">Keep working with the lid closed</span>
                <span class="rsub" class:warn={!selM.stats.thermal_privileged}>
                  {#if selM.stats.thermal_privileged}
                    The machine will not sleep when you shut it.
                  {:else}
                    Needs the admin setup from install.
                  {/if}
                </span>
              </span>
              <button
                class="switch"
                class:on={selM.stats.keep_lid}
                onclick={() => toggleLid(selM)}
                disabled={lidBusy[selM.id]}
                role="switch"
                aria-checked={selM.stats.keep_lid}
                aria-label="Toggle lid-closed hold"
              >
                <span class="knob"></span>
              </button>
            </div>
          {/if}
          {#if selM.stats}
            <div class="row">
              <span class="rk">
                <span class="rname">Stop everything if it overheats</span>
                <span class="rsub">
                  {#if selM.stats.cpu_temp_c > 0}
                    Running at {Math.round(selM.stats.cpu_temp_c)}°C now.
                  {:else if selM.stats.thermal_pressure}
                    Thermal pressure is {selM.stats.thermal_pressure} now.
                  {:else}
                    This machine reports no temperature, so the time limit is the guard.
                  {/if}
                </span>
              </span>
              <button
                class="switch"
                class:on={selM.stats.thermal_guard}
                onclick={() => saveThermal(selM, { enabled: !selM.stats?.thermal_guard })}
                disabled={thBusy[selM.id]}
                role="switch"
                aria-checked={selM.stats.thermal_guard}
                aria-label="Toggle thermal guard"
              >
                <span class="knob"></span>
              </button>
            </div>
            {#if selM.stats.thermal_guard}
              <!-- The limits belong to the switch above, so they are inside the
                   same card and indented under it rather than floating as their
                   own group. -->
              <div class="row sub">
                <div class="limits">
                  {#if selM.stats.cpu_temp_c > 0}
                    <label class="lim">
                      <span class="limk">Stop at</span>
                      <input
                        class="num mono"
                        type="number"
                        min="50"
                        max="105"
                        value={selM.stats.thermal_soft_c}
                        disabled={thBusy[selM.id]}
                        onchange={(e) => saveThermal(selM, { soft_c: +e.currentTarget.value })}
                      />
                      <span class="limu">°C</span>
                    </label>
                  {/if}
                  <label class="lim">
                    <span class="limk">Or after</span>
                    <input
                      class="num mono"
                      type="number"
                      min="0"
                      max="72"
                      value={selM.stats.thermal_max_hours}
                      disabled={thBusy[selM.id]}
                      onchange={(e) => saveThermal(selM, { max_hours: +e.currentTarget.value })}
                    />
                    <span class="limu">hours awake (0 = never)</span>
                  </label>
                </div>
                {#if selM.stats.cpu_temp_c > 0 || selM.stats.thermal_pressure}
                  <label class="check">
                    <input
                      type="checkbox"
                      checked={selM.stats.thermal_action === 'poweroff'}
                      disabled={thBusy[selM.id]}
                      onchange={(e) =>
                        saveThermal(selM, {
                          action: e.currentTarget.checked ? 'poweroff' : 'sleep',
                          hard_c: e.currentTarget.checked ? selM.stats?.thermal_hard_c || 100 : 0,
                        })}
                    />
                    <span class="checkk">
                      <span class="rname sm">Power off if it keeps climbing</span>
                      <span class="rsub" class:warn={!selM.stats.thermal_privileged}>
                        {#if selM.stats.thermal_privileged}
                          A last resort, once stopping the work was not enough.
                        {:else}
                          Needs the admin setup from install.
                        {/if}
                      </span>
                    </span>
                  </label>
                  {#if selM.stats.thermal_action === 'poweroff' && selM.stats.cpu_temp_c > 0}
                    <label class="lim">
                      <span class="limk">Power off at</span>
                      <input
                        class="num mono"
                        type="number"
                        min="50"
                        max="105"
                        value={selM.stats.thermal_hard_c}
                        disabled={thBusy[selM.id]}
                        onchange={(e) => saveThermal(selM, { hard_c: +e.currentTarget.value })}
                      />
                      <span class="limu">°C</span>
                    </label>
                  {/if}
                {/if}
              </div>
            {/if}
          {/if}
        </div>

      {:else if section === 'reviews'}
        <GitHubApp machineId={selM.id} />
      {/if}
    </div>
  </div>
</Page>

<style>
  /* Rail beside panel, the pair centred as one block. Centring the LAYOUT and
     not the panel is what stops the content stranding itself against the left
     edge of a wide screen with a lake of empty to its right. */
  .layout {
    display: grid;
    grid-template-columns: 190px minmax(0, 1fr);
    gap: 34px;
    max-width: 920px;
    margin: 0 auto;
    padding: 24px 20px calc(56px + var(--safe-bottom));
  }
  .rail {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 1px;
    position: sticky;
    top: 0;
    align-self: start;
  }
  .rgroup {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 18px 10px 6px;
    font-size: 10.5px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .rgroup:first-child {
    padding-top: 2px;
  }
  /* The machine's name is a value, not a label, so it keeps its own case and
     takes the mono voice this app gives data everywhere else. */
  .rgroup.mach {
    text-transform: none;
    letter-spacing: 0;
  }
  .rmach {
    font-family: var(--mono, monospace);
    font-size: 11.5px;
    font-weight: 500;
    color: var(--text-3);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .rdot {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .rdot.live {
    background: var(--live);
  }
  .rlink {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 7px 10px;
    border-radius: 8px;
    color: var(--text-3);
    font-size: 13px;
    text-align: left;
  }
  .rlink:hover {
    background: var(--panel);
    color: var(--text);
  }
  .rlink.on {
    background: var(--panel-2);
    color: var(--text);
    font-weight: 550;
  }
  .rcount {
    font-size: 11px;
    color: var(--text-4);
  }

  .panel {
    min-width: 0;
  }
  /* Every section states itself. This is also what gives the top of the panel
     something to be: a page whose content is three switches used to open with
     three switches floating against nothing. */
  .shead {
    margin: 0 0 16px;
  }
  .shead h2 {
    margin: 0;
    font-size: 19px;
    font-weight: 600;
    letter-spacing: -0.015em;
    color: var(--text);
  }
  .shead p {
    margin: 5px 0 0;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--text-4);
    max-width: 52ch;
  }

  /* The card is the unit of this page. Rows live inside one, divided by
     hairlines, so a group of settings reads as a group instead of as loose
     lines ruled across the whole width. */
  .card {
    border: 1px solid var(--border);
    border-radius: 14px;
    background: var(--panel);
    overflow: hidden;
  }
  .card + .card,
  .card + .foot,
  .foot + .card {
    margin-top: 14px;
  }
  .card.pad {
    display: flex;
    flex-direction: column;
    gap: 9px;
    padding: 14px;
    background: none;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 16px;
    flex-wrap: wrap;
    padding: 14px 16px;
  }
  .row + .row {
    border-top: 1px solid var(--border);
  }
  .rk {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 3px;
    text-align: left;
  }
  .rname {
    font-size: 13.5px;
    color: var(--text);
  }
  .rname.sm {
    font-size: 12.5px;
    color: var(--text-2);
  }
  .rsub {
    font-size: 11.5px;
    line-height: 1.55;
    color: var(--text-4);
    max-width: 46ch;
    overflow-wrap: anywhere;
  }
  /* The one status colour this app reserves for "be careful". */
  .rsub.warn {
    color: color-mix(in srgb, var(--busy) 80%, var(--text-3));
  }
  /* Settings that only exist while the switch above them is on. */
  .row.sub {
    flex-direction: column;
    align-items: flex-start;
    gap: 12px;
    padding-left: 30px;
    background: var(--bg);
  }

  /* A machine row: the whole left side is the target, because picking a machine
     is the point of this list. */
  .mrow {
    padding: 0;
  }
  .mrow.sel {
    background: var(--panel-2);
  }
  .mpick {
    flex: 1;
    min-width: 0;
    padding: 14px 16px;
    background: none;
  }
  .mpick:hover .rname {
    color: var(--text);
  }
  .mtop {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .macts {
    flex: none;
    display: flex;
    align-items: center;
    gap: 8px;
    padding-right: 16px;
  }
  .tag {
    font-size: 9.5px;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .tag.on {
    color: var(--text-3);
  }

  .switch {
    flex: none;
    position: relative;
    width: 44px;
    height: 26px;
    border-radius: 100px;
    background: var(--panel-3);
    border: 1px solid var(--border);
    transition: background 0.15s, border-color 0.15s;
  }
  .switch.on {
    background: var(--white);
    border-color: var(--white);
  }
  .switch:disabled {
    opacity: 0.55;
  }
  .knob {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    background: var(--text-3);
    transition: transform 0.15s, background 0.15s;
  }
  .switch.on .knob {
    transform: translateX(18px);
    background: #0b0b0c;
  }

  .limits {
    display: flex;
    flex-wrap: wrap;
    gap: 10px 20px;
  }
  .lim {
    display: flex;
    align-items: baseline;
    gap: 7px;
    font-size: 12px;
    color: var(--text-3);
  }
  .limk {
    color: var(--text-3);
  }
  .num {
    width: 58px;
    padding: 5px 8px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: 7px;
    color: var(--text);
    font-size: 12.5px;
    text-align: right;
  }
  .num:focus-visible {
    outline: none;
    border-color: var(--border-2);
  }
  .num::-webkit-outer-spin-button,
  .num::-webkit-inner-spin-button {
    appearance: none;
    margin: 0;
  }
  .limu {
    font-size: 11px;
    color: var(--text-4);
  }
  .check {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    cursor: pointer;
  }
  .checkk {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .btn {
    padding: 6px 13px;
    border-radius: 8px;
    background: var(--panel-2);
    color: var(--text-2);
    font-size: 12px;
    font-weight: 500;
  }
  .btn:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .btn.solid {
    background: var(--white);
    color: #0b0b0c;
  }
  .btn.ghost {
    background: none;
    color: var(--text-4);
  }
  .btn.ghost:hover {
    background: var(--panel-2);
    color: var(--text-2);
  }
  .btn:disabled {
    opacity: 0.5;
  }
  .foot {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    margin-top: 14px;
  }

  .min {
    width: 100%;
    padding: 9px 11px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text);
    font-size: 12.5px;
    outline: none;
  }
  .min:focus {
    border-color: var(--border-2);
  }
  .card.pad .btn {
    align-self: flex-start;
  }

  .note {
    margin: 14px 2px 0;
    font-size: 11.5px;
    line-height: 1.6;
    color: var(--text-4);
    max-width: 52ch;
  }
  .err {
    margin: 0 0 12px;
    font-size: 12px;
    color: var(--alert);
  }
  .err.small {
    flex-basis: 100%;
    margin: 0;
    padding: 0 16px 12px;
    font-size: 11px;
  }

  /* Phone: the rail becomes a scrolling strip of chips above the panel. A
     drill-down would be the other option and is worse here, since it puts a
     second back button inside a page that already has one. */
  @media (max-width: 760px) {
    .layout {
      grid-template-columns: minmax(0, 1fr);
      gap: 16px;
      padding: 14px 12px calc(48px + var(--safe-bottom));
    }
    .rail {
      position: static;
      flex-direction: row;
      gap: 6px;
      overflow-x: auto;
      scrollbar-width: none;
      padding-bottom: 2px;
    }
    .rail::-webkit-scrollbar {
      display: none;
    }
    /* The headings cannot come with it, so the machine chip carries the scope:
       it is the one that says which machine everything after it belongs to. */
    .rgroup {
      display: none;
    }
    .rgroup.mach {
      display: inline-flex;
      flex: none;
      padding: 7px 12px;
      border-radius: 999px;
      border: 1px solid var(--border);
      background: var(--panel);
    }
    .rlink {
      flex: none;
      padding: 8px 13px;
      border-radius: 999px;
      border: 1px solid transparent;
      white-space: nowrap;
    }
    .rlink.on {
      border-color: var(--border-2);
    }
    .row {
      padding: 13px 13px;
    }
    .mpick {
      padding: 13px;
    }
    .macts {
      padding: 0 13px 13px;
    }
  }
</style>
