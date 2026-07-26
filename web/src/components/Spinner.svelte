<script lang="ts">
  // An inline spinner for a control that is busy with something it started.
  //
  // Distinct from Skeleton, and the difference is who is waiting for what. A
  // skeleton stands in for content that has not arrived, so it takes the content's
  // shape. A spinner belongs on an action you just triggered, where the shape is
  // already on screen and the only question is whether the press did anything.
  //
  // Buttons here said "Starting…" and nothing else. That is enough to read but not
  // enough to watch: a word that changed once looks the same as a word that is
  // stuck, so a slow start and a hung start were indistinguishable. A turning ring
  // is the cheapest way to say the wait is still alive.
  //
  // Drawn with a border rather than an SVG stroke so it inherits currentColor and
  // costs one element. Under prefers-reduced-motion the global rule stops the
  // rotation, which would leave a lopsided static ring, so the reduced case swaps
  // to a pulsing dot instead: still alive, no rotation.
  let { size = 12 }: { size?: number } = $props()
</script>

<span class="sp" style="width:{size}px; height:{size}px" aria-hidden="true"></span>

<style>
  .sp {
    display: inline-block;
    flex: none;
    border-radius: 50%;
    border: 1.5px solid currentColor;
    /* One transparent quadrant is what makes the rotation legible. */
    border-top-color: transparent;
    opacity: 0.85;
    animation: turn 0.7s linear infinite;
  }
  @keyframes turn {
    to {
      transform: rotate(360deg);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .sp {
      border: 0;
      background: currentColor;
      /* Slow enough to be information rather than motion, and opacity only. */
      animation: breathe 1.6s ease-in-out infinite !important;
    }
    @keyframes breathe {
      50% {
        opacity: 0.25;
      }
    }
  }
</style>
