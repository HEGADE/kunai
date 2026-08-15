package server

// What a review session is a review OF.
//
// The review itself lives in the transcript, which is exactly where it should be:
// it is a conversation, it survives a restart, and reopening the session shows it
// again. But the transcript cannot answer the one question posting needs, which is
// which pull request and WHICH COMMIT these findings belong to. Without that
// recorded separately, closing the tab makes an unposted review unpostable.
//
// The commit is the load-bearing half. A review is of a specific head SHA, and by
// the time you press Post your colleague may have pushed again. Posting findings
// about code that has moved would put comments on lines nobody wrote, so the SHA
// travels with the draft and posting checks it.
//
// One JSON file beside sessionmeta.json, same atomic-write idiom.

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/review"
)

// prReview binds a session to the pull request it is reviewing.
type prReview struct {
	SessionID string `json:"session_id"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	Number    int    `json:"number"`
	Title     string `json:"title"`
	// Worktree is the throwaway checkout this review reads, kept so it can be
	// swept later. Cleared once the directory is gone.
	Worktree string `json:"worktree,omitempty"`
	// RepoDir is the local checkout this review was started from.
	//
	// Recorded because a review runs in a detached worktree that is deliberately
	// not registered as work in progress, so nothing else can say which repository
	// its session belongs to. Without it the session's directory
	// (.../worktrees/kunai/review/4) was taken for a repository of its own, and
	// the dashboard listed the same pull requests twice: once under "kunai" and
	// once under a phantom repo called "4".
	RepoDir string `json:"repo_dir,omitempty"`
	// BaseRef is the branch this merges into, so the header can say nightly->main
	// rather than just naming the pull request.
	BaseRef string `json:"base_ref,omitempty"`
	// HeadSHA is the commit that was read. Findings are only ever posted against
	// this, never against whatever the head has become.
	HeadSHA string `json:"head_sha"`
	// FromFork records the trust decision made at creation, so posting and the UI
	// do not have to re-derive it (and cannot re-derive it differently).
	FromFork bool `json:"from_fork"`
	// Requester is who clicked Review, named in the posted review because one bot
	// identity is shared across a team.
	Requester string `json:"requester,omitempty"`
	// Draft is the parsed findings, saved when the review turn ends. Absent until
	// then, which is how the UI tells "still working" from "nothing found".
	Draft *review.Draft `json:"draft,omitempty"`
	// Phase is how far the review got: survey, find, verify or done. Recorded so
	// the view can say what is happening rather than showing an unexplained
	// several-minute wait, which on a phased review is longer than it was.
	Phase string `json:"phase,omitempty"`
	// Survey is what the first phase concluded: what the change is FOR and where
	// the risk sits.
	//
	// Recorded, where it used to be computed and thrown away as soon as the find
	// prompt had been built from it. It is the only account of what the reviewer
	// decided to look at, which makes it both the most interesting thing to read
	// during the several minutes finding takes and the thing to argue with when a
	// review comes back having looked in the wrong place.
	Survey *review.Survey `json:"survey,omitempty"`
	// Files is the change being reviewed, so a screen somebody watches for
	// minutes can say what is under review rather than nothing at all.
	Files []review.FileSummary `json:"files,omitempty"`
	// Timeline is when each phase began, appended as they start.
	//
	// A phased review is minutes of silence, and "how long has it been reading"
	// is a different question from "how long has it been going". Without this the
	// only clock available is the running turn's, which restarts at every phase.
	Timeline []phaseStart `json:"timeline,omitempty"`
	// Surveyed records whether this review has a survey phase at all, which a
	// small change skips.
	//
	// Recorded rather than inferred, because it cannot be inferred: once a review
	// is in `find` there is nothing in the record that says whether a survey ran
	// before it, so a progress display had to either invent a step that will
	// never light or claim a step ran that never did. One bool is cheaper than
	// either lie.
	Surveyed bool `json:"surveyed,omitempty"`
	// Dropped are the candidates the verification pass refuted, with its reason.
	//
	// Kept deliberately. A reviewer you can audit is one you will trust: showing
	// "4 considered and dropped" with the reasons behind a click is what says the
	// filtering is real, where silently presenting three findings looks exactly
	// like a reviewer that only managed to find three.
	Dropped []review.Dropped `json:"dropped,omitempty"`
	// ParseError explains a reply that carried no usable review block, so the UI
	// can say what happened instead of showing an empty draft for ever.
	ParseError string `json:"parse_error,omitempty"`
	// PostedURL is set once the review is on GitHub, which is also what stops it
	// being posted twice from the same draft.
	PostedURL string    `json:"posted_url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// phaseStart is one entry in a review's timeline.
type phaseStart struct {
	Phase string    `json:"phase"`
	At    time.Time `json:"at"`
}

// Posted reports that this draft has already been sent.
func (p prReview) Posted() bool { return p.PostedURL != "" }

// beganPhase appends a phase to the timeline, ignoring a repeat.
//
// A repair keeps the review in the same phase and asks again, so without this a
// review that stumbled once would show the same step twice and the elapsed time
// for it would restart.
func (p *prReview) beganPhase(phase string, at time.Time) {
	if n := len(p.Timeline); n > 0 && p.Timeline[n-1].Phase == phase {
		return
	}
	p.Timeline = append(p.Timeline, phaseStart{Phase: phase, At: at})
}

type prReviewStore struct {
	mu   sync.Mutex
	path string
	data map[string]prReview // by session id
}

func newPRReviewStore(path string) *prReviewStore {
	s := &prReviewStore{path: path, data: map[string]prReview{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.data)
		if s.data == nil {
			s.data = map[string]prReview{}
		}
	}
	return s
}

// repoOf returns the local repository a review session belongs to, or "".
// Cheap by design: an in-memory lookup, because it is consulted for every
// session on every listing.
func (s *prReviewStore) repoOf(sessionID string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[sessionID].RepoDir
}

func (s *prReviewStore) get(sessionID string) (prReview, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.data[sessionID]
	return rec, ok
}

func (s *prReviewStore) put(rec prReview) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[rec.SessionID] = rec
	s.saveLocked()
}

// update applies a change to an existing record, and reports whether there was
// one. Taking a function keeps read-modify-write under the one lock, so a draft
// arriving while a post is in flight cannot overwrite the posted URL.
func (s *prReviewStore) update(sessionID string, fn func(*prReview)) (prReview, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.data[sessionID]
	if !ok {
		return prReview{}, false
	}
	fn(&rec)
	s.data[sessionID] = rec
	s.saveLocked()
	return rec, true
}

// all returns a copy of every record, for the sweep.
func (s *prReviewStore) all() []prReview {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]prReview, 0, len(s.data))
	for _, rec := range s.data {
		out = append(out, rec)
	}
	return out
}

// latestFor is the most recent review this machine holds for one pull request.
//
// It exists so the dashboard can tell the truth after a reload. The row used to
// know about a review only from state held in the component that started it, so
// navigating away and back, or simply refreshing, made a running review vanish
// from the row and offer "Review" again -- which on a finished review now starts
// a whole second one and spends real quota, because the button looked untouched.
//
// Newest wins: a pull request reviewed at three commits has three records, and
// the one worth reporting is the last reading.
func (s *prReviewStore) latestFor(owner, repo string, number int) (prReview, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best prReview
	found := false
	for _, rec := range s.data {
		if rec.Number != number || !strings.EqualFold(rec.Owner, owner) || !strings.EqualFold(rec.Repo, repo) {
			continue
		}
		if !found || rec.CreatedAt.After(best.CreatedAt) {
			best, found = rec, true
		}
	}
	return best, found
}

func (s *prReviewStore) delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[sessionID]; ok {
		delete(s.data, sessionID)
		s.saveLocked()
	}
}

func (s *prReviewStore) saveLocked() {
	if s.path == "" {
		return
	}
	b, err := json.Marshal(s.data)
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
}
