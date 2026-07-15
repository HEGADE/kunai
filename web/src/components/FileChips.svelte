<script lang="ts">
  import type { Attachment } from '../lib/types'

  // What rode along with a message. Metadata only: the bytes went to Claude and
  // are never served back, so this is a record of what was sent, not a preview.
  let { files }: { files: Attachment[] } = $props()

  const isImage = (f: Attachment) => f.media_type.startsWith('image/')

  // A short kind badge, preferring the real extension over the media subtype:
  // "shot.png"/"image/png" -> PNG, "application/pdf" -> PDF.
  function kind(f: Attachment): string {
    const sub = (f.media_type.split('/')[1] ?? '').split('+')[0]
    const ext = f.name.includes('.') ? f.name.split('.').pop()! : ''
    return (ext || sub).toUpperCase().slice(0, 4)
  }
</script>

<div class="files">
  {#each files as f, i (f.id || i)}
    <span class="file" title={f.name}>
      <span class="ic">
        {#if isImage(f)}
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2.5" /><circle cx="8.5" cy="8.5" r="1.5" /><path d="M21 15l-5-5L5 21" /></svg>
        {:else}
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" /><path d="M14 2v6h6" /></svg>
        {/if}
      </span>
      <span class="fn mono">{f.name}</span>
      {#if kind(f)}<span class="kd">{kind(f)}</span>{/if}
    </span>
  {/each}
</div>

<style>
  .files {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  /* Inset against the message bubble: a step darker, so it reads as something
     carried by the message rather than another bubble. */
  .file {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    max-width: 100%;
    padding: 5px 8px 5px 7px;
    background: var(--panel);
    border: 1px solid var(--border-2);
    border-radius: 8px;
    font-size: 12px;
    line-height: 1.2;
    color: var(--text-2);
  }
  .ic {
    flex: none;
    display: flex;
    color: var(--text-4);
  }
  .fn {
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .kd {
    flex: none;
    padding-left: 7px;
    border-left: 1px solid var(--border-2);
    font-size: 9.5px;
    letter-spacing: 0.06em;
    color: var(--text-4);
  }
</style>
