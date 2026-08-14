<script lang="ts">
  import type { ReviewPhase } from '../../lib/api'

  // Where the review has got to.
  //
  // This is the screen somebody actually looks at, because a phased review runs
  // for minutes and there is nothing else on the page until it produces
  // something. A spinner and an elapsed clock cannot distinguish working from
  // hung, so the wait reads as a hang and people reload or start it again.
  //
  // Three named steps, one lit, is the whole design: it says what is happening
  // now, that there are stages, and how many are left. Every part of it is read
  // off the recorded phase, so it cannot claim progress that is not happening.
  //
  // The survey is skipped on a small change, so it is not drawn at all in that
  // case rather than shown as a step that will never light: a progression with a
  // permanently dead first step is worse than one with two steps.
  let { phase, skippedSurvey }: { phase: ReviewPhase; skippedSurvey: boolean } = $props()

  const STEPS: { key: ReviewPhase; label: string; doing: string }[] = [
    { key: 'survey', label: 'Read', doing: 'Reading the change' },
    { key: 'find', label: 'Find', doing: 'Looking for problems' },
    { key: 'verify', label: 'Check', doing: 'Trying to refute what it found' },
  ]

  const steps = $derived(skippedSurvey ? STEPS.slice(1) : STEPS)
  const at = $derived(steps.findIndex((s) => s.key === phase))
  const now = $derived(steps[at]?.doing ?? 'Reviewing')
</script>

<div class="trail">
  <p class="now">{now}</p>
  <ol>
    {#each steps as s, i (s.key)}
      <li class:done={at > i} class:on={at === i}>
        <span class="dot" aria-hidden="true"></span>
        <span class="lbl">{s.label}</span>
      </li>
    {/each}
  </ol>
</div>

<style>
  .trail {
    margin-bottom: 20px;
  }
  .now {
    margin: 0 0 10px;
    font-size: 13.5px;
    color: var(--text-2);
  }
  ol {
    display: flex;
    align-items: center;
    gap: 0;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  li {
    display: flex;
    align-items: center;
    gap: 7px;
    padding-right: 14px;
    font-size: 10.5px;
    font-weight: 600;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--text-4);
  }
  /* The connector is drawn by the step rather than between steps, so the last
     one simply does not have it and there is no trailing rule into nothing. */
  li:not(:last-child)::after {
    content: '';
    width: 34px;
    height: 1px;
    margin-left: 7px;
    background: var(--border);
  }
  li.done::after {
    background: var(--border-2);
  }
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    border: 1px solid var(--text-4);
  }
  li.done {
    color: var(--text-3);
  }
  li.done .dot {
    background: var(--text-3);
    border-color: var(--text-3);
  }
  li.on {
    color: var(--live);
  }
  li.on .dot {
    border-color: var(--live);
    background: var(--live);
    animation: pulse 1.8s ease-in-out infinite;
  }
  @keyframes pulse {
    50% {
      opacity: 0.3;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    li.on .dot {
      animation: none;
    }
  }
</style>
