<script lang="ts">
  import type { ChatConnection, PendingPermission } from '../lib/chat.svelte'
  import ToolBody from './tools/ToolBody.svelte'
  import QuestionForm from './QuestionForm.svelte'
  let { chat }: { chat: ChatConnection } = $props()

  const current = $derived<PendingPermission | undefined>(chat.pending[0])
  const extra = $derived(chat.pending.length - 1)
  // AskUserQuestion isn't an allow/deny gate — the user's selection IS the
  // answer, returned as `answers` on allow.
  const isQuestion = $derived(current?.tool_name === 'AskUserQuestion')
</script>

{#if current}
  <div class="gate">
    <div class="inner">
      {#if isQuestion}
        <div class="head">
          <span class="k">Claude is asking</span>
          {#if extra > 0}<span class="more">+{extra} more</span>{/if}
        </div>
        <QuestionForm
          input={current.input}
          onSubmit={(answers) => chat.resolve(current.request_id, 'allow', false, answers)}
          onSkip={() => chat.resolve(current.request_id, 'deny')}
        />
      {:else}
        <div class="head">
          <span class="k">Authorize</span>
          <span class="tool mono">{current.tool_name}</span>
          {#if extra > 0}<span class="more">+{extra} more</span>{/if}
        </div>
        <p class="ask">{current.perm_title || `Claude wants to run ${current.tool_name}`}</p>
        <div class="detail"><ToolBody name={current.tool_name} input={current.input} /></div>
        <div class="actions">
          <button class="deny" onclick={() => chat.resolve(current.request_id, 'deny')}>Deny</button>
          <button class="allow" onclick={() => chat.resolve(current.request_id, 'allow')}>Allow</button>
        </div>
        <button class="always" onclick={() => chat.resolve(current.request_id, 'allow', true)}>
          Always allow {current.tool_name} this session
        </button>
      {/if}
    </div>
  </div>
{/if}

<style>
  .gate {
    background: var(--panel);
    border-top: 1px solid var(--border-2);
    animation: rise 0.16s ease-out;
  }
  @keyframes rise {
    from {
      transform: translateY(8px);
      opacity: 0;
    }
  }
  .inner {
    max-width: 720px;
    margin: 0 auto;
    padding: 15px 20px calc(var(--safe-bottom) + 14px);
  }
  .head {
    display: flex;
    align-items: baseline;
    gap: 9px;
    margin-bottom: 8px;
  }
  .k {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-3);
  }
  .tool {
    font-size: 12.5px;
    font-weight: 500;
    color: var(--text);
  }
  .more {
    font-size: 11px;
    color: var(--text-4);
    margin-left: auto;
  }
  .ask {
    margin: 0 0 10px;
    font-size: 14.5px;
    color: var(--text);
  }
  .detail {
    margin: 0 0 13px;
    max-height: 40vh;
    overflow: auto;
  }
  .actions {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 9px;
  }
  .deny,
  .allow {
    padding: 12px;
    border-radius: var(--r);
    font-weight: 550;
    font-size: 14px;
  }
  .deny {
    background: var(--panel-2);
    border: 1px solid var(--border);
    color: var(--text);
  }
  .deny:hover {
    border-color: var(--border-2);
  }
  .allow {
    background: var(--white);
    color: #0b0b0c;
  }
  .allow:hover {
    opacity: 0.9;
  }
  .always {
    width: 100%;
    margin-top: 9px;
    padding: 8px;
    font-size: 12px;
    color: var(--text-3);
  }
  .always:hover {
    color: var(--text);
  }
</style>
