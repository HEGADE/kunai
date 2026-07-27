<script lang="ts">
  // Handing one conversation to somebody who is not on your tailnet.
  //
  // This is not a social share sheet. It is lending a key to a machine you own,
  // and the design says so: the permission control is the largest thing here, it
  // is first, and it reads as a LADDER rather than three equal pills, because the
  // tiers genuinely nest. Somebody who can work can also ask, and can also watch.
  // Filling downward shows how far in you are letting them, which is the one
  // question this dialog exists to answer.
  //
  // The other change worth stating: an expiry is composed, not picked off a list.
  // A list can never say "1 day 4 hours", and a duration is abstract anyway, so
  // the wall-clock instant it lands on is shown underneath. That is what people
  // actually reason about ("it dies before my flight"), and it is the thing a
  // number of seconds cannot tell you.
  import Spinner from './Spinner.svelte'
  import {
    createShare,
    revokeShare,
    approveShareGuest,
    denyShareGuest,
    funnelStatus,
    openFunnel,
    getShare,
  } from '../lib/api'
  import type { Share, ShareTier, FunnelState } from '../lib/types'
  import { MIN_TTL, MAX_TTL, clampTTL, splitDuration, expiryWords } from '../lib/duration'

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

  let tier = $state<ShareTier>('view')
  // Composed rather than chosen, so any duration is expressible.
  let days = $state(0)
  let hours = $state(1)
  let mins = $state(0)
  let detail = $state(false)
  let fromNow = $state(false)
  let unattended = $state(false)

  let funnel = $state<FunnelState | null>(null)
  let funnelBusy = $state(false)

  // The rungs, in order. `adds` is what this rung grants over the one above it,
  // which is the only honest way to describe a ladder: each is the previous plus
  // something.
  const RUNGS: { id: ShareTier; name: string; adds: string }[] = [
    { id: 'view', name: 'Watch', adds: 'Reads the conversation as it happens.' },
    { id: 'ask', name: 'Ask', adds: 'Can also prompt. The agent reads and searches, changes nothing.' },
    { id: 'work', name: 'Work', adds: 'Can also have it edit files, inside this folder only.' },
  ]
  const rungIndex = $derived(RUNGS.findIndex((r) => r.id === tier))
  const canPrompt = $derived(tier !== 'view')

  const ttl = $derived(clampTTL(days * 86400 + hours * 3600 + mins * 60))
  // What the composed duration actually lands on. Recomputed on a slow tick so an
  // open dialog does not drift into saying yesterday.
  let now = $state(Date.now())
  $effect(() => {
    const t = setInterval(() => (now = Date.now()), 30_000)
    return () => clearInterval(t)
  })
  const landsOn = $derived(expiryWords(now + ttl * 1000, now))
  // True when what was typed had to be pulled back inside the allowed range, so
  // the dialog can say so rather than silently disagreeing with the fields.
  const clamped = $derived(days * 86400 + hours * 3600 + mins * 60 !== ttl)

  const PRESETS: { label: string; d: number; h: number; m: number }[] = [
    { label: '15 min', d: 0, h: 0, m: 15 },
    { label: '1 hour', d: 0, h: 1, m: 0 },
    { label: '8 hours', d: 0, h: 8, m: 0 },
    { label: '1 day', d: 1, h: 0, m: 0 },
    { label: '5 days', d: 5, h: 0, m: 0 },
  ]
  const activePreset = $derived(
    PRESETS.findIndex((p) => p.d === days && p.h === hours && p.m === mins),
  )

  function preset(p: (typeof PRESETS)[number]) {
    days = p.d
    hours = p.h
    mins = p.m
  }

  function bump(unit: 'd' | 'h' | 'm', by: number) {
    if (unit === 'd') days = Math.max(0, Math.min(5, days + by))
    if (unit === 'h') hours = Math.max(0, Math.min(23, hours + by))
    if (unit === 'm') mins = Math.max(0, Math.min(59, mins + by * 5))
  }

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
        ttl_secs: ttl,
        detail,
        from_now: fromNow,
        // A standing yes applies ONLY to calls the folder guard already cleared.
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

  async function act(fn: () => Promise<Share | null | void>) {
    busy = true
    err = ''
    try {
      const s = await fn()
      if (s !== undefined) {
        share = s ?? null
        onchange(share)
      }
    } catch (e) {
      err = e instanceof Error ? e.message : String(e)
    } finally {
      busy = false
    }
  }

  // Stopping is asking for a state, so it ends in that state whatever happened.
  // The server treats an already-gone link as success; if the call fails for some
  // other reason the dialog still closes, because leaving it open showing a link
  // the owner has just told us to stop is the one outcome that would be actively
  // misleading. The reason is logged rather than trapped behind a dialog they
  // were trying to dismiss.
  async function revoke() {
    if (!share) return
    busy = true
    try {
      await revokeShare(base, share.token)
    } catch (e) {
      console.error('kunai: stop sharing failed', e)
    } finally {
      busy = false
      onchange(null)
      onclose()
    }
  }

  const approve = () =>
    act(() => (share?.pending ? approveShareGuest(base, share.token, share.pending.code) : Promise.resolve()))

  const deny = (unpair = false) =>
    act(() => (share ? denyShareGuest(base, share.token, unpair) : Promise.resolve()))

  async function turnOnFunnel(port: number) {
    funnelBusy = true
    err = ''
    try {
      funnel = await openFunnel(base, port)
      const again = await getShare(base, sessionId)
      if (again) {
        share = again
        onchange(again)
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

  const liveRung = $derived(share ? RUNGS.find((r) => r.id === share!.tier) : null)
</script>

{#snippet stepper(unit: 'd' | 'h' | 'm', value: number, suffix: string, max: number)}
  <div class="step">
    <button aria-label="less {suffix}" onclick={() => bump(unit, -1)} disabled={value <= 0}>−</button>
    <span class="num mono">{value}<em>{suffix}</em></span>
    <button aria-label="more {suffix}" onclick={() => bump(unit, 1)} disabled={value >= max}>+</button>
  </div>
{/snippet}

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
          Anyone with the link can watch this conversation in a browser, with no
          Tailscale and no account. <b>Only one person you approve can send to it.</b>
        </p>

        <section>
          <h3>How far they get</h3>
          <!-- The ladder. Rungs at or above the selection are filled, because the
               tiers nest: a guest who can work can also ask and watch. Three
               separate pills would imply they were alternatives. -->
          <div class="ladder" role="radiogroup" aria-label="How far they get">
            {#each RUNGS as r, i (r.id)}
              <button
                class="rung"
                class:reached={i <= rungIndex}
                class:current={i === rungIndex}
                role="radio"
                aria-checked={i === rungIndex}
                onclick={() => (tier = r.id)}
              >
                <span class="mark" aria-hidden="true"></span>
                <span class="rname">{r.name}</span>
                <span class="radds">{r.adds}</span>
              </button>
            {/each}
          </div>
        </section>

        {#if canPrompt}
          <p class="note">
            The agent runs <b>without a shell</b> while this is shared, and can only
            touch this session's folder. Every tool call is still yours to approve.
          </p>
          <label class="sw">
            <input type="checkbox" bind:checked={unattended} />
            <span class="track" aria-hidden="true"><span class="knob"></span></span>
            <span class="swtext">
              <b>Let them work while you are away</b>
              <em>Approves changes inside the folder automatically. Anything reaching
                outside it still waits for you.</em>
            </span>
          </label>
        {/if}

        <section>
          <h3>How long</h3>
          <div class="presets">
            {#each PRESETS as p, i (p.label)}
              <button class="chip" class:on={i === activePreset} onclick={() => preset(p)}>{p.label}</button>
            {/each}
          </div>
          <div class="dial">
            {@render stepper('d', days, 'd', 5)}
            {@render stepper('h', hours, 'h', 23)}
            {@render stepper('m', mins, 'm', 59)}
          </div>
          <!-- The instant, not the interval. "5 days" is abstract; "Saturday at
               22:40" is the thing you can actually check against your week. -->
          <p class="lands mono">{landsOn}</p>
          {#if clamped}
            <p class="hint">Links last between 5 minutes and 5 days.</p>
          {/if}
        </section>

        <section>
          <h3>What they see</h3>
          <div class="switches">
            <label class="sw">
              <input type="checkbox" checked={!fromNow} onchange={(e) => (fromNow = !e.currentTarget.checked)} />
              <span class="track" aria-hidden="true"><span class="knob"></span></span>
              <span class="swtext">
                <b>Everything so far</b>
                <em>Off shares only what happens from the moment you create the link.</em>
              </span>
            </label>
            <label class="sw">
              <input type="checkbox" bind:checked={detail} />
              <span class="track" aria-hidden="true"><span class="knob"></span></span>
              <span class="swtext">
                <b>File contents and command output</b>
                <em>Off shows a tool call by name and shape, so a config file the
                  agent happened to read does not travel with it.</em>
              </span>
            </label>
          </div>
        </section>
      {:else}
        <div class="linkbox">
          <div class="linkrow">
            <input class="link mono" readonly value={share.url} onclick={(e) => e.currentTarget.select()} />
            <button class="copy" class:done={copied} onclick={copyLink}>{copied ? 'Copied' : 'Copy'}</button>
          </div>
          <p class="summary">
            <span class="tag">{liveRung?.name ?? share.tier}</span>
            <span class="mono">{expiryWords(share.expires_at * 1000, now)}</span>
            {#if share.detail.ToolInputs}<span class="mono">· full detail</span>{/if}
          </p>
        </div>

        {#if needsFunnel}
          <div class="panel">
            <p><b>Nobody outside your tailnet can open this yet.</b> Tailscale Funnel
              has to serve it. Traffic is routed by name and stays encrypted until it
              reaches this machine.</p>
            {#if funnel?.available === false}
              <p class="hint">Tailscale is not available on this machine.</p>
            {:else if freePorts.length}
              <p class="cmd mono">tailscale funnel --bg --https={freePorts[0]} …</p>
              <button class="go" disabled={funnelBusy} onclick={() => turnOnFunnel(freePorts[0])}>
                {#if funnelBusy}<Spinner />{/if}
                Turn on public access
              </button>
            {:else}
              <p class="hint">Every Funnel port is already serving something else.
                Free one of 443, 8443 or 10000 and reopen this.</p>
            {/if}
          </div>
        {/if}

        {#if share.pending}
          <div class="panel pair">
            <p><b>{share.pending.name || 'Someone'}</b> wants to send to this session.
              They should be seeing this code:</p>
            <p class="code mono">{share.pending.code}</p>
            <div class="prow">
              <button class="go" disabled={busy} onclick={approve}>Let them in</button>
              <button class="ghost" disabled={busy} onclick={() => deny(false)}>No</button>
            </div>
          </div>
        {:else if share.guest}
          <div class="panel">
            <p><span class="dot live" aria-hidden="true"></span>
              <b>{share.guest.name || 'A guest'}</b> is paired and can send to this session.</p>
            <p class="hint">{share.turns} turn{share.turns === 1 ? '' : 's'} sent</p>
            <button class="ghost" disabled={busy} onclick={() => deny(true)}>Remove them</button>
          </div>
        {:else if share.tier !== 'view'}
          <p class="hint">Nobody is paired yet. The first person to open the link and
            ask will show up here for you to approve.</p>
        {/if}
      {/if}

    </div>

    <!-- Errors sit above the footer, outside the scrolling body. Inside it they
         appended below whatever was on screen, so on a short window the thing
         explaining why a button did nothing was itself off-screen -- which is
         indistinguishable from the button doing nothing. -->
    {#if err}<p class="err">{err}</p>{/if}

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
    max-width: 520px;
    max-height: min(88dvh, 780px);
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
    padding: 13px 13px 12px 15px;
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
    padding: 15px;
    display: flex;
    flex-direction: column;
    gap: 17px;
  }
  .lede {
    margin: 0;
    font-size: 12.5px;
    line-height: 1.55;
    color: var(--text-3);
  }
  .lede b {
    color: var(--text-2);
    font-weight: 500;
  }

  section {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  /* An eyebrow in the mono voice: it names a section of a decision rather than
     labelling a field, and the tracking keeps it quiet at this size. */
  h3 {
    margin: 0;
    font-family: var(--mono);
    font-size: 9.5px;
    font-weight: 500;
    letter-spacing: 0.13em;
    text-transform: uppercase;
    color: var(--text-4);
  }

  /* --- the ladder ---------------------------------------------------------- */
  .ladder {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border);
    border-radius: var(--r);
    overflow: hidden;
  }
  .rung {
    position: relative;
    display: grid;
    grid-template-columns: 20px 1fr;
    grid-template-rows: auto auto;
    align-items: center;
    gap: 0 8px;
    padding: 10px 12px;
    border: 0;
    border-top: 1px solid var(--border);
    background: transparent;
    text-align: left;
    cursor: pointer;
  }
  /* Where the ladder stops. With three rungs filled, the fill alone says "you got
     at least this far" and cannot say which one you chose; this edge does, and it
     is the only white in the control. */
  .rung.current::before {
    content: '';
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    width: 2px;
    background: var(--white);
  }
  .rung:first-child {
    border-top: 0;
  }
  /* Reached rungs fill, because the tiers nest: choosing Work means Watch and Ask
     are included, and a control that showed only the last one picked would be
     describing them as alternatives. */
  .rung.reached {
    background: var(--panel);
  }
  .rung.current {
    background: var(--panel-3);
  }
  .rung:hover:not(.current) {
    background: var(--panel-3);
  }
  .mark {
    grid-row: 1 / span 2;
    justify-self: center;
    width: 9px;
    height: 9px;
    border-radius: 50%;
    border: 1.5px solid var(--border-2);
  }
  .rung.reached .mark {
    border-color: var(--text-3);
    background: var(--text-3);
  }
  .rung.current .mark {
    border-color: var(--white);
    background: var(--white);
  }
  .rname {
    font-size: 13px;
    font-weight: 500;
    color: var(--text-3);
  }
  .rung.reached .rname {
    color: var(--text);
  }
  .radds {
    font-size: 11.5px;
    line-height: 1.45;
    color: var(--text-4);
  }
  .rung.current .radds {
    color: var(--text-3);
  }

  /* --- duration ------------------------------------------------------------ */
  .presets {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
  }
  .chip {
    height: 26px;
    padding: 0 10px;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: transparent;
    color: var(--text-3);
    font-size: 11.5px;
    cursor: pointer;
  }
  .chip:hover {
    color: var(--text);
    border-color: var(--border-2);
  }
  .chip.on {
    background: var(--panel-3);
    border-color: var(--border-2);
    color: var(--text);
  }
  .dial {
    display: flex;
    gap: 6px;
  }
  .step {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 36px;
    padding: 0 4px;
    border: 1px solid var(--border);
    border-radius: 9px;
    background: var(--panel);
  }
  .step button {
    width: 26px;
    height: 26px;
    border: 0;
    border-radius: 7px;
    background: transparent;
    color: var(--text-3);
    font-size: 15px;
    line-height: 1;
    cursor: pointer;
  }
  .step button:hover:not(:disabled) {
    background: var(--panel-3);
    color: var(--text);
  }
  .step button:disabled {
    opacity: 0.3;
    cursor: default;
  }
  .num {
    font-size: 14px;
    color: var(--text);
    font-variant-numeric: tabular-nums;
  }
  .num em {
    font-style: normal;
    font-size: 11px;
    color: var(--text-4);
    margin-left: 1px;
  }
  .lands {
    margin: 0;
    font-size: 11.5px;
    color: var(--text-3);
  }

  /* --- switches ------------------------------------------------------------ */
  .switches {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .sw {
    display: grid;
    grid-template-columns: 30px 1fr;
    gap: 10px;
    cursor: pointer;
  }
  .sw input {
    position: absolute;
    opacity: 0;
    pointer-events: none;
  }
  .track {
    position: relative;
    width: 30px;
    height: 17px;
    margin-top: 1px;
    border-radius: 999px;
    background: var(--panel-3);
    border: 1px solid var(--border);
    transition: background var(--t-fast, 0.12s) ease;
  }
  .knob {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 11px;
    height: 11px;
    border-radius: 50%;
    background: var(--text-4);
    transition:
      transform var(--t-fast, 0.12s) ease,
      background var(--t-fast, 0.12s) ease;
  }
  .sw input:checked + .track {
    background: var(--white);
    border-color: var(--white);
  }
  .sw input:checked + .track .knob {
    transform: translateX(13px);
    background: #0b0b0c;
  }
  .sw input:focus-visible + .track {
    outline: 2px solid var(--text-3);
    outline-offset: 2px;
  }
  .swtext {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .swtext b {
    font-size: 12.5px;
    font-weight: 500;
    color: var(--text-2);
  }
  .swtext em {
    font-style: normal;
    font-size: 11.5px;
    line-height: 1.45;
    color: var(--text-4);
  }

  /* The one paragraph people will assume works the other way. */
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
  .note b {
    color: var(--text-2);
    font-weight: 500;
  }

  /* --- the link ------------------------------------------------------------ */
  .linkbox {
    display: flex;
    flex-direction: column;
    gap: 7px;
  }
  .linkrow {
    display: flex;
    gap: 6px;
  }
  .link {
    flex: 1;
    min-width: 0;
    height: 36px;
    padding: 0 11px;
    background: var(--panel);
    border: 1px solid var(--border-2);
    border-radius: 9px;
    color: var(--text);
    font-size: 12px;
  }
  .copy {
    flex: none;
    height: 36px;
    padding: 0 14px;
    background: var(--white);
    border: 0;
    border-radius: 9px;
    color: #0b0b0c;
    font-size: 12px;
    font-weight: 500;
    cursor: pointer;
  }
  .copy.done {
    background: var(--panel-3);
    color: var(--text-2);
  }
  .summary {
    display: flex;
    align-items: center;
    gap: 7px;
    margin: 0;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .tag {
    padding: 2px 7px;
    border-radius: 999px;
    background: var(--panel-3);
    color: var(--text-2);
    font-size: 10.5px;
  }

  .panel {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 11px 12px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--r);
  }
  .panel p {
    margin: 0;
    font-size: 12px;
    line-height: 1.55;
    color: var(--text-3);
  }
  .panel b {
    color: var(--text-2);
    font-weight: 500;
  }
  .dot {
    display: inline-block;
    width: 6px;
    height: 6px;
    margin-right: 5px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .dot.live {
    background: var(--live);
  }
  .cmd {
    font-size: 11.5px;
    color: var(--text-4) !important;
    /* Geist Mono ligates "--" into one dash, which turns "funnel --bg" into
       something nobody could retype. */
    font-variant-ligatures: none;
  }
  /* Read aloud or off a screen, so it is set large and widely tracked. */
  .code {
    align-self: flex-start;
    padding: 5px 13px;
    background: var(--panel-3);
    border-radius: 8px;
    color: var(--text) !important;
    font-size: 20px;
    letter-spacing: 0.2em;
  }
  .prow {
    display: flex;
    gap: 8px;
  }
  .hint {
    margin: 0;
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-4);
  }
  .err {
    flex: none;
    margin: 0;
    padding: 9px 15px;
    border-top: 1px solid var(--border);
    background: var(--panel);
    font-size: 12px;
    line-height: 1.45;
    color: var(--alert);
  }

  footer {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 15px calc(var(--safe-bottom) + 12px);
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
    border-radius: 9px;
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

  @media (prefers-reduced-motion: reduce) {
    .track,
    .knob {
      transition: none;
    }
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
  }
</style>
