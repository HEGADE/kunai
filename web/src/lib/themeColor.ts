// The colour a mobile browser paints its own chrome with: the status bar behind
// the notch, and the address bar below. It follows the <meta name="theme-color">
// tag live, so it can track what is actually at the top of the screen instead of
// being fixed at build time.
//
// This matters on the nightly channel, where the sidebar header is a night-sky
// purple. Left at the app background, the status bar sat as a black band above a
// purple header, which reads as a rendering fault rather than a design.

// appTop is the ordinary top of the app: the canvas colour, matching --bg.
export const appTop = '#0b0b0c'

// nightlyTop is the nightly header's top edge, sampled from the rendered
// gradient rather than picked from its stops.
//
// The gradient runs across the header, from #2f2756 on the left to #231d41 on
// the right, and a status bar is one flat colour, so no value can match all of
// it. This is the average, which puts the seam where it is least visible rather
// than matching one end and being obviously wrong at the other.
export const nightlyTop = '#2a234e'

// themeColorFor is the rule, kept pure so it can be reasoned about without a DOM.
//
// On a phone an open session covers the screen, and its header is the ordinary
// canvas: the sidebar's purple is nowhere in sight. Tinting the status bar for it
// then would be worse than not tinting at all, because the colour would belong to
// something the user cannot see.
export function themeColorFor(opts: { nightly: boolean; chatOpen: boolean }): string {
  return opts.nightly && !opts.chatOpen ? nightlyTop : appTop
}

// applyThemeColor writes the value into the document, creating the tag if a
// document somehow lacks it. Idempotent, so it is safe to call from an effect
// that runs on every state change.
export function applyThemeColor(color: string): void {
  if (typeof document === 'undefined') return
  let tag = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
  if (!tag) {
    tag = document.createElement('meta')
    tag.name = 'theme-color'
    document.head.appendChild(tag)
  }
  if (tag.content !== color) {
    tag.content = color
  }
}
