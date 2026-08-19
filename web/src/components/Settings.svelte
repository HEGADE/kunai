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

  // On a phone the rail is a horizontal strip, so the open section can be
  // scrolled off the right-hand end: arriving at /settings/unattended showed a
  // strip starting at Notifications with nothing marked, which reads as no
  // section being open at all. Only ever scrolls SIDEWAYS (inline), and only
  // when the strip actually scrolls, so the desktop rail is untouched.
  let rail = $state<HTMLElement | null>(null)
  $effect(() => {
    void section
    const el = rail
    if (!el || el.scrollWidth <= el.clientWidth) return
    el.querySelector('.rlink.on')?.scrollIntoView({ inline: 'center', block: 'nearest', behavior: 'smooth' })
  })

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

  <!-- One glyph per section, in the same hand as the app's own nav icons: 24
       viewBox, 1.7 stroke, round caps. Eight text labels in a column is a list
       rather than a navigation; the icon is what makes a section findable
       without reading, and it is the only thing that gives the rail a left edge
       to align to. -->
  {#snippet icon(s: SettingsSection)}
    {#if s === 'notifications'}
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M18 8a6 6 0 10-12 0c0 6-2.5 7.5-2.5 7.5h17S18 14 18 8z" /><path d="M13.7 19a2 2 0 01-3.4 0" /></svg>
    {:else if s === 'machines'}
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="7" rx="2" /><rect x="3" y="13" width="18" height="7" rx="2" /><path d="M7 7.5h.01M7 16.5h.01" /></svg>
    {:else if s === 'accounts'}
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="8" r="3.5" /><path d="M5 20c0-3.6 3.1-6 7-6s7 2.4 7 6" /></svg>
    {:else if s === 'providers'}
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="6.4" y="6.4" width="11.2" height="11.2" rx="2.2" /><path d="M9.9 2.9v3.5M14.1 2.9v3.5M9.9 17.6v3.5M14.1 17.6v3.5M2.9 9.9h3.5M2.9 14.1h3.5M17.6 9.9h3.5M17.6 14.1h3.5" /></svg>
    {:else if s === 'channels'}
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="2" /><path d="M7.8 7.8a6 6 0 000 8.4M16.2 16.2a6 6 0 000-8.4" /><path d="M4.9 4.9a10 10 0 000 14.2M19.1 19.1a10 10 0 000-14.2" /></svg>
    {:else if s === 'network'}
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 9.5a15 15 0 0119 0" /><path d="M5.5 13a10.5 10.5 0 0113 0" /><path d="M8.5 16.5a6 6 0 017 0" /><path d="M12 20h.01" /></svg>
    {:else if s === 'unattended'}
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M20 14.5A8.5 8.5 0 019.5 4a8.5 8.5 0 1010.5 10.5z" /></svg>
    {:else}
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M4 5.5h9M4 12h9M4 18.5h5" /><path d="M15.5 17.5l2 2 4-4.5" /></svg>
    {/if}
  {/snippet}

<Page title="Settings">

  <div class="layout">
    <nav class="rail" aria-label="Settings sections" bind:this={rail}>
      <!-- The group headings are the point: they say whose settings the links
           under them change. Without that, a page of switches cannot tell you
           which ones follow the machine picker, which is what made the old
           column unreadable. -->
      <div class="rgroup">This device</div>
      <button class="rlink" class:on={section === 'notifications'} onclick={() => go('notifications')}>
        <span class="ric">{@render icon('notifications')}</span>
        <span class="rlbl">Notifications</span>
      </button>

      <div class="rgroup">Fleet</div>
      <button class="rlink" class:on={section === 'machines'} onclick={() => go('machines')}>
        <span class="ric">{@render icon('machines')}</span>
        <span class="rlbl">Machines</span>
        <span class="rcount mono">{app.machines.length}</span>
      </button>

      {#if selM}
        <!-- The machine's own name as the heading. A static word like "Machine"
             would be one more thing that does not say which one.
             It sits on the group-label line and above a hairline, because as a
             plain dot-and-name row at link height it read as a nav item that
             happened to be disabled. -->
        <div class="rgroup mach">
          <span class="rdot" class:live={selM.online}></span>
          <span class="rmach">{selM.label}</span>
        </div>
        {#each MACHINE_SECTIONS as s (s)}
          <button class="rlink" class:on={section === s} onclick={() => go(s)}>
            <span class="ric">{@render icon(s)}</span>
            <span class="rlbl">{SECTIONS[s].title}</span>
          </button>
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
        <div class="st-card">
          <div class="st-row">
            <span class="st-k">
              <span class="st-name">Push notifications</span>
              <span class="st-sub-text">
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
        {#if hint}<p class="st-note">{hint}</p>{/if}
        <p class="st-note">
          Notifications are per device, so turning them on here does not turn them on
          anywhere else you use kunai.
        </p>

      {:else if section === 'machines'}
        <div class="st-card">
          {#each app.machines as m (m.id)}
            <!-- Selecting a machine is what everything under its name in the
                 rail then follows, so the row says so rather than leaving you
                 to infer it from a highlight. -->
            <!-- Only marked as chosen when there is something to choose between:
                 on a one-machine fleet a highlighted row is a band of fill
                 answering a question nobody asked. -->
            <div class="st-row mrow" class:sel={selM?.id === m.id && app.machines.length > 1}>
              <button class="mpick" onclick={() => (pickedM = m.id)} aria-label="Settings for {m.label}">
                <span class="st-k">
                  <span class="mtop">
                    <span class="rdot" class:live={m.online}></span>
                    <span class="st-name">{m.label}</span>
                    {#if m.self}<span class="tag">this one</span>{/if}
                    {#if selM?.id === m.id && app.machines.length > 1}<span class="tag on">showing</span>{/if}
                  </span>
                  <span class="st-sub-text mono">
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
                  <button class="st-btn solid" disabled={app.updating[m.id]} onclick={() => app.updateMachine(m.id)}>
                    {updateLabel(m)}
                  </button>
                {/if}
                {#if !m.self}
                  <button class="st-btn ghost" onclick={() => app.removeMachine(m.id)}>Remove</button>
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
        <div class="st-actions foot">
          <button class="st-btn quiet" onclick={discover} disabled={discovering}>
            {discovering ? 'Scanning the tailnet…' : 'Find machines'}
          </button>
          <button class="st-btn" onclick={() => (showAddMachine = !showAddMachine)}>
            {showAddMachine ? 'Cancel' : 'Add by address'}
          </button>
        </div>
        {#if showAddMachine}
          <div class="st-form">
            <div class="st-pair">
              <label class="st-field">
                <span class="st-label">Label</span>
                <input class="st-input" placeholder="Studio Mac" bind:value={newLabel} autocomplete="off" />
              </label>
              <label class="st-field">
                <span class="st-label">Address</span>
                <input
                  class="st-input mono"
                  placeholder="https://host.tailnet.ts.net:8443"
                  bind:value={newUrl}
                  autocomplete="off"
                  autocapitalize="off"
                  spellcheck="false"
                  onkeydown={(e) => e.key === 'Enter' && addMachine()}
                />
              </label>
            </div>
            <div class="st-actions">
              <button class="st-btn solid" onclick={addMachine} disabled={adding || !newUrl.trim()}>
                {adding ? 'Adding…' : 'Add machine'}
              </button>
            </div>
          </div>
        {/if}
        <p class="st-note">
          Machines on your tailnet are found on their own. Adding by address is for one
          that discovery cannot see.
        </p>

      {:else if !selM}
        <p class="st-note">No machines yet.</p>
      {:else if !selM.online}
        <p class="st-note">{selM.label} is offline. Nothing here can be changed until it is back.</p>

      {:else if section === 'accounts'}
        <!-- The accounts themselves, with their real sign-in flow. Settings used
             to carry a second, worse copy of this list beside a link to it. -->
        <Accounts machineId={selM.id} />
        {#if selM.stats?.clis && selM.stats.clis.length > 1}
          <div class="st-card">
            <div class="st-row">
              <span class="st-k">
                <span class="st-name">Move to another account at the limit</span>
                <span class="st-sub-text">
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
        <div class="st-card">
          {#if selM.stats?.keep_awake_supported}
            <div class="st-row">
              <span class="st-k">
                <span class="st-name">Stay awake while locked</span>
                <span class="st-sub-text">Needs the lid open and power.</span>
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
            <div class="st-row">
              <span class="st-k">
                <span class="st-name">Keep working with the lid closed</span>
                <span class="st-sub-text" class:warn={!selM.stats.thermal_privileged}>
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
            <div class="st-row">
              <span class="st-k">
                <span class="st-name">Stop everything if it overheats</span>
                <span class="st-sub-text">
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
              <div class="st-row sub">
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
                      <span class="st-name sm">Power off if it keeps climbing</span>
                      <span class="st-sub-text" class:warn={!selM.stats.thermal_privileged}>
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
  /* The card, row, button and field come from settings.css, shared by every
     section. What is left here is the page's own frame: the rail, the section
     header, and the handful of things only this page has. */

  /* Rail beside panel, the pair centred as one block. Centring the LAYOUT and
     not the panel is what stops the content stranding itself against the left
     edge of a wide screen with a lake of empty to its right. */
  .layout {
    display: grid;
    grid-template-columns: 190px minmax(0, 1fr);
    gap: 34px;
    max-width: 900px;
    margin: 0 auto;
    padding: 22px 20px calc(56px + var(--safe-bottom));
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
    padding: 16px 10px 5px;
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .rgroup:first-child {
    padding-top: 2px;
  }
  /* The machine's name is a value, not a label, so it keeps its own case and
     takes the mono voice this app gives data everywhere else. The rule above it
     is what stops it reading as a nav item that happens to be disabled: a
     heading needs to sit on something, and a dot plus a word at link height
     sits on nothing. */
  .rgroup.mach {
    text-transform: none;
    letter-spacing: 0;
    margin-top: 10px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }
  .rmach {
    font-family: var(--mono, monospace);
    font-size: 11px;
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
    gap: 10px;
    padding: 7px 10px;
    border-radius: 8px;
    color: var(--text-3);
    font-size: 13px;
    text-align: left;
  }
  .rlbl {
    flex: 1;
    min-width: 0;
  }
  /* The icon is dimmer than its label at rest and comes up with it on hover, so
     a row reads as one thing rather than as a glyph next to a word. */
  .ric {
    flex: none;
    width: 17px;
    height: 17px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    color: var(--text-4);
    transition: color 0.12s;
  }
  .ric :global(svg) {
    width: 17px;
    height: 17px;
  }
  .rlink:hover {
    background: var(--panel);
    color: var(--text);
  }
  .rlink:hover .ric {
    color: var(--text-3);
  }
  .rlink.on {
    background: var(--panel-2);
    color: var(--text);
    font-weight: 550;
  }
  .rlink.on .ric {
    color: var(--text);
  }
  .rcount {
    flex: none;
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
    margin: 0 0 14px;
  }
  .shead h2 {
    margin: 0;
    font-size: 17px;
    font-weight: 600;
    letter-spacing: -0.015em;
    color: var(--text);
  }
  .shead p {
    margin: 4px 0 0;
    font-size: 12.5px;
    line-height: 1.55;
    color: var(--text-4);
    max-width: 56ch;
  }

  /* The switch. The one control this page owns that settings.css does not,
     because nothing else in the app uses one. */
  .switch {
    flex: none;
    position: relative;
    width: 40px;
    height: 23px;
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
    width: 17px;
    height: 17px;
    border-radius: 50%;
    background: var(--text-3);
    transition: transform 0.15s, background 0.15s;
  }
  .switch.on .knob {
    transform: translateX(17px);
    background: #0b0b0c;
  }

  /* A machine row: the whole left side is the target, because picking a machine
     is the point of this list. */
  /* The row's own padding moves onto the button, so the whole left side is the
     target rather than just the words in it. The numbers have to match the
     shared row exactly or the machine list sits off the page's left edge. */
  .mrow {
    padding: 0;
  }
  .mrow.sel {
    background: var(--panel);
  }
  .mpick {
    flex: 1;
    min-width: 0;
    display: flex;
    padding: 13px 10px;
    background: none;
  }
  .mpick:hover :global(.st-name) {
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
    padding-right: 10px;
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

  /* Settings that only exist while the switch above them is on. Indented and
     hung off a single vertical hairline, which says "these belong to the thing
     above" with one line rather than by putting a box around them. */
  .st-row.sub {
    flex-direction: column;
    align-items: flex-start;
    gap: 12px;
    margin-left: 2px;
    padding-left: 16px;
    border-top: none;
    border-left: 1px solid var(--border);
    border-radius: 0;
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

  .foot {
    margin-top: 14px;
  }
  .err {
    margin: 0 0 12px;
    font-size: 12px;
    color: var(--alert);
  }
  .err.small {
    flex-basis: 100%;
    margin: 0;
    padding: 0 10px 12px;
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
    .mpick {
      padding: 12px 8px;
    }
    .macts {
      padding: 0 8px 12px;
    }
  }
</style>
