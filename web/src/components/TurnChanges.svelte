<script lang="ts">
  import { SvelteSet } from 'svelte/reactivity'
  import type { Turn } from '../lib/turns'
  import { fileEditsOf } from '../lib/toolMeta'
  import { langFromPath } from '../lib/highlight'
  import DiffView from './tools/DiffView.svelte'
  import CodeView from './tools/CodeView.svelte'

  // What this one query changed: a compact card of the files its Edit/Write/
  // MultiEdit calls touched, each expandable to its diff. Fed entirely from the
  // turn's tool inputs (fileEditsOf), so it is per-query, needs no git, and stays
  // correct after the work is committed — the diffs live in the conversation.
  let { turn }: { turn: Turn } = $props()

  const files = $derived(fileEditsOf(turn.blocks))
  const added = $derived(files.reduce((n, f) => n + f.added, 0))
  const removed = $derived(files.reduce((n, f) => n + f.removed, 0))

  const open = new SvelteSet<string>()
  function toggle(path: string) {
    if (open.has(path)) open.delete(path)
    else open.add(path)
  }
</script>

{#if files.length}
  <div class="tchanges">
    <div class="chead">
      <span class="eyebrow">Changed files</span>
      <span class="n mono">{files.length}</span>
      <span class="counts mono">
        {#if added > 0}<span class="add">+{added}</span>{/if}
        {#if removed > 0}<span class="del">−{removed}</span>{/if}
      </span>
    </div>
    <div class="list">
      {#each files as f (f.path)}
        <button class="frow" class:open={open.has(f.path)} onclick={() => toggle(f.path)}>
          <svg class="chev" class:down={open.has(f.path)} width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6" /></svg>
          <span class="nm">{f.name}</span>
          <span class="dir mono">{f.path.replace(/[^/]*$/, '').replace(/\/$/, '')}</span>
          <span class="sp"></span>
          <span class="counts mono">
            {#if f.added > 0}<span class="add">+{f.added}</span>{/if}
            {#if f.removed > 0}<span class="del">−{f.removed}</span>{/if}
          </span>
        </button>
        {#if open.has(f.path)}
          <div class="diffs">
            {#each f.ops as op, k (k)}
              {#if op.kind === 'edit'}
                <DiffView oldStr={op.oldStr} newStr={op.newStr} replaceAll={op.replaceAll} />
              {:else}
                <CodeView code={op.content} lang={langFromPath(f.path)} />
              {/if}
            {/each}
          </div>
        {/if}
      {/each}
    </div>
  </div>
{/if}

<style>
  /* A light card, sibling to a tool card: the review of exactly what this reply
     changed, sitting under the reply that changed it. */
  .tchanges {
    margin: 4px 0 2px;
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg-raised);
    overflow: hidden;
  }
  .chead {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 8px 11px;
    border-bottom: 1px solid var(--border);
  }
  .eyebrow {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .n {
    font-size: 12px;
    color: var(--text-3);
  }
  .list {
    display: flex;
    flex-direction: column;
    padding: 5px 6px 6px;
  }
  .frow {
    display: flex;
    align-items: baseline;
    gap: 8px;
    width: 100%;
    text-align: left;
    padding: 5px 7px;
    border-radius: var(--r-sm);
    font-size: 13px;
    color: var(--text-2);
  }
  .frow:hover,
  .frow.open {
    background: var(--panel);
    color: var(--text);
  }
  .chev {
    flex: none;
    align-self: center;
    color: var(--text-4);
    transition: transform 0.12s ease;
  }
  .chev.down {
    transform: rotate(90deg);
  }
  .nm {
    flex: none;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 60%;
  }
  .dir {
    flex: 1;
    min-width: 0;
    font-size: 11px;
    color: var(--text-4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    unicode-bidi: plaintext;
  }
  .sp {
    flex: 1;
  }
  .counts {
    flex: none;
    display: flex;
    gap: 7px;
    font-size: 11.5px;
  }
  .add {
    color: #6fae90;
  }
  .del {
    color: #c98a83;
  }
  .diffs {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 2px 6px 8px 24px;
  }
</style>
