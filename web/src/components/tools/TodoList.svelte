<script lang="ts">
  type Todo = { content?: string; status?: string; activeForm?: string }
  let { todos }: { todos: Todo[] } = $props()
  const label = (t: Todo) =>
    t.status === 'in_progress' && t.activeForm ? t.activeForm : (t.content ?? '')
</script>

<div class="todos">
  {#each todos as t, i (i)}
    <div class="td {t.status ?? 'pending'}">
      <span class="mk">
        {#if t.status === 'completed'}
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5" /></svg>
        {:else if t.status === 'in_progress'}
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="9" opacity="0.35" /><path d="M12 3a9 9 0 019 9" stroke-linecap="round" /></svg>
        {:else}
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="12" cy="12" r="9" /></svg>
        {/if}
      </span>
      <span class="lbl">{label(t)}</span>
    </div>
  {/each}
</div>

<style>
  .todos {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .td {
    display: flex;
    align-items: baseline;
    gap: 9px;
    padding: 3px 2px;
    font-size: 13.5px;
    color: var(--text);
  }
  .mk {
    flex: none;
    position: relative;
    top: 2px;
    display: inline-flex;
    color: var(--text-4);
  }
  .td.completed .mk {
    color: var(--live);
  }
  .td.in_progress .mk {
    color: var(--busy);
  }
  .td.completed .lbl {
    color: var(--text-3);
    text-decoration: line-through;
  }
  .td.in_progress .lbl {
    color: var(--text);
  }
  .td.pending .lbl {
    color: var(--text-2);
  }
</style>
