<script lang="ts">
  import type { Decision } from '../../lib/review'
  import { postLabel } from '../../lib/review'

  // The decision, and the one irreversible action.
  //
  // At the bottom rather than in the header, for two reasons. It is where the
  // reader's eye already is once they have worked down the list, and it is the
  // half of the screen a thumb reaches on a phone. The header keeps identity and
  // the way back to the conversation; nothing you act on repeatedly lives there.
  //
  // What it says is what will actually be SENT, counted at whatever severity the
  // reader overruled the finding to. The old headline counted the model's own
  // severity, so demoting the only blocker still announced a blocker.
  let {
    d,
    posting,
    posted,
    postedUrl,
    onpost,
  }: {
    d: Decision
    posting: boolean
    posted: boolean
    postedUrl?: string
    onpost: () => void
  } = $props()
</script>

<footer class="bar">
  <div class="state">
    {#if posted}
      <span class="done">Posted to GitHub</span>
    {:else if !d.total}
      <span class="quiet">Nothing found. Posting sends that as the review.</span>
    {:else}
      <span class="count"><b>{d.keep}</b> to post</span>
      {#if d.drop}<span class="quiet">{d.drop} dropped</span>{/if}
      {#if d.keep}
        <span class="quiet">{d.inline} on the line, {d.summary} in the summary</span>
      {/if}
    {/if}
  </div>

  <div class="acts">
    <p class="keys mono">j k move &middot; o open &middot; d drop &middot; &#8984;&#9166; post</p>
    {#if posted}
      <a class="link" href={postedUrl} target="_blank" rel="noreferrer">Open on GitHub &rarr;</a>
    {:else}
      <button class="post" onclick={onpost} disabled={posting} class:warn={d.blockers > 0}>
        {postLabel(d, posting)}
      </button>
    {/if}
  </div>
</footer>

<style>
  .bar {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    flex-wrap: wrap;
    padding: 11px 18px calc(var(--safe-bottom) + 11px);
    border-top: 1px solid var(--border);
    background: var(--bg);
  }
  .state {
    display: flex;
    align-items: baseline;
    gap: 12px;
    flex-wrap: wrap;
    min-width: 0;
    font-size: 12px;
    color: var(--text-3);
  }
  .count b {
    font-weight: 650;
    color: var(--text);
    font-variant-numeric: tabular-nums;
  }
  .quiet {
    color: var(--text-4);
    font-variant-numeric: tabular-nums;
  }
  .done {
    color: var(--live);
    font-weight: 550;
  }
  .acts {
    display: flex;
    align-items: center;
    gap: 16px;
    flex: none;
  }
  .keys {
    margin: 0;
    font-size: 10.5px;
    color: var(--text-4);
    white-space: nowrap;
  }
  /* No keyboard to hint at, and no hover to discover it with. */
  @media (pointer: coarse) {
    .keys {
      display: none;
    }
  }
  .post {
    padding: 7px 17px;
    border-radius: var(--r-sm);
    background: var(--white);
    color: #0b0b0c;
    font-size: 12.5px;
    font-weight: 600;
  }
  .post:disabled {
    opacity: 0.5;
  }
  /* A review that blocks a merge is a different thing to send, and the button is
     the last place to say so. */
  .post.warn {
    background: var(--alert);
    color: #fff;
  }
  .link {
    font-size: 12.5px;
    color: var(--live);
    text-decoration: none;
  }

  @media (max-width: 560px) {
    .bar {
      padding: 10px 12px calc(var(--safe-bottom) + 10px);
    }
    .post {
      padding: 10px 18px;
    }
  }
</style>
