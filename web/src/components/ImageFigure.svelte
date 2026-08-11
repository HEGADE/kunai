<script lang="ts">
  import { lightbox } from '../lib/lightbox.svelte'
  import { fileNameFor, saveImage } from '../lib/imageActions'

  // A picture, framed, with the actions a picture needs.
  //
  // The component half of the same thing withImageFrames builds in
  // Markdown.svelte. Two implementations exist because the two places genuinely
  // differ: an image inside an assistant reply arrives as sanitized HTML in an
  // {@html} block, where there is no component to mount and the frame has to be
  // assembled from DOM nodes; an image that came back from a tool is ordinary
  // markup and deserves ordinary Svelte. They share the class names, the styles
  // (src/image-frame.css), the viewer store and the save helper, so the only
  // thing not shared is the mechanics of getting the elements onto the page.

  let {
    src,
    alt = '',
    caption = '',
  }: { src: string; alt?: string; caption?: string } = $props()

  let broken = $state(false)
  let saving = $state(false)

  // Reset when pointed at a different file, or a frame that failed once would
  // stay marked broken after the agent rewrote the image at the same card.
  $effect(() => {
    void src
    broken = false
  })

  async function save() {
    if (saving) return
    saving = true
    try {
      await saveImage(src, fileNameFor(src, alt))
    } finally {
      saving = false
    }
  }
</script>

<figure class="imgfr" data-broken={broken ? '' : undefined}>
  <div class="imgstage">
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <img
      {src}
      {alt}
      loading="lazy"
      onerror={() => (broken = true)}
      onclick={() => lightbox.show(src, alt)}
    />
    <div class="imgacts">
      <button
        class="imgbtn"
        type="button"
        title="View full size"
        aria-label="View full size"
        onclick={() => lightbox.show(src, alt)}
      >
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 3H3v6" /><path d="M15 21h6v-6" /><path d="M3 3l7 7" /><path d="M21 21l-7-7" /></svg>
      </button>
      <button
        class="imgbtn"
        type="button"
        title="Download image"
        aria-label="Download image"
        data-busy={saving ? '' : undefined}
        onclick={save}
      >
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12" /><path d="M7 11l5 5 5-5" /><path d="M4 20h16" /></svg>
      </button>
    </div>
  </div>
  {#if caption}<figcaption class="imgcap mono">{caption}</figcaption>{/if}
</figure>
