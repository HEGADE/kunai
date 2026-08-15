<script lang="ts">
  import { toasts } from '../lib/toast.svelte'

  // Where a toast goes, which is the whole design decision.
  //
  // TOP centre, not the usual bottom corner, and that is chosen for this app
  // rather than copied. kunai puts its primary actions at the bottom -- the
  // composer, the review's send bar -- and an error that does not auto-dismiss
  // would sit permanently on top of the button you press to try again.
  //
  // BELOW the chrome, not over it. It used to start 12px from the top, which on
  // every screen that has a header put a floating panel across the middle of the
  // one row that holds the title and the actions: it read as a thing that had
  // gone wrong rather than as an answer to what you just did. It now clears the
  // 44px header and sits in the content, where the eye already is.
  //
  // A KIND is a mark, not a shade. The severity used to be a 2px inset shadow
  // down the left edge, which is invisible at a glance and indistinguishable
  // between the three; the answer to "did that work" should be readable without
  // reading. So: an icon, in the one colour that already means this in kunai.
  //
  // And the SHAPE is a title and a quiet second line, because that is what these
  // messages actually are. "Applied to internal/server/shareupload.go:196 (-1
  // +2). Not committed." is two sentences in one line, which in a 560px box is a
  // paragraph floating over somebody's work. What happened goes on top; what it
  // means for you goes underneath, in a colour that says read this second.
  //
  // One of these is mounted per entry point. Everything else raises a toast
  // through the module store and owns no furniture of its own.
</script>

{#if toasts.items.length}
  <div class="deck" role="status" aria-live="polite">
    {#each toasts.items as t (t.id)}
      <div class="toast {t.kind}">
        <span class="mark" aria-hidden="true">
          {#if t.kind === 'error'}
            <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
              <path d="M12 8v5" /><path d="M12 16.5v.01" />
              <path d="M10.3 3.9 2.4 17.6A2 2 0 0 0 4.1 20.6h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" stroke-linejoin="round" />
            </svg>
          {:else if t.kind === 'done'}
            <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M4.5 12.5 9.5 17.5 19.5 6.5" />
            </svg>
          {:else}
            <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
              <path d="M12 11v6" /><path d="M12 7.5v.01" />
            </svg>
          {/if}
        </span>

        <div class="body">
          <p class="text">{t.text}</p>
          {#if t.detail}<p class="detail">{t.detail}</p>{/if}
        </div>

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
        <!-- Only where it is needed. An error waits to be read and has to be
             dismissible; anything else is already leaving, and a close button on
             a message that closes itself is one more thing in the way. -->
        {#if t.kind === 'error'}
          <button class="x" onclick={() => toasts.dismiss(t.id)} aria-label="Dismiss">
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M6 6l12 12M18 6L6 18" stroke-linecap="round" />
            </svg>
          </button>
        {:else}
          <!-- It is going, and it shows how long it has. A message that vanishes
               with no warning reads as a glitch the first time it happens. -->
          <span class="fuse" aria-hidden="true"></span>
        {/if}
      </div>
    {/each}
  </div>
{/if}

<style>
  .deck {
    position: fixed;
    top: calc(var(--safe-top) + 56px);
    left: 50%;
    transform: translateX(-50%);
    z-index: 200;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    width: min(440px, calc(100vw - 24px));
    /* The deck spans the width so the toasts inside can be centred, but it must
       not swallow clicks on what is underneath it. */
    pointer-events: none;
  }
  .toast {
    pointer-events: auto;
    position: relative;
    display: flex;
    align-items: flex-start;
    gap: 11px;
    width: 100%;
    padding: 12px 12px 12px 13px;
    border-radius: 10px;
    /* Its own surface rather than the panel token: this floats over five
       different screens, one of which (the review) has a register of its own,
       and a toast that changes colour with the page underneath it looks like
       part of the page rather than an answer from the app. */
    border: 1px solid #2a2c33;
    background: #14161a;
    box-shadow: 0 10px 34px rgba(0, 0, 0, 0.55), 0 1px 0 rgba(255, 255, 255, 0.03) inset;
    overflow: hidden;
    animation: drop 0.18s cubic-bezier(0.2, 0.8, 0.3, 1);
  }
  @keyframes drop {
    from {
      opacity: 0;
      transform: translateY(-8px);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .toast {
      animation: none;
    }
    .fuse {
      animation: none;
      width: 100%;
    }
  }

  /* The mark. One colour each, and each is a colour that already means this
     elsewhere in kunai, so nothing new has to be learned. */
  .mark {
    display: grid;
    place-items: center;
    width: 22px;
    height: 22px;
    flex: none;
    border-radius: 6px;
    margin-top: 1px;
  }
  .error .mark {
    color: var(--alert);
    background: color-mix(in srgb, var(--alert) 14%, transparent);
  }
  .done .mark {
    color: var(--live);
    background: color-mix(in srgb, var(--live) 14%, transparent);
  }
  .info .mark {
    color: var(--text-3);
    background: rgba(255, 255, 255, 0.06);
  }

  .body {
    flex: 1;
    min-width: 0;
  }
  .text {
    margin: 0;
    font-size: 13px;
    line-height: 1.45;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .detail {
    margin: 3px 0 0;
    font-size: 12px;
    line-height: 1.45;
    color: var(--text-4);
    overflow-wrap: anywhere;
  }

  .act {
    flex: none;
    margin-top: 1px;
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
    margin-top: 1px;
    border-radius: var(--r-sm);
    color: var(--text-4);
  }
  .x:hover {
    color: var(--text);
    background: var(--panel-3);
  }

  /* The dwell, drawn. Matches DWELL_MS in lib/toast.svelte.ts; if that changes
     this has to, which is why the number is named in both places. */
  .fuse {
    position: absolute;
    left: 0;
    bottom: 0;
    height: 2px;
    width: 100%;
    background: color-mix(in srgb, var(--live) 55%, transparent);
    transform-origin: left;
    animation: burn 4.2s linear forwards;
  }
  .info .fuse {
    background: rgba(255, 255, 255, 0.18);
  }
  @keyframes burn {
    to {
      transform: scaleX(0);
    }
  }

  @media (max-width: 560px) {
    .deck {
      width: calc(100vw - 20px);
      top: calc(var(--safe-top) + 52px);
    }
    /* A real tap target on the one control that is ever there. */
    .x {
      width: 30px;
      height: 30px;
    }
  }
</style>
