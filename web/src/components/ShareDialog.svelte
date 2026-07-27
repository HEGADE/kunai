<script lang="ts">
  // Handing one conversation to somebody who is not on your tailnet.
  //
  // The order of this form is the order the decisions matter in. What the other
  // person may DO comes first and is explained in words, because it is the only
  // control here that can cost you something; expiry and scope follow; the link
  // itself appears last, once there is something to hand over. A picker that
  // reads as four equal pills would hide which one is the risky one.
  import SegMenu, { type SegOption } from './SegMenu.svelte'
  import Spinner from './Spinner.svelte'
  import {
    createShare,
    revokeShare,
    approveShareGuest,
    denyShareGuest,
    funnelStatus,
    openFunnel,
  } from '../lib/api'
  import type { Share, ShareTier, FunnelState } from '../lib/types'

  let {
    base = '',
    sessionId,
    title,
    existing = null,
    onclose,
    onchange,
  }: {
    base?: string
    sessionId: string
    title: string
    existing?: Share | null
    onclose: () => void
    onchange: (s: Share | null) => void
  } = $props()

  let share = $state<Share | null>(existing)
  let busy = $state(false)
  let err = $state('')
  let copied = $state(false)
  let armRevoke = $state(false)

  // Form state, only meaningful before a link exists.
  let tier = $state<ShareTier>('view')
  let ttl = $state('3600')
  let detail = $state(false)
  let fromNow = $state(false)
  let unattended = $state(false)

  let funnel = $state<FunnelState | null>(null)
  let funnelBusy = $state(false)

  const TIERS: SegOption[] = [
    { id: 'view', label: 'Can watch', hint: 'Reads the conversation. Sends nothing.' },
    { id: 'ask', label: 'Can ask', hint: 'Prompts the agent. It may read and search, never change anything.' },
    { id: 'work', label: 'Can work', hint: 'Prompts the agent. It may also edit files, inside this folder only.' },
  ]

  // Five minutes to five days, which is the range the server enforces anyway.
  const TTLS: SegOption[] = [
    { id: '300', label: '5 minutes' },
    { id: '1800', label: '30 minutes' },
    { id: '7200', label: '2 hours' },
    { id: '28800', label: '8 hours' },
    { id: '86400', label: '1 day' },
    { id: '432000', label: '5 days' },
  ]

  const SCOPE: SegOption[] = [
    { id: 'all', label: 'The whole conversation' },
    { id: 'now', label: 'Only what happens from now' },
  ]

  const canPrompt = $derived(tier !== 'view')

  // Funnel state is only interesting once there is a link to reach, so it is
  // fetched when the dialog opens and after anything that could change it.
  $effect(() => {
    funnelStatus(base)
      .then((f) => (funnel = f))
      .catch(() => (funnel = null))
  })

  const needsFunnel = $derived(!!share && !share.reachable)
  const freePorts = $derived(funnel?.free ?? [])

  async function create() {
    busy = true
    err = ''
    try {
      const s = await createShare(base, {
        session_id: sessionId,
        tier,
        ttl_secs: Number(ttl),
        detail,
        from_now: fromNow,
        // A standing yes applies ONLY to calls the folder guard already cleared;
        // anything reaching outside still stops for you whatever this says.
        mode: canPrompt && unattended ? 'acceptEdits' : '',
        max_turns: 0,
      })
      share = s
      onchange(s)
      funnel = await funnelStatus(base).catch(() => funnel)
    } catch (e) {
      err = e instanceof Error ? e.message : String(e)
    } finally {
      busy = false
    }
  }

  async function revoke() {
    if (!share) return
    busy = true
    err = ''
    try {
      await revokeShare(base, share.token)
      share = null
      onchange(null)
      onclose()
    } catch (e) {
      err = e instanceof Error ? e.message : String(e)
    } finally {
      busy = false
    }
  }

  async function approve() {
    if (!share?.pending) return
    busy = true
    try {
      share = await approveShareGuest(base, share.token, share.pending.code)
      onchange(share)
    } catch (e) {
      err = e instanceof Error ? e.message : String(e)
    } finally {
      busy = false
    }
  }

  async function deny(unpair = false) {
    if (!share) return
    busy = true
    try {
      share = await denyShareGuest(base, share.token, unpair)
      onchange(share)
    } catch (e) {
      err = e instanceof Error ? e.message : String(e)
    } finally {
      busy = false
    }
  }

  async function turnOnFunnel(port: number) {
    funnelBusy = true
    err = ''
    try {
      funnel = await openFunnel(base, port)
      // The link's reachability changed, so re-read it rather than guessing.
      if (share) {
        const again = await import('../lib/api').then((m) => m.getShare(base, sessionId))
        if (again) {
          share = again
          onchange(again)
        }
      }
    } catch (e) {
      err = e instanceof Error ? e.message : String(e)
    } finally {
      funnelBusy = false
    }
  }

  async function copyLink() {
    if (!share) return
    try {
      await navigator.clipboard.writeText(share.url)
      copied = true
      setTimeout(() => (copied = false), 1400)
    } catch {
      err = 'Could not copy. Select the link and copy it by hand.'
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.stopPropagation()
      onclose()
    }
  }

  // "in 3 hours", from the expiry the server set.
  function until(ts: number): string {
    const s = ts - Math.floor(Date.now() / 1000)
    if (s <= 0) return 'expired'
    if (s < 3600) return `${Math.round(s / 60)} min`
    if (s < 86400) return `${Math.round(s / 3600)} h`
    return `${Math.round(s / 86400)} d`
  }

  const tierWord = $derived(
    share ? (TIERS.find((t) => t.id === share!.tier)?.label ?? share!.tier) : '',
  )
</script>

<div class="backdrop" onclick={onclose} role="presentation">
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="modal"
    onclick={(e) => e.stopPropagation()}
    onkeydown={onKey}
    role="dialog"
    aria-modal="true"
    aria-label="Share this session"
    tabindex="-1"
  >
    <header>
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><path d="M4 12v7a2 2 0 002 2h12a2 2 0 002-2v-7" /><path d="M16 6l-4-4-4 4" /><path d="M12 2v14" /></svg>
      <h2>Share <span class="tname">{title || 'this session'}</span></h2>
      <button class="close" onclick={onclose} aria-label="Close">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M18 6L6 18M6 6l12 12" /></svg>
      </button>
    </header>

    <div class="body">
      {#if !share}
        <p class="lede">
          Anyone with the link can open this conversation in a browser, with no
          Tailscale and no account. Only one person you approve can send anything.
        </p>

        <div class="field">
          <span class="flabel">They can</span>
          <SegMenu
            value={tier}
            options={TIERS}
            up={false}
            note="What the other person may do with this session"
            onpick={(id) => (tier = id as ShareTier)} />
          <p class="hint">{TIERS.find((t) => t.id === tier)?.hint}</p>
        </div>

        {#if canPrompt}
          <!-- Stated plainly rather than buried, because it is the thing people
               will assume works the other way. -->
          <p class="note">
            The agent runs without a shell while this is shared, and can only
            touch this session's folder. Every tool call it makes is still yours
            to approve.
          </p>
          <label class="check">
            <input type="checkbox" bind:checked={unattended} />
            <span>
              <b>Let them work while you are away</b>
              <em>Approves file changes inside the folder automatically. Anything
                reaching outside it still waits for you.</em>
            </span>
          </label>
        {/if}

        <div class="row">
          <div class="field">
            <span class="flabel">Expires in</span>
            <SegMenu value={ttl} options={TTLS} up={false} onpick={(id) => (ttl = id)} />
          </div>
          <div class="field">
            <span class="flabel">They see</span>
            <SegMenu
              value={fromNow ? 'now' : 'all'}
              options={SCOPE}
              up={false}
              onpick={(id) => (fromNow = id === 'now')} />
          </div>
        </div>

        <label class="check">
          <input type="checkbox" bind:checked={detail} />
          <span>
            <b>Include file contents and command output</b>
            <em>Off by default: a tool call is shown by name and shape, so a
              config file the agent happened to read does not travel with it.</em>
          </span>
        </label>
      {:else}
        <div class="linkbox">
          <span class="flabel">Link</span>
          <div class="linkrow">
            <input class="link mono" readonly value={share.url} onclick={(e) => e.currentTarget.select()} />
            <button class="copy" onclick={copyLink}>{copied ? 'Copied' : 'Copy'}</button>
          </div>
          <p class="hint">
            {tierWord} · expires in {until(share.expires_at)}
            {#if share.detail.ToolInputs}· full detail{/if}
          </p>
        </div>

        {#if needsFunnel}
          <div class="warn">
            <p>
              <b>Nobody outside your tailnet can open this yet.</b>
              Tailscale Funnel has to serve it. Traffic is routed by name and
              stays encrypted until it reaches this machine.
            </p>
            {#if funnel?.available === false}
              <p class="hint">Tailscale is not available on this machine.</p>
            {:else if freePorts.length}
              <p class="cmd mono">tailscale funnel --bg --https={freePorts[0]} …</p>
              <button class="go" disabled={funnelBusy} onclick={() => turnOnFunnel(freePorts[0])}>
                {#if funnelBusy}<Spinner />{/if}
                Turn on public access
              </button>
            {:else}
              <p class="hint">
                Every Funnel port is already serving something else. Free one of
                443, 8443 or 10000 and reopen this.
              </p>
            {/if}
          </div>
        {/if}

        {#if share.pending}
          <div class="pair">
            <p>
              <b>{share.pending.name || 'Someone'}</b> wants to send to this session.
              They should be seeing this code:
            </p>
            <p class="code mono">{share.pending.code}</p>
            <div class="pactions">
              <button class="go" disabled={busy} onclick={approve}>Let them in</button>
              <button class="ghost" disabled={busy} onclick={() => deny(false)}>No</button>
            </div>
          </div>
        {:else if share.guest}
          <div class="pair">
            <p><b>{share.guest.name || 'A guest'}</b> is paired and can send to this session.</p>
            <p class="hint">
              {share.turns} turn{share.turns === 1 ? '' : 's'} sent
            </p>
            <button class="ghost" disabled={busy} onclick={() => deny(true)}>Remove them</button>
          </div>
        {:else if share.tier !== 'view'}
          <p class="hint">
            Nobody is paired yet. The first person to open the link and ask will
            show up here for you to approve.
          </p>
        {/if}
      {/if}

      {#if err}<p class="err">{err}</p>{/if}
    </div>

    <footer>
      {#if share}
        <button class="danger" class:armed={armRevoke} disabled={busy}
          onclick={() => (armRevoke ? revoke() : (armRevoke = true))}>
          {armRevoke ? 'Stop sharing — sure?' : 'Stop sharing'}
        </button>
        <span class="spacer"></span>
        <button class="ghost" onclick={onclose}>Done</button>
      {:else}
        <span class="spacer"></span>
        <button class="ghost" onclick={onclose}>Cancel</button>
        <button class="go" disabled={busy} onclick={create}>
          {#if busy}<Spinner />{/if}
          Create link
        </button>
      {/if}
    </footer>
  </div>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: calc(var(--safe-top) + 16px) 16px 16px;
    background: rgba(0, 0, 0, 0.58);
    backdrop-filter: blur(2px);
  }
  .modal {
    display: flex;
    flex-direction: column;
    width: 100%;
    max-width: 560px;
    max-height: min(86dvh, 760px);
    overflow: hidden;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r-lg);
    box-shadow: 0 24px 64px -20px rgba(0, 0, 0, 0.8);
  }
  header {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 14px 14px 12px 16px;
    border-bottom: 1px solid var(--border);
    color: var(--text-3);
  }
  h2 {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: 13.5px;
    font-weight: 600;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .tname {
    color: var(--text-2);
    font-weight: 500;
  }
  .close {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    border: 0;
    border-radius: 7px;
    background: transparent;
    color: var(--text-3);
    cursor: pointer;
  }
  .close:hover {
    background: var(--panel-3);
    color: var(--text);
  }
  .body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 14px 16px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .lede {
    margin: 0;
    font-size: 12.5px;
    line-height: 1.55;
    color: var(--text-2);
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 5px;
    min-width: 0;
  }
  .row {
    display: flex;
    gap: 14px;
  }
  .row .field {
    flex: 1;
  }
  .flabel {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-4);
  }
  .hint {
    margin: 0;
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-4);
  }
  /* The one paragraph people will assume works the other way, so it gets a rule
     and a surface rather than blending into the hints. */
  .note {
    margin: 0;
    padding: 9px 11px;
    border-left: 2px solid var(--border-2);
    background: var(--panel);
    border-radius: 0 7px 7px 0;
    font-size: 11.5px;
    line-height: 1.55;
    color: var(--text-3);
  }
  .check {
    display: flex;
    gap: 9px;
    cursor: pointer;
  }
  .check input {
    margin-top: 2px;
    accent-color: var(--white);
  }
  .check span {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .check b {
    font-size: 12.5px;
    font-weight: 500;
    color: var(--text-2);
  }
  .check em {
    font-style: normal;
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-4);
  }
  .linkbox {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .linkrow {
    display: flex;
    gap: 6px;
  }
  .link {
    flex: 1;
    min-width: 0;
    height: 34px;
    padding: 0 10px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text);
    font-size: 12px;
  }
  .copy {
    flex: none;
    height: 34px;
    padding: 0 12px;
    background: var(--panel-3);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text-2);
    font-size: 12px;
    cursor: pointer;
  }
  .copy:hover {
    color: var(--text);
  }
  .warn,
  .pair {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 11px 12px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r);
  }
  .warn p,
  .pair p {
    margin: 0;
    font-size: 12px;
    line-height: 1.55;
    color: var(--text-3);
  }
  .warn b,
  .pair b {
    color: var(--text-2);
    font-weight: 500;
  }
  .cmd {
    font-size: 11.5px;
    color: var(--text-4);
    /* Geist Mono ligates "--" into a single dash, which turns "funnel --bg" into
       something that reads as "funnel--bg". This is a command somebody may retype,
       so the characters have to be the ones they would type. */
    font-variant-ligatures: none;
  }
  /* The pairing code is read aloud or off a screen, so it is set large and wide. */
  .code {
    align-self: flex-start;
    padding: 5px 12px;
    background: var(--panel-3);
    border-radius: 8px;
    color: var(--text);
    font-size: 19px;
    letter-spacing: 0.18em;
  }
  .pactions {
    display: flex;
    gap: 8px;
  }
  .err {
    margin: 0;
    font-size: 12px;
    color: var(--alert);
  }
  footer {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 16px calc(var(--safe-bottom) + 12px);
    border-top: 1px solid var(--border);
  }
  .spacer {
    flex: 1;
  }
  .go,
  .ghost,
  .danger {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 7px;
    height: 34px;
    padding: 0 14px;
    border-radius: 8px;
    border: 1px solid transparent;
    font-size: 12.5px;
    font-weight: 500;
    cursor: pointer;
  }
  .go {
    background: var(--white);
    color: #0b0b0c;
  }
  .go:disabled {
    opacity: 0.55;
    cursor: default;
  }
  .ghost {
    background: transparent;
    border-color: var(--border);
    color: var(--text-2);
  }
  .ghost:hover {
    color: var(--text);
    background: var(--panel);
  }
  .danger {
    background: transparent;
    border-color: var(--border);
    color: var(--text-3);
  }
  .danger:hover,
  .danger.armed {
    color: var(--alert);
    border-color: var(--alert);
  }
  @media (max-width: 640px) {
    .backdrop {
      align-items: flex-end;
      padding: 0;
    }
    .modal {
      max-width: 100%;
      max-height: 92dvh;
      border-radius: 18px 18px 0 0;
    }
    .row {
      flex-direction: column;
      gap: 14px;
    }
  }
</style>
