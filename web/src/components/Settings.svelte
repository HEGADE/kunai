<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { enablePush, disablePush, isSubscribed, pushState } from '../lib/push'

  const st = $derived(app.stats)
  const supported = pushState() !== 'unsupported'

  let on = $state(false)
  let busy = $state(false)
  let hint = $state('')

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
              A generic wake-up when a session needs you or finishes. No content leaves the tailnet.
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
    font-size: 11.5px;
    font-weight: 550;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-4);
    padding: 16px 2px 10px;
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
