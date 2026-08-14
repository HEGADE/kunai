<script lang="ts">
  import { toasts } from '../lib/toast.svelte'

  // Where a toast goes, which is the whole design decision.
  //
  // TOP centre, not the usual bottom corner, and that is chosen for this app
  // rather than copied. kunai puts its primary actions at the bottom -- the
  // composer, the review's Post bar -- and an error that does not auto-dismiss
  // would sit permanently on top of the button you press to try again. The top
  // strip holds a title and one ghost button, so nothing there is in the way,
  // and it is where the eye goes when the screen answers you.
  //
  // One of these is mounted per entry point. Everything else raises a toast
  // through the module store and owns no furniture of its own.
</script>

{#if toasts.items.length}
  <div class="deck" role="status" aria-live="polite">
    {#each toasts.items as t (t.id)}
      <div class="toast {t.kind}">
        <p class="text">{t.text}</p>
        {#if t.action}
          <button
            class="act"
            onclick={() => {
              t.action?.run()
              toasts.dismiss(t.id)
            }}
          >
            {t.action.label}
          </button>
        {/if}
        <button class="x" onclick={() => toasts.dismiss(t.id)} aria-label="Dismiss">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M6 6l12 12M18 6L6 18" stroke-linecap="round" />
          </svg>
        </button>
      </div>
    {/each}
  </div>
{/if}

<style>
  .deck {
    position: fixed;
    top: calc(var(--safe-top) + 12px);
    left: 50%;
    transform: translateX(-50%);
    z-index: 200;
    display: flex;
    flex-direction: column;
    gap: 8px;
    width: min(560px, calc(100vw - 24px));
    /* The deck spans the width so the toasts inside can be centred, but it must
       not swallow clicks on the header underneath it. */
    pointer-events: none;
  }
  .toast {
    pointer-events: auto;
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 11px 12px 11px 14px;
    border-radius: var(--r);
    border: 1px solid var(--border-2);
    /* Opaque, not translucent: this sits over a conversation and a message you
       have to read through is not a message. */
    background: var(--panel-2);
    box-shadow: 0 8px 28px rgba(0, 0, 0, 0.45);
    animation: drop 0.18s ease-out;
  }
  @keyframes drop {
    from {
      opacity: 0;
      transform: translateY(-6px);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .toast {
      animation: none;
    }
  }
  /* The severity marker. A left rule rather than a tinted panel, so the words
     stay the brightest thing in it. */
  .toast.error {
    box-shadow: inset 2px 0 0 var(--alert), 0 8px 28px rgba(0, 0, 0, 0.45);
    border-color: color-mix(in srgb, var(--alert) 40%, var(--border-2));
  }
  .toast.done {
    box-shadow: inset 2px 0 0 var(--live), 0 8px 28px rgba(0, 0, 0, 0.45);
  }
  .toast.info {
    box-shadow: inset 2px 0 0 var(--text-4), 0 8px 28px rgba(0, 0, 0, 0.45);
  }
  .text {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: 13px;
    line-height: 1.55;
    color: var(--text);
  }
  .act {
    flex: none;
    padding: 4px 11px;
    border-radius: var(--r-sm);
    border: 1px solid var(--border-2);
    color: var(--text);
    font-size: 12px;
    font-weight: 550;
    white-space: nowrap;
  }
  .act:hover {
    background: var(--panel-3);
  }
  .x {
    flex: none;
    display: grid;
    place-items: center;
    width: 22px;
    height: 22px;
    border-radius: var(--r-sm);
    color: var(--text-4);
  }
  .x:hover {
    color: var(--text);
    background: var(--panel-3);
  }

  @media (max-width: 560px) {
    .deck {
      width: calc(100vw - 20px);
    }
    .text {
      font-size: 12.5px;
    }
    /* A real tap target on the one control that is always there. */
    .x {
      width: 30px;
      height: 30px;
    }
  }
</style>
