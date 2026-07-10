<script lang="ts">
  import { app } from '../lib/app.svelte'
</script>

<div class="empty">
  <p class="lead">Select a session, or start a new one.</p>
  <p class="sub">Claude Code, driven directly over your tailnet.</p>
  <button class="cta" onclick={() => app.newSession()}>New session</button>
</div>

<style>
  .empty {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    text-align: center;
    padding: 40px;
  }
  .lead {
    font-size: 16px;
    font-weight: 500;
    color: var(--text);
    margin: 0 0 6px;
  }
  .sub {
    font-size: 14px;
    color: var(--text-3);
    margin: 0 0 22px;
  }
  .cta {
    padding: 9px 18px;
    border-radius: var(--r);
    background: var(--white);
    color: #0b0b0c;
    font-weight: 550;
    font-size: 13.5px;
    transition: opacity 0.12s;
  }
  .cta:hover {
    opacity: 0.9;
  }
</style>
