package server

import (
	"context"
	"errors"

	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/telegram"
)

// The one place a chat channel is allowed to create a session.
//
// Before this existed the bot held the session manager directly, which meant a
// session started from Telegram skipped armSession (so it never notified) and
// had no way to resume, because resuming needs the transcript seeding that lives
// here. Routing every channel through this adapter means a session born in a
// chat is the same object as one born in the app, and a second channel gets all
// of it by implementing nothing.

// channelSessions adapts the server to what a chat channel needs.
type channelSessions struct{ srv *Server }

// recentForChannel bounds the resume list a chat is offered. A phone screen
// holds a handful; the app is where the full history belongs.
const recentForChannel = 8

func (c channelSessions) Start(ctx context.Context, cwd string) (*session.Session, error) {
	cli := c.srv.resolveCLI("")
	return c.create(ctx, session.CreateOptions{
		Cwd:     cwd,
		Model:   c.srv.model(),
		Effort:  c.srv.effort(),
		CLIName: cli.Name, Bin: cli.Bin, Env: cli.effectiveEnv(),
	})
}

// Resume reopens a past session on the account that owns its transcript, seeded
// the same way the app seeds a reopen: without the seed the model keeps its
// context but the conversation comes back blank, and without the right account
// it resumes from a stale copy of the transcript.
func (c channelSessions) Resume(ctx context.Context, id string) (*session.Session, error) {
	if id == "" {
		return nil, errors.New("no session id")
	}
	// Already running (the app reopened it, or another chat is driving it):
	// attach to that rather than racing a second CLI onto the same transcript.
	if sess, ok := c.srv.mgr.Get(id); ok {
		return sess, nil
	}
	entry, ok := c.find(id)
	if !ok {
		return nil, errors.New("no session with that id")
	}
	cli := c.srv.resolveCLI(entry.CLI)
	dir := cli.configDir()
	opts := session.CreateOptions{
		Cwd:     entry.Cwd,
		Title:   entry.Title,
		Model:   c.srv.model(),
		Effort:  c.srv.effort(),
		Resume:  id,
		CLIName: cli.Name, Bin: cli.Bin, Env: cli.effectiveEnv(),
	}
	opts.Seed, opts.HistBefore = loadTranscriptSeed(dir, id)
	opts.ContextTokens, opts.Overhead = loadTranscriptContextTokens(dir, id)
	return c.create(ctx, opts)
}

func (c channelSessions) Recent(limit int) []telegram.Past {
	if limit <= 0 {
		limit = recentForChannel
	}
	out := make([]telegram.Past, 0, limit)
	for _, e := range c.srv.pastSessions(limit) {
		out = append(out, telegram.Past{ID: e.ID, Cwd: e.Cwd, Title: e.Title, When: e.Mtime})
	}
	return out
}

func (c channelSessions) Get(id string) (*session.Session, bool) { return c.srv.mgr.Get(id) }
func (c channelSessions) List() []session.Meta                   { return c.srv.mgr.List() }
func (c channelSessions) Close(id string)                        { c.srv.mgr.Close(id) }

// create is the shared tail of Start and Resume: make it, then arm it, so a
// chat-born session notifies and answers rate limits like any other.
func (c channelSessions) create(ctx context.Context, opts session.CreateOptions) (*session.Session, error) {
	sess, err := c.srv.mgr.Create(ctx, opts)
	if err != nil {
		return nil, err
	}
	c.srv.armSession(sess)
	return sess, nil
}

// find locates a past session by id, which is also how its owning account is
// discovered. Only ids that are not currently live are searched, because a live
// one was already handled.
func (c channelSessions) find(id string) (HistoryEntry, bool) {
	for _, e := range c.srv.pastSessions(historyMaxLimit) {
		if e.ID == id {
			return e, true
		}
	}
	return HistoryEntry{}, false
}

// pastSessions lists resumable sessions newest first, excluding live ones. It is
// the shared core of the history endpoint and the channel's resume list, so both
// see the same set on the same account rules.
func (s *Server) pastSessions(limit int) []HistoryEntry {
	live := map[string]bool{}
	for _, m := range s.mgr.List() {
		live[m.ID] = true
	}
	var keep map[string]bool
	if s.sessionMeta != nil {
		keep = s.sessionMeta.pinnedIDs()
	}
	return scanHistory(live, limit, s.accountRoots(), keep)
}

// model and effort are the configured defaults, resolved the same way for every
// caller so a session started from a chat matches one started in the app.
func (s *Server) model() string {
	if s.cfg.DefaultModel != "" {
		return s.cfg.DefaultModel
	}
	return defaultModel
}

func (s *Server) effort() string {
	if s.cfg.DefaultEffort != "" {
		return s.cfg.DefaultEffort
	}
	return defaultEffort
}
