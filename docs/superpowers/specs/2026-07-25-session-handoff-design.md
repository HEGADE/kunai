# Session handoff: continue a reply in a fresh session

## Problem

A long session runs out of context. The agent's last useful act is usually a plan
("here is how I would build this"), and the natural next step is to start a fresh
session and implement it. Today that handoff is manual and it fails in a specific,
expensive way: the plan exists only as chat text in the exhausted session, so a new
session told to "build the plan" has nothing to find. Observed live: a fresh session
spent 29s and $0.47 searching `docs/superpowers/specs/`, the repo, and memory before
correctly reporting there was no plan anywhere.

Copy-and-paste is the workaround, and on a phone it is a bad one: the reply is long,
selecting it is fiddly, and the new session still has to be pointed at the same
directory, account, and model by hand.

This is a kunai-shaped problem rather than a general chat problem. Sessions here are
long, unattended, and often driven from a phone, so they hit the context wall more
often than an interactive desktop session would, and the person who needs to restart
the work is frequently not at a keyboard.

## Approach

Add one action to a turn's footer, beside Copy: **Continue in a fresh session**. It
creates a new session and seeds it with that reply as the opening brief.

Deliberately NOT plan detection. Sniffing for headings, numbered steps, or the word
"plan" would be fragile in both directions (it would miss plans written as prose and
fire on replies that merely mention one), and the action is useful for any reply you
want to act on with a clean window, not only plans. Offering it on every assistant
turn is always correct and needs no heuristic.

### What carries over

The new session must be able to do the work, so it inherits the parent's execution
context, not just the text:

- **cwd** — the same directory. Without it the new agent cannot find the code.
- **account (`cli`)** — the same account, so it bills where the work was happening
  and can read that account's transcripts.
- **model and effort** — a plan written by Opus should not be implemented by Haiku
  because the new session fell back to a default.

### The seed prompt

Not a raw paste. A raw paste reads as "here is some text" and the new agent tends to
discuss the plan rather than build it. Frame it so the instruction is unambiguous:

```
This plan came from an earlier session that ran out of context. Implement it.

<plan>
…the reply text…
</plan>
```

The `<plan>` wrapper is the same trick `session.LoopPrompt` uses for loop
iterations: an explicit tag survives transcript replay and keeps the model from
mistaking the brief for conversation.

### Title

Derive the new session's title from the reply's first heading or first line, so it is
findable in Recent as something meaningful rather than a truncated wall of text.

## Implementation sketch

Client-only; every server piece already exists.

- `web/src/components/TurnFooter.svelte` — add the action next to Copy. It already
  computes `reply` (the turn's trailing text) for the clipboard, which is exactly the
  text to hand off, so no new extraction logic is needed.
- `web/src/lib/app.svelte.ts` — add `handOff(machineId, cwd, opts)`:
  `createSession({cwd, cli, model, effort, title})`, then `app.open(machineId, id)`,
  then send the framed seed as the first prompt. `quickStart` is the model to follow.
- The parent session's `cli`/`model` come from its `Meta`, already on the client.

Nothing on the Go side. `createSession` accepts cwd/title/cli/model/effort today, and
a session accepts a prompt the moment it is created (prompts queue in the driver's
out channel while the CLI boots, so there is no race to handle).

## Trade-offs and open questions

- **The handoff is one reply, not the conversation.** Deliberate: the point is a
  clean window, and dragging the whole history back in defeats it. If the reply
  omits something the work needs, that is the plan's fault and the fix is a better
  plan, not a bigger paste.
- **No link back to the parent.** Considered and left out for v1: a "continued from"
  reference would be nice for provenance, but it needs a wire field and the title
  plus the shared cwd already make the relationship obvious in Recent.
- **Attachments do not carry.** The reply's text is the brief; images from the parent
  turn are not re-sent. Worth revisiting if it bites.
- **Should it also write the plan to disk?** Tempting, since a file is what made this
  discoverable at all. But writing files as a side effect of a UI action is
  surprising, and the seeded session can be asked to save the plan itself. Left out.

## Why not the alternatives

- **"Copy" is enough.** It is not, on a phone, and it loses cwd/account/model.
- **Detect plans and offer the button only there.** Fragile, and narrower than the
  real need.
- **Auto-hand-off when context runs out.** Too magic: it would spend money starting
  work the person may not want started, and the exhausted agent is the worst judge of
  whether its own plan is ready.
