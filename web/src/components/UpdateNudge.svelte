<script lang="ts">
  // One machine's "there is a newer build" row, for the sidebar.
  //
  // It lives above the configuration nav rather than on the home screen because
  // the home screen is a place you go, and this is something you should see
  // wherever you happen to be. The sidebar is the only chrome that is always
  // there, so it is the only place a nudge can be one without becoming a nag: it
  // appears when there is an update, and it is absent the rest of the time.
  //
  // Compact on purpose. The sidebar is 288px and this sits directly above the
  // four destinations, so it has to read at a glance and never push them off the
  // bottom: a label and an action on one line, the versions on a second.
  import { app } from '../lib/app.svelte'
  import type { Machine } from '../lib/types'

  let { machine, named = false }: { machine: Machine; named?: boolean } = $props()

  const busy = $derived(!!app.updating[machine.id])
  const err = $derived(app.updateError[machine.id] ?? '')
  // -1 means "started, nothing reported yet"; 1 means the binary is in place and
  // the service is coming back.
  const progress = $derived(app.updateProgress[machine.id] ?? -1)

  const label = $derived.by(() => {
    if (!busy) return err ? 'Retry' : 'Update'
    if (progress >= 1) return 'Restarting'
    if (progress >= 0) return `${Math.round(progress * 100)}%`
    return '…'
  })

  const from = $derived(machine.stats?.kunai_version ?? '')
  const to = $derived(app.latestVersion ?? '')
</script>

<div class="nudge" class:busy>
  <div class="top">
    <span class="dot" class:on={!busy}></span>
    <span class="head">{named ? machine.label : 'Update available'}</span>
    <button class="btn" disabled={busy} onclick={() => app.updateMachine(machine.id)}>
      {label}
    </button>
  </div>

  {#if err}
    <!-- Kept in the row rather than raised as a toast: the retry button is here,
         so the reason it failed belongs next to it. -->
    <span class="sub mono err" title={err}>{err}</span>
  {:else}
    <span class="sub mono" title="{from} → {to}">{from} → {to}</span>
  {/if}

  <!-- What the update costs. "sessions resume" is the load-bearing half: this
       restarts the service, and somebody with an agent mid-task needs to know
       that is survivable before they tap it. The machine is named only when the
       heading is not already naming it, so a fleet of three does not print the
       same hostname six times. -->
  <span class="cost">restarts {named ? 'it' : machine.label}, sessions resume</span>

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
  /* The one status dot here. It stops pulsing once the update is running, because
     at that point the thing it was asking for is happening. */
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
  /* Both lines are the same tier and ellipsise rather than wrap: a version string
     is long, the sidebar is narrow, and a nudge that grows to three lines starts
     competing with the sessions it sits above. The full text is in the title. */
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
