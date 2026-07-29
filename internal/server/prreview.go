package server

// Reviewing a pull request: the one place a review is born.
//
// Structurally this mirrors channelsessions.go. A review is not a subsystem with
// its own idea of how an agent runs; it is an ORDINARY SESSION, created through
// the same machinery as any other, which is why it appears in the sidebar, speaks
// over the same socket, and can be interrupted and argued with once the findings
// are in. Everything specific to reviewing lives in what it is given: a detached
// worktree at the pull request's head, a prompt (internal/review), and a toolset
// narrowed when the code came from a fork.
//
// Nothing here polls. A review happens because a person clicked, which is what
// keeps this small: no schedule, no webhook, no coordination between machines
// beyond asking GitHub whether this commit has already been reviewed.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hegade/kunai/internal/ghapp"
	"github.com/hegade/kunai/internal/review"
	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/worktree"
)

// forkToolset is withheld from a session reviewing code from a fork.
//
// Reading is untouched: Read, Grep and Glob are how a review earns its keep, and
// none of them execute anything. What goes is the ability to run commands and to
// write, because the diff was authored by somebody who cannot push to this
// repository and the agent is about to read all of it. Tests do not run on a fork
// PR, and that is the trade being made deliberately.
var forkToolset = []string{"Bash", "Write", "Edit", "MultiEdit", "NotebookEdit", "Task"}

// trustedToolset is withheld even on your own team's pull requests. A review
// reads and reports; it has no reason to edit the checkout it is reading, and a
// review that modifies files would make its own findings unreproducible.
var trustedToolset = []string{"Write", "Edit", "MultiEdit", "NotebookEdit"}

// startReview creates the worktree and the session for one pull request review.
func (s *Server) startReview(ctx context.Context, repoDir string, number int, requester string) (*session.Session, error) {
	app, err := s.githubApp()
	if err != nil {
		return nil, err
	}
	repo, err := s.repoAt(repoDir)
	if err != nil {
		return nil, err
	}

	pr, err := app.PullRequest(ctx, repo, number)
	if err != nil {
		return nil, err
	}
	files, err := app.PullRequestFiles(ctx, repo, number)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s#%d changes nothing that can be reviewed", repo, number)
	}

	// Fetched from the base repository's refs/pull/<n>/head, which is the one
	// place a fork's commits are reachable without adding a remote.
	ref := fmt.Sprintf("refs/pull/%d/head", number)
	if err := worktree.FetchRef(repoDir, "origin", ref); err != nil {
		return nil, err
	}
	sha, err := worktree.ResolveRef(repoDir, "refs/kunai/pull/"+fmt.Sprint(number)+"/head")
	if err != nil {
		return nil, fmt.Errorf("could not resolve the head of %s#%d after fetching it: %w", repo, number, err)
	}
	if pr.Head.SHA != "" && !strings.EqualFold(sha, pr.Head.SHA) {
		// The pull request moved between reading it and fetching it. Reviewing the
		// commit we actually have is correct; recording that commit is what keeps
		// the eventual comments attached to the code that was read.
		log.Printf("pr review: %s#%d moved while fetching (%s -> %s); reviewing what was fetched", repo, number, pr.Head.SHA, sha)
	}

	wt, err := worktree.CreateReview(worktree.ReviewOptions{
		Repo: repoDir, Root: s.worktreeRoot(), Name: fmt.Sprint(number), SHA: sha,
	})
	if err != nil {
		return nil, err
	}

	fromFork := pr.FromFork(repo)
	prompt := review.Prompt(review.Request{
		Repo: repo.String(), Number: number, Title: pr.Title, Author: pr.User.Login,
		BaseRef: pr.Base.Ref, HeadSHA: sha, FromFork: fromFork,
		Diff:       unifiedDiff(files),
		Files:      filenames(files),
		PriorNotes: priorNotes(ctx, app, repo, number),
	})

	cli := s.resolveCLI(s.reviewAccount())
	sess, err := s.mgr.Create(ctx, session.CreateOptions{
		Cwd:     wt.Path,
		Title:   fmt.Sprintf("Review #%d %s", number, pr.Title),
		Model:   s.model(),
		Effort:  s.effort(),
		CLIName: cli.Name, Bin: cli.Bin, Env: cli.effectiveEnv(),
		// A review never needs to approve a tool call: everything it may do is
		// safe by construction once the dangerous tools are withheld, and stopping
		// to ask would strand a review nobody is watching.
		Mode:            session.LoopPermissionMode,
		DisallowedTools: toolsetFor(fromFork),
	})
	if err != nil {
		_ = worktree.RemoveReview(wt)
		return nil, err
	}
	s.armSession(sess)

	if s.prReviews != nil {
		s.prReviews.put(prReview{
			SessionID: sess.ID, Owner: repo.Owner, Repo: repo.Name, Number: number,
			Title: pr.Title, HeadSHA: sha, FromFork: fromFork,
			Requester: requester, CreatedAt: time.Now(),
		})
	}
	// The draft is collected off the request path: the caller gets its session id
	// immediately and watches the review happen, exactly like any other session.
	go s.collectDraft(sess, files)

	if err := sess.Prompt(prompt, nil, nil); err != nil {
		return nil, fmt.Errorf("the review session was created but would not start: %w", err)
	}
	return sess, nil
}

// worktreeRoot is where review checkouts are made, shared with the worktree
// store so one repository's reviews and its work sit under the same directory.
func (s *Server) worktreeRoot() string {
	if s.worktrees == nil {
		return ""
	}
	return s.worktrees.root
}

// reviewAccount is which account a review runs on. Empty means the default, and
// it is a seam rather than a setting today: reviews are chunky, so pointing them
// at a second account or a provider is the obvious next control, and having the
// call site already ask for one means adding it does not touch this file.
func (s *Server) reviewAccount() string { return "" }

// toolsetFor is the trust decision, in one place so it cannot be made twice and
// differently.
func toolsetFor(fromFork bool) []string {
	if fromFork {
		return forkToolset
	}
	return trustedToolset
}

// collectDraft waits for the review turn to finish and saves what it produced.
//
// Done by watching the session rather than by hooking it, because a session has
// exactly one turn-end hook and auto-failover already owns it. Subscribing is
// also honest about what this is: another reader of the conversation, with no
// power to change what the session does.
func (s *Server) collectDraft(sess *session.Session, files []ghapp.FileDiff) {
	_, backlog, sub := sess.Attach(0)
	defer sess.Detach(sub)

	var text strings.Builder
	consider := func(ev session.AppEvent) bool {
		switch ev.T {
		case session.EvAssistant:
			if ev.Text != "" {
				text.WriteString(ev.Text)
				text.WriteString("\n")
			}
		case session.EvResult:
			return true
		}
		return false
	}
	for _, ev := range backlog {
		if consider(ev) {
			s.saveDraft(sess.ID, text.String(), files)
			return
		}
	}
	for ev := range sub.Events() {
		if consider(ev) {
			s.saveDraft(sess.ID, text.String(), files)
			return
		}
	}
	// The channel closed without a result: the session went away mid-review, and
	// there is nothing to save. The record stays, so the UI can say the review did
	// not finish rather than showing an empty draft for ever.
}

// saveDraft parses the agent's answer and records it against the session.
func (s *Server) saveDraft(sessionID, text string, files []ghapp.FileDiff) {
	if s.prReviews == nil {
		return
	}
	draft, err := review.Parse(text)
	if err != nil {
		s.prReviews.update(sessionID, func(rec *prReview) {
			rec.ParseError = err.Error()
		})
		log.Printf("pr review: session %s produced no usable review block: %v", sessionID, err)
		return
	}
	// Placement is decided now, while the diff that decided it is in hand, so the
	// draft the user reads is already the truth about where each finding lands.
	plan := review.Build(draft, review.ParseDiff(toReviewFiles(files)))
	total, inline, summary := plan.Counts()
	s.prReviews.update(sessionID, func(rec *prReview) {
		rec.Draft, rec.ParseError = &draft, ""
	})
	log.Printf("pr review: session %s drafted %d finding(s), %d inline and %d in the summary", sessionID, total, inline, summary)
}

// repoAt reads the GitHub repository a local checkout belongs to.
func (s *Server) repoAt(dir string) (ghapp.Repo, error) {
	root, err := worktree.Root(dir)
	if err != nil {
		return ghapp.Repo{}, fmt.Errorf("%s is not a git repository", dir)
	}
	remote, err := worktree.RemoteURL(root, "origin")
	if err != nil {
		return ghapp.Repo{}, err
	}
	return ghapp.ParseRemote(remote)
}

// githubApp returns the configured App, or an error saying what is missing.
func (s *Server) githubApp() (*ghapp.App, error) {
	s.ghMu.Lock()
	defer s.ghMu.Unlock()
	if s.gh != nil {
		return s.gh, nil
	}
	creds, err := loadGitHubCredentials(s.cfg.DataDir)
	if err != nil {
		if errors.Is(err, ghapp.ErrNoCredentials) {
			return nil, fmt.Errorf("kunai has no GitHub App on this machine yet: add one in Settings to review pull requests")
		}
		return nil, err
	}
	s.gh = ghapp.New(creds)
	return s.gh, nil
}

// priorNotes gathers what humans have already said, so a review adds to the
// conversation instead of restating it. Best effort: a review is still worth
// running when this cannot be read.
func priorNotes(ctx context.Context, app *ghapp.App, repo ghapp.Repo, number int) []string {
	reviews, err := app.Reviews(ctx, repo, number)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range reviews {
		if r.ByBot() || strings.TrimSpace(r.Body) == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s", r.User.Login, collapse(r.Body)))
	}
	return out
}

// collapse flattens a comment to one line and caps it, because these are context
// for the prompt rather than the material being reviewed.
func collapse(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}

// unifiedDiff stitches the per-file patches back into one diff for the prompt.
// The agent cannot run `git diff` on a fork PR (no Bash), so kunai hands it over.
func unifiedDiff(files []ghapp.FileDiff) string {
	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", f.Filename, f.Filename)
		if f.Patch == "" {
			fmt.Fprintf(&b, "(%s, no textual diff available)\n", f.Status)
			continue
		}
		b.WriteString(f.Patch)
		b.WriteString("\n")
	}
	return b.String()
}

func filenames(files []ghapp.FileDiff) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Filename)
	}
	return out
}

// toReviewFiles converts the wire shape to the logic package's. The duplication
// is deliberate: internal/review must not depend on GitHub's JSON.
func toReviewFiles(files []ghapp.FileDiff) []review.FileDiff {
	out := make([]review.FileDiff, 0, len(files))
	for _, f := range files {
		out = append(out, review.FileDiff{Filename: f.Filename, Status: f.Status, Patch: f.Patch})
	}
	return out
}
