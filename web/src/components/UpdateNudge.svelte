<script lang="ts">
  // "There is a newer build", for the sidebar.
  //
  // ONE row, however many machines are behind. That is not a space saving, it is
  // the truth: every machine tracks the same channel, so they are all going to
  // the same version, and there is only ever one update to decide about. A card
  // per machine printed the same target twice, the same warning twice, and stood
  // up two competing primary buttons for a single decision.
  //
  // It lives above the configuration nav because the sidebar is the only chrome
  // that is always on screen, and it is absent entirely when everything is
  // current, so it nudges rather than nags.
  import { app } from '../lib/app.svelte'
  import type { Machine } from '../lib/types'

  let { machines }: { machines: Machine[] } = $props()

  const many = $derived(machines.length > 1)
  const to = $derived(app.latestVersion ?? '')
  // With one machine the jump is worth stating in full. With several the "from"
  // versions can differ and the useful fact is where they all end up.
  const from = $derived(machines[0]?.stats?.kunai_version ?? '')

  const busyOnes = $derived(machines.filter((m) => app.updating[m.id]))
  const failed = $derived(machines.filter((m) => app.updateError[m.id]))
  const busy = $derived(busyOnes.length > 0)

  const label = $derived.by(() => {
    if (busy) {
      if (many) return `${machines.length - busyOnes.length}/${machines.length}`
      const p = app.updateProgress[machines[0].id] ?? -1
      if (p >= 1) return 'Restarting'
      if (p >= 0) return `${Math.round(p * 100)}%`
      return '…'
    }
    if (failed.length) return 'Retry'
    return many ? 'Update all' : 'Update'
  })

  // One machine is named; several are counted.
  //
  // Listing hostnames looked better until a real one ran long enough to push
  // "restarts, sessions resume" off the end of the line, and between the two the
  // consequence has to win: a name is nice to know, a service restart is
  // something you agree to. The names stay in the title.
  const who = $derived(many ? `${machines.length} machines` : (machines[0]?.label ?? ''))
  const names = $derived(machines.map((m) => m.label).join(', '))

  // One bar for the whole fleet, so the row keeps its height whatever is running.
  const progress = $derived.by(() => {
    if (!busy) return -1
    const total = machines.reduce((sum, m) => {
      const p = app.updateProgress[m.id] ?? 0
      return sum + (p < 0 ? 0 : Math.min(1, p))
    }, 0)
    return total / machines.length
  })

  function run() {
    for (const m of machines) {
      if (!app.updating[m.id]) app.updateMachine(m.id)
    }
  }
</script>

<div class="nudge" class:busy>
  <div class="top">
    <span class="dot" class:on={!busy}></span>
    <span class="head">Update available</span>
    <button class="btn" disabled={busy} onclick={run}>{label}</button>
  </div>

  {#if failed.length}
    <span class="sub mono err" title={app.updateError[failed[0].id]}>
      {failed.length === 1 ? failed[0].label : `${failed.length} machines`}: {app.updateError[failed[0].id]}
    </span>
  {:else if many}
    <span class="sub mono" title={to}>{to}</span>
  {:else}
    <span class="sub mono" title="{from} → {to}">{from} → {to}</span>
  {/if}

  <!-- The cost, said once. It restarts the service, and somebody with an agent
       mid-task needs to know that is survivable before they tap it. -->
  <span class="cost" title={names}>{who} · restarts, sessions resume</span>

  {#if busy && progress >= 0 && progress < 1}
    <div class="bar"><div class="fill" style="width: {Math.round(progress * 100)}%"></div></div>
  {/if}
</div>

<style>
  .nudge {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 9px 10px 10px;
    border: 1px solid var(--border);
    border-radius: var(--r);
    background: var(--panel);
  }
  .nudge.busy {
    border-color: var(--border-2);
  }
  .top {
    display: flex;
    align-items: center;
    gap: 7px;
  }
  .head {
    flex: 1;
    min-width: 0;
    font-size: 12px;
    font-weight: 500;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .dot {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-4);
  }
  .dot.on {
    background: var(--busy);
  }
  .btn {
    flex: none;
    height: 24px;
    padding: 0 10px;
    border: 0;
    border-radius: 999px;
    background: var(--white);
    color: #0b0b0c;
    font-size: 11.5px;
    font-weight: 500;
    cursor: pointer;
  }
  .btn:disabled {
    background: var(--panel-3);
    color: var(--text-3);
    cursor: default;
  }
  /* Both lines ellipsise rather than wrap. A hostname can be long and the sidebar
     is 288px; a nudge that grows to five lines starts competing with the sessions
     it sits above. The full text is in the title. */
  .sub,
  .cost {
    font-size: 10.5px;
    line-height: 1.45;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .err {
    color: var(--alert);
  }
  .bar {
    height: 2px;
    margin-top: 5px;
    border-radius: 2px;
    background: var(--panel-3);
    overflow: hidden;
  }
  .fill {
    height: 100%;
    background: var(--text-3);
    transition: width var(--t-mid, 0.16s) ease;
  }
</style>
