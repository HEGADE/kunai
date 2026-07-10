<script lang="ts">
  let { size = 22 }: { size?: number } = $props()
</script>

<span class="wordmark" style="--s:{size}px">
  <svg viewBox="0 0 24 40" width={size * 0.55} height={size} aria-hidden="true">
    <defs>
      <linearGradient id="blade" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stop-color="var(--wire-bright)" />
        <stop offset="1" stop-color="var(--wire)" />
      </linearGradient>
    </defs>
    <path d="M12 2 L18.5 15 L12 26 L5.5 15 Z" fill="url(#blade)" />
    <rect x="10.8" y="23" width="2.4" height="6" fill="var(--wire)" />
    <circle cx="12" cy="33" r="4.2" fill="none" stroke="var(--wire)" stroke-width="2.4" />
  </svg>
  <b>kunai</b>
</span>

<style>
  .wordmark {
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }
  b {
    font-family: var(--mono);
    font-weight: 600;
    font-size: calc(var(--s) * 0.8);
    letter-spacing: 0.04em;
    color: var(--ink);
  }
</style>
