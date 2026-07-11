<script lang="ts">
  import { langFromPath } from '../../lib/highlight'
  import CodeView from './CodeView.svelte'
  import DiffView from './DiffView.svelte'
  import TodoList from './TodoList.svelte'

  let { name, input }: { name: string; input: unknown } = $props()

  type Obj = Record<string, unknown>
  const i = $derived((input && typeof input === 'object' ? input : {}) as Obj)
  const s = (v: unknown): string => (typeof v === 'string' ? v : '')
  const n = (v: unknown): number | undefined => (typeof v === 'number' ? v : undefined)
  const edits = $derived(Array.isArray(i.edits) ? (i.edits as Obj[]) : [])
  const todos = $derived(Array.isArray(i.todos) ? (i.todos as { content?: string; status?: string; activeForm?: string }[]) : [])
  const pretty = $derived(JSON.stringify(input ?? {}, null, 2))
  const readRange = $derived.by(() => {
    const off = n(i.offset)
    const lim = n(i.limit)
    if (off == null) return ''
    return `lines ${off}–${lim != null ? off + lim : 'end'}`
  })
</script>

<div class="tb">
  {#if name === 'Edit'}
    <DiffView oldStr={s(i.old_string)} newStr={s(i.new_string)} path={s(i.file_path)} replaceAll={i.replace_all === true} />
  {:else if name === 'MultiEdit'}
    <div class="path mono">{s(i.file_path)}</div>
    <div class="stack">
      {#each edits as e, k (k)}
        <DiffView oldStr={s(e.old_string)} newStr={s(e.new_string)} replaceAll={e.replace_all === true} />
      {/each}
    </div>
  {:else if name === 'Write'}
    <div class="path mono">{s(i.file_path)}</div>
    <CodeView code={s(i.content)} lang={langFromPath(s(i.file_path))} />
  {:else if name === 'Bash'}
    {#if s(i.description)}<div class="desc">{s(i.description)}</div>{/if}
    <CodeView code={s(i.command)} lang="bash" label="SHELL" />
  {:else if name === 'Read'}
    <div class="fields">
      <div class="f"><span class="k">Path</span><span class="v mono">{s(i.file_path)}</span></div>
      {#if readRange}<div class="f"><span class="k">Range</span><span class="v mono">{readRange}</span></div>{/if}
    </div>
  {:else if name === 'Grep'}
    <div class="fields">
      <div class="f"><span class="k">Pattern</span><span class="v mono">{s(i.pattern)}</span></div>
      {#if s(i.path)}<div class="f"><span class="k">Path</span><span class="v mono">{s(i.path)}</span></div>{/if}
      {#if s(i.glob)}<div class="f"><span class="k">Glob</span><span class="v mono">{s(i.glob)}</span></div>{/if}
      {#if s(i.output_mode)}<div class="f"><span class="k">Mode</span><span class="v mono">{s(i.output_mode)}</span></div>{/if}
    </div>
  {:else if name === 'Glob'}
    <div class="fields">
      <div class="f"><span class="k">Pattern</span><span class="v mono">{s(i.pattern)}</span></div>
      {#if s(i.path)}<div class="f"><span class="k">Path</span><span class="v mono">{s(i.path)}</span></div>{/if}
    </div>
  {:else if name === 'TodoWrite'}
    <TodoList {todos} />
  {:else if name === 'WebFetch'}
    <div class="fields">
      <div class="f"><span class="k">URL</span><a class="v mono link" href={s(i.url)} target="_blank" rel="noopener noreferrer">{s(i.url)}</a></div>
    </div>
    {#if s(i.prompt)}<div class="quote">{s(i.prompt)}</div>{/if}
  {:else if name === 'WebSearch'}
    <div class="fields">
      <div class="f"><span class="k">Query</span><span class="v">{s(i.query)}</span></div>
    </div>
  {:else if name === 'Task'}
    <div class="fields">
      {#if s(i.subagent_type)}<div class="f"><span class="k">Agent</span><span class="v mono">{s(i.subagent_type)}</span></div>{/if}
      {#if s(i.description)}<div class="f"><span class="k">Task</span><span class="v">{s(i.description)}</span></div>{/if}
    </div>
    {#if s(i.prompt)}<div class="quote">{s(i.prompt)}</div>{/if}
  {:else}
    <pre class="raw mono">{pretty}</pre>
  {/if}
</div>

<style>
  .tb {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .stack {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .path {
    font-size: 11.5px;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    direction: rtl;
    unicode-bidi: plaintext;
    text-align: left;
  }
  .desc {
    font-size: 12.5px;
    color: var(--text-3);
  }
  .fields {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }
  .f {
    display: flex;
    gap: 10px;
    font-size: 13px;
  }
  .k {
    flex: none;
    width: 58px;
    color: var(--text-4);
  }
  .v {
    min-width: 0;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .v.mono {
    font-size: 12px;
  }
  .link {
    text-decoration: underline;
    text-underline-offset: 2px;
    text-decoration-color: var(--text-3);
  }
  .quote {
    font-size: 12.5px;
    color: var(--text-2);
    padding-left: 11px;
    border-left: 2px solid var(--border-2);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    max-height: 160px;
    overflow-y: auto;
  }
  .raw {
    margin: 0;
    padding: 11px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    font-size: 12px;
    line-height: 1.55;
    color: var(--text-2);
    white-space: pre-wrap;
    word-break: break-word;
    overflow-x: auto;
  }
</style>
