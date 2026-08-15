// Fetching a review's draft.
//
// Through the shared cache rather than a bare fetch in an effect, and the
// difference is visible rather than theoretical. The draft is read on mount, at
// the end of every phase while the review runs, and again every time you come
// back to it, so three properties matter and a raw fetch has none of them:
//
//   - **Deduped in flight.** Two reads that overlap share one request. The view
//     re-reads whenever the session goes idle, and a phase ending is exactly
//     when the session list poll also fires.
//   - **Cached by key.** Leaving the review and returning repaints from what is
//     in hand instead of blanking to a skeleton and asking again.
//   - **pending is distinguishable from stale.** A skeleton belongs on "nothing
//     to show yet" and never on "showing last second's answer while a refresh is
//     out behind it", which is what makes a refresh flicker.
//
// The load itself never throws: the error lands on the cache entry and the view
// reads it, so no call site repeats the same try/catch.

import { reviewDraft, type ReviewDraft } from './api'
import { Resource, keys as sharedKeys } from './query.svelte'

// A review's draft goes stale quickly while it is running (each phase adds
// findings) and not at all once it is done. One short TTL covers both, because
// a finished review is only re-read when something asks, and the cache makes
// that free.
const DRAFT_TTL = 4_000

export const reviewKeys = {
  draft: (base: string, sessionId: string) => `review:draft:${base}:${sessionId}`,
}

/** The draft for one review session, cached and shared. */
export class DraftResource extends Resource<ReviewDraft> {
  constructor() {
    super(DRAFT_TTL)
  }

  /**
   * Read the draft for a session.
   *
   * `force` is for the moments the cache cannot know about: a phase just ended,
   * or the review was posted and the record changed underneath us.
   */
  read(base: string, sessionId: string, opts: { force?: boolean } = {}) {
    if (!sessionId) return Promise.resolve()
    return this.load(reviewKeys.draft(base, sessionId), () => reviewDraft(base, sessionId), opts)
  }
}

// Re-exported so a caller needs one import for the whole data layer of this
// screen rather than reaching into two modules for halves of the same idea.
export { sharedKeys }
