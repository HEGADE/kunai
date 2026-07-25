package server

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/worktree"
)

// The worktree store annotates git with what git cannot know: which base a
// worktree was made from, how its setup command went, and which of its files are
// symlinks back into the main checkout. git remains the source of truth for what
// worktrees exist, exactly as it is for checkpoints; this file is the annotation
// and is safe to lose.
//
// Which sessions are using a worktree is deliberately NOT persisted. It is
// derived from the live session list on every read, because a persisted copy
// goes stale the moment a session ends and the one thing this list is used for
// is refusing to delete a worktree somebody is working in.

// worktreeRecord is one worktree kunai made.
type worktreeRecord struct {
	worktree.Info
	CreatedAt int64                `json:"created_at"`
	Setup     worktree.SetupResult `json:"setup"`
	Shared    []string             `json:"shared,omitempty"`
	// Removed marks a worktree that has been deleted. The record outlives it so
	// the sessions that ran there still know which repository they belonged to;
	// their transcripts outlive the worktree and nothing else records that.
	Removed bool `json:"removed,omitempty"`
	// ready is closed once setup has finished, so a session create can wait for a
	// worktree to be fit to work in rather than starting an agent against a
	// half-installed tree. Nil for a record loaded from disk, whose setup is over.
	ready chan struct{}
}

// removedKeep bounds how many deleted worktrees are remembered for the sake of
// grouping their past sessions.
const removedKeep = 100

// repoConfig is the per-repository configuration kunai keeps on this machine. It
// is the fallback for a repository that has no checked-in kunai.json, so nothing
// has to be written into someone else's project to use the feature.
type repoConfig struct {
	Setup string `json:"setup,omitempty"`
}

// worktreeSettings are the machine-wide preferences.
type worktreeSettings struct {
	// FromOrigin starts new work from origin/<base> rather than the local ref,
	// so it does not begin on a branch that is weeks behind. Default true.
	FromOrigin bool `json:"from_origin"`
}

func defaultWorktreeSettings() worktreeSettings {
	return worktreeSettings{FromOrigin: true}
}

// worktreeFile is the on-disk shape: preferences, per-repo setup, and the
// records, in one file with one section each.
//
// Settings is a pointer so an absent section keeps the defaults. Decoding into a
// value would read a missing section as the zero value and quietly turn
// FromOrigin off, which is the sort of default-flip nobody notices until new work
// starts from a stale branch.
type worktreeFile struct {
	Settings  *worktreeSettings          `json:"settings,omitempty"`
	Repos     map[string]repoConfig      `json:"repos,omitempty"`
	Worktrees map[string]*worktreeRecord `json:"worktrees,omitempty"`
}

type worktreeStore struct {
	mu       sync.Mutex
	path     string // worktrees.json
	root     string // where worktrees are created
	settings worktreeSettings
	repos    map[string]repoConfig
	records  map[string]*worktreeRecord

	// live reports the sessions currently running, so the store can say which
	// worktrees are in use without importing the manager's whole surface.
	live func() []session.Meta
}

func newWorktreeStore(dataDir string, live func() []session.Meta) *worktreeStore {
	s := &worktreeStore{
		path:     filepath.Join(dataDir, "worktrees.json"),
		root:     filepath.Join(dataDir, "worktrees"),
		settings: defaultWorktreeSettings(),
		repos:    map[string]repoConfig{},
		records:  map[string]*worktreeRecord{},
		live:     live,
	}
	if dataDir == "" {
		return s
	}
	s.load()
	return s
}

func (s *worktreeStore) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var f worktreeFile
	if json.Unmarshal(b, &f) != nil {
		return
	}
	if f.Settings != nil {
		s.settings = *f.Settings
	}
	if f.Repos != nil {
		s.repos = f.Repos
	}
	if f.Worktrees != nil {
		s.records = f.Worktrees
	}
}

// save writes the file. The caller holds the lock.
func (s *worktreeStore) saveLocked() {
	if s.path == "" {
		return
	}
	b, err := json.MarshalIndent(worktreeFile{
		Settings:  &s.settings,
		Repos:     s.repos,
		Worktrees: s.records,
	}, "", "  ")
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

// --- configuration ------------------------------------------------------------

func (s *worktreeStore) getSettings() worktreeSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *worktreeStore) setSettings(v worktreeSettings) {
	s.mu.Lock()
	s.settings = v
	s.saveLocked()
	s.mu.Unlock()
}

// setupFor resolves the setup command for a repository. The repository's own
// checked-in kunai.json wins, because that is the one that travels with the
// project; this machine's stored override is the fallback for a repository you
// do not want to write a file into. A suggestion is only ever returned as a
// suggestion, never run without being shown.
func (s *worktreeStore) setupFor(repo string) worktree.SetupProposal {
	if p := worktree.ProposeSetup(repo); p.Source == worktree.SourceProject {
		return p
	}
	s.mu.Lock()
	cfg, ok := s.repos[repo]
	s.mu.Unlock()
	if ok && cfg.Setup != "" {
		return worktree.SetupProposal{Command: cfg.Setup, Source: worktree.SourceProject, Why: "this machine"}
	}
	return worktree.ProposeSetup(repo)
}

// rememberSetup stores a setup command for a repository on this machine, so the
// next worktree of it does not have to be told again.
func (s *worktreeStore) rememberSetup(repo, command string) {
	s.mu.Lock()
	if command == "" {
		delete(s.repos, repo)
	} else {
		s.repos[repo] = repoConfig{Setup: command}
	}
	s.saveLocked()
	s.mu.Unlock()
}

// --- records ------------------------------------------------------------------

// get returns a snapshot of a record. It is a copy on purpose: Setup and Shared
// are rewritten by the setup goroutine, so handing out the pointer would race
// every reader against a background install.
func (s *worktreeStore) get(path string) (worktreeRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[path]
	// A tombstone is not a worktree. It still answers repoFor, so past sessions
	// group correctly, but nothing may start a session in it or merge it.
	if !ok || rec.Removed {
		return worktreeRecord{}, false
	}
	return rec.snapshot(), true
}

// snapshot copies a record's mutable state. The caller holds the lock. The ready
// channel is shared rather than copied, which is safe: it is set once at creation
// and only ever closed.
func (rec *worktreeRecord) snapshot() worktreeRecord {
	out := *rec
	out.Shared = append([]string(nil), rec.Shared...)
	return out
}

func (s *worktreeStore) put(rec *worktreeRecord) {
	s.mu.Lock()
	s.records[rec.Path] = rec
	s.saveLocked()
	s.mu.Unlock()
}

// forget marks a worktree as gone without dropping what it knew.
//
// The record is kept because the sessions that ran in it are not: their
// transcripts outlive the worktree, and the only thing that still says which
// repository they belonged to is this record. Deleting it stranded every past
// session of a discarded worktree under a heading named after a directory that
// no longer exists. Removed records are filtered out of every listing and pruned
// once there are more than a few, so the file does not grow without bound.
func (s *worktreeStore) forget(path string) {
	s.mu.Lock()
	if rec, ok := s.records[path]; ok {
		rec.Removed = true
	}
	s.pruneRemovedLocked()
	s.saveLocked()
	s.mu.Unlock()
}

// pruneRemovedLocked keeps the newest removedKeep tombstones and drops the rest.
// A session old enough to fall off this list is old enough that its heading no
// longer matters.
func (s *worktreeStore) pruneRemovedLocked() {
	var removed []*worktreeRecord
	for _, rec := range s.records {
		if rec.Removed {
			removed = append(removed, rec)
		}
	}
	if len(removed) <= removedKeep {
		return
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i].CreatedAt > removed[j].CreatedAt })
	for _, rec := range removed[removedKeep:] {
		delete(s.records, rec.Path)
	}
}

// all returns every record, newest first, dropping any whose directory has gone
// (removed from a terminal, say). git is the truth about what exists.
func (s *worktreeStore) all() []worktreeRecord {
	s.mu.Lock()
	out := make([]worktreeRecord, 0, len(s.records))
	changed := false
	for path, rec := range s.records {
		if rec.Removed {
			continue
		}
		// git is the truth about what exists: a worktree deleted from a terminal
		// is gone, but its record is kept as a tombstone for the same reason a
		// discarded one is.
		if _, err := os.Stat(path); err != nil {
			rec.Removed = true
			changed = true
			continue
		}
		out = append(out, rec.snapshot())
	}
	if changed {
		s.pruneRemovedLocked()
		s.saveLocked()
	}
	s.mu.Unlock()

	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// repoFor returns the main checkout a path is a worktree of, or "" when the path
// is not a worktree kunai made. Cheap: a map lookup, because it is called once
// per row of every session and history listing.
func (s *worktreeStore) repoFor(cwd string) string {
	if s == nil || cwd == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.records[cwd]; ok {
		return rec.Repo
	}
	return ""
}

// tagRepos marks the entries whose directory is a worktree with the repository
// they belong to, so the sidebar groups them under that codebase rather than
// giving each worktree a heading of its own.
func (s *worktreeStore) tagRepos(metas []session.Meta) {
	for i := range metas {
		metas[i].Repo = s.repoFor(metas[i].Cwd)
	}
}

// sessionsIn lists the live sessions whose cwd is this worktree. A worktree may
// hold more than one, which is why removal asks "is anyone here" rather than
// "did its session end".
func (s *worktreeStore) sessionsIn(path string) []string {
	if s.live == nil {
		return nil
	}
	var out []string
	for _, m := range s.live() {
		if m.Cwd == path {
			out = append(out, m.ID)
		}
	}
	return out
}

// --- creation -----------------------------------------------------------------

// createRequest is what the API accepts and what create needs.
type createRequest struct {
	Repo string `json:"repo"`
	Name string `json:"name"`
	Base string `json:"base"`
	// Setup is a pointer so absent and empty mean different things. Absent means
	// "use whatever this repository resolves to", which is what a one-tap start
	// needs: it never showed the user a command, so it must not silently decide
	// there isn't one. An explicit "" means the user looked and chose none.
	Setup *string `json:"setup"`
	// Remember stores Setup as this repository's command for next time.
	Remember bool `json:"remember"`
}

// setupCommand resolves what this request should actually run.
func (req createRequest) setupCommand(store *worktreeStore, repo string) string {
	if req.Setup != nil {
		return *req.Setup
	}
	return store.setupFor(repo).Command
}

// create makes the worktree and starts its setup, returning as soon as the
// worktree exists.
//
// Setup is deliberately not awaited here: a cold install takes minutes and an
// HTTP request should not. The record carries a running setup state and a ready
// channel, so the client can show progress and a session create can wait. If
// creating the worktree itself fails there is nothing to clean up, because
// nothing was made.
func (s *worktreeStore) create(req createRequest) (worktreeRecord, error) {
	info, err := worktree.Create(worktree.CreateOptions{
		Repo:       req.Repo,
		Root:       s.root,
		Name:       req.Name,
		Base:       req.Base,
		FromOrigin: s.getSettings().FromOrigin,
	})
	if err != nil {
		return worktreeRecord{}, err
	}
	// Resolved after Create, because the repository path is only canonical once
	// git has told us where its main checkout is.
	setup := req.setupCommand(s, info.Repo)
	if req.Remember && req.Setup != nil {
		s.rememberSetup(info.Repo, setup)
	}

	rec := &worktreeRecord{Info: info, CreatedAt: time.Now().Unix()}
	if setup == "" {
		rec.Setup = worktree.SetupResult{State: worktree.SetupNone}
		s.put(rec)
		return *rec, nil
	}

	rec.Setup = worktree.SetupResult{State: worktree.SetupRunning, Command: setup}
	rec.ready = make(chan struct{})
	s.put(rec)
	go s.runSetup(rec, setup)

	s.mu.Lock()
	out := rec.snapshot()
	s.mu.Unlock()
	return out, nil
}

// runSetup executes the command and folds the outcome into the record. It runs
// on the server's own lifetime rather than a request's: the request that started
// it has long returned, and an install killed by a client disconnecting would
// leave a worktree in a worse state than either finishing or failing.
func (s *worktreeStore) runSetup(rec *worktreeRecord, command string) {
	defer close(rec.ready)

	res, err := worktree.RunSetup(context.Background(), rec.Info, command, worktree.DefaultSetupTimeout)
	if err != nil {
		res = worktree.SetupResult{State: worktree.SetupFailed, Command: command, Output: err.Error()}
	}
	if res.Failed() {
		log.Printf("worktree: setup for %s: %s", rec.Path, res.State)
	}

	// Read the links back off disk rather than trusting what the command said it
	// would do, so the agent's warning names what is actually shared.
	shared := worktree.SharedPaths(rec.Info)

	s.mu.Lock()
	rec.Setup = res
	rec.Shared = shared
	s.saveLocked()
	s.mu.Unlock()
}

// waitReady blocks until a worktree's setup has finished, so a session never
// starts against a half-installed tree. Returns immediately for a worktree with
// no setup, and gives up if the caller's context does.
func (s *worktreeStore) waitReady(ctx context.Context, path string) {
	rec, ok := s.get(path)
	if !ok || rec.ready == nil {
		return
	}
	select {
	case <-rec.ready:
	case <-ctx.Done():
	}
}

// brief renders what the agent working in this worktree is told.
func (rec worktreeRecord) brief() string {
	return worktree.Brief(rec.Info, rec.Setup, rec.Shared)
}
