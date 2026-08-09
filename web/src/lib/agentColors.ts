// One colour per agent, used identically everywhere on the Usage page.
//
// This is the second sanctioned break in the near-monochrome rule, and it is the
// same break as the first: a colour is spent only where it stands for somebody
// else's product, because there the colour IS the information. On this page that
// is load-bearing rather than decorative -- the whole question is which brain the
// month went on, and a stacked column is unreadable if its segments are three
// steps of the same gray. app.css already carries --claude/--openai/--grok for
// the sidebar's brand marks; those are tuned to sit quietly beside text, and two
// of them are near-identical grays, so they cannot be reused as a chart palette.
//
// The palette is not a taste judgement. It was run through the dataviz
// validator (OKLab dE, protan/deutan/tritan simulation, contrast against this
// app's own #0b0b0c canvas) and every check passes for the three agents that
// actually occur, on the ALL-pairs list rather than only on adjacent pairs:
//
//   lightness band  PASS   chroma floor  PASS
//   CVD separation  PASS   worst pair Codex<->Claude dE 9.4 (deutan), target >= 8
//   normal vision   PASS   worst pair Grok<->Codex dE 20.9, floor 15
//   contrast        PASS   all >= 3:1 on #0b0b0c
//
// Two near misses are worth recording, because both look fine to the eye and are
// not. Anthropic's own clay (#d97757, the --claude token) FAILS against Codex's
// green at dE 4.6 under protanopia and sits outside the dark lightness band, so
// the chart uses a slightly deeper step of the same hue. And the obvious
// blue/violet pairing for Grok and Kimi collapses to dE 1.9 -- indistinguishable
// -- which is why Kimi is magenta.
//
// Colour is never the only channel regardless: every segment carries a 2px gap in
// the surface colour, every legend row states its own name, and the breakdown
// table repeats every number in text.

export interface AgentColor {
  name: string
  color: string
}

// Fixed order, and that is a correctness rule rather than tidiness. Segments are
// stacked and legends listed in THIS order, never by size, so that a quiet week
// for Claude cannot reshuffle the stack and repaint what the reader had already
// learned. Colour follows the agent, never its rank.
const PALETTE: AgentColor[] = [
  { name: 'Claude', color: '#d95926' },
  { name: 'Codex', color: '#199e70' },
  { name: 'Grok', color: '#3987e5' },
  { name: 'Kimi', color: '#d55181' },
]

// "Other" is the residual bucket the server puts an unrecognised model family in.
// It is deliberately a neutral rather than a fifth hue: it is not an identity, it
// is the absence of one, and giving it a colour would say kunai knows something
// about it that it does not.
const OTHER = '#82838a'

export const AGENT_ORDER: string[] = [...PALETTE.map((p) => p.name), 'Other']

/** The colour standing for an agent family, anywhere on the page. */
export function agentColor(name: string): string {
  return PALETTE.find((p) => p.name === name)?.color ?? OTHER
}

/** Position in the fixed order; unknown names sort last, beside Other. */
export function agentRank(name: string): number {
  const i = AGENT_ORDER.indexOf(name)
  return i < 0 ? AGENT_ORDER.length : i
}
