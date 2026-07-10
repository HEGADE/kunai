<script lang="ts">
  let { size = 15 }: { size?: number } = $props()
</script>

<span class="wm" style="--s:{size}px">
  <svg viewBox="0 0 24 40" width={size * 0.42} height={size * 0.8} aria-hidden="true">
    <path d="M12 2 L18 15 L12 25 L6 15 Z" fill="currentColor" />
    <rect x="11" y="23" width="2" height="6" fill="currentColor" />
    <circle cx="12" cy="33" r="3.6" fill="none" stroke="currentColor" stroke-width="2" />
  </svg>
  <b>kunai</b>
</span>

<style>
  .wm {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    color: var(--text);
  }
  b {
    font-weight: 600;
    font-size: var(--s);
    letter-spacing: -0.01em;
  }
</style>
