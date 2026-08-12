<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { updateAvailable } from '../lib/update'
  import { enablePush, disablePush, isSubscribed, pushState } from '../lib/push'
  import { setKeepAwake, setThermal, setLid, setFailover, getCLIs, setCLIs } from '../lib/api'
  import type { Machine, CLIProfile } from '../lib/types'
  import GitHubApp from './GitHubApp.svelte'
  import LanAccess from './LanAccess.svelte'
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
  // headings say whose settings each section changes, and the third heading is
  // the machine's own name. Picking a different machine renames it, and the four
  // sections under it are exactly the ones that follow.
  type SectionId = 'notifications' | 'machines' | 'network' | 'unattended' | 'accounts' | 'reviews'

  let section = $state<SectionId>('notifications')

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

  // Per-machine Claude accounts. Loaded lazily, edited live (no restart). The
  // first account is the default and can't be removed.
  let accounts = $state<Record<string, CLIProfile[]>>({})
  let acctBusy = $state<Record<string, boolean>>({})
  let newName = $state<Record<string, string>>({})
  let newDir = $state<Record<string, string>>({})
  $effect(() => {
    for (const m of app.machines) if (m.online && !accounts[m.id]) loadAccounts(m)
  })
  async function loadAccounts(m: Machine) {
    try {
      accounts = { ...accounts, [m.id]: await getCLIs(m.url) }
    } catch {
      /* offline or old build without the endpoint: leave it unset */
    }
  }
  async function commitAccounts(m: Machine, list: CLIProfile[]) {
    if (acctBusy[m.id]) return
    acctBusy = { ...acctBusy, [m.id]: true }
    machErr = ''
    try {
      accounts = { ...accounts, [m.id]: await setCLIs(m.url, list) }
      await app.refresh() // so the New Session picker updates immediately
    } catch (e) {
      machErr = (e as Error).message
    } finally {
      const b = { ...acctBusy }
      delete b[m.id]
      acctBusy = b
    }
  }
  async function addAccount(m: Machine) {
    const name = (newName[m.id] || '').trim()
    const dir = (newDir[m.id] || '').trim()
    if (!name || !dir) return
    await commitAccounts(m, [...(accounts[m.id] ?? []), { name, bin: 'claude', dir }])
    newName = { ...newName, [m.id]: '' }
    newDir = { ...newDir, [m.id]: '' }
  }
  function removeAccount(m: Machine, name: string) {
    commitAccounts(m, (accounts[m.id] ?? []).filter((c) => c.name !== name))
  }

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

<Page title="Settings" sub={scopeSub}>
  {#snippet actions()}
    {#if section === 'machines'}
      <!-- Finding and adding machines is fleet management rather than a setting,
           so it is an action on the Machines section instead of a row of
           controls buried among the switches. -->
      <button class="act" onclick={discover} disabled={discovering}>
        {discovering ? 'Scanning…' : 'Discover'}
      </button>
      <button class="act solid" onclick={() => (showAddMachine = !showAddMachine)}>Add</button>
    {/if}
  {/snippet}

  <div class="layout">
    <nav class="rail" aria-label="Settings sections">
      <!-- The group headings are the point: they say whose settings the links
           under them change. Without that, a page of switches cannot tell you
           which ones follow the machine picker. -->
      <div class="rgroup">This device</div>
      <button class="rlink" class:on={section === 'notifications'} onclick={() => (section = 'notifications')}>
        Notifications
      </button>

      <div class="rgroup">Fleet</div>
      <button class="rlink" class:on={section === 'machines'} onclick={() => (section = 'machines')}>
        Machines
        <span class="rcount mono">{app.machines.length}</span>
      </button>

      {#if selM}
        <!-- The machine's own name as a heading, and a way to change it. A
             static word like "Machine" would be one more thing that does not
             say which one. -->
        <button class="rgroup pick" onclick={() => (section = 'machines')} title="Choose a different machine">
          <span class="rdot" class:live={selM.online}></span>
          {selM.label}
        </button>
        <button class="rlink" class:on={section === 'network'} onclick={() => (section = 'network')}>Network</button>
        <button class="rlink" class:on={section === 'unattended'} onclick={() => (section = 'unattended')}>Unattended</button>
        <button class="rlink" class:on={section === 'accounts'} onclick={() => (section = 'accounts')}>Accounts</button>
        <button class="rlink" class:on={section === 'reviews'} onclick={() => (section = 'reviews')}>Reviews</button>
      {/if}
    </nav>

    <div class="panel">
      {#if machErr}<p class="err">{machErr}</p>{/if}

      {#if section === 'notifications'}
        <div class="row">
          <span class="rk">
            <span class="rname">Push notifications</span>
            <span class="rsub">
              {#if !supported}
                Not supported in this browser.
              {:else}
                No content leaves the tailnet.
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
        {#if hint}<p class="hint">{hint}</p>{/if}
        <p class="note">
          Notifications are per device, so turning them on here does not turn them on
          anywhere else you use kunai.
        </p>

      {:else if section === 'machines'}
        {#if showAddMachine}
          <div class="addrow">
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
            <button class="add" onclick={addMachine} disabled={adding || !newUrl.trim()}>Add</button>
          </div>
        {/if}

        <!-- One card per machine, and picking one is what the four sections
             below it in the rail will change. The version and its update button
             live here because they are facts about the machine rather than
             settings on it. -->
        {#each app.machines as m (m.id)}
          <div class="mcard" class:sel={selM?.id === m.id}>
            <button class="mpick" onclick={() => (pickedM = m.id)}>
              <span class="mtop">
                <span class="rdot" class:live={m.online}></span>
                <span class="mname">{m.label}</span>
                {#if m.self}<span class="mtag">this machine</span>{/if}
              </span>
              <span class="murl mono">{m.url || 'this machine'}</span>
              {#if m.stats}
                <span class="mbuild mono">
                  kunai {m.stats.kunai_version || '—'} · claude {m.stats.claude_version || '—'}{m.stats.arch
                    ? ` · ${m.stats.os}/${m.stats.arch}`
                    : ''}
                </span>
              {:else if !m.online}
                <span class="mbuild">Offline</span>
              {/if}
            </button>
            <div class="macts">
              {#if outdated(m)}
                <button class="act solid" disabled={app.updating[m.id]} onclick={() => app.updateMachine(m.id)}>
                  {updateLabel(m)}
                </button>
              {/if}
              {#if !m.self}
                <button class="act quiet" onclick={() => app.removeMachine(m.id)}>Remove</button>
              {/if}
            </div>
            {#if app.updateError[m.id]}
              <p class="err small">Update failed: {app.updateError[m.id]}</p>
            {/if}
          </div>
        {/each}

      {:else if !selM}
        <p class="note">No machines yet.</p>
      {:else if !selM.online}
        <p class="note">{selM.label} is offline. Nothing to change here until it is back.</p>

      {:else if section === 'network'}
        <LanAccess base={selM.url} label={selM.label} />

      {:else if section === 'unattended'}
        {#if selM.stats?.keep_awake_supported}
          <div class="row">
            <span class="rk">
              <span class="rname">Keep awake while locked</span>
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
              {#if !selM.stats.thermal_privileged}
                <span class="rsub warn">Needs the admin setup from install.</span>
              {/if}
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
                  {Math.round(selM.stats.cpu_temp_c)}°C now
                {:else if selM.stats.thermal_pressure}
                  {selM.stats.thermal_pressure} pressure now
                {:else}
                  No temperature here — the time limit is the guard.
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
            <div class="sub-panel">
              <div class="thlimits">
                {#if selM.stats.cpu_temp_c > 0}
                  <label class="thlim">
                    <span class="thk">Trip at</span>
                    <input
                      class="thin mono"
                      type="number"
                      min="50"
                      max="105"
                      value={selM.stats.thermal_soft_c}
                      disabled={thBusy[selM.id]}
                      onchange={(e) => saveThermal(selM, { soft_c: +e.currentTarget.value })}
                    />
                    <span class="thu">°C</span>
                  </label>
                {/if}
                <label class="thlim">
                  <span class="thk">Time limit</span>
                  <input
                    class="thin mono"
                    type="number"
                    min="0"
                    max="72"
                    value={selM.stats.thermal_max_hours}
                    disabled={thBusy[selM.id]}
                    onchange={(e) => saveThermal(selM, { max_hours: +e.currentTarget.value })}
                  />
                  <span class="thu">hours awake (0 = off)</span>
                </label>
              </div>
              {#if selM.stats.cpu_temp_c > 0 || selM.stats.thermal_pressure}
                <label class="thcheck">
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
                  <span class="thck">
                    <span class="thcname">Power off if it keeps climbing</span>
                    <span class="thcsub" class:warn={!selM.stats.thermal_privileged}>
                      {#if selM.stats.thermal_privileged}
                        Last resort, once stopping everything was not enough.
                      {:else}
                        Needs the admin setup from install.
                      {/if}
                    </span>
                  </span>
                </label>
                {#if selM.stats.thermal_action === 'poweroff' && selM.stats.cpu_temp_c > 0}
                  <label class="thlim">
                    <span class="thk">Power off at</span>
                    <input
                      class="thin mono"
                      type="number"
                      min="50"
                      max="105"
                      value={selM.stats.thermal_hard_c}
                      disabled={thBusy[selM.id]}
                      onchange={(e) => saveThermal(selM, { hard_c: +e.currentTarget.value })}
                    />
                    <span class="thu">°C</span>
                  </label>
                {/if}
              {/if}
            </div>
          {/if}
        {/if}

      {:else if section === 'accounts'}
        {#if selM.stats?.clis && selM.stats.clis.length > 1}
          <div class="row">
            <span class="rk">
              <span class="rname">Auto-failover on limit</span>
              <span class="rsub">
                When an account hits its 5-hour or weekly wall, roll to the account with
                the most headroom (Claude or provider) and continue.
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
        {/if}

        {#if accounts[selM.id]}
          {#each accounts[selM.id] as c, i (c.name)}
            <div class="row acct">
              <span class="acctname">
                {c.name}{#if i === 0}<span class="acctdef">default</span>{/if}
              </span>
              <span class="acctdir mono">{c.dir || c.bin}</span>
              {#if i > 0}
                <button
                  class="acctx"
                  onclick={() => removeAccount(selM, c.name)}
                  disabled={acctBusy[selM.id]}
                  aria-label="Remove account"
                >
                  <svg width="9" height="9" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M1 1l8 8M9 1l-8 8" /></svg>
                </button>
              {/if}
            </div>
          {/each}
          {#if showAddAcct[selM.id]}
            <div class="addrow">
              <input class="min" placeholder="Name (e.g. Work)" bind:value={newName[selM.id]} autocomplete="off" />
              <input class="min mono" placeholder="Config folder, e.g. /Users/you/.claude-work" bind:value={newDir[selM.id]} autocomplete="off" autocapitalize="off" spellcheck="false" />
              <button class="add" onclick={() => addAccount(selM)} disabled={acctBusy[selM.id] || !(newName[selM.id] || '').trim() || !(newDir[selM.id] || '').trim()}>Add</button>
            </div>
            <p class="note">
              Log in once first: <span class="mono">CLAUDE_CONFIG_DIR=&lt;folder&gt; claude</span>
            </p>
          {:else}
            <button class="more" onclick={() => (showAddAcct[selM.id] = true)}>+ Add account</button>
          {/if}
          <p class="note">
            Signing in from the app, without a terminal, is on the
            <button class="link" onclick={() => app.openAccounts()}>Accounts</button> page.
          </p>
        {/if}

      {:else if section === 'reviews'}
        <GitHubApp machineId={selM.id} />
      {/if}
    </div>
  </div>
</Page>

<style>
  /* Rail beside panel on a laptop; on a phone the rail becomes a strip of chips
     above the panel. A drill-down would be the other option and is worse here:
     it puts a second back button inside a page that already has one, and these
     sections are small enough that one tap should reach any of them. */
  .layout {
    display: grid;
    grid-template-columns: 186px 1fr;
    gap: 26px;
    max-width: 940px;
    margin: 0 auto;
    padding: 20px 16px calc(40px + var(--safe-bottom));
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
    padding: 16px 10px 6px;
    font-size: 10.5px;
    font-weight: 600;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--text-4);
    text-align: left;
  }
  .rgroup:first-child {
    padding-top: 2px;
  }
  .rgroup.pick {
    background: none;
    border: none;
    cursor: pointer;
    /* The machine name is data, so it does not take the uppercase treatment the
       two fixed headings do: those are labels, this is a value. */
    text-transform: none;
    letter-spacing: 0.01em;
    font-size: 11.5px;
    color: var(--text-3);
    font-family: var(--mono, monospace);
  }
  .rgroup.pick:hover {
    color: var(--text);
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
    border-radius: var(--r-sm);
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
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  /* One setting per row: what it is, one line of what it does, and the control.
     The hairline between rows is the only rule on the page; a bordered card per
     setting made a wall of boxes out of six switches. */
  .row {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 13px 2px;
    border-bottom: 1px solid var(--border);
  }
  .rk {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .rname {
    font-size: 13.5px;
    color: var(--text);
  }
  .rsub {
    font-size: 11.5px;
    line-height: 1.55;
    color: var(--text-4);
  }
  /* The one status colour reserved for "be careful". */
  .rsub.warn {
    color: color-mix(in srgb, var(--busy) 80%, var(--text-3));
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

  /* Settings that only exist while their switch is on, indented under it. */
  .sub-panel {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 12px 2px 14px 16px;
    border-bottom: 1px solid var(--border);
    border-left: 1px solid var(--border);
    margin-left: 2px;
  }
  .thlimits {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 18px;
  }
  .thlim {
    display: flex;
    align-items: baseline;
    gap: 7px;
    font-size: 12px;
    color: var(--text-3);
  }
  .thin {
    width: 56px;
    padding: 4px 7px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text);
    font-size: 12.5px;
    text-align: right;
  }
  .thin:focus-visible {
    outline: none;
    border-color: var(--border-2);
  }
  .thin::-webkit-outer-spin-button,
  .thin::-webkit-inner-spin-button {
    appearance: none;
    margin: 0;
  }
  .thu {
    font-size: 11px;
    color: var(--text-4);
  }
  .thcheck {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    cursor: pointer;
  }
  .thck {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .thcname {
    font-size: 12.5px;
    color: var(--text-2);
  }
  .thcsub {
    font-size: 11px;
    color: var(--text-4);
  }
  .thcsub.warn {
    color: color-mix(in srgb, var(--busy) 80%, var(--text-3));
  }

  /* A machine: what it is, where it is, what it runs, and the two things you do
     to it. Selecting one is what the rail's machine group then follows. */
  .mcard {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
    padding: 12px 12px;
    border: 1px solid var(--border);
    border-radius: var(--r);
    margin-bottom: 8px;
    background: var(--panel);
  }
  .mcard.sel {
    border-color: var(--border-2);
    background: var(--panel-2);
  }
  .mpick {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 3px;
    text-align: left;
    background: none;
  }
  .mtop {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .mname {
    font-size: 13.5px;
    color: var(--text);
  }
  .mtag {
    font-size: 9.5px;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .murl,
  .mbuild {
    font-size: 11px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    unicode-bidi: plaintext;
  }
  .macts {
    flex: none;
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .act {
    padding: 5px 12px;
    border-radius: var(--r-sm);
    background: var(--panel-2);
    color: var(--text-2);
    font-size: 12px;
    font-weight: 500;
  }
  .act:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .act.solid {
    background: var(--white);
    color: #0b0b0c;
  }
  .act.quiet {
    background: none;
    color: var(--text-4);
  }
  .act.quiet:hover {
    color: var(--text-2);
    background: var(--panel-2);
  }
  .act:disabled {
    opacity: 0.5;
  }

  .addrow {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--r);
    margin-bottom: 10px;
  }
  .min {
    width: 100%;
    padding: 8px 11px;
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
  .add {
    align-self: flex-start;
    padding: 6px 14px;
    border-radius: var(--r-sm);
    background: var(--panel-3);
    color: var(--text-2);
    font-size: 12.5px;
  }
  .add:hover {
    color: var(--text);
  }
  .add:disabled {
    opacity: 0.5;
  }
  .more {
    align-self: flex-start;
    margin-top: 10px;
    font-size: 12px;
    color: var(--text-4);
  }
  .more:hover {
    color: var(--text-2);
  }

  .acct {
    gap: 10px;
  }
  .acctname {
    flex: none;
    display: flex;
    align-items: baseline;
    gap: 6px;
    font-size: 12.5px;
    color: var(--text-2);
  }
  .acctdef {
    font-size: 9.5px;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .acctdir {
    flex: 1;
    min-width: 0;
    font-size: 11px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    unicode-bidi: plaintext;
    text-align: left;
  }
  .acctx {
    flex: none;
    color: var(--text-4);
  }
  .acctx:hover {
    color: var(--alert);
  }

  .note {
    margin: 12px 0 0;
    font-size: 11.5px;
    line-height: 1.6;
    color: var(--text-4);
  }
  .link {
    color: var(--text-2);
    font-size: inherit;
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  .link:hover {
    color: var(--text);
  }
  .hint {
    margin: 10px 0 0;
    font-size: 12px;
    color: var(--text-3);
  }
  .err {
    margin: 0 0 10px;
    font-size: 12px;
    color: var(--alert);
  }
  .err.small {
    flex-basis: 100%;
    margin: 6px 0 0;
    font-size: 11px;
  }

  /* Phone: the rail becomes a scrolling strip of chips above the panel. The
     group headings cannot come with it, so the page subtitle carries the scope
     instead -- which is why Page takes one. */
  @media (max-width: 720px) {
    .layout {
      grid-template-columns: 1fr;
      gap: 14px;
      padding: 14px 12px calc(40px + var(--safe-bottom));
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
    .rgroup {
      display: none;
    }
    .rgroup.pick {
      display: inline-flex;
      flex: none;
      padding: 7px 12px;
      border-radius: 999px;
      border: 1px solid var(--border);
    }
    .rlink {
      flex: none;
      padding: 7px 13px;
      border-radius: 999px;
      border: 1px solid transparent;
      white-space: nowrap;
    }
    .rlink.on {
      border-color: var(--border-2);
    }
    .mcard {
      align-items: flex-start;
    }
  }
</style>
