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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hegade/kunai/internal/ghapp"
	"github.com/hegade/kunai/internal/review"
	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/worktree"
)

// reviewToolset is withheld from every review, its own pull request or a
// stranger's.
//
// Reading is untouched, and reading is the whole job: Read, Grep and Glob are how
// a review earns its keep over a diff read in isolation, and none of them execute
// anything or need permission.
//
// Bash is withheld even on your own team's code, which is a change of mind and
// worth recording. It was allowed there so a review could run the tests, which is
// a real gain in quality. But a permission mode that runs safe work still stops
// to ask about a risky command, and a review is watched by nobody by design: the
// dashboard sends you away from it deliberately. So the first unusual command
// parked the whole review on a question nobody was there to answer, and the view
// reported "nothing worth reporting" because no findings had arrived. A reviewer
// that hangs silently is worth less than one that cannot run tests, and this is
// the same trade the loop already makes when it borrows acceptEdits.
//
// The happy consequence is that the toolset no longer depends on trust: a fork
// and your own branch get exactly the same one, so there is no second list to
// keep in step and no way to hand a stranger's diff more than it should have.
//
// Task is deliberately NOT withheld any more, and it is what makes the
// verification phase worth having. A subagent starts with a fresh context: it
// sees the claim it was given and nothing of the reasoning that produced it,
// which is precisely the independence the phase exists to buy, and it gets that
// independence in parallel and for free. Running the checks in this session
// instead would mean asking the model that just wrote the findings whether the
// findings are right, which is the failure the phase was added to fix. A
// subagent inherits these same restrictions, so it can read and nothing else.
var reviewToolset = []string{"Bash", "Write", "Edit", "MultiEdit", "NotebookEdit"}

// reviewToolsOwner marks a review's restriction as the review's own, so nothing
// else lifts it. See session.CreateOptions.ToolsOwner.
const reviewToolsOwner = "pr-review"

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

	// A review already running for this pull request is handed back rather than
	// joined by a second one. Clicking Review twice, or clicking it again after a
	// push moved the head, otherwise started another whole run: two sessions with
	// the same name in the sidebar, two worktrees, two lots of quota, and two
	// drafts of which only one can ever be posted. Bounded to a LIVE session, so
	// a finished review never blocks re-reviewing at a new commit.
	if s.prReviews != nil {
		for _, rec := range s.prReviews.all() {
			if rec.Number != number || !strings.EqualFold(rec.Owner, repo.Owner) || !strings.EqualFold(rec.Repo, repo.Name) {
				continue
			}
			if rec.Posted() {
				continue
			}
			if sess, live := s.mgr.Get(rec.SessionID); live {
				log.Printf("pr review: %s#%d is already being reviewed in session %s; reusing it", repo, number, rec.SessionID)
				return sess, nil
			}
		}
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
	// The repository's main checkout, not the directory the request named, which
	// may itself have been a worktree. This is what the review session reports as
	// its repo so it groups under the codebase rather than under its own
	// throwaway directory.
	repoRoot := wt.Repo

	fromFork := pr.FromFork(repo)
	// Written into the worktree rather than pasted into the prompt. A large pull
	// request inlined costs its entire diff in tokens before the model has decided
	// anything is worth reading; on disk it reads the parts that matter and skips
	// the lockfile. Read reaches it on a fork's review too, where Bash does not.
	diffPath, err := writeDiff(wt.Path, number, files)
	if err != nil {
		_ = worktree.RemoveReview(wt)
		return nil, err
	}
	// The review as a sequence of phases rather than one question. NewRun decides
	// for itself whether this change is big enough to be worth surveying first.
	run := review.NewRun(review.Request{
		Repo: repo.String(), Number: number, Title: pr.Title, Author: pr.User.Login,
		BaseRef: pr.Base.Ref, HeadSHA: sha, FromFork: fromFork,
		DiffPath:   diffPath,
		Files:      fileSummaries(files),
		PriorNotes: priorNotes(ctx, app, repo, number),
	})

	// The account and model reviews run on, which is deliberately its own choice:
	// a review is chunky and unattended, so spending the window you are working in
	// is the wrong default once you review more than occasionally.
	rc := s.reviewCfg.get()
	cli := s.resolveCLI(rc.CLI)
	model := rc.Model
	if model == "" {
		model = s.model()
	}
	sess, err := s.mgr.Create(ctx, session.CreateOptions{
		Cwd:     wt.Path,
		Title:   fmt.Sprintf("Review #%d %s", number, pr.Title),
		Model:   model,
		Effort:  s.effort(),
		CLIName: cli.Name, Bin: cli.Bin, Env: cli.effectiveEnv(),
		// A review never needs to approve a tool call: everything it may do is
		// safe by construction once the dangerous tools are withheld, and stopping
		// to ask would strand a review nobody is watching.
		Mode:            session.LoopPermissionMode,
		DisallowedTools: reviewToolset,
		// Claimed, so the share reconciler leaves it alone. Without this it read a
		// review's withheld tools as an expired share and respawned the session
		// about a minute in, which ended the running turn and looked from the
		// outside like the review stopping by itself.
		ToolsOwner: reviewToolsOwner,
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
			RepoDir: repoRoot, Worktree: wt.Path,
			Requester: requester, CreatedAt: time.Now(),
			Phase: string(run.Phase),
		})
	}
	s.reviewRuns.put(sess.ID, &reviewRun{run: run, files: files})

	// Collected from inside the session rather than by subscribing to it. A
	// subscriber is dropped when its buffer fills (emitLocked), which is right for
	// a phone that cannot keep up and fatal here: a review streams for minutes, the
	// watcher was routinely dropped part-way, and it then saved nothing and logged
	// nothing because from its side the conversation had simply ended. Three real
	// reviews produced neither a draft nor a parse error before this was found.
	//
	// Every turn now feeds the phase machine, which asks the next question or
	// finishes. See prreviewrun.go.
	sess.SetAnswerHook(func(text string) { s.advanceReview(sess.ID, text) })

	// Sent as a brief rather than as a prompt: the instructions and the whole diff
	// are context the model must read, not something the user said, and printing
	// them into the chat buried the review under several screens of schema.
	prompt, brief, ok := run.Next()
	if !ok {
		return nil, fmt.Errorf("internal: a new review had nothing to ask")
	}
	if err := sess.PromptBrief(prompt, brief); err != nil {
		return nil, fmt.Errorf("the review session was created but would not start: %w", err)
	}
	return sess, nil
}

// tagReviewRepos points a review session at the repository it is reviewing.
//
// A review runs in a detached worktree that is deliberately NOT registered as
// work in progress, so nothing else can say which codebase it belongs to. Left
// untagged, its directory (.../worktrees/kunai/review/4) was taken for a
// repository in its own right: the sidebar gave it a heading called "4", and the
// dashboard listed every pull request twice, once under the real repo and once
// under the phantom. An in-memory lookup, because this runs for every session on
// every listing.
func (s *Server) tagReviewRepos(metas []session.Meta) {
	if s.prReviews == nil {
		return
	}
	for i := range metas {
		if metas[i].Repo != "" {
			continue // already known to be a worktree of something
		}
		if dir := s.prReviews.repoOf(metas[i].ID); dir != "" {
			metas[i].Repo = dir
		}
	}
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

// reviewDiffDir is where a review's diff is written inside its worktree. Dotted
// and namespaced so it is obviously kunai's and obviously not part of the change
// being reviewed; the whole checkout is thrown away afterwards regardless.
const reviewDiffDir = ".kunai-review"

// writeDiff stitches the per-file patches into one diff on disk and returns its
// path, relative to the worktree so the prompt can name it the way the agent
// will type it.
func writeDiff(worktreePath string, number int, files []ghapp.FileDiff) (string, error) {
	dir := filepath.Join(worktreePath, reviewDiffDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("could not prepare the review diff: %w", err)
	}
	name := fmt.Sprintf("pr-%d.diff", number)

	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", f.Filename, f.Filename)
		if f.Patch == "" {
			// Binary, or too large for GitHub to render. Said plainly so the model
			// knows the file changed and that there is nothing here to read.
			fmt.Fprintf(&b, "(%s, no textual diff available)\n", f.Status)
			continue
		}
		b.WriteString(f.Patch)
		b.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("could not write the review diff: %w", err)
	}
	return filepath.Join(reviewDiffDir, name), nil
}

// fileSummaries is the orientation list: what changed and how much, which is how
// the model decides what to open first.
func fileSummaries(files []ghapp.FileDiff) []review.FileSummary {
	out := make([]review.FileSummary, 0, len(files))
	for _, f := range files {
		out = append(out, review.FileSummary{
			Path: f.Filename, Status: f.Status,
			Additions: f.Additions, Deletions: f.Deletions,
		})
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
