<script lang="ts">
  import { iconFor } from '../../lib/toolMeta'
  let { name, size = 14 }: { name: string; size?: number } = $props()
  const key = $derived(iconFor(name))
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width="1.7"
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
>
  {#if key === 'terminal'}
    <path d="M4 5h16a1 1 0 011 1v12a1 1 0 01-1 1H4a1 1 0 01-1-1V6a1 1 0 011-1z" />
    <path d="M7 9l3 3-3 3M13 15h4" />
  {:else if key === 'edit'}
    <path d="M12 20h9" />
    <path d="M16.5 3.5a2.1 2.1 0 013 3L7 19l-4 1 1-4z" />
  {:else if key === 'write'}
    <path d="M14 3H6a2 2 0 00-2 2v14a2 2 0 002 2h12a2 2 0 002-2V9z" />
    <path d="M14 3v6h6M12 12v6M9 15h6" />
  {:else if key === 'read'}
    <path d="M14 3H6a2 2 0 00-2 2v14a2 2 0 002 2h12a2 2 0 002-2V9z" />
    <path d="M14 3v6h6M8 13h8M8 17h5" />
  {:else if key === 'search'}
    <circle cx="11" cy="11" r="7" />
    <path d="M21 21l-4.3-4.3" />
  {:else if key === 'glob'}
    <path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" />
    <path d="M11 13l1.5 1.5L15 12" />
  {:else if key === 'todo'}
    <path d="M9 11l3 3L22 4" />
    <path d="M21 12v7a2 2 0 01-2 2H5a2 2 0 01-2-2V5a2 2 0 012-2h11" />
  {:else if key === 'web'}
    <circle cx="12" cy="12" r="9" />
    <path d="M3 12h18M12 3a15 15 0 010 18M12 3a15 15 0 000 18" />
  {:else if key === 'task'}
    <path d="M12 2l9 5-9 5-9-5z" />
    <path d="M3 12l9 5 9-5M3 17l9 5 9-5" />
  {:else if key === 'ask'}
    <path d="M21 11.5a8.38 8.38 0 01-.9 3.8 8.5 8.5 0 01-7.6 4.7 8.38 8.38 0 01-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 01-.9-3.8 8.5 8.5 0 014.7-7.6 8.38 8.38 0 013.8-.9h.5a8.48 8.48 0 018 8v.5z" />
    <path d="M9.6 9.5a2.4 2.4 0 014.7.7c0 1.6-2.4 2.4-2.4 2.4M12 16h.01" />
  {:else if key === 'skill'}
    <path d="M12 3l1.9 4.9L19 9.6l-4 3.4 1.2 5.2L12 15.6 7.8 18.2 9 13 5 9.6l5.1-1.7z" />
  {:else if key === 'plan'}
    <rect x="5" y="4" width="14" height="17" rx="2" />
    <path d="M9 3h6v3H9zM9 11l1.5 1.5L13 10M9 16h5" />
  {:else if key === 'clock'}
    <circle cx="12" cy="12" r="9" />
    <path d="M12 7v5l3 2" />
  {:else}
    <rect x="4" y="4" width="16" height="16" rx="2.5" />
    <path d="M9 9h6v6H9z" />
  {/if}
</svg>
