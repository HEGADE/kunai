<script lang="ts">
  // One control on a composer bar: a quiet label that opens a menu of options.
  //
  // This is the shape the chat composer has always used for model, effort,
  // permission and account, and it is the right one for the same reason it was
  // there: these are settings on the thing you are writing, so they should read
  // as one strip under the text rather than as four rows of buttons competing
  // with it. Rows of chips state every option at once, which is only worth the
  // space when choosing is the task; here writing the prompt is the task.
  //
  // Extracted so the worktree dialog and the composer cannot drift into two
  // slightly different versions of the same control.
  export interface SegOption {
    id: string
    label: string
    hint?: string
    // mono for an option whose label is data rather than prose, e.g. a real
    // upstream model id.
    mono?: boolean
  }

  let {
    value,
    options,
    label,
    title = '',
    note = '',
    mono = false,
    align = 'left',
    up = true,
    disabled = false,
    onpick,
  }: {
    value: string
    options: SegOption[]
    // label overrides what the closed button shows; by default it is the
    // selected option's label, which is what you want almost everywhere.
    label?: string
    title?: string
    // note is a line above the options, for a consequence worth stating before
    // the choice rather than after it.
    note?: string
    mono?: boolean
    // align right for a control near the right edge, so its menu opens inward
    // instead of off the screen.
    align?: 'left' | 'right'
    // up is the composer's direction: a control sitting at the bottom of a box
    // opens its menu above itself. False for one near the top of a dialog, where
    // upward would put the menu off the screen.
    up?: boolean
    disabled?: boolean
    onpick: (id: string) => void
  } = $props()

  let open = $state(false)
  let pop: HTMLDivElement | null = $state(null)
  // How far to slide the menu back into view. Measured rather than guessed with
  // an alignment prop: which control is the rightmost one changes with the
  // options (a provider hides the effort control), so a hardcoded "this one
  // opens leftward" is right until the day it silently is not.
  let dx = $state(0)
  const margin = 8

  $effect(() => {
    if (!open || !pop) {
      dx = 0
      return
    }
    dx = 0
    const r = pop.getBoundingClientRect()
    const over = r.right - (window.innerWidth - margin)
    const under = margin - r.left
    if (over > 0) dx = -over
    else if (under > 0) dx = under
  })

  const current = $derived(options.find((o) => o.id === value))
  const shown = $derived(label ?? current?.label ?? value)

  // Escape closes the menu and stops there. Without the stopPropagation a dialog
  // hosting this control sees the same key and closes itself, so dismissing a
  // menu you opened by mistake threw away everything you had typed. Handled on
  // the wrapper rather than on window, because every focusable part of the menu
  // (the button, the options, the scrim) is inside it, so bubbling reaches this
  // before the host; a window listener would only fire after the host had acted.
  function onKey(e: KeyboardEvent) {
    if (!open || e.key !== 'Escape') return
    e.preventDefault()
    e.stopPropagation()
    open = false
  }
</script>

<div class="segwrap" onkeydown={onKey} role="presentation">
  <button
    class="seg"
    class:mono
    class:open
    {disabled}
    {title}
    onclick={() => (open = !open)}
    aria-haspopup="menu"
    aria-expanded={open}
  >
    <span class="segtext">{shown}</span>
  </button>
  {#if open}
    <button class="scrim" onclick={() => (open = false)} aria-label="Close"></button>
    <div
      class="pop"
      class:right={align === 'right'}
      class:down={!up}
      bind:this={pop}
      style={dx ? `transform: translateX(${dx}px)` : ''}
      role="menu"
    >
      {#if note}<p class="note">{note}</p>{/if}
      {#each options as o (o.id)}
        <button
          class:active={o.id === value}
          onclick={() => {
            open = false
            if (o.id !== value) onpick(o.id)
          }}
        >
          <span class="ml" class:mono={o.mono}>{o.label}</span>
          {#if o.hint}<span class="mh">{o.hint}</span>{/if}
        </button>
      {/each}
      {#if options.length === 0}<p class="note plain">Nothing to choose from.</p>{/if}
    </div>
  {/if}
</div>

<style>
  .segwrap {
    position: relative;
    display: inline-flex;
    align-items: center;
    min-width: 0;
  }
  .seg {
    display: inline-flex;
    align-items: center;
    height: 28px;
    padding: 0 9px;
    border: 0;
    border-radius: 8px;
    background: transparent;
    color: var(--text-3, var(--text-2));
    font: inherit;
    font-size: 12.5px;
    font-weight: 500;
    white-space: nowrap;
    min-width: 0;
    cursor: pointer;
    transition: color 0.12s, background 0.12s;
  }
  .seg:hover:not(:disabled),
  .seg.open {
    color: var(--text);
    background: var(--panel-3);
  }
  .seg:disabled {
    color: var(--text-4);
    cursor: default;
  }
  .seg.mono {
    font-family: var(--mono);
    font-size: 12px;
  }
  /* A long label (an account name, a real model id) truncates rather than
     shoving the rest of the strip off the edge. */
  .segtext {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 150px;
  }
  @media (max-width: 560px) {
    .segtext {
      max-width: 110px;
    }
  }

  .scrim {
    position: fixed;
    inset: 0;
    z-index: 70;
    border: 0;
    background: transparent;
    cursor: default;
  }
  .pop {
    position: absolute;
    z-index: 71;
    bottom: calc(100% + 8px);
    left: 0;
    min-width: 220px;
    max-width: calc(100vw - 24px);
    max-height: 260px;
    overflow-y: auto;
    padding: 5px;
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    border-radius: var(--r);
    box-shadow: 0 16px 40px -14px rgba(0, 0, 0, 0.7);
  }
  .pop.right {
    left: auto;
    right: 0;
  }
  .pop.down {
    bottom: auto;
    top: calc(100% + 6px);
  }
  .note {
    margin: 0 0 4px;
    padding: 6px 11px 8px;
    font-size: 11px;
    color: var(--text-4);
    border-bottom: 1px solid var(--border);
  }
  .note.plain {
    border-bottom: 0;
  }
  .pop button {
    display: flex;
    flex-direction: column;
    gap: 1px;
    width: 100%;
    padding: 7px 11px;
    border: 0;
    border-radius: var(--r-sm);
    background: transparent;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .pop button:hover,
  .pop button.active {
    background: var(--panel-3);
  }
  .pop button.active .ml {
    color: var(--text);
  }
  .ml {
    font-size: 13px;
    font-weight: 550;
    color: var(--text-2);
  }
  .ml.mono {
    font-family: var(--mono);
    font-size: 12px;
  }
  .mh {
    font-size: 11.5px;
    color: var(--text-4);
  }
</style>
