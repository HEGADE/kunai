<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { enablePush, disablePush, isSubscribed, pushState } from '../lib/push'
  import { setKeepAwake, setThermal, setLid, getCLIs, setCLIs } from '../lib/api'
  import type { Machine, CLIProfile } from '../lib/types'

  const st = $derived(app.stats)
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
  // until asked for. Left open they are permanent clutter in a panel you came to
  // flip one switch in.
  let showAddMachine = $state(false)
  let showAddAcct = $state<Record<string, boolean>>({})

  async function addMachine() {
    const url = newUrl.trim()
    if (!url || adding) return
    adding = true
    machErr = ''
    try {
      await app.addMachine(newLabel.trim(), url)
      newLabel = ''
      newUrl = ''
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
</script>

<div class="backdrop" onclick={() => app.closeSettings()} role="presentation">
  <div class="modal" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
    <div class="grab" aria-hidden="true"></div>
    <header>
      <h2>Settings</h2>
      <button class="close" onclick={() => app.closeSettings()} aria-label="Close">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
      </button>
    </header>

    <div class="body">
      <div class="sec">Notifications</div>
      <div class="row">
        <div class="rmeta">
          <span class="rname">Push notifications</span>
          <span class="rsub">
            {#if !supported}
              Not supported in this browser.
            {:else}
              No content leaves the tailnet.
            {/if}
          </span>
        </div>
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

      <div class="sec">
        Machines
        <button class="discover" onclick={discover} disabled={discovering}>
          {discovering ? 'Scanning…' : 'Discover'}
        </button>
      </div>
      <div class="info">
        {#each app.machines as m (m.id)}
          <div class="irow mrow">
            <span class="mdot" class:live={m.online}></span>
            <span class="mmeta">
              <span class="mlabel">{m.label}{#if m.self}<span class="mself">this</span>{/if}</span>
              <span class="murl mono">{m.url}</span>
            </span>
            {#if !m.self}
              <button class="mx" onclick={() => app.removeMachine(m.id)} aria-label="Remove machine">
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
              </button>
            {/if}
          </div>
          {#if m.online && m.stats?.keep_awake_supported}
            <div class="irow awrow">
              <span class="awk">
                <span class="awname">Keep awake while locked</span>
                <span class="awsub">Needs the lid open and power.</span>
              </span>
              <button
                class="switch"
                class:on={m.stats.keep_awake}
                onclick={() => toggleAwake(m)}
                disabled={awBusy[m.id]}
                role="switch"
                aria-checked={m.stats.keep_awake}
                aria-label="Toggle keep awake"
              >
                <span class="knob"></span>
              </button>
            </div>
          {/if}
          {#if m.online && m.stats?.keep_lid_supported}
            <div class="irow awrow">
              <span class="awk">
                <span class="awname">Keep working with the lid closed</span>
                {#if !m.stats.thermal_privileged}
                  <span class="awsub warn">Needs the admin setup from install.</span>
                {/if}
              </span>
              <button
                class="switch"
                class:on={m.stats.keep_lid}
                onclick={() => toggleLid(m)}
                disabled={lidBusy[m.id]}
                role="switch"
                aria-checked={m.stats.keep_lid}
                aria-label="Toggle lid-closed hold"
              >
                <span class="knob"></span>
              </button>
            </div>
          {/if}
          {#if m.online && m.stats}
            <div class="irow awrow">
              <span class="awk">
                <span class="awname">Stop everything if it overheats</span>
                <span class="awsub">
                  {#if m.stats.cpu_temp_c > 0}
                    {Math.round(m.stats.cpu_temp_c)}°C now
                  {:else if m.stats.thermal_pressure}
                    {m.stats.thermal_pressure} pressure now
                  {:else}
                    No temperature here — the time limit is the guard.
                  {/if}
                </span>
              </span>
              <button
                class="switch"
                class:on={m.stats.thermal_guard}
                onclick={() => saveThermal(m, { enabled: !m.stats?.thermal_guard })}
                disabled={thBusy[m.id]}
                role="switch"
                aria-checked={m.stats.thermal_guard}
                aria-label="Toggle thermal guard"
              >
                <span class="knob"></span>
              </button>
            </div>
            {#if m.stats.thermal_guard}
              <div class="thlimits">
                {#if m.stats.cpu_temp_c > 0}
                  <label class="thlim">
                    <span class="thk">Trip at</span>
                    <input
                      class="thin mono"
                      type="number"
                      min="50"
                      max="105"
                      value={m.stats.thermal_soft_c}
                      disabled={thBusy[m.id]}
                      onchange={(e) => saveThermal(m, { soft_c: +e.currentTarget.value })}
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
                    value={m.stats.thermal_max_hours}
                    disabled={thBusy[m.id]}
                    onchange={(e) => saveThermal(m, { max_hours: +e.currentTarget.value })}
                  />
                  <span class="thu">hours awake (0 = off)</span>
                </label>
              </div>
              {#if m.stats.cpu_temp_c > 0 || m.stats.thermal_pressure}
                <div class="thpower">
                  <label class="thcheck">
                    <input
                      type="checkbox"
                      checked={m.stats.thermal_action === 'poweroff'}
                      disabled={thBusy[m.id]}
                      onchange={(e) =>
                        saveThermal(m, {
                          action: e.currentTarget.checked ? 'poweroff' : 'sleep',
                          hard_c: e.currentTarget.checked ? m.stats?.thermal_hard_c || 100 : 0,
                        })}
                    />
                    <span class="thck">
                      <span class="thcname">Power off if it keeps climbing</span>
                      <span class="thcsub" class:warn={!m.stats.thermal_privileged}>
                        {#if m.stats.thermal_privileged}
                          Last resort, once stopping everything was not enough.
                        {:else}
                          Needs the admin setup from install.
                        {/if}
                      </span>
                    </span>
                  </label>
                  {#if m.stats.thermal_action === 'poweroff' && m.stats.cpu_temp_c > 0}
                    <label class="thlim">
                      <span class="thk">Power off at</span>
                      <input
                        class="thin mono"
                        type="number"
                        min="50"
                        max="105"
                        value={m.stats.thermal_hard_c}
                        disabled={thBusy[m.id]}
                        onchange={(e) => saveThermal(m, { hard_c: +e.currentTarget.value })}
                      />
                      <span class="thu">°C</span>
                    </label>
                  {/if}
                </div>
              {/if}
            {/if}
          {/if}
          {#if m.online && accounts[m.id]}
            <div class="acctblock">
              <div class="acctlabel">Claude accounts</div>
              {#each accounts[m.id] as c, i (c.name)}
                <div class="acctrow">
                  <span class="acctname">{c.name}{#if i === 0}<span class="acctdef">default</span>{/if}</span>
                  <span class="acctdir mono">{c.dir || c.bin}</span>
                  {#if i > 0}
                    <button class="acctx" onclick={() => removeAccount(m, c.name)} disabled={acctBusy[m.id]} aria-label="Remove account">
                      <svg width="9" height="9" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M1 1l8 8M9 1l-8 8" /></svg>
                    </button>
                  {/if}
                </div>
              {/each}
              {#if showAddAcct[m.id]}
                <div class="acctadd">
                  <input class="min" placeholder="Name (e.g. Work)" bind:value={newName[m.id]} autocomplete="off" />
                  <input class="min mono" placeholder="Config folder, e.g. /Users/you/.claude-work" bind:value={newDir[m.id]} autocomplete="off" autocapitalize="off" spellcheck="false" />
                  <button class="add" onclick={() => addAccount(m)} disabled={acctBusy[m.id] || !(newName[m.id] || '').trim() || !(newDir[m.id] || '').trim()}>Add</button>
                </div>
                <p class="acctnote">
                  Log in once first: <span class="mono">CLAUDE_CONFIG_DIR=&lt;folder&gt; claude</span>
                </p>
              {:else}
                <button class="more" onclick={() => (showAddAcct[m.id] = true)}>+ Add account</button>
              {/if}
            </div>
          {/if}
        {/each}
      </div>
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
      {:else}
        <button class="more" onclick={() => (showAddMachine = true)}>+ Add manually</button>
      {/if}
      {#if machErr}<p class="hint">{machErr}</p>{/if}

      <div class="sec">Server</div>
      <div class="info">
        {#if st?.hostname}
          <div class="irow"><span class="ik">Host</span><span class="iv mono">{st.hostname}</span></div>
        {/if}
        <div class="irow"><span class="ik">Link</span><span class="iv mono">direct over tailnet</span></div>
        {#if st?.claude_version}
          <div class="irow"><span class="ik">Claude</span><span class="iv mono">{st.claude_version}</span></div>
        {/if}
        {#if st?.kunai_version}
          <div class="irow"><span class="ik">Kunai</span><span class="iv mono">{st.kunai_version}{st.arch ? ` · ${st.os}/${st.arch}` : ''}</span></div>
        {/if}
        {#if st}
          <div class="irow"><span class="ik">Sessions</span><span class="iv mono">{st.sessions} active</span></div>
        {/if}
      </div>
    </div>
  </div>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 50;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 20px;
    animation: fade 0.14s ease-out;
  }
  @keyframes fade {
    from {
      opacity: 0;
    }
  }
  .modal {
    width: 100%;
    max-width: 480px;
    max-height: min(78dvh, 620px);
    background: var(--panel);
    border: 1px solid var(--border-2);
    border-radius: var(--r-lg);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    box-shadow: 0 24px 70px -24px rgba(0, 0, 0, 0.75);
    animation: pop 0.16s ease-out;
  }
  @keyframes pop {
    from {
      transform: translateY(10px);
      opacity: 0;
    }
  }
  .grab {
    display: none;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 18px 20px 12px;
  }
  h2 {
    font-size: 16px;
    font-weight: 600;
    letter-spacing: -0.01em;
    margin: 0;
  }
  .close {
    width: 30px;
    height: 30px;
    border-radius: 50%;
    background: var(--panel-2);
    border: 1px solid var(--border);
    color: var(--text-3);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .close:hover {
    color: var(--text);
  }
  .body {
    flex: 1;
    overflow-y: auto;
    padding: 4px 20px 20px;
  }
  .sec {
    display: flex;
    align-items: center;
    justify-content: space-between;
    font-size: 11.5px;
    font-weight: 550;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-4);
    padding: 16px 2px 10px;
  }
  .discover {
    text-transform: none;
    letter-spacing: 0;
    font-size: 12px;
    font-weight: 500;
    color: var(--text-2);
    padding: 4px 10px;
    border-radius: 100px;
    border: 1px solid var(--border);
    background: var(--panel-2);
  }
  .discover:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .discover:disabled {
    opacity: 0.55;
  }
  .mrow {
    gap: 11px;
  }
  .awrow {
    gap: 14px;
    padding-left: 34px;
  }
  .awk {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .awname {
    font-size: 13px;
    color: var(--text-2);
  }
  .awsub {
    font-size: 11px;
    color: var(--text-4);
    line-height: 1.45;
  }
  .thlimits {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 18px;
    padding: 2px 8px 8px 34px;
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
  /* A warning subline earns the one status colour reserved for "be careful". */
  .awsub.warn {
    color: color-mix(in srgb, var(--busy) 80%, var(--text-3));
  }
  .acctblock {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 4px 8px 10px 34px;
    padding-top: 9px;
    border-top: 1px solid var(--border);
  }
  .acctlabel {
    font-size: 12.5px;
    color: var(--text-2);
  }
  .acctrow {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12.5px;
  }
  .acctname {
    flex: none;
    display: flex;
    align-items: baseline;
    gap: 6px;
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
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: 4px;
    color: var(--text-4);
  }
  .acctx:hover {
    color: var(--text);
    background: var(--panel-3);
  }
  .acctadd {
    display: flex;
    gap: 6px;
    margin-top: 2px;
  }
  .acctadd .min {
    min-width: 0;
  }
  .acctadd .min:first-child {
    flex: 0 0 34%;
  }
  .acctadd .min:nth-child(2) {
    flex: 1;
  }
  .acctnote {
    margin: 0;
    font-size: 11px;
    line-height: 1.5;
    color: var(--text-4);
  }
  /* The way into a form you rarely want: present, but not taking up the room a
     form would. */
  .more {
    align-self: flex-start;
    padding: 5px 0;
    font-size: 12px;
    color: var(--text-3);
  }
  .more:hover {
    color: var(--text);
  }
  .thpower {
    display: flex;
    flex-direction: column;
    gap: 9px;
    margin: 4px 8px 10px 34px;
    padding-top: 9px;
    border-top: 1px solid var(--border);
  }
  .thcheck {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    cursor: pointer;
  }
  .thcheck input {
    margin-top: 2px;
    accent-color: var(--alert);
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
    line-height: 1.45;
    color: var(--text-4);
  }
  .mdot {
    flex: none;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .mdot.live {
    background: var(--live);
  }
  .mmeta {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .mlabel {
    font-size: 13.5px;
    color: var(--text);
    display: flex;
    align-items: center;
    gap: 7px;
  }
  .mself {
    padding: 0 5px;
    border-radius: 4px;
    background: var(--panel-3);
    color: var(--text-4);
    font-size: 9.5px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .murl {
    font-size: 11px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .mx {
    flex: none;
    width: 28px;
    height: 28px;
    border-radius: 50%;
    color: var(--text-4);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .mx:hover {
    color: var(--text-2);
    background: var(--panel-3);
  }
  .addrow {
    display: flex;
    gap: 7px;
    margin-top: 8px;
  }
  .min {
    min-width: 0;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    padding: 9px 11px;
    font-size: 12.5px;
    color: var(--text);
    outline: none;
  }
  .min:first-child {
    flex: 0 0 88px;
  }
  .min:nth-child(2) {
    flex: 1;
  }
  .min:focus {
    border-color: var(--border-2);
  }
  .add {
    flex: none;
    padding: 0 14px;
    border-radius: var(--r-sm);
    background: var(--white);
    color: #0b0b0c;
    font-size: 13px;
    font-weight: 550;
  }
  .add:disabled {
    opacity: 0.45;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 14px 16px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
  }
  .rmeta {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .rname {
    font-size: 14px;
    font-weight: 500;
    color: var(--text);
  }
  .rsub {
    font-size: 12px;
    color: var(--text-3);
    line-height: 1.5;
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
  .hint {
    margin: 10px 2px 0;
    font-size: 12.5px;
    color: var(--text-3);
    line-height: 1.5;
  }
  .info {
    display: flex;
    flex-direction: column;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    overflow: hidden;
  }
  .irow {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 12px 16px;
  }
  .irow + .irow {
    border-top: 1px solid var(--border);
  }
  .ik {
    font-size: 13px;
    color: var(--text-3);
  }
  .iv {
    font-size: 12.5px;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* Phone: bottom sheet. */
  @media (max-width: 640px) {
    .backdrop {
      align-items: flex-end;
      padding: 0;
    }
    .modal {
      max-width: none;
      max-height: 86dvh;
      border-radius: 20px 20px 0 0;
      border-left: none;
      border-right: none;
      border-bottom: none;
      animation: rise 0.2s ease-out;
    }
    @keyframes rise {
      from {
        transform: translateY(24px);
        opacity: 0;
      }
    }
    .grab {
      display: block;
      width: 38px;
      height: 4px;
      border-radius: 3px;
      background: var(--border-2);
      margin: 10px auto 0;
      flex: none;
    }
    header {
      padding-top: 10px;
    }
    .body {
      padding-bottom: calc(var(--safe-bottom) + 20px);
    }
  }
</style>
