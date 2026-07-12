<script lang="ts">
  import { iconForFile } from '../../lib/langIcons'

  let {
    path,
    name,
    added,
    removed,
  }: { path?: string; name: string; added?: number; removed?: number } = $props()
  const icon = $derived(iconForFile(path || name))
</script>

<span class="chip">
  {#if icon}
    <svg class="li" viewBox="0 0 24 24" width="12" height="12" style="color:{icon.color}" aria-hidden="true">
      <path fill="currentColor" d={icon.d} />
    </svg>
  {:else}
    <svg class="li gen" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
      <path d="M14 3v5h5" />
      <path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
    </svg>
  {/if}
  <span class="fn">{name}</span>
  {#if added}<span class="stat add">+{added}</span>{/if}
  {#if removed}<span class="stat del">−{removed}</span>{/if}
</span>

<style>
  .chip {
    flex: 0 1 auto;
    min-width: 0;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 1px 8px 1px 6px;
    border: 1px solid var(--border-2);
    border-radius: 6px;
    background: var(--panel);
  }
  .li {
    flex: none;
  }
  .li.gen {
    color: var(--text-4);
  }
  .fn {
    min-width: 0;
    font-size: 12.5px;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .stat {
    flex: none;
    font-family: var(--mono);
    font-size: 11px;
    font-weight: 500;
    letter-spacing: 0;
  }
  .stat.add {
    color: var(--live);
  }
  .stat.del {
    color: var(--alert);
  }
</style>
