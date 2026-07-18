<script lang="ts">
  let { size = 15 }: { size?: number } = $props()
</script>

<!-- The same kunai as the app icon (angled blade, ring at the pommel), drawn
     monochrome so it sits in the near-mono UI while reading as one brand. -->
<span class="wm" style="--s:{size}px">
  <svg class="mk" viewBox="0 0 64 64" width={size * 1.15} height={size * 1.15} aria-hidden="true">
    <g transform="rotate(32 32 32)">
      <circle cx="32" cy="12" r="6.2" fill="none" stroke="currentColor" stroke-width="3.4" />
      <rect x="29.4" y="16.6" width="5.2" height="12.4" rx="1" fill="currentColor" />
      <path d="M23.6 29 L40.4 29 L37.2 32.2 L26.8 32.2 Z" fill="currentColor" />
      <path d="M32 31 L24.6 35.4 L32 60 L39.4 35.4 Z" fill="currentColor" />
    </g>
  </svg>
  <b>kunai</b>
</span>

<style>
  .wm {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    color: var(--text);
  }
  .mk {
    flex: none;
    margin-left: -2px;
  }
  b {
    font-weight: 600;
    font-size: var(--s);
    letter-spacing: -0.01em;
  }
</style>
