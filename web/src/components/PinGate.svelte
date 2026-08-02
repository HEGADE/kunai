<script lang="ts">
  import { lanPin } from '../lib/lanpin.svelte'

  // The sign-in screen for a device reaching kunai over the network.
  //
  // It says as little as possible on failure, on purpose: the server does not
  // reveal whether a PIN is wrong or whether one is even set, and a screen that
  // was more helpful would undo that. The one thing it does say plainly is how
  // long a lockout has left, because an owner who has mistyped needs to know the
  // difference between "wait a minute" and "something is broken".

  let pin = $state('')
  const locked = $derived(lanPin.retryAfterMs > 0)

  // Count the lockout down so the wait is visible rather than a dead button.
  $effect(() => {
    if (!locked) return
    const t = setInterval(() => lanPin.tick(1000), 1000)
    return () => clearInterval(t)
  })

  const waitLabel = $derived.by(() => {
    const secs = Math.ceil(lanPin.retryAfterMs / 1000)
    if (secs >= 60) {
      const mins = Math.ceil(secs / 60)
      return `${mins} minute${mins === 1 ? '' : 's'}`
    }
    return `${secs} second${secs === 1 ? '' : 's'}`
  })

  async function submit(e: Event) {
    e.preventDefault()
    if (locked || lanPin.busy) return
    const ok = await lanPin.signIn(pin)
    if (!ok) pin = ''
  }
</script>

<div class="gate">
  <form class="card" onsubmit={submit}>
    <h1>kunai</h1>
    <p class="sub">This machine is on your network. Enter its PIN to continue.</p>

    <input
      class="pin mono"
      type="password"
      inputmode="numeric"
      autocomplete="one-time-code"
      placeholder="••••••"
      bind:value={pin}
      disabled={locked || lanPin.busy}
      aria-label="PIN"
    />

    {#if locked}
      <p class="msg wait">Too many attempts. Try again in {waitLabel}.</p>
    {:else if lanPin.error}
      <p class="msg err">{lanPin.error}</p>
    {/if}

    <button class="go" type="submit" disabled={locked || lanPin.busy || pin.length < 6}>
      {lanPin.busy ? 'Checking…' : 'Unlock'}
    </button>

    <p class="foot">
      Set or change the PIN in Settings, on the machine itself.
    </p>
  </form>
</div>

<style>
  .gate {
    position: fixed;
    inset: 0;
    z-index: 200;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
    background: var(--bg);
  }
  .card {
    width: 100%;
    max-width: 320px;
    display: flex;
    flex-direction: column;
    gap: 12px;
    text-align: center;
  }
  h1 {
    margin: 0;
    font-size: 19px;
    font-weight: 600;
    letter-spacing: -0.014em;
    color: var(--text);
  }
  .sub {
    margin: 0 0 4px;
    font-size: 13px;
    line-height: 1.55;
    color: var(--text-3);
  }
  /* Wide tracking so six characters read as six, which is the whole content. */
  .pin {
    width: 100%;
    padding: 13px 14px;
    text-align: center;
    font-size: 21px;
    letter-spacing: 0.42em;
    text-indent: 0.42em;
    color: var(--text);
    background: var(--panel);
    border: 1px solid var(--border-2);
    border-radius: var(--r-sm);
  }
  .pin:focus {
    outline: none;
    border-color: var(--text-3);
  }
  .pin:disabled {
    opacity: 0.55;
  }
  .go {
    padding: 10px 14px;
    border-radius: var(--r-sm);
    background: var(--white);
    color: #0b0b0c;
    font-size: 13.5px;
    font-weight: 550;
  }
  .go:disabled {
    opacity: 0.4;
  }
  .msg {
    margin: 0;
    font-size: 12.5px;
    line-height: 1.5;
  }
  .err {
    color: var(--alert);
  }
  /* Amber: the colour this app already uses for "blocked, waiting on something". */
  .wait {
    color: var(--busy);
  }
  .foot {
    margin: 6px 0 0;
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-4);
  }
</style>
