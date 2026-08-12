<script lang="ts">
  import {
    getLanPin, setLanPin, clearLanPin, getLanDevices, forgetLanDevices,
    type LanPinState, type LanDevice,
  } from '../lib/api'
  import { shortAgo } from '../lib/reltime'

  // The lock on a machine's network listener, as a settings panel.
  //
  // Its own component rather than another block inside Settings, because it holds
  // real state (a PIN being composed, a device list, two failure paths) and
  // Settings is already long enough that a reader cannot hold it in their head.
  //
  // The design decision worth stating: the PIN is shown as you type it. This is
  // the one screen where hiding it is theatre -- you are sitting at the machine,
  // choosing a secret rather than proving one, and a PIN mistyped behind dots is
  // a PIN you cannot use from the tablet and cannot explain why. The screen that
  // ACCEPTS a PIN, over the network, hides it as you would expect.
  let { base = '', label = 'this machine' }: { base?: string; label?: string } = $props()

  let pinState = $state<LanPinState | null>(null)
  let devices = $state<LanDevice[]>([])
  let editing = $state(false)
  let pin = $state('')
  let busy = $state(false)
  let err = $state('')
  let saved = $state(false)
  let armRemove = $state(false)

  async function load() {
    try {
      pinState = await getLanPin(base)
      devices = pinState.set ? await getLanDevices(base).catch(() => []) : []
    } catch {
      pinState = null
    }
  }
  $effect(() => {
    void base
    void load()
  })

  // Only the shape the client can judge without duplicating the server's rules.
  // Whether a PIN is too obvious is the server's call, and its answer is shown
  // verbatim rather than guessed at here, so the two can never disagree.
  const min = $derived(pinState?.min_len ?? 6)
  const max = $derived(pinState?.max_len ?? 12)
  const digitsOnly = $derived(/^\d*$/.test(pin))
  const longEnough = $derived(pin.length >= min && pin.length <= max)
  const canSave = $derived(digitsOnly && longEnough && !busy)

  async function save() {
    if (!canSave) return
    busy = true
    err = ''
    try {
      await setLanPin(base, pin)
      pin = ''
      editing = false
      saved = true
      setTimeout(() => (saved = false), 2200)
      await load()
    } catch (e) {
      err = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function remove() {
    busy = true
    err = ''
    try {
      await clearLanPin(base)
      armRemove = false
      await load()
    } catch (e) {
      err = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function signOutAll() {
    busy = true
    try {
      await forgetLanDevices(base)
      await load()
    } catch (e) {
      err = (e as Error).message
    } finally {
      busy = false
    }
  }

  const ago = (unix: number) => shortAgo(new Date(unix * 1000).toISOString())
</script>

{#if pinState}
  <!-- No heading of its own: this is the whole of Settings' Network section and
       the section header already names it. It carried one when it was a block
       among many in one long column. -->
  <div class="st-card">
    <!-- The state, said in one line, before any control. -->
    <div class="st-row">
      <span class="st-k">
        <span class="st-name">
          {#if !pinState.set}
            Off — no PIN set
          {:else if pinState.enabled}
            Locked with a PIN
          {:else}
            PIN ready — network access is off
          {/if}
        </span>
        <span class="st-sub-text">
          {#if !pinState.set}
            Another device on your wifi can reach {label} without Tailscale, once you
            set a PIN. Setting one turns it on straight away.
          {:else if pinState.enabled}
            Devices on your wifi need this PIN. They stay signed in afterwards.
          {:else}
            A PIN is set, but no network address was found to serve.
          {/if}
        </span>
      </span>
      {#if !editing}
        <button class="st-btn" onclick={() => { editing = true; pin = '' }}>
          {pinState.set ? 'Change' : 'Set a PIN'}
        </button>
      {/if}
    </div>

    {#if editing}
      <div class="st-row ed">
        <!-- Visible on purpose: see the note at the top of this file. -->
        <input
          class="pinin mono"
          inputmode="numeric"
          autocomplete="off"
          spellcheck="false"
          maxlength={max}
          placeholder={'0'.repeat(min)}
          bind:value={pin}
          onkeydown={(e) => e.key === 'Enter' && save()}
          aria-label="New PIN"
        />
        <button class="st-btn solid savepin" onclick={save} disabled={!canSave}>Save</button>
        <button class="plain" onclick={() => { editing = false; pin = ''; err = '' }}>Cancel</button>
      </div>
      <!-- One live line that says what is still missing, so Save is never a
           button you press to find out. -->
      <p class="rule" class:ok={canSave}>
        {#if !digitsOnly}
          Digits only.
        {:else if pin.length < min}
          {min - pin.length} more digit{min - pin.length === 1 ? '' : 's'}.
        {:else}
          {pin.length} digits. Avoid runs and repeats — those get guessed first.
        {/if}
      </p>
    {/if}

    {#if saved}<p class="rule ok">PIN saved. Every device was signed out.</p>{/if}
    {#if err}<p class="rule bad">{err}</p>{/if}

    <!-- Where to point the other device. Without this the PIN is set and you
         still have to go and find your own address. -->
    {#if pinState.set && pinState.urls.length}
      <div class="st-row">
        <span class="st-k">
          <span class="st-name">Open on another device</span>
          {#each pinState.urls as u (u)}
            <span class="st-sub-text mono url">{u}</span>
          {/each}
          <span class="st-sub-text">
            The certificate is self-signed, so the browser warns once per device.
          </span>
        </span>
      </div>
      <!-- Bound is not reachable. A firewall that drops incoming traffic leaves
           the address above looking perfect from here and timing out over there,
           with nothing to say why, so the way out is printed next to it. -->
      {#if pinState.firewall}
        <div class="st-row">
          <span class="st-k">
            <span class="st-name warn">{pinState.firewall.tool} may be blocking it</span>
            <span class="st-sub-text">
              This machine's firewall drops incoming connections by default. If the
              other device cannot reach the address, run this once:
            </span>
            <code class="cmd mono">{pinState.firewall.command}</code>
          </span>
        </div>
      {/if}
    {/if}

    {#if pinState.set && devices.length}
      <div class="st-row">
        <span class="st-k">
          <span class="st-name">Signed in ({devices.length})</span>
          {#each devices as d (`${d.created}-${d.label ?? ''}`)}
            <span class="st-sub-text">
              {d.label || 'device'} · added {ago(d.created)} · seen {ago(d.seen)}
            </span>
          {/each}
        </span>
        <button class="plain" onclick={signOutAll} disabled={busy}>Sign out all</button>
      </div>
    {/if}

    {#if pinState.set && !editing}
      <div class="st-row">
        <span class="st-k">
          <span class="st-name">Remove the PIN</span>
          <span class="st-sub-text">Removes the PIN and stops serving this network straight away.</span>
        </span>
        {#if armRemove}
          <span class="confirm">
            <button class="plain danger" onclick={remove} disabled={busy}>Remove</button>
            <button class="plain" onclick={() => (armRemove = false)}>Keep</button>
          </span>
        {:else}
          <button class="plain" onclick={() => (armRemove = true)}>Remove</button>
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  /* Self-contained on purpose. Svelte scopes styles per component, so the class
     names Settings uses for its own rows do not reach here -- borrowing them
     rendered this panel as a wall of unstyled text. Owning the look is also the
     honest arrangement for a component that is meant to be movable. */
  /* The card, rows, labels and buttons come from settings.css, shared with every
     other section. What is left here is what only this panel has: the PIN entry,
     the live rule under it, and the firewall command.
     One rule has to survive the move: a feedback line sits BETWEEN two rows, so
     the divider has to follow a rule as well as a row or it disappears exactly
     when a message is showing. */
  .rule + :global(.st-row) {
    border-top: 1px solid var(--border);
  }
  .savepin:disabled {
    opacity: 0.35;
  }
  .plain {
    flex: none;
    height: 28px;
    padding: 0 11px;
    border-radius: 8px;
    font-size: 12.5px;
    color: var(--text-3);
    background: none;
  }
  .plain:hover:not(:disabled) {
    color: var(--text);
    background: var(--panel-3);
  }
  .danger:hover:not(:disabled) {
    color: var(--alert);
  }
  .confirm {
    display: flex;
    gap: 4px;
    flex: none;
  }
  /* The PIN being composed. Wide tracking so the digits are countable, which is
     the whole reason it is shown rather than dotted. */
  .ed {
    gap: 8px;
  }
  .pinin {
    flex: 1;
    min-width: 0;
    max-width: 180px;
    height: 32px;
    padding: 0 12px;
    font-size: 15px;
    letter-spacing: 0.3em;
    text-indent: 0.3em;
    color: var(--text);
    background: var(--bg);
    border: 1px solid var(--border-2);
    border-radius: 8px;
  }
  .pinin:focus {
    outline: none;
    border-color: var(--text-3);
  }
  /* Feedback sits under the row it belongs to, inset to the same gutter, so it
     reads as part of that control rather than a new item in the card. */
  .rule {
    margin: 0;
    padding: 0 16px 12px;
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-4);
  }
  .rule.ok {
    color: var(--live);
  }
  .rule.bad {
    color: var(--alert);
  }
  .url {
    font-size: 11.5px;
    color: var(--text-2);
    word-break: break-all;
  }
  /* Amber, this app's colour for "blocked on something you have to do". */
  .warn {
    color: var(--busy);
  }
  .cmd {
    display: block;
    margin-top: 5px;
    padding: 7px 9px;
    font-size: 11px;
    line-height: 1.5;
    color: var(--text-2);
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    /* Wrap at spaces, never inside a word: break-all split "proto" across two
       lines, which is unreadable and worse to retype. */
    white-space: pre-wrap;
    word-break: normal;
    overflow-wrap: break-word;
    user-select: all;
  }
  code {
    font-size: 0.92em;
    color: var(--text-2);
  }
</style>
