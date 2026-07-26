<script lang="ts">
  // A placeholder for content that is genuinely being fetched for the first time.
  //
  // The point of pairing this with the query cache is how rarely it should appear.
  // Before the cache, opening the worktree dialog always meant reading "reading
  // branches…" while a request went out for a branch list that had not changed
  // since the last time you opened it. Now the cached value paints immediately and
  // this shows only on a true first load, which is the only case where a
  // placeholder is honest.
  //
  // It shows the SHAPE of what is coming -- rows of the right height, in the right
  // number -- rather than a spinner, because the shape is information: you can see
  // that a list is arriving and roughly how long it will be, so the layout does not
  // jump when it lands.
  //
  // The shimmer is a background sweep at a few percent white, in keeping with the
  // near-monochrome theme, and the global prefers-reduced-motion rule flattens it
  // to a static block.
  let {
    rows = 3,
    height = 14,
    gap = 8,
    // width lets a row end short of the full width, so a list of placeholders does
    // not read as a solid slab. Given as percentages, cycled across the rows.
    widths = [92, 74, 84],
    label = 'Loading',
  }: {
    rows?: number
    height?: number
    gap?: number
    widths?: number[]
    label?: string
  } = $props()
</script>

<div class="sk" style="gap:{gap}px" role="status" aria-label={label} aria-busy="true">
  {#each Array(rows) as _, i (i)}
    <div class="row" style="height:{height}px; width:{widths[i % widths.length]}%"></div>
  {/each}
</div>

<style>
  .sk {
    display: flex;
    flex-direction: column;
    width: 100%;
  }
  .row {
    border-radius: var(--r-sm);
    /* Two layers: a flat base so there is always something visible, and a sweep
       over it. The sweep is what says "arriving" rather than "empty".
       The base is --border, not --panel. --panel measures 1.06:1 against this
       app's background, which is to say invisible: the first version of this
       rendered eight placeholder rows that could not be seen at all, and only a
       screenshot caught it. --border is ~1.5:1, which reads as a block without
       competing with real content at 16.5:1. */
    /* Separate properties, not the `background` shorthand. A colour is only legal
       in the shorthand's FINAL layer, so `background: <gradient> <colour>, <colour>`
       is invalid and the browser drops the whole declaration -- which is how the
       first version of this rendered eight rows with no background at all,
       measurably transparent and invisible on screen. */
    background-color: var(--border);
    background-image: linear-gradient(
      90deg,
      transparent 0%,
      rgba(255, 255, 255, 0.07) 50%,
      transparent 100%
    );
    background-size: 220% 100%;
    background-repeat: no-repeat;
    animation: sweep 1.25s var(--ease) infinite;
  }
  /* Stagger, so the rows read as one object arriving rather than several blinking
     in lockstep. */
  .row:nth-child(2) {
    animation-delay: 0.09s;
  }
  .row:nth-child(3) {
    animation-delay: 0.18s;
  }
  .row:nth-child(n + 4) {
    animation-delay: 0.27s;
  }
  @keyframes sweep {
    from {
      background-position: 160% 0;
    }
    to {
      background-position: -60% 0;
    }
  }
  /* With motion off the sweep would be a frozen gradient sitting mid-row, which
     reads as a rendering fault. A flat block is the honest still version. */
  @media (prefers-reduced-motion: reduce) {
    .row {
      background-image: none;
      animation: none;
    }
  }
</style>
