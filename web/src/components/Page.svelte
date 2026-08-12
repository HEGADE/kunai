<script lang="ts">
  import type { Snippet } from 'svelte'
  import { app } from '../lib/app.svelte'

  // The chrome every place shares.
  //
  // Settings, Accounts, Providers and Channels were modals; Usage was already a
  // route and had this header hand-written. Five copies of one header is how
  // they drift, so it lives here once: the back button, the title, the optional
  // subtitle that names what you are looking at, and a slot for the one or two
  // actions that belong to the whole page.
  //
  // The back button is not decoration on a phone. `data-full` hides the sidebar
  // when a place is open, so without it there is no way out of Settings except
  // the browser's own back, which a home-screen PWA does not show.
  let {
    title,
    sub = '',
    actions,
    children,
  }: {
    title: string
    sub?: string
    actions?: Snippet
    children: Snippet
  } = $props()
</script>

<section class="page" aria-label={title}>
  <header class="top">
    <button class="back" onclick={() => app.closeView()} aria-label="Back" title="Back">
      <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6" /></svg>
    </button>
    <div class="ttl">
      <h1>{title}</h1>
      {#if sub}<p class="sub">{sub}</p>{/if}
    </div>
    {#if actions}
      <div class="acts">{@render actions()}</div>
    {/if}
  </header>
  <div class="body">{@render children()}</div>
</section>

<style>
  .page {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }
  /* Chrome over a scrolling canvas, so it takes the same hairline treatment the
     chat header does: no band, no fill, just a seam. */
  .top {
    flex: none;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 16px 11px;
    padding-top: max(10px, var(--safe-top));
    border-bottom: 1px solid var(--border);
  }
  .back {
    flex: none;
    width: 34px;
    height: 34px;
    margin-left: -6px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 10px;
    color: var(--text-3);
  }
  .back:hover {
    background: var(--panel);
    color: var(--text);
  }
  .ttl {
    flex: 1;
    min-width: 0;
  }
  .ttl h1 {
    margin: 0;
    font-size: 16.5px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }
  .sub {
    margin: 1px 0 0;
    font-size: 11.5px;
    color: var(--text-4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .acts {
    flex: none;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }
</style>
