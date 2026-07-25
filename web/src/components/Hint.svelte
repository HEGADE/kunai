<script lang="ts">
  // A hover explanation for a control whose icon cannot carry its own meaning.
  //
  // Not the title attribute, for two reasons this project has already been bitten
  // by: a title is a cursor affordance, so a phone never sees it, and it renders
  // as an unstyled system tooltip after a delay long enough that nobody waits.
  // Not a CSS popover either: these hang off buttons inside the sidebar's
  // scrolling list, and anything positioned within it is clipped by the scroll
  // container. So the bubble is fixed-position, measured from the trigger.
  //
  // The wrapper is display:contents, so wrapping a control does not change the
  // layout it was already in.
  import type { Snippet } from 'svelte'

  let {
    title = '',
    body = '',
    children,
  }: { title?: string; body?: string; children: Snippet } = $props()

  let host = $state<HTMLElement | null>(null)
  let at = $state<{ top: number; left: number } | null>(null)

  // gap is the space between the control and the bubble, and margin keeps the
  // bubble off the window edges when a control sits near one.
  const gap = 8
  const margin = 10
  const width = 260

  function show() {
    const el = host?.firstElementChild as HTMLElement | null
    if (!el) return
    const r = el.getBoundingClientRect()
    // Below by default; above when there is no room below, which is where these
    // controls usually are (a sidebar heading near the bottom of a phone screen).
    const below = r.bottom + gap
    const room = window.innerHeight - below
    at = {
      top: room > 120 ? below : Math.max(margin, r.top - gap - 120),
      left: Math.min(
        Math.max(margin, r.left + r.width / 2 - width / 2),
        window.innerWidth - width - margin,
      ),
    }
  }
  const hide = () => (at = null)
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<span
  class="host"
  bind:this={host}
  onmouseenter={show}
  onmouseleave={hide}
  onfocusin={show}
  onfocusout={hide}
>
  {@render children()}
</span>

{#if at}
  <div class="hint" role="tooltip" style="top:{at.top}px; left:{at.left}px; width:{width}px">
    {#if title}<p class="ht">{title}</p>{/if}
    {#if body}<p class="hb">{body}</p>{/if}
  </div>
{/if}

<style>
  .host {
    display: contents;
  }
  .hint {
    position: fixed;
    /* Explicit, because these hang off controls that sit in mono rows and would
       otherwise inherit it. This is prose: prose explains, mono states. */
    font-family: var(--sans);
    z-index: 60;
    padding: 9px 11px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r-sm);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
    pointer-events: none; /* never steals the hover it is explaining */
  }
  .ht {
    margin: 0 0 3px;
    font-size: 12px;
    color: var(--text);
  }
  .hb {
    margin: 0;
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-3);
  }
</style>
