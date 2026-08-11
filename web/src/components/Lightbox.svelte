<script lang="ts">
  import { lightbox } from '../lib/lightbox.svelte'
  import { fileNameFor, saveImage } from '../lib/imageActions'

  // Full-size view of one picture. Mounted once for the whole app; see
  // lib/lightbox.svelte.ts for why it is not per-message state.

  let saving = $state(false)
  const shown = $derived(lightbox.current)

  async function save() {
    if (!shown || saving) return
    saving = true
    try {
      await saveImage(shown.src, fileNameFor(shown.src, shown.alt))
    } finally {
      saving = false
    }
  }

  // Escape closes, which is the one keyboard convention an overlay must honour.
  // Bound on the window rather than the panel so it works before anything inside
  // has been focused.
  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape' && lightbox.open) {
      e.stopPropagation()
      lightbox.close()
    }
  }
</script>

<svelte:window onkeydown={onKey} />

{#if shown}
  <!-- The backdrop closes on click; the panel stops the click so a drag that
       ends on the image does not dismiss the thing you were looking at. -->
  <div
    class="back"
    onclick={() => lightbox.close()}
    role="presentation"
    aria-hidden="true"
  ></div>
  <div class="wrap" role="dialog" aria-modal="true" aria-label={shown.alt || 'Image'}>
    <div class="bar">
      {#if shown.alt}<span class="cap">{shown.alt}</span>{/if}
      <span class="sp"></span>
      <button class="act" onclick={save} disabled={saving} title="Download this image">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12" /><path d="M7 11l5 5 5-5" /><path d="M4 20h16" /></svg>
        <span>{saving ? 'Saving…' : 'Download'}</span>
      </button>
      <button class="act icon" onclick={() => lightbox.close()} title="Close (Esc)" aria-label="Close">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M5 5l14 14M19 5L5 19" /></svg>
      </button>
    </div>
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <img class="full" src={shown.src} alt={shown.alt} />
  </div>
{/if}

<style>
  /* Nearly opaque, and blurred. At 0.82 the session header showed through hard
     enough that its buttons sat inches from this one's, and two toolbars arguing
     reads as a rendering fault rather than as a layer. What is behind an overlay
     should be gone, not dimmed. */
  .back {
    position: fixed;
    inset: 0;
    z-index: 200;
    background: rgba(0, 0, 0, 0.94);
    backdrop-filter: blur(6px);
  }
  .wrap {
    position: fixed;
    inset: 0;
    z-index: 201;
    display: flex;
    flex-direction: column;
    padding: calc(10px + var(--safe-top, 0px)) 14px calc(14px + var(--safe-bottom, 0px));
    pointer-events: none;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 34px;
    pointer-events: auto;
  }
  .cap {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 12.5px;
    color: var(--text-3);
  }
  .sp {
    flex: 1;
  }
  .act {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    flex: none;
    padding: 6px 11px;
    border-radius: 100px;
    border: 1px solid var(--border);
    background: var(--panel);
    color: var(--text-2);
    font-size: 12.5px;
  }
  .act.icon {
    padding: 6px;
  }
  .act:hover:not(:disabled) {
    color: var(--text);
    border-color: var(--border-2);
  }
  .act:disabled {
    opacity: 0.6;
  }
  /* The picture takes whatever the bar leaves, and never more than its own size:
     scaling a 1254px image up to a 4K window makes it soft for no reason. */
  .full {
    flex: 1;
    min-height: 0;
    margin-top: 10px;
    object-fit: contain;
    align-self: center;
    max-width: 100%;
    pointer-events: auto;
    border-radius: var(--r-sm);
  }
</style>
