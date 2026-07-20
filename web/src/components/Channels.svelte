<script lang="ts">
  import { untrack } from 'svelte'
  import { app } from '../lib/app.svelte'
  import type { ChannelInfo } from '../lib/types'
  import { listChannels, saveChannel, answerChannelRequest, revokeChannelPerson } from '../lib/api'

  // Channels are the ways to reach kunai other than this app. Telegram today,
  // Slack next, so the screen is a list of channels rather than a Telegram
  // screen: a new one is another row here, not another page.
  //
  // The shape is a stack of rows that open in place. Closed, a row is a status
  // line you can read at a glance; open, it is everything that channel needs.
  // With two or three of these, a list that expands beats tabs or a grid of
  // cards, because the answer you usually want ("is it on, is anyone waiting")
  // is readable without opening anything.
  let machineId = $state(app.activeMachineId ?? app.machines[0]?.id ?? '')
  const base = $derived(app.baseForMachine(machineId))
  const machine = $derived(app.machines.find((m) => m.id === machineId) ?? null)

  let channels = $state<ChannelInfo[]>([])
  let loading = $state(true)
  let error = $state('')
  let open = $state('')
  let token = $state('')
  let busy = $state('')

  async function load() {
    error = ''
    try {
      channels = await listChannels(base)
    } catch (e) {
      error = (e as Error).message
    } finally {
      loading = false
    }
  }
  $effect(() => {
    void base
    untrack(() => {
      open = ''
      token = ''
      loading = true
      load()
    })
  })

  // Anyone waiting is the one thing worth pulling forward, so it is polled: a
  // code read out over the phone should appear here without a refresh.
  $effect(() => {
    void base
    const t = setInterval(() => untrack(load), 10_000)
    return () => clearInterval(t)
  })

  function replace(next: ChannelInfo) {
    channels = channels.map((c) => (c.id === next.id ? next : c))
  }

  async function connect(c: ChannelInfo) {
    const t = token.trim()
    if (!t || busy) return
    busy = c.id
    try {
      replace(await saveChannel(base, c.id, { token: t }))
      token = ''
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = ''
    }
  }

  async function disconnect(c: ChannelInfo) {
    busy = c.id
    try {
      replace(await saveChannel(base, c.id, { token: '' }))
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = ''
    }
  }

  async function setDetail(c: ChannelInfo, detail: boolean) {
    try {
      replace(await saveChannel(base, c.id, { detail }))
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function answer(c: ChannelInfo, code: string, approve: boolean) {
    busy = code
    try {
      replace(await answerChannelRequest(base, c.id, code, approve))
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = ''
    }
  }

  async function revoke(c: ChannelInfo, person: string) {
    busy = person
    try {
      replace(await revokeChannelPerson(base, c.id, person))
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = ''
    }
  }

  const waitingCount = $derived(channels.reduce((n, c) => n + c.waiting.length, 0))
  const who = (p: { name?: string; username?: string; id?: string }) =>
    p.name || (p.username ? '@' + p.username : p.id || 'Someone')
</script>

<div class="backdrop" onclick={() => app.closeChannels()} role="presentation">
<section class="sheet" role="dialog" aria-label="Channels" onclick={(e) => e.stopPropagation()}>
  <header class="top">
    <button class="back" onclick={() => app.closeChannels()} aria-label="Back">
      <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6" /></svg>
    </button>
    <h1>Channels</h1>
    {#if app.machines.length > 1}
      <label class="mpick">
        <select bind:value={machineId} aria-label="Machine">
          {#each app.machines as m (m.id)}
            <option value={m.id}>{m.label}{m.self ? ' · this machine' : ''}</option>
          {/each}
        </select>
        <svg class="mchev" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6" /></svg>
      </label>
    {/if}
  </header>

  <p class="lede">
    Reach {machine ? machine.label : 'this machine'} from somewhere other than this
    app. Your files and command output stay here; a channel carries the
    conversation and the buttons, nothing else.
  </p>

  {#if waitingCount > 0}
    <p class="waitbar">{waitingCount} {waitingCount === 1 ? 'person is' : 'people are'} waiting to be let in.</p>
  {/if}

  {#if error}<p class="state err">{error}</p>{/if}

  {#if loading}
    <div class="list" aria-hidden="true">
      {#each [0, 1] as i (i)}<div class="row"><span class="dot checking"></span><span class="skname"></span></div>{/each}
    </div>
  {:else}
    <div class="list">
      {#each channels as c (c.id)}
        {@const isOpen = open === c.id}
        <div class="ch" class:open={isOpen}>
          <button
            class="row"
            disabled={!c.available}
            onclick={() => (open = isOpen ? '' : c.id)}
            aria-expanded={isOpen}
          >
            <span class="dot" class:on={c.connected} class:hollow={c.available && !c.connected}></span>
            <span class="nm">{c.name}</span>
            {#if c.waiting.length}
              <span class="badge">{c.waiting.length} waiting</span>
            {/if}
            <span class="sub mono">
              {#if !c.available}soon
              {:else if c.connected}{c.people.length} {c.people.length === 1 ? 'person' : 'people'}
              {:else}not connected{/if}
            </span>
            {#if c.available}
              <svg class="chev" class:flip={isOpen} width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6" /></svg>
            {/if}
          </button>

          {#if isOpen && c.available}
            <div class="body">
              <!-- Anyone waiting comes first: it is the only thing here that is
                   blocking a person on the other end. -->
              {#each c.waiting as req (req.code)}
                <div class="req">
                  <span class="code mono">{req.code}</span>
                  <span class="reqwho">
                    <span class="rn">{who(req)}</span>
                    <span class="rs">wants access</span>
                  </span>
                  <button class="ghost" disabled={busy === req.code} onclick={() => answer(c, req.code, false)}>Deny</button>
                  <button class="primary" disabled={busy === req.code} onclick={() => answer(c, req.code, true)}>Approve</button>
                </div>
              {/each}

              {#if !c.has_secret}
                <div class="setup">
                  <p class="hint">{c.help}</p>
                  <div class="tokenrow">
                    <input
                      class="mono"
                      type="password"
                      placeholder="Bot token"
                      bind:value={token}
                      onkeydown={(e) => e.key === 'Enter' && connect(c)}
                    />
                    <button class="primary" disabled={!token.trim() || busy === c.id} onclick={() => connect(c)}>
                      {busy === c.id ? 'Saving…' : 'Connect'}
                    </button>
                  </div>
                  <p class="hint">
                    Then message the bot. It replies with a code, and it appears
                    here for you to approve.
                  </p>
                </div>
              {:else}
                {#if c.people.length}
                  <ul class="people">
                    {#each c.people as p (p.id)}
                      <li>
                        <span class="pn">{who(p)}</span>
                        <span class="pid mono">{p.id}</span>
                        <button class="rm" disabled={busy === p.id} onclick={() => revoke(c, p.id)} aria-label="Remove {who(p)}">
                          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18" /><path d="M8 6V4a2 2 0 012-2h4a2 2 0 012 2v2" /><path d="M6 6l1 14a2 2 0 002 2h6a2 2 0 002-2l1-14" /></svg>
                        </button>
                      </li>
                    {/each}
                  </ul>
                {:else}
                  <p class="hint">
                    Nobody can use it yet. Message the bot and approve the code it
                    gives you.
                  </p>
                {/if}

                <!-- The privacy switch. Stated as what it sends rather than as a
                     feature name, because that is the decision being made. -->
                <label class="detail">
                  <input type="checkbox" checked={c.detail} onchange={(e) => setDetail(c, e.currentTarget.checked)} />
                  <span class="dtext">
                    <span class="dt">Send file contents and command output</span>
                    <span class="dh">
                      Off by default. Leaving it off means {c.name} sees which file
                      was edited, never what is in it.
                    </span>
                  </span>
                </label>

                <button class="unlink" disabled={busy === c.id} onclick={() => disconnect(c)}>
                  Disconnect {c.name}
                </button>
              {/if}
            </div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</section>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 60;
    background: rgba(0, 0, 0, 0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 20px;
  }
  .sheet {
    width: 100%;
    max-width: 520px;
    max-height: min(90dvh, 820px);
    display: flex;
    flex-direction: column;
    background: var(--bg-raised, var(--bg));
    border: 1px solid var(--border-2);
    border-radius: 20px;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    box-shadow: 0 30px 80px -30px rgba(0, 0, 0, 0.8);
    padding: 20px 22px 24px;
  }
  .top {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .back {
    flex: none;
    width: 34px;
    height: 34px;
    margin-left: -6px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 10px;
    color: var(--text-3);
  }
  .back:hover {
    background: var(--panel);
    color: var(--text);
  }
  .top h1 {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: 19px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }
  .mpick {
    flex: none;
    position: relative;
    display: inline-flex;
    align-items: center;
  }
  .mpick select {
    appearance: none;
    -webkit-appearance: none;
    height: 32px;
    padding: 0 28px 0 12px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 100px;
    color: var(--text-2);
    font-size: 12.5px;
    max-width: 150px;
  }
  .mchev {
    position: absolute;
    right: 10px;
    color: var(--text-4);
    pointer-events: none;
  }
  .lede {
    margin: 13px 2px 16px;
    font-size: 13px;
    line-height: 1.6;
    color: var(--text-3);
  }
  /* Someone waiting is a person blocked on you, so it is the one thing here
     allowed to raise its voice. */
  .waitbar {
    margin: 0 0 12px;
    padding: 9px 12px;
    border-radius: 9px;
    background: color-mix(in oklab, var(--busy) 12%, transparent);
    color: var(--busy);
    font-size: 12.5px;
  }

  .list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .ch {
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    background: var(--panel);
    overflow: hidden;
  }
  .ch.open {
    border-color: var(--border-2);
  }
  .row {
    display: flex;
    align-items: center;
    gap: 11px;
    width: 100%;
    text-align: left;
    padding: 14px 15px;
    min-height: 54px;
  }
  .row:disabled {
    cursor: default;
  }
  .row:not(:disabled):hover {
    background: var(--panel-2);
  }
  .dot {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    box-sizing: border-box;
  }
  .dot.on {
    background: var(--live);
    box-shadow: 0 0 0 3px color-mix(in oklab, var(--live) 16%, transparent);
  }
  .dot.hollow {
    border: 1.5px solid var(--text-4);
  }
  .dot.checking {
    background: var(--text-3);
    animation: pulse 1.1s ease-in-out infinite;
  }
  @keyframes pulse {
    0%,
    100% {
      opacity: 0.3;
    }
    50% {
      opacity: 1;
    }
  }
  .nm {
    flex: 1;
    min-width: 0;
    font-size: 14.5px;
    font-weight: 550;
    color: var(--text);
  }
  .row:disabled .nm {
    color: var(--text-3);
  }
  .badge {
    flex: none;
    font-size: 11px;
    padding: 3px 8px;
    border-radius: 100px;
    background: color-mix(in oklab, var(--busy) 16%, transparent);
    color: var(--busy);
  }
  .sub {
    flex: none;
    font-size: 11.5px;
    color: var(--text-4);
  }
  .chev {
    flex: none;
    color: var(--text-4);
    transition: transform 0.15s;
  }
  .chev.flip {
    transform: rotate(180deg);
  }
  .skname {
    height: 11px;
    width: 96px;
    border-radius: 4px;
    background: var(--panel-3);
    animation: pulse 1.1s ease-in-out infinite;
  }

  .body {
    display: flex;
    flex-direction: column;
    gap: 14px;
    padding: 4px 15px 15px;
  }
  /* A pending request leads with its code, because that is what the person on
     the other end is reading out to you. */
  .req {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px;
    padding: 10px 11px;
    border: 1px solid color-mix(in oklab, var(--busy) 35%, var(--border-2));
    border-radius: 11px;
  }
  .code {
    flex: none;
    font-size: 14px;
    font-weight: 600;
    letter-spacing: 0.1em;
    color: var(--busy);
  }
  .reqwho {
    flex: 1 1 7rem;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .rn {
    font-size: 13px;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .rs {
    font-size: 11px;
    color: var(--text-4);
  }

  .setup {
    display: flex;
    flex-direction: column;
    gap: 9px;
  }
  .tokenrow {
    display: flex;
    gap: 8px;
  }
  .tokenrow input {
    flex: 1;
    min-width: 0;
    height: 40px;
    padding: 0 12px;
    background: var(--bg);
    border: 1px solid var(--border-2);
    border-radius: 10px;
    color: var(--text);
    font-size: 13.5px;
  }
  .tokenrow input:focus {
    outline: none;
    border-color: var(--text-4);
  }
  .hint {
    margin: 0;
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-4);
  }

  .people {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
  }
  .people li {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 9px 0;
  }
  .people li + li {
    border-top: 1px solid var(--border);
  }
  .pn {
    flex: 1;
    min-width: 0;
    font-size: 13.5px;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .pid {
    flex: none;
    font-size: 11px;
    color: var(--text-4);
  }
  .rm {
    flex: none;
    width: 28px;
    height: 28px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 8px;
    color: var(--text-4);
  }
  .rm:hover,
  .rm:active {
    color: var(--alert);
    background: var(--panel-2);
  }

  .detail {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding-top: 12px;
    border-top: 1px solid var(--border);
    cursor: pointer;
  }
  .detail input {
    margin-top: 2px;
    accent-color: var(--text-2);
  }
  .dtext {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .dt {
    font-size: 13px;
    color: var(--text);
  }
  .dh {
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-4);
  }

  .ghost,
  .primary,
  .unlink {
    flex: none;
    height: 32px;
    padding: 0 13px;
    border-radius: 9px;
    font-size: 12.5px;
    font-weight: 550;
  }
  .ghost {
    color: var(--text-3);
  }
  .ghost:hover {
    color: var(--text);
    background: var(--panel-2);
  }
  .primary {
    background: var(--text);
    color: var(--bg);
  }
  .primary:disabled {
    opacity: 0.4;
  }
  .unlink {
    align-self: flex-start;
    padding: 0;
    color: var(--text-4);
    font-weight: 500;
  }
  .unlink:hover {
    color: var(--alert);
  }

  .state {
    font-size: 13px;
    color: var(--text-4);
    padding: 4px 2px 10px;
  }
  .state.err {
    color: var(--alert);
  }
</style>
