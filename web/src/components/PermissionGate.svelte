<script lang="ts">
  import type { ChatConnection, PendingPermission } from '../lib/chat.svelte'
  let { chat }: { chat: ChatConnection } = $props()

  const current = $derived<PendingPermission | undefined>(chat.pending[0])
  const extra = $derived(chat.pending.length - 1)
  const detail = $derived.by(() => {
    const i = (current?.input ?? {}) as Record<string, unknown>
    if (typeof i.command === 'string') return i.command
    if (typeof i.file_path === 'string') return i.file_path
    if (typeof i.path === 'string') return i.path
    if (typeof i.url === 'string') return i.url
    return JSON.stringify(i, null, 2)
  })
</script>

{#if current}
  <div class="gate">
    <div class="head">
      <span class="tag label">authorize{extra > 0 ? ` · +${extra}` : ''}</span>
      <span class="tool mono">{current.tool_name}</span>
    </div>
    <p class="ask">{current.perm_title || `Claude wants to run ${current.tool_name}`}</p>
    <pre class="detail mono">{detail}</pre>
    <div class="actions">
      <button class="deny" onclick={() => chat.resolve(current.request_id, 'deny')}>Deny</button>
      <button class="allow" onclick={() => chat.resolve(current.request_id, 'allow')}>Allow</button>
    </div>
    <button class="always mono" onclick={() => chat.resolve(current.request_id, 'allow', true)}>
      always allow {current.tool_name} this session
    </button>
  </div>
{/if}

<style>
  .gate {
    background: var(--bg-2);
    border-top: 2px solid var(--amber);
    box-shadow: 0 -16px 36px rgba(0, 0, 0, 0.5);
    padding: 13px 16px calc(var(--safe-bottom) + 12px);
    animation: rise 0.2s ease-out;
  }
  @keyframes rise {
    from {
      transform: translateY(10px);
      opacity: 0;
    }
  }
  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: 7px;
  }
  .tag {
    color: var(--amber);
  }
  .tool {
    font-size: 12.5px;
    font-weight: 600;
    color: var(--amber);
  }
  .ask {
    margin: 0 0 9px;
    font-size: 15px;
    font-weight: 550;
    color: var(--ink);
  }
  .detail {
    margin: 0 0 13px;
    padding: 10px 12px;
    background: var(--bg);
    border: 1px solid var(--line);
    border-radius: var(--r-sm);
    font-size: 12px;
    line-height: 1.5;
    color: var(--ink-dim);
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 118px;
    overflow: auto;
  }
  .actions {
    display: grid;
    grid-template-columns: 1fr 1.5fr;
    gap: 10px;
  }
  .deny,
  .allow {
    padding: 14px;
    border-radius: var(--r);
    font-weight: 650;
    font-size: 15px;
  }
  .deny {
    background: var(--stop-dim);
    color: var(--stop);
    border: 1px solid transparent;
  }
  .deny:active {
    border-color: var(--stop);
  }
  .allow {
    background: var(--go);
    color: #062015;
  }
  .allow:active {
    filter: brightness(1.1);
  }
  .always {
    width: 100%;
    margin-top: 9px;
    padding: 9px;
    font-size: 11.5px;
    letter-spacing: 0.04em;
    color: var(--ink-faint);
  }
  .always:active {
    color: var(--go);
  }
</style>
