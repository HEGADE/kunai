<script lang="ts">
  import type { SessionStatus } from '../lib/sessionStatus'

  // One badge, wherever a session's state is shown. The sidebar row and the end
  // of a turn use the same component for the same reason they use the same
  // resolver: two places rendering one fact will drift, and this one is meant to
  // be recognised at a glance rather than read twice.
  let { status }: { status: SessionStatus } = $props()
</script>

<span class="badge" data-kind={status.kind}>{status.label}</span>

<style>
  .badge {
    flex: none;
    padding: 1px 5px;
    border-radius: 5px;
    font-size: 10px;
    font-weight: 500;
    letter-spacing: 0.02em;
    line-height: 1.5;
    white-space: nowrap;
    color: var(--text-3);
    background: var(--panel-3);
  }
  /* Tinted, not filled, so a column of these reads as a set of states rather
     than a row of alerts. "Asking" is the exception: it is the only one you have
     to act on, so it is the only one allowed to shout. */
  .badge[data-kind='done'] {
    color: var(--live);
    background: color-mix(in srgb, var(--live) 16%, transparent);
  }
  .badge[data-kind='running'] {
    color: var(--busy);
    background: color-mix(in srgb, var(--busy) 16%, transparent);
    animation: soften 1.8s ease-in-out infinite;
  }
  .badge[data-kind='needs'] {
    color: var(--busy);
    background: color-mix(in srgb, var(--busy) 34%, transparent);
    font-weight: 600;
  }
  .badge[data-kind='error'] {
    color: var(--alert);
    background: color-mix(in srgb, var(--alert) 16%, transparent);
  }
  .badge[data-kind='offline'] {
    color: var(--text-3);
    background: var(--panel-3);
  }
  @keyframes soften {
    50% {
      opacity: 0.55;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .badge[data-kind='running'] {
      animation: none;
    }
  }
</style>
