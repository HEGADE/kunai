package server

// The diff a review is placed against, remembered.
//
// Opening a finished review was slow, and the reason was not the parsing: every
// read of the draft called GitHub for the pull request's files. That is one
// round trip per open, per poll, and a phased review polls the draft at the end
// of every phase, so a review nobody was even watching spent somebody's rate
// limit and a reader watching one waited on github.com to see findings that had
// been computed and saved minutes earlier.
//
// The fix rests on one fact: **a commit's diff never changes.** Keyed by the
// SHA, this can be cached without a staleness question, which is why the key is
// the commit and not the pull request. Posting still asks for the CURRENT head's
// files and gets its own entry under that SHA, so re-anchoring after a push is
// unaffected.

import (
	"context"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/ghapp"
	"github.com/hegade/kunai/internal/review"
)

const (
	// diffCacheMax is how many commits' diffs are kept. Small: a machine reviews
	// a handful of pull requests at a time and each entry is a whole diff.
	diffCacheMax = 12
	// diffCacheTTL bounds the memory rather than the correctness, since the
	// content cannot go stale. Long enough to cover reading a review.
	diffCacheTTL = 30 * time.Minute
)

type diffEntry struct {
	files []review.FileDiff
	at    time.Time
}

type diffCache struct {
	mu sync.Mutex
	m  map[string]diffEntry
}

func newDiffCache() *diffCache { return &diffCache{m: map[string]diffEntry{}} }

// filesAt returns a commit's changed files, asking GitHub only the first time.
//
// A failure is NOT cached: the next open should try again rather than inherit a
// blip for half an hour, and the callers all degrade gracefully on nil.
func (s *Server) filesAt(ctx context.Context, repo ghapp.Repo, number int, sha string) []review.FileDiff {
	key := repo.Owner + "/" + repo.Name + "#" + itoa(number) + "@" + sha
	if c := s.diffs; c != nil && sha != "" {
		c.mu.Lock()
		e, ok := c.m[key]
		c.mu.Unlock()
		if ok && time.Since(e.at) < diffCacheTTL {
			return e.files
		}
	}

	app, err := s.githubApp()
	if err != nil {
		return nil
	}
	raw, err := app.PullRequestFiles(ctx, repo, number)
	if err != nil {
		return nil
	}
	files := toReviewFiles(raw)

	if c := s.diffs; c != nil && sha != "" {
		c.mu.Lock()
		// Oldest out when full. A map scan rather than a real LRU because the cap
		// is twelve: the bookkeeping would cost more than the scan.
		if len(c.m) >= diffCacheMax {
			oldest, at := "", time.Now()
			for k, v := range c.m {
				if v.at.Before(at) {
					oldest, at = k, v.at
				}
			}
			delete(c.m, oldest)
		}
		c.m[key] = diffEntry{files: files, at: time.Now()}
		c.mu.Unlock()
	}
	return files
}
