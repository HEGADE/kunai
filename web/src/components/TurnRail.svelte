<script lang="ts">
  // A rail of the questions you asked, down the edge of the log.
  //
  // A long session's scrollbar tells you where you are in a column of pixels,
  // which is not a thing anybody wants to know. What you actually navigate by is
  // "the bit where I asked about the funnel" -- your own prompts are the
  // landmarks, and everything between two of them is one exchange. So the rail
  // is one tick per prompt, evenly spaced rather than proportional to height: a
  // turn that ran forty tool calls is not forty times more worth finding than a
  // one-line question, and spacing by height buries the short ones.
  //
  // It stays a hairline until you go near it. This is a way back to something,
  // not a thing to look at, and a permanent list of your own prompts down the
  // side of the conversation would compete with the conversation.
  import type { Turn } from '../lib/turns'

  let {
    turns,
    scroller,
    firstVisible = 0,
  }: {
    turns: Turn[]
    scroller: HTMLElement | null
    firstVisible?: number
  } = $props()

  // Only prompts. A turn with no user message is the agent continuing (a loop
  // iteration, a resumed seed), and it is not somewhere you meant to go back to.
  const marks = $derived(
    turns
      .map((t, i) => ({ i, text: (t.user ?? '').trim() }))
      .filter((m) => m.text.length > 0),
  )

  // Below this the rail is furniture: two or three prompts are all on screen
  // already, and a jump control for them is a control that never earns its
  // pixels. The same reason the sidebar renders no heading for a single group.
  const MIN_MARKS = 4
  const show = $derived(marks.length >= MIN_MARKS)

  // Fixed spacing, so the rail is as tall as it needs to be and no taller.
  //
  // Spreading the ticks over the whole height was the whole problem: at twenty
  // prompts they became a comb running the length of the window, a foot from the
  // real scrollbar, which reads as a second broken scrollbar rather than as an
  // index. Eight pixels apart makes twenty marks a 152px cluster -- small enough
  // to be one object the eye takes in at once, which is what an index is.
  const SPACING = 8
  const MAX_H = 320
  const railH = $derived(Math.min((marks.length - 1) * SPACING, MAX_H))

  let hovering = $state(false)
  let active = $state(-1)

  function jump(i: number) {
    // Queried rather than held as a ref array: the log is windowed and re-keys
    // as older turns are revealed, so a stored element can outlive its turn.
    const el = scroller?.querySelector<HTMLElement>(`[data-turn="${firstVisible + i}"]`)
    if (!el || !scroller) return
    active = i
    scroller.scrollTo({ top: el.offsetTop - 12, behavior: 'smooth' })
  }

  function label(text: string): string {
    const one = text.replace(/\s+/g, ' ').trim()
    return one.length > 60 ? one.slice(0, 59) + '…' : one
  }
</script>

{#if show}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <nav
    class="rail"
    class:open={hovering}
    style="height: {railH}px"
    aria-label="Jump to a question"
    onmouseenter={() => (hovering = true)}
    onmouseleave={() => (hovering = false)}
  >
    {#each marks as m, k (m.i)}
      <button
        class="tick"
        class:on={active === m.i}
        style="top: {marks.length > 1 ? (k / (marks.length - 1)) * 100 : 0}%"
        onclick={() => jump(m.i)}
        title={label(m.text)}
      >
        <span class="dash"></span>
        <span class="lbl">{label(m.text)}</span>
      </button>
    {/each}
  </nav>
{/if}

<style>
  /* Centred on the log rather than spanning it, and invisible until you go for
     it. Drawn at rest it is a column of marks nobody asked for, sitting beside
     the scrollbar and competing with it; the pointer arriving at the right edge
     is the only moment it has anything to say. */
  .rail {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    right: 14px;
    width: 26px;
    z-index: 8;
    opacity: 0;
    transition: opacity var(--t) var(--ease);
    pointer-events: auto;
  }
  .rail:hover,
  .rail:focus-within {
    opacity: 1;
  }
  /* The hit area is wider than the marks, because a 2px dash is a target nobody
     hits on purpose. The rail itself catches the pointer; the ticks sit inside
     it. */
  .tick {
    position: absolute;
    right: 0;
    transform: translateY(-50%);
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    height: 18px;
    padding: 0;
    background: none;
    cursor: pointer;
  }
  .dash {
    flex: none;
    width: 10px;
    height: 1.5px;
    border-radius: 2px;
    background: var(--border-2);
    transition:
      width var(--t) var(--ease),
      background var(--t) var(--ease);
  }
  .rail.open .dash {
    width: 14px;
    background: var(--text-4);
  }
  .tick:hover .dash,
  .tick.on .dash {
    background: var(--text-2);
    width: 16px;
  }
  /* The prompt itself, only while the rail is open and only for the tick under
     the pointer. Showing every label at once rebuilds the conversation down the
     margin, which is the thing being navigated away from. */
  .lbl {
    position: absolute;
    right: 20px;
    max-width: 46ch;
    padding: 4px 9px;
    border-radius: var(--r-sm);
    background: var(--panel-2);
    border: 1px solid var(--border-2);
    box-shadow: 0 12px 30px -14px rgba(0, 0, 0, 0.8);
    font-size: 11.5px;
    line-height: 1.35;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    opacity: 0;
    pointer-events: none;
    transition: opacity var(--t-fast) var(--ease);
  }
  .tick:hover .lbl,
  .tick:focus-visible .lbl {
    opacity: 1;
  }
  /* No hover on a phone, and the log is one column wide there: a rail would take
     width from the conversation to offer a gesture the device cannot make. */
  @media (max-width: 860px), (hover: none) {
    .rail {
      display: none;
    }
  }
</style>
