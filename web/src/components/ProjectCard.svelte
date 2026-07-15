<script lang="ts">
  import type { ProjectInfo } from '../lib/types'

  // A codebase joining the session. This is a dossier, not a message: what the
  // model was told about the project, in the app's data voice. Nothing here was
  // read — Claude reaches the files by the path when it needs them.
  let { project }: { project: ProjectInfo } = $props()

  const langs = $derived((project.langs ?? []).slice(0, 4))
  const facts = $derived(
    [
      project.branch,
      project.files ? `${project.files} files` : '',
      (project.docs ?? []).join(' '),
      (project.build ?? []).join(' '),
    ].filter(Boolean),
  )
</script>

<div class="card">
  <div class="top">
    <span class="eyebrow">Project added</span>
    <span class="name mono">{project.name}</span>
  </div>
  <span class="path mono">{project.path}</span>

  {#if langs.length}
    <div class="langs">
      {#each langs as l (l.name)}
        <span class="lang mono"><span class="ln">{l.name}</span><span class="lc">{l.files}</span></span>
      {/each}
    </div>
  {/if}

  {#if facts.length}
    <span class="facts mono">{facts.join('  ·  ')}</span>
  {/if}
  <span class="note">Claude has the layout and the path. It reads the files when it needs them.</span>
</div>

<style>
  /* Set apart from the conversation: this is the session being handed something,
     not somebody speaking. */
  .card {
    display: flex;
    flex-direction: column;
    gap: 7px;
    padding: 12px 14px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-left: 2px solid var(--border-2);
    border-radius: var(--r-sm);
  }
  .top {
    display: flex;
    align-items: baseline;
    gap: 9px;
  }
  .eyebrow {
    font-size: 9.5px;
    letter-spacing: 0.11em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  .name {
    font-size: 13px;
    color: var(--text);
  }
  .path {
    font-size: 11.5px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    unicode-bidi: plaintext;
    text-align: left;
  }
  .langs {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
    padding-top: 1px;
  }
  .lang {
    display: inline-flex;
    align-items: baseline;
    gap: 6px;
    padding: 2px 7px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: 5px;
    font-size: 11px;
  }
  .ln {
    color: var(--text-2);
  }
  .lc {
    color: var(--text-4);
  }
  .facts {
    font-size: 11px;
    color: var(--text-4);
  }
  .note {
    font-size: 11.5px;
    color: var(--text-3);
  }
</style>
